package codex

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/workspace"
)

const (
	ScopeWorkspace = "workspace"
	ScopeGlobal    = "global"

	maxScopePathBytes = 4096
)

// CandidateSource supplies current revisions visible to a resolved workspace.
// The source must include both workspace and user-global candidates; the
// selector applies the trust, lifecycle, scope, dedup, and ordering rules.
type CandidateSource interface {
	ListSessionStart(context.Context, string) ([]retrieval.Candidate, error)
}

type CandidateSourceFunc func(context.Context, string) ([]retrieval.Candidate, error)

func (source CandidateSourceFunc) ListSessionStart(ctx context.Context, workspaceID string) ([]retrieval.Candidate, error) {
	return source(ctx, workspaceID)
}

// MemorySource is a deterministic in-memory source useful for composition
// tests. Production composition can replace it with a SQLite-backed adapter.
type MemorySource struct {
	Candidates []retrieval.Candidate
}

func (source MemorySource) ListSessionStart(_ context.Context, _ string) ([]retrieval.Candidate, error) {
	return append([]retrieval.Candidate(nil), source.Candidates...), nil
}

// WorkspaceResolution is the only workspace identity accepted by the
// selector. No event field can override it.
type WorkspaceResolution struct {
	Workspace domain.Workspace
	Warning   string
}

type WorkspaceResolver interface {
	ResolveBound(context.Context, string) (WorkspaceResolution, error)
}

// BoundWorkspaceResolver resolves only the persisted absolute-path binding.
// It does not infer, create, inspect, or canonicalize filesystem paths.
type BoundWorkspaceResolver struct {
	Store workspace.Store
	Key   func(string) string
	Clock func() time.Time
}

func NewBoundWorkspaceResolver(store workspace.Store) *BoundWorkspaceResolver {
	return &BoundWorkspaceResolver{Store: store, Clock: func() time.Time { return time.Now().UTC() }}
}

func (resolver *BoundWorkspaceResolver) ResolveBound(ctx context.Context, workingDirectory string) (WorkspaceResolution, error) {
	if resolver == nil || resolver.Store == nil {
		return WorkspaceResolution{}, domain.NewError(domain.CodeUnavailable, "workspace binding unavailable", true)
	}
	key, err := resolver.bindingKey(workingDirectory)
	if err != nil {
		return WorkspaceResolution{}, err
	}
	binding, found, err := resolver.Store.ReadBinding(ctx, key)
	if err != nil {
		return WorkspaceResolution{}, err
	}
	if !found {
		return WorkspaceResolution{}, domain.NewError(domain.CodeNotFound, "workspace binding not found", false)
	}
	resolved, found, err := resolver.Store.ReadWorkspace(ctx, binding.WorkspaceID)
	if err != nil {
		return WorkspaceResolution{}, err
	}
	if !found {
		return WorkspaceResolution{}, domain.NewError(domain.CodeUnavailable, "workspace binding target unavailable", true)
	}
	warning := ""
	if !binding.FirstInferenceWarned && binding.Kind != workspace.BindingExplicit {
		warning = fmt.Sprintf("Workspace inferred as %s. Use an explicit workspace selection to choose another workspace.", resolved.Name)
		binding.FirstInferenceWarned = true
		clock := resolver.Clock
		if clock == nil {
			clock = func() time.Time { return time.Now().UTC() }
		}
		binding.UpdatedAt = clock().UTC()
		if err := resolver.Store.SaveBinding(ctx, binding); err != nil {
			return WorkspaceResolution{}, err
		}
	}
	return WorkspaceResolution{Workspace: resolved, Warning: warning}, nil
}

func (resolver *BoundWorkspaceResolver) bindingKey(value string) (string, error) {
	if resolver.Key != nil {
		key := resolver.Key(value)
		if key == "" || len([]byte(key)) > maxScopePathBytes || strings.ContainsAny(key, "\x00\r\n") {
			return "", domain.NewError(domain.CodeValidation, "workspace binding key invalid", false)
		}
		return key, nil
	}
	absolute, err := absoluteWorkingDirectory(value)
	if err != nil {
		return "", err
	}
	return "path:" + filepath.Clean(absolute), nil
}

