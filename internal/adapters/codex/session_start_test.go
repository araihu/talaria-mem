package codex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/workspace"
)

type sessionClock struct{ now time.Time }

func (clock sessionClock) Now() time.Time { return clock.now }

type sessionResolver struct {
	workspace domain.Workspace
	warning   string
}

func (resolver sessionResolver) ResolveBound(context.Context, string) (WorkspaceResolution, error) {
	return WorkspaceResolution{Workspace: resolver.workspace, Warning: resolver.warning}, nil
}

type sessionGuard struct {
	calls  []application.OutputRequest
	status ports.ScanStatus
	err    error
}

func (guard *sessionGuard) Check(_ context.Context, request application.OutputRequest) (application.OutputResult, error) {
	guard.calls = append(guard.calls, request)
	if guard.err != nil {
		return application.OutputResult{}, guard.err
	}
	if guard.status == "" {
		guard.status = ports.ScanClean
	}
	if guard.status != ports.ScanClean {
		return application.OutputResult{Status: guard.status}, nil
	}
	return application.OutputResult{Allowed: true, Status: ports.ScanClean, Generation: "test"}, nil
}

type sessionUsage struct {
	stats      map[string]retrieval.UsageStats
	deliveries []string
}

func (usage *sessionUsage) Stats(_ context.Context, memoryID string, _ time.Time) (retrieval.UsageStats, error) {
	return usage.stats[memoryID], nil
}

func (usage *sessionUsage) RecordDelivery(_ context.Context, memoryID, session string, _ time.Time) (bool, error) {
	usage.deliveries = append(usage.deliveries, memoryID+":"+session)
	return true, nil
}

func sessionCandidate(id, workspaceID string, global bool, kind domain.MemoryKind, title, content string, pinned bool, created time.Time) retrieval.Candidate {
	if global {
		workspaceID = ""
	}
	return retrieval.Candidate{
		Memory:   domain.Memory{ID: id, WorkspaceID: workspaceID, UserGlobal: global, Kind: kind, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CurrentRevisionID: id + "-revision", Pinned: pinned, CreatedAt: created, UpdatedAt: created},
		Revision: domain.MemoryRevision{ID: id + "-revision", MemoryID: id, Number: 1, Kind: kind, Title: title, Content: content, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, Provenance: domain.Provenance{Actor: "cli", Source: "test", Labels: []string{"fixture"}}, CreatedAt: created},
		RawBM25:  -1,
	}
}

func sessionRequest() Request {
	return Request{EventID: "event-1", SessionID: "codex-session-1", HookName: HookName, WorkingDirectory: "/workspace/repo"}
}

func testSessionService(candidates []retrieval.Candidate, guard *sessionGuard) *Service {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	return NewService(Config{
		Resolver: sessionResolver{workspace: domain.Workspace{ID: "workspace-1", Name: "repo"}},
		Source:   MemorySource{Candidates: candidates},
		Guard:    guard,
		Clock:    sessionClock{now: now},
		ReceiptID: func() (string, error) {
			return "018f0f00-0000-7000-8000-000000000001", nil
		},
	})
}

