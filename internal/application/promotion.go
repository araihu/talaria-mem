package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type PromotionReceipt struct {
	Version             string    `json:"version"`
	ID                  string    `json:"id"`
	MemoryID            string    `json:"memory_id"`
	RevisionID          string    `json:"revision_id"`
	ContentFingerprint  string    `json:"content_fingerprint"`
	OutputPath          string    `json:"output_path"`
	ExistingFingerprint string    `json:"existing_fingerprint,omitempty"`
	ExpiresAt           time.Time `json:"expires_at"`
	Applied             bool      `json:"applied"`
	OutputFingerprint   string    `json:"output_fingerprint,omitempty"`
	Actor               string    `json:"actor"`
	CreatedAt           time.Time `json:"created_at"`
}

type PromotionRequest struct {
	MemoryID           string
	ExpectedRevisionID string
	OutputPath         string
	DryRun             bool
	ApplyReceipt       *PromotionReceipt
	Force              bool
}

type PromotionResult struct {
	Version    string           `json:"version"`
	DryRun     bool             `json:"dry_run"`
	Receipt    PromotionReceipt `json:"receipt"`
	OutputPath string           `json:"output_path,omitempty"`
}

type PromotionService struct {
	Source   RepositoryReader
	Guard    *ContentOutputGuard
	Files    ports.ManagedFileStore
	Clock    ports.Clock
	mu       sync.Mutex
	receipts map[string]PromotionReceipt
}

type RepositoryReader interface {
	ReadCurrent(context.Context, string) (domain.Memory, domain.MemoryRevision, error)
}

func NewPromotionService(source RepositoryReader, guard *ContentOutputGuard, files ports.ManagedFileStore, clock ports.Clock) *PromotionService {
	return &PromotionService{Source: source, Guard: guard, Files: files, Clock: clock, receipts: make(map[string]PromotionReceipt)}
}

func (service *PromotionService) DryRun(ctx context.Context, request PromotionRequest) (PromotionResult, error) {
	if request.MemoryID == "" || request.ExpectedRevisionID == "" || request.OutputPath == "" {
		return PromotionResult{}, domain.NewError(domain.CodeValidation, "memory, revision, and output are required", false)
	}
	if service == nil || service.Source == nil {
		return PromotionResult{}, domain.NewError(domain.CodeUnavailable, "promotion source unavailable", true)
	}
	memory, revision, err := service.Source.ReadCurrent(ctx, request.MemoryID)
	if err != nil {
		return PromotionResult{}, err
	}
	if err := validatePromotionTarget(memory, revision, request.ExpectedRevisionID); err != nil {
		return PromotionResult{}, err
	}
	if err := service.scanPromotion(ctx, memory, revision); err != nil {
		return PromotionResult{}, err
	}
	data := renderSkill(revision)
	now := service.now()
	receipt := PromotionReceipt{Version: SkillWireVersion, ID: receiptID(now), MemoryID: memory.ID, RevisionID: revision.ID, ContentFingerprint: contentFingerprint(data), OutputPath: request.OutputPath, ExpiresAt: now.Add(PromotionReceiptTTL), Actor: string(ActorCLI), CreatedAt: now}
	if service.Files != nil {
		if fingerprint, fingerprintErr := service.Files.FingerprintNoFollow(ctx, request.OutputPath); fingerprintErr == nil {
			receipt.ExistingFingerprint = fingerprint.SHA256Hex
		} else if !os.IsNotExist(fingerprintErr) {
			return PromotionResult{}, fingerprintErr
		}
	}
	service.mu.Lock()
	service.receipts[receipt.ID] = receipt
	service.mu.Unlock()
	return PromotionResult{Version: SkillWireVersion, DryRun: true, Receipt: receipt, OutputPath: request.OutputPath}, nil
}