// WorkspaceResolverFunc makes a resolver seam easy to compose without
// creating a one-off interface implementation.
type WorkspaceResolverFunc func(context.Context, string) (WorkspaceResolution, error)

func (resolver WorkspaceResolverFunc) ResolveBound(ctx context.Context, workingDirectory string) (WorkspaceResolution, error) {
	return resolver(ctx, workingDirectory)
}

type UsageStatsSource interface {
	Stats(context.Context, string, time.Time) (retrieval.UsageStats, error)
}

type ContentGuard interface {
	Check(context.Context, application.OutputRequest) (application.OutputResult, error)
}

// Config contains replaceable seams. Guard, Resolver, and Source are
// mandatory at runtime; missing seams fail closed rather than returning any
// memory content.
type Config struct {
	Resolver  WorkspaceResolver
	Source    CandidateSource
	Usage     UsageStatsSource
	Guard     ContentGuard
	Clock     ports.Clock
	ReceiptID func() (string, error)
	MaxItems  int
	MaxBytes  int
}

type Service struct {
	config Config
}

type SessionStartService = Service
type Selector = Service

func NewService(config Config) *Service {
	if config.MaxItems <= 0 || config.MaxItems > domain.MaxSessionStartItems {
		config.MaxItems = domain.MaxSessionStartItems
	}
	if config.MaxBytes <= 0 || config.MaxBytes > domain.MaxSessionStartBytes {
		config.MaxBytes = domain.MaxSessionStartBytes
	}
	if config.Clock == nil {
		config.Clock = wallClock{}
	}
	if config.ReceiptID == nil {
		config.ReceiptID = newReceiptID
	}
	return &Service{config: config}
}

func NewSessionStart(config Config) *Service        { return NewService(config) }
func NewSessionStartService(config Config) *Service { return NewService(config) }
func NewSelector(config Config) *Service            { return NewService(config) }

// SessionStart selects verified context for one hook session. Raw session
// identifiers are passed to the usage seam only; this package never stores or
// logs them.
func (service *Service) SessionStart(ctx context.Context, request Request) (Response, error) {
	if service == nil {
		return Response{}, unavailable()
	}
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	if service.config.Resolver == nil || service.config.Source == nil || service.config.Guard == nil {
		return Response{}, unavailable()
	}
	resolved, err := service.config.Resolver.ResolveBound(ctx, request.WorkingDirectory)
	if err != nil {
		return Response{}, err
	}
	if resolved.Workspace.ID == "" {
		return Response{}, unavailable()
	}
	candidates, err := service.config.Source.ListSessionStart(ctx, resolved.Workspace.ID)
	if err != nil {
		return Response{}, safeSourceError(err)
	}
	now := service.config.Clock.Now().UTC()
	visible, err := service.prepareCandidates(ctx, candidates, resolved.Workspace.ID, request.SessionID, now)
	if err != nil {
		return Response{}, err
	}
	ordered, err := service.deduplicateAndRank(visible, resolved.Workspace.ID)
	if err != nil {
		return Response{}, err
	}
	receipt, err := service.config.ReceiptID()
	if err != nil || !looksLikeUUIDv7(receipt) {
		return Response{}, unavailable()
	}
	response := Response{Version: ResponseVersion, WorkspaceID: resolved.Workspace.ID, ReceiptID: receipt, Warning: resolved.Warning, Items: make([]ContextItem, 0)}
	for _, candidate := range ordered {
		if len(response.Items) >= service.config.MaxItems {
			break
		}
		allowed, err := service.scan(ctx, candidate, resolved.Workspace.ID)
		if err != nil {
			return Response{}, err
		}
		if !allowed {
			continue
		}
		item, err := contextItem(candidate, resolved.Workspace.ID)
		if err != nil {
			return Response{}, err
		}
		candidateItems := append(append([]ContextItem(nil), response.Items...), item)
		candidateResponse := response
		candidateResponse.Items = candidateItems
		candidateResponse.Included = len(candidateItems)
		candidateResponse.Omitted = len(ordered) - candidateResponse.Included
		encoded, err := responseBytes(candidateResponse)
		if err != nil {
			return Response{}, err
		}
		if len(encoded) > service.config.MaxBytes {
			continue
		}
		response.Items = candidateItems
		if usage, ok := service.config.Usage.(interface {
			RecordDelivery(context.Context, string, string, time.Time) (bool, error)
		}); ok {
			if _, err := usage.RecordDelivery(ctx, candidate.Candidate.Memory.ID, request.SessionID, now); err != nil {
				return Response{}, safeSourceError(err)
			}
		}
	}
	response.Included = len(response.Items)
	response.Omitted = len(ordered) - response.Included
	if response.Omitted < 0 {
		response.Omitted = 0
	}
	if err := validResponse(response); err != nil {
		return Response{}, err
	}
	encoded, err := responseBytes(response)
	if err != nil || len(encoded) > service.config.MaxBytes {
		return Response{}, unavailable()
	}
	return response, nil
}

