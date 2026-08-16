package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type MemoryRecord struct {
	Memory    domain.Memory
	Revision  domain.MemoryRevision
	Revisions []domain.MemoryRevision
}

type MergeConflict struct {
	SourceMemoryID string
	TargetMemoryID string
	Reason         string
	ReviewFlag     string
}

type MergePlan struct {
	SourceWorkspaceID string
	TargetWorkspaceID string
	SourceWatermark   int64
	TargetWatermark   int64
	Aliases           map[string]string
	Conflicts         []MergeConflict
	MemoryIDs         []string
	ProjectionScopes  []string
}

type MergeReceipt struct {
	ID                string
	SourceWorkspaceID string
	TargetWorkspaceID string
	SourceWatermark   int64
	TargetWatermark   int64
	PlanDigest        string
	ExpiresAt         time.Time
	Applied           bool
	ReviewRequired    bool
}

type MergeStore interface {
	ReadWorkspace(ctx context.Context, idOrName string) (domain.Workspace, bool, error)
	ListMemories(ctx context.Context, workspaceID string) ([]MemoryRecord, error)
	ReadRedirect(ctx context.Context, sourceWorkspaceID string) (string, bool, error)
	ApplyMerge(ctx context.Context, plan MergePlan, receipt MergeReceipt) error
}

type MergeService struct {
	Store    MergeStore
	Clock    func() time.Time
	mu       sync.Mutex
	receipts map[string]MergeReceipt
}

func NewMergeService(store MergeStore, clock func() time.Time) *MergeService {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &MergeService{Store: store, Clock: clock, receipts: map[string]MergeReceipt{}}
}