func TestSessionStartSelectionPriorityScopeDedupAndMetadata(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	candidates := []retrieval.Candidate{
		sessionCandidate("regular-global", "", true, domain.MemoryKindState, "regular-global", "global", false, now),
		sessionCandidate("failure-global", "", true, domain.MemoryKindFailure, "failure", "failure", false, now),
		sessionCandidate("pinned-workspace", "workspace-1", false, domain.MemoryKindState, "pinned", "pinned", true, now),
		sessionCandidate("instruction-workspace", "workspace-1", false, domain.MemoryKindStandingInstruction, "instruction", "same\r\nbody", true, now),
		sessionCandidate("duplicate-global", "", true, domain.MemoryKindStandingInstruction, "instruction", "same\nbody", true, now.Add(time.Hour)),
		sessionCandidate("regular-workspace", "workspace-1", false, domain.MemoryKindProcedure, "regular-workspace", "workspace", false, now),
		sessionCandidate("unverified", "workspace-1", false, domain.MemoryKindState, "hidden", "hidden", false, now),
		sessionCandidate("other-workspace", "other", false, domain.MemoryKindState, "other", "other", false, now),
	}
	candidates[6].Memory.Trust = domain.TrustUnverified
	candidates[6].Revision.Trust = domain.TrustUnverified
	guard := &sessionGuard{}
	usage := &sessionUsage{stats: map[string]retrieval.UsageStats{"regular-workspace": {DistinctSessionHits: 20}}}
	service := testSessionService(candidates, guard)
	service.config.Usage = usage

	response, err := service.SessionStart(context.Background(), sessionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.WorkspaceID != "workspace-1" || response.Included != 5 || response.Omitted != 0 {
		t.Fatalf("response counts = %+v", response)
	}
	wantIDs := []string{"instruction-workspace", "pinned-workspace", "failure-global", "regular-workspace", "regular-global"}
	for index, want := range wantIDs {
		if response.Items[index].MemoryID != want {
			t.Fatalf("item %d = %s, want %s", index, response.Items[index].MemoryID, want)
		}
		if response.Items[index].Trust != domain.TrustVerified || response.Items[index].Lifecycle != domain.LifecycleActive || !response.Items[index].Untrusted {
			t.Fatalf("unsafe item = %+v", response.Items[index])
		}
	}
	if !strings.Contains(response.Items[0].Content, "same\r\nbody") || !strings.Contains(response.Items[0].Content, UntrustedReferenceStart) || !strings.Contains(response.Items[0].Content, UntrustedReferenceEnd) {
		t.Fatalf("content not delimited = %q", response.Items[0].Content)
	}
	if len(guard.calls) != response.Included || guard.calls[0].Route != ports.FieldSessionStart {
		t.Fatalf("guard calls = %d, route=%s", len(guard.calls), guard.calls[0].Route)
	}
	if len(usage.deliveries) != response.Included || !strings.HasSuffix(usage.deliveries[0], ":codex-session-1") {
		t.Fatalf("usage deliveries = %v", usage.deliveries)
	}
}

func TestSessionStartPinReserveAndLegacyStateFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	candidates := make([]retrieval.Candidate, 0, domain.MaxPinnedItemsPerScope+1)
	for index := 0; index <= domain.MaxPinnedItemsPerScope; index++ {
		candidates = append(candidates, sessionCandidate("pin-"+string(rune('a'+index)), "workspace-1", false, domain.MemoryKindStandingInstruction, "pin", "body", true, now))
	}
	service := testSessionService(candidates, &sessionGuard{})
	if _, err := service.SessionStart(context.Background(), sessionRequest()); !domain.IsCode(err, domain.CodeUnavailable) {
		t.Fatalf("reserve error = %v, want unavailable", err)
	}
	legacy := sessionCandidate("legacy", "workspace-1", false, domain.MemoryKindStandingInstruction, "legacy", "body", true, now)
	legacy.Memory.Trust = domain.TrustUnverified
	legacy.Revision.Trust = domain.TrustUnverified
	service = testSessionService([]retrieval.Candidate{legacy}, &sessionGuard{})
	if _, err := service.SessionStart(context.Background(), sessionRequest()); !domain.IsCode(err, domain.CodeUnavailable) {
		t.Fatalf("legacy pin error = %v, want unavailable", err)
	}
}