func (service *Service) Start(ctx context.Context, request Request) (Response, error) {
	return service.SessionStart(ctx, request)
}

func (service *Service) Select(ctx context.Context, request Request) (Response, error) {
	return service.SessionStart(ctx, request)
}

func (service *Service) prepareCandidates(ctx context.Context, candidates []retrieval.Candidate, workspaceID, consumerSession string, now time.Time) ([]retrieval.Candidate, error) {
	counts := map[string]pinReserve{ScopeWorkspace: {}, ScopeGlobal: {}}
	visible := make([]retrieval.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !inScope(candidate, workspaceID) {
			continue
		}
		scope := candidateScope(candidate)
		if candidate.Memory.Pinned && candidate.Revision.Kind == domain.MemoryKindStandingInstruction {
			if candidate.Memory.Trust != domain.TrustVerified || candidate.Memory.Lifecycle != domain.LifecycleActive || candidate.Revision.Trust != domain.TrustVerified || candidate.Revision.Lifecycle != domain.LifecycleActive {
				return nil, domain.NewError(domain.CodeUnavailable, "inconsistent pinned memory state", true)
			}
			reserve := counts[scope]
			reserve.Items++
			reserve.Bytes += len([]byte(candidate.Revision.Content))
			counts[scope] = reserve
		}
		if !eligible(candidate, workspaceID) {
			continue
		}
		if err := validateCandidate(candidate); err != nil {
			return nil, err
		}
		if service.config.Usage != nil {
			stats, err := service.config.Usage.Stats(ctx, candidate.Memory.ID, now)
			if err != nil {
				return nil, safeSourceError(err)
			}
			candidate.Score = retrieval.ScoreMemory(0, candidate.Revision.CreatedAt, now, stats.DistinctSessionHits)
		} else {
			candidate.Score = retrieval.ScoreMemory(0, candidate.Revision.CreatedAt, now, 0)
		}
		visible = append(visible, candidate)
	}
	for _, scope := range []string{ScopeWorkspace, ScopeGlobal} {
		reserve := counts[scope]
		if reserve.Items > domain.MaxPinnedItemsPerScope || reserve.Bytes > domain.MaxPinnedContentPerScope {
			return nil, domain.NewError(domain.CodeUnavailable, "inconsistent pinned memory state", true)
		}
	}
	_ = consumerSession // UsageStats intentionally uses only the bounded session hook identity at the service boundary.
	return visible, nil
}

type pinReserve struct {
	Items int
	Bytes int
}