func (service *MergeService) DryRun(ctx context.Context, sourceWorkspaceID, targetWorkspaceID string, ttl time.Duration) (MergePlan, MergeReceipt, error) {
	if service == nil || service.Store == nil {
		return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeUnavailable, "workspace store unavailable", true)
	}
	if sourceWorkspaceID == "" || targetWorkspaceID == "" || sourceWorkspaceID == targetWorkspaceID {
		return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeValidation, "distinct source and target workspaces are required", false)
	}
	source, found, err := service.Store.ReadWorkspace(ctx, sourceWorkspaceID)
	if err != nil {
		return MergePlan{}, MergeReceipt{}, err
	}
	if !found {
		return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeNotFound, "source workspace not found", false)
	}
	target, found, err := service.Store.ReadWorkspace(ctx, targetWorkspaceID)
	if err != nil {
		return MergePlan{}, MergeReceipt{}, err
	}
	if !found {
		return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeNotFound, "target workspace not found", false)
	}
	if redirect, found, err := service.Store.ReadRedirect(ctx, target.ID); err != nil {
		return MergePlan{}, MergeReceipt{}, err
	} else if found && redirect != "" {
		return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeValidation, "target workspace already redirects", false)
	}
	if redirect, found, err := service.Store.ReadRedirect(ctx, source.ID); err != nil {
		return MergePlan{}, MergeReceipt{}, err
	} else if found && redirect != "" {
		return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeValidation, "source workspace already redirects", false)
	}
	sourceRecords, err := service.Store.ListMemories(ctx, source.ID)
	if err != nil {
		return MergePlan{}, MergeReceipt{}, err
	}
	targetRecords, err := service.Store.ListMemories(ctx, target.ID)
	if err != nil {
		return MergePlan{}, MergeReceipt{}, err
	}
	targetByID := make(map[string]MemoryRecord, len(targetRecords))
	for _, record := range targetRecords {
		targetByID[record.Memory.ID] = record
	}
	normalized := make(map[string]string, len(targetRecords))
	for _, record := range targetRecords {
		value, err := domain.NormalizeV1(string(record.Revision.Kind), record.Revision.Title, record.Revision.Content, record.Revision.Tags)
		if err != nil {
			return MergePlan{}, MergeReceipt{}, err
		}
		normalized[value] = record.Memory.ID
	}
	plan := MergePlan{SourceWorkspaceID: source.ID, TargetWorkspaceID: target.ID, SourceWatermark: source.RevisionWatermark, TargetWatermark: target.RevisionWatermark, Aliases: map[string]string{}, ProjectionScopes: []string{source.ID, target.ID}}
	for _, record := range sourceRecords {
		if _, collision := targetByID[record.Memory.ID]; collision {
			return MergePlan{}, MergeReceipt{}, domain.NewError(domain.CodeRevisionConflict, "memory ID collision", false)
		}
		value, err := domain.NormalizeV1(string(record.Revision.Kind), record.Revision.Title, record.Revision.Content, record.Revision.Tags)
		if err != nil {
			return MergePlan{}, MergeReceipt{}, err
		}
		if canonical, duplicate := normalized[value]; duplicate {
			plan.Aliases[record.Memory.ID] = canonical
		} else {
			for _, targetRecord := range targetRecords {
				if targetRecord.Revision.Kind == record.Revision.Kind && targetRecord.Revision.Title == record.Revision.Title && (targetRecord.Revision.Content != record.Revision.Content || strings.Join(targetRecord.Revision.Tags, "\x00") != strings.Join(record.Revision.Tags, "\x00")) {
					plan.Conflicts = append(plan.Conflicts, MergeConflict{SourceMemoryID: record.Memory.ID, TargetMemoryID: targetRecord.Memory.ID, Reason: "non-identical normalized content", ReviewFlag: "REQ-6.1-MERGE-REVIEW"})
				}
			}
			plan.MemoryIDs = append(plan.MemoryIDs, record.Memory.ID)
		}
	}
	sort.Strings(plan.MemoryIDs)
	digest, err := digestPlan(plan)
	if err != nil {
		return MergePlan{}, MergeReceipt{}, err
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	now := service.Clock().UTC()
	id := "merge-" + digest[:16]
	receipt := MergeReceipt{ID: id, SourceWorkspaceID: source.ID, TargetWorkspaceID: target.ID, SourceWatermark: source.RevisionWatermark, TargetWatermark: target.RevisionWatermark, PlanDigest: digest, ExpiresAt: now.Add(ttl), ReviewRequired: len(plan.Conflicts) > 0}
	service.mu.Lock()
	service.receipts[id] = receipt
	service.mu.Unlock()
	return plan, receipt, nil
}

func (service *MergeService) Apply(ctx context.Context, plan MergePlan, receipt MergeReceipt) error {
	if service == nil || service.Store == nil {
		return domain.NewError(domain.CodeUnavailable, "workspace store unavailable", true)
	}
	if receipt.ID == "" || receipt.PlanDigest == "" {
		return domain.NewError(domain.CodeValidation, "merge receipt required", false)
	}
	if service.Clock().UTC().After(receipt.ExpiresAt) {
		return domain.NewError(domain.CodeValidation, "merge receipt expired", false)
	}
	digest, err := digestPlan(plan)
	if err != nil {
		return err
	}
	if digest != receipt.PlanDigest || plan.SourceWorkspaceID != receipt.SourceWorkspaceID || plan.TargetWorkspaceID != receipt.TargetWorkspaceID || plan.SourceWatermark != receipt.SourceWatermark || plan.TargetWatermark != receipt.TargetWatermark {
		return domain.NewError(domain.CodeRevisionConflict, "merge receipt does not match plan", false)
	}
	service.mu.Lock()
	stored, found := service.receipts[receipt.ID]
	if !found {
		service.mu.Unlock()
		return domain.NewError(domain.CodeValidation, "unknown merge receipt", false)
	}
	if found && stored.Applied {
		service.mu.Unlock()
		return nil
	}
	if found {
		receipt = stored
	}
	service.mu.Unlock()
	source, sourceFound, err := service.Store.ReadWorkspace(ctx, plan.SourceWorkspaceID)
	if err != nil {
		return err
	}
	target, targetFound, err := service.Store.ReadWorkspace(ctx, plan.TargetWorkspaceID)
	if err != nil {
		return err
	}
	if !sourceFound || !targetFound || source.RevisionWatermark != plan.SourceWatermark || target.RevisionWatermark != plan.TargetWatermark {
		return domain.NewError(domain.CodeRevisionConflict, "workspace merge watermark is stale", false)
	}
	if err := service.Store.ApplyMerge(ctx, plan, receipt); err != nil {
		return err
	}
	receipt.Applied = true
	service.mu.Lock()
	service.receipts[receipt.ID] = receipt
	service.mu.Unlock()
	return nil
}