func (service *PromotionService) Apply(ctx context.Context, request PromotionRequest) (PromotionResult, error) {
	if request.ApplyReceipt == nil {
		return PromotionResult{}, domain.NewError(domain.CodeValidation, "promotion receipt is required", false)
	}
	receipt := *request.ApplyReceipt
	now := service.now()
	if receipt.ID == "" || receipt.Applied || !receipt.ExpiresAt.After(now) {
		return PromotionResult{}, domain.NewError(domain.CodeValidation, "promotion receipt is expired or already used", false)
	}
	service.mu.Lock()
	stored, found := service.receipts[receipt.ID]
	service.mu.Unlock()
	if !found || stored != receipt {
		return PromotionResult{}, domain.NewError(domain.CodeValidation, "promotion receipt is invalid", false)
	}
	if service.Source == nil || service.Files == nil {
		return PromotionResult{}, domain.NewError(domain.CodeUnavailable, "promotion unavailable", true)
	}
	memory, revision, err := service.Source.ReadCurrent(ctx, receipt.MemoryID)
	if err != nil {
		return PromotionResult{}, err
	}
	if err := validatePromotionTarget(memory, revision, receipt.RevisionID); err != nil {
		return PromotionResult{}, err
	}
	if err := service.scanPromotion(ctx, memory, revision); err != nil {
		return PromotionResult{}, err
	}
	data := renderSkill(revision)
	if contentFingerprint(data) != receipt.ContentFingerprint {
		return PromotionResult{}, domain.NewError(domain.CodeRevisionConflict, "promotion source changed", false)
	}
	if request.OutputPath != "" && request.OutputPath != receipt.OutputPath {
		return PromotionResult{}, domain.NewError(domain.CodeRevisionConflict, "promotion output changed", false)
	}
	existing, existingErr := service.Files.FingerprintNoFollow(ctx, receipt.OutputPath)
	if existingErr == nil {
		if existing.SHA256Hex != receipt.ExistingFingerprint && !request.Force {
			return PromotionResult{}, domain.NewError(domain.CodeRevisionConflict, "promotion target changed", false)
		}
	} else if !os.IsNotExist(existingErr) {
		return PromotionResult{}, existingErr
	} else if receipt.ExistingFingerprint != "" {
		return PromotionResult{}, domain.NewError(domain.CodeRevisionConflict, "promotion target disappeared", false)
	}
	if err := writeManagedSkill(ctx, service.Files, receipt.OutputPath, data, existing, existingErr == nil, request.Force); err != nil {
		return PromotionResult{}, err
	}
	resultFingerprint := contentFingerprint(data)
	receipt.Applied = true
	receipt.OutputFingerprint = resultFingerprint
	service.mu.Lock()
	service.receipts[receipt.ID] = receipt
	service.mu.Unlock()
	return PromotionResult{Version: SkillWireVersion, Receipt: receipt, OutputPath: receipt.OutputPath}, nil
}

func validatePromotionTarget(memory domain.Memory, revision domain.MemoryRevision, expectedRevision string) error {
	if revision.ID != expectedRevision {
		return domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	if memory.Kind != domain.MemoryKindProcedure || revision.Kind != domain.MemoryKindProcedure {
		return domain.NewError(domain.CodeValidation, "only procedures may be promoted", false)
	}
	if memory.Trust != domain.TrustVerified || revision.Trust != domain.TrustVerified || memory.Lifecycle != domain.LifecycleActive || revision.Lifecycle != domain.LifecycleActive {
		return domain.NewError(domain.CodeValidation, "procedure is not active and verified", false)
	}
	return nil
}

func (service *PromotionService) scanPromotion(ctx context.Context, memory domain.Memory, revision domain.MemoryRevision) error {
	if service == nil || service.Guard == nil {
		return domain.NewError(domain.CodeUnavailable, "content scanner unavailable", true)
	}
	fields := []ports.TextField{{Name: ports.FieldSkillPromotion, Value: revision.Title}, {Name: ports.FieldSkillPromotion, Value: revision.Content}}
	for _, tag := range revision.Tags {
		fields = append(fields, ports.TextField{Name: ports.FieldSkillPromotion, Value: tag})
	}
	_, err := service.Guard.Check(ctx, OutputRequest{Route: ports.FieldSkillPromotion, WorkspaceID: memory.WorkspaceID, MemoryID: memory.ID, RevisionID: revision.ID, Fields: fields})
	return err
}

func renderSkill(revision domain.MemoryRevision) []byte {
	title := strings.ReplaceAll(strings.ReplaceAll(revision.Title, "\r", " "), "\n", " ")
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("version: ")
	builder.WriteString(SkillWireVersion)
	builder.WriteString("\nsource_revision: ")
	builder.WriteString(revision.ID)
	builder.WriteString("\n---\n\n# ")
	builder.WriteString(title)
	builder.WriteString("\n\n")
	builder.WriteString(revision.Content)
	if !strings.HasSuffix(revision.Content, "\n") {
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}

func writeManagedSkill(ctx context.Context, files ports.ManagedFileStore, target string, data []byte, existing ports.FileFingerprint, exists, force bool) error {
	temp, err := files.CreateTemp(ctx, filepathDir(target), ".talaria-skill-", ports.ManagedFileMode)
	if err != nil {
		return err
	}
	if _, err := temp.Writer.Write(data); err != nil {
		_ = temp.Writer.Close()
		return err
	}
	if err := temp.Writer.Close(); err != nil {
		return err
	}
	if err := files.SyncFile(ctx, temp.Path); err != nil {
		return err
	}
	var expected *ports.FileFingerprint
	if exists {
		if !force {
			expected = &existing
		} else {
			expected = &existing
		}
	}
	return files.ReplaceNoFollow(ctx, temp.Path, target, expected)
}

func contentFingerprint(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (service *PromotionService) now() time.Time {
	if service != nil && service.Clock != nil {
		return service.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

func receiptID(now time.Time) string {
	return fmt.Sprintf("%x-%x", now.UnixNano(), sha256.Sum256([]byte(now.UTC().Format(time.RFC3339Nano))))[:32]
}

var _ = sync.Mutex{}