func (service *Service) deduplicateAndRank(candidates []retrieval.Candidate, workspaceID string) ([]rankedCandidate, error) {
	// Workspace scope wins deduplication even when a global duplicate has a
	// higher score or pin priority. The second sort is deterministic for ties.
	sort.SliceStable(candidates, func(left, right int) bool {
		leftScope, rightScope := scopeRank(candidates[left], workspaceID), scopeRank(candidates[right], workspaceID)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		leftTier, rightTier := tier(candidates[left]), tier(candidates[right])
		if leftTier != rightTier {
			return leftTier < rightTier
		}
		return candidateBefore(candidates[left], candidates[right])
	})

	seen := make(map[string]struct{}, len(candidates))
	ranked := make([]rankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		normalized, err := domain.NormalizeV1(string(candidate.Revision.Kind), candidate.Revision.Title, candidate.Revision.Content, candidate.Revision.Tags)
		if err != nil {
			return nil, domain.NewError(domain.CodeUnavailable, "stored memory normalization failed", true)
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		ranked = append(ranked, rankedCandidate{Candidate: candidate, Tier: tier(candidate), Scope: candidateScope(candidate)})
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].Tier != ranked[right].Tier {
			return ranked[left].Tier < ranked[right].Tier
		}
		leftScope, rightScope := scopeRank(ranked[left].Candidate, workspaceID), scopeRank(ranked[right].Candidate, workspaceID)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		return candidateBefore(ranked[left].Candidate, ranked[right].Candidate)
	})
	return ranked, nil
}

type rankedCandidate struct {
	Candidate retrieval.Candidate
	Tier      int
	Scope     string
}

func candidateBefore(left, right retrieval.Candidate) bool {
	if left.Score.CombinedBoost != right.Score.CombinedBoost {
		return left.Score.CombinedBoost > right.Score.CombinedBoost
	}
	if !left.Revision.CreatedAt.Equal(right.Revision.CreatedAt) {
		return left.Revision.CreatedAt.After(right.Revision.CreatedAt)
	}
	return left.Memory.ID < right.Memory.ID
}

func tier(candidate retrieval.Candidate) int {
	if candidate.Memory.Pinned && candidate.Revision.Kind == domain.MemoryKindStandingInstruction {
		return 0
	}
	if candidate.Memory.Pinned {
		return 1
	}
	if candidate.Revision.Kind == domain.MemoryKindFailure && (candidate.Revision.ResolutionState == "" || candidate.Revision.ResolutionState == domain.ResolutionOpen) {
		return 2
	}
	return 3
}

func scopeRank(candidate retrieval.Candidate, workspaceID string) int {
	if candidate.Memory.UserGlobal {
		return 1
	}
	if candidate.Memory.WorkspaceID == workspaceID {
		return 0
	}
	return 2
}

func candidateScope(candidate retrieval.Candidate) string {
	if candidate.Memory.UserGlobal {
		return ScopeGlobal
	}
	return ScopeWorkspace
}

func inScope(candidate retrieval.Candidate, workspaceID string) bool {
	return candidate.Memory.UserGlobal || candidate.Memory.WorkspaceID == workspaceID
}

func eligible(candidate retrieval.Candidate, workspaceID string) bool {
	return inScope(candidate, workspaceID) &&
		candidate.Memory.Trust == domain.TrustVerified &&
		candidate.Memory.Lifecycle == domain.LifecycleActive &&
		candidate.Revision.Trust == domain.TrustVerified &&
		candidate.Revision.Lifecycle == domain.LifecycleActive
}

func validateCandidate(candidate retrieval.Candidate) error {
	if candidate.Memory.ID == "" || candidate.Revision.ID == "" || candidate.Revision.MemoryID != "" && candidate.Revision.MemoryID != candidate.Memory.ID {
		return domain.NewError(domain.CodeUnavailable, "stored memory identity is invalid", true)
	}
	if !candidate.Revision.Kind.Valid() || candidate.Revision.Number < 1 {
		return domain.NewError(domain.CodeUnavailable, "stored memory metadata is invalid", true)
	}
	if err := domain.ValidateMemoryText(candidate.Revision.Title, []byte(candidate.Revision.Content), candidate.Revision.Tags); err != nil {
		return domain.NewError(domain.CodeUnavailable, "stored memory text is invalid", true)
	}
	if err := domain.ValidateProvenance(candidate.Revision.Provenance); err != nil {
		return domain.NewError(domain.CodeUnavailable, "stored memory provenance is invalid", true)
	}
	return nil
}