func TestSessionStartFindingExcludesAndScannerFailureReturnsNoContent(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	candidate := sessionCandidate("secret", "workspace-1", false, domain.MemoryKindState, "title", "secret", false, now)
	guard := &sessionGuard{err: domain.NewError(domain.CodeQuarantine, "content quarantined", false)}
	service := testSessionService([]retrieval.Candidate{candidate}, guard)
	response, err := service.SessionStart(context.Background(), sessionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Included != 0 || response.Omitted != 1 || len(response.Items) != 0 {
		t.Fatalf("finding response = %+v", response)
	}

	guard = &sessionGuard{err: domain.NewError(domain.CodeUnavailable, "scanner unavailable", true)}
	service = testSessionService([]retrieval.Candidate{candidate}, guard)
	response, err = service.SessionStart(context.Background(), sessionRequest())
	if !domain.IsCode(err, domain.CodeUnavailable) || len(response.Items) != 0 {
		t.Fatalf("scanner response=%+v err=%v", response, err)
	}
}

func TestSessionStartBoundsAndWorkspaceBinding(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	candidates := make([]retrieval.Candidate, 0, domain.MaxSessionStartItems+1)
	for index := 0; index <= domain.MaxSessionStartItems; index++ {
		id := "memory-" + string(rune('a'+index))
		candidates = append(candidates, sessionCandidate(id, "workspace-1", false, domain.MemoryKindState, id, "content-"+id, false, now))
	}
	service := testSessionService(candidates, &sessionGuard{})
	response, err := service.SessionStart(context.Background(), sessionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Included != domain.MaxSessionStartItems || response.Omitted != 1 {
		t.Fatalf("bounded response = %+v", response)
	}
	encoded, err := responseBytes(response)
	if err != nil || len(encoded) > domain.MaxSessionStartBytes {
		t.Fatalf("response bytes=%d err=%v", len(encoded), err)
	}

	store := workspace.NewMemoryStore()
	workspaceValue := domain.Workspace{ID: "workspace-1", Name: "repo", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateWorkspace(context.Background(), workspaceValue); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBinding(context.Background(), workspace.Binding{Key: "path:/workspace/repo", WorkspaceID: workspaceValue.ID, Kind: workspace.BindingRootFingerprint}); err != nil {
		t.Fatal(err)
	}
	resolver := NewBoundWorkspaceResolver(store)
	resolved, err := resolver.ResolveBound(context.Background(), "/workspace/repo")
	if err != nil || resolved.Workspace.ID != workspaceValue.ID || resolved.Warning == "" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	resolved, err = resolver.ResolveBound(context.Background(), "/workspace/repo")
	if err != nil || resolved.Warning != "" {
		t.Fatalf("second resolved=%+v err=%v", resolved, err)
	}
	if _, err := resolver.ResolveBound(context.Background(), "/workspace/other"); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("unbound error=%v", err)
	}
}

func TestSessionStartRanksPinnedAndUsageBoostBeyondTwentyCandidates(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	candidates := make([]retrieval.Candidate, 0, domain.MaxSessionStartItems+3)
	for index := 0; index < domain.MaxSessionStartItems+1; index++ {
		id := "recent-" + string(rune('a'+index))
		candidates = append(candidates, sessionCandidate(id, "workspace-1", false, domain.MemoryKindState, id, "recent", false, now))
	}
	oldUsage := sessionCandidate("old-usage", "workspace-1", false, domain.MemoryKindState, "old usage", "old usage", false, now.Add(-365*24*time.Hour))
	oldPinned := sessionCandidate("old-pinned", "workspace-1", false, domain.MemoryKindStandingInstruction, "old pinned", "old pinned", true, now.Add(-365*24*time.Hour))
	candidates = append(candidates, oldUsage, oldPinned)
	guard := &sessionGuard{}
	service := testSessionService(candidates, guard)
	service.config.Usage = &sessionUsage{stats: map[string]retrieval.UsageStats{"old-usage": {DistinctSessionHits: 20}}}
	response, err := service.SessionStart(context.Background(), sessionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Included != domain.MaxSessionStartItems || response.Omitted != 3 {
		t.Fatalf("response counts = %+v", response)
	}
	seen := make(map[string]bool, len(response.Items))
	for _, item := range response.Items {
		seen[item.MemoryID] = true
	}
	if !seen[oldUsage.Memory.ID] || !seen[oldPinned.Memory.ID] {
		t.Fatalf("boosted candidates missing from response: %v", seen)
	}
}

func TestSessionStartNoImplicitWorkspaceOverride(t *testing.T) {
	called := ""
	service := NewService(Config{
		Resolver: WorkspaceResolverFunc(func(_ context.Context, directory string) (WorkspaceResolution, error) {
			called = directory
			return WorkspaceResolution{Workspace: domain.Workspace{ID: "workspace-1"}}, nil
		}),
		Source: MemorySource{}, Guard: &sessionGuard{},
		ReceiptID: func() (string, error) { return "018f0f00-0000-7000-8000-000000000001", nil },
	})
	request := sessionRequest()
	request.WorkingDirectory = "/workspace/repo"
	response, err := service.SessionStart(context.Background(), request)
	if err != nil || response.WorkspaceID != "workspace-1" || called != request.WorkingDirectory {
		t.Fatalf("response=%+v called=%q err=%v", response, called, err)
	}
}

func TestRunHookDoesNotExposeServiceErrorsAsContent(t *testing.T) {
	var output strings.Builder
	err := Run(context.Background(), strings.NewReader(validHookJSON()), &output, nil)
	if err == nil || output.Len() != 0 {
		t.Fatalf("run output=%q err=%v", output.String(), err)
	}
}