func digestPlan(plan MergePlan) (string, error) {
	aliases := make([][2]string, 0, len(plan.Aliases))
	for source, target := range plan.Aliases {
		aliases = append(aliases, [2]string{source, target})
	}
	sort.Slice(aliases, func(i, j int) bool {
		if aliases[i][0] == aliases[j][0] {
			return aliases[i][1] < aliases[j][1]
		}
		return aliases[i][0] < aliases[j][0]
	})
	clone := struct {
		Source, Target                   string
		SourceWatermark, TargetWatermark int64
		Aliases                          [][2]string
		MemoryIDs                        []string
		Conflicts                        []MergeConflict
	}{plan.SourceWorkspaceID, plan.TargetWorkspaceID, plan.SourceWatermark, plan.TargetWatermark, aliases, append([]string(nil), plan.MemoryIDs...), append([]MergeConflict(nil), plan.Conflicts...)}
	sort.Strings(clone.MemoryIDs)
	bytes, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:]), nil
}

func (store *MemoryStore) ReadRedirect(ctx context.Context, sourceWorkspaceID string) (string, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.redirects[sourceWorkspaceID]
	return value, ok, nil
}
func (store *MemoryStore) ListMemories(ctx context.Context, workspaceID string) ([]MemoryRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	records := append([]MemoryRecord(nil), store.memories[workspaceID]...)
	return records, nil
}
func (store *MemoryStore) AddMemory(workspaceID string, record MemoryRecord) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.memories[workspaceID] = append(store.memories[workspaceID], record)
}
func (store *MemoryStore) ApplyMerge(ctx context.Context, plan MergePlan, receipt MergeReceipt) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.redirects[plan.SourceWorkspaceID]; ok {
		if existing == plan.TargetWorkspaceID {
			return nil
		}
		return domain.NewError(domain.CodeValidation, "workspace redirect cycle", false)
	}
	for alias, canonical := range plan.Aliases {
		store.aliases[alias] = canonical
	}
	if len(plan.MemoryIDs) > 0 {
		selected := make(map[string]struct{}, len(plan.MemoryIDs))
		for _, id := range plan.MemoryIDs {
			selected[id] = struct{}{}
		}
		var moving []MemoryRecord
		var retained []MemoryRecord
		for _, record := range store.memories[plan.SourceWorkspaceID] {
			if _, ok := selected[record.Memory.ID]; ok {
				record.Memory.WorkspaceID = plan.TargetWorkspaceID
				moving = append(moving, record)
			} else {
				retained = append(retained, record)
			}
		}
		store.memories[plan.SourceWorkspaceID] = retained
		store.memories[plan.TargetWorkspaceID] = append(store.memories[plan.TargetWorkspaceID], moving...)
	}
	store.redirects[plan.SourceWorkspaceID] = plan.TargetWorkspaceID
	return nil
}

func (plan MergePlan) ReviewFlags() []string {
	flags := make([]string, 0, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		if conflict.ReviewFlag != "" {
			flags = append(flags, conflict.ReviewFlag)
		}
	}
	sort.Strings(flags)
	return flags
}