func (service *Service) scan(ctx context.Context, candidate rankedCandidate, workspaceID string) (bool, error) {
	memory := candidate.Candidate.Memory
	revision := candidate.Candidate.Revision
	fields := []ports.TextField{
		{Name: ports.FieldTitle, Value: revision.Title},
		{Name: ports.FieldContent, Value: revision.Content},
	}
	for _, tag := range revision.Tags {
		fields = append(fields, ports.TextField{Name: ports.FieldTag, Value: tag})
	}
	for _, label := range revision.Provenance.Labels {
		fields = append(fields, ports.TextField{Name: ports.FieldProvenanceLabel, Value: label})
	}
	if revision.Provenance.SourceLocator != "" {
		fields = append(fields, ports.TextField{Name: ports.FieldSourceLocator, Value: revision.Provenance.SourceLocator})
	}
	result, err := service.config.Guard.Check(ctx, application.OutputRequest{Route: ports.FieldSessionStart, WorkspaceID: workspaceID, MemoryID: memory.ID, RevisionID: revision.ID, Fields: fields})
	if err != nil {
		if domain.IsCode(err, domain.CodeQuarantine) {
			return false, nil
		}
		return false, err
	}
	if !result.Allowed {
		if result.Quarantined || result.Status == ports.ScanFinding {
			return false, nil
		}
		return false, unavailable()
	}
	if result.Status != ports.ScanClean {
		return false, unavailable()
	}
	return true, nil
}

func contextItem(candidate rankedCandidate, workspaceID string) (ContextItem, error) {
	memory := candidate.Candidate.Memory
	revision := candidate.Candidate.Revision
	if len([]byte(revision.Content)) > domain.MaxContentBytes {
		return ContextItem{}, domain.NewError(domain.CodeUnavailable, "stored memory content is oversized", true)
	}
	return ContextItem{
		MemoryID: memory.ID, RevisionID: revision.ID, WorkspaceID: workspaceID,
		Scope: candidate.Scope, Kind: revision.Kind, Trust: domain.TrustVerified,
		Lifecycle: domain.LifecycleActive, Pinned: memory.Pinned, ResolutionState: revision.ResolutionState,
		Title: revision.Title, Content: DelimitUntrustedReference(revision.Content),
		Tags: append([]string{}, revision.Tags...), Score: candidate.Candidate.Score.CombinedBoost,
		Provenance: Provenance{Actor: revision.Provenance.Actor, Source: revision.Provenance.Source, Labels: append([]string{}, revision.Provenance.Labels...), SourceLocator: revision.Provenance.SourceLocator},
		Untrusted:  true,
	}, nil
}

func absoluteWorkingDirectory(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || len([]byte(value)) > maxScopePathBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", domain.NewError(domain.CodeValidation, "working directory is invalid", false)
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == string(filepath.Separator) && value != string(filepath.Separator) {
		return "", domain.NewError(domain.CodeValidation, "working directory is invalid", false)
	}
	return clean, nil
}

func unavailable() error {
	return domain.NewError(domain.CodeUnavailable, "session context unavailable", true)
}

func safeSourceError(err error) error {
	if err == nil {
		return unavailable()
	}
	if code := domain.CodeOf(err); code != "" {
		return domain.NewError(code, "session context unavailable", domain.IsRetryable(err))
	}
	return unavailable()
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

func newReceiptID() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	millis := time.Now().UTC().UnixMilli()
	if millis < 0 {
		return "", errors.New("receipt clock before epoch")
	}
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(millis))
	copy(raw[:6], stamp[2:])
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	return uuid.UUID(raw).String(), nil
}

func looksLikeUUIDv7(value string) bool {
	return domain.ValidateUUIDv7(value) == nil
}

// Run decodes one event, dispatches it to the bounded selector, and writes
// only the safe response. It is the in-process equivalent used by tests and
// by future composition; the installed shell hook remains HTTP-only.
func Run(ctx context.Context, input io.Reader, output io.Writer, service interface {
	SessionStart(context.Context, Request) (Response, error)
}) error {
	request, err := DecodeRequest(input)
	if err != nil {
		return err
	}
	if service == nil {
		return unavailable()
	}
	response, err := service.SessionStart(ctx, request)
	if err != nil {
		return err
	}
	return EncodeResponse(output, response)
}
