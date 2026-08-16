package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func workspaceAt(id, name string) domain.Workspace {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	return domain.Workspace{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
}
func workspaceMemory(id, workspaceID, title, content string) MemoryRecord {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	return MemoryRecord{Memory: domain.Memory{ID: id, WorkspaceID: workspaceID, Kind: domain.MemoryKindState, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: now, UpdatedAt: now}, Revision: domain.MemoryRevision{ID: id + "-revision", MemoryID: id, Number: 1, Kind: domain.MemoryKindState, Title: title, Content: content, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: now}}
}

func TestNormalizeGitRemoteAndInferenceWarning(t *testing.T) {
	if got, err := NormalizeGitRemote("https://github.com/AraiHu/Talaria-Mem.git"); err != nil || got != "github.com/araihu/talaria-mem" {
		t.Fatalf("normalized remote = %q, err=%v", got, err)
	}
	if _, err := NormalizeGitRemote("https://user:secret@example.com/a/b"); err == nil {
		t.Fatal("credentials accepted")
	}
	store := NewMemoryStore()
	resolver := NewResolver(store, func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) })
	first, err := resolver.Resolve(context.Background(), ResolutionInput{GitRemote: "git@github.com:araihu/talaria-mem.git"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Inferred || first.FirstUseWarning == "" {
		t.Fatalf("first resolution = %+v", first)
	}
	second, err := resolver.Resolve(context.Background(), ResolutionInput{GitRemote: "git@github.com:araihu/talaria-mem.git"})
	if err != nil {
		t.Fatal(err)
	}
	if second.FirstUseWarning != "" || second.Workspace.ID != first.Workspace.ID {
		t.Fatalf("second resolution = %+v", second)
	}
}

func TestMergeExactDuplicateConflictReviewAndReceipt(t *testing.T) {
	store := NewMemoryStore()
	_ = store.CreateWorkspace(context.Background(), workspaceAt("source", "source"))
	_ = store.CreateWorkspace(context.Background(), workspaceAt("target", "target"))
	store.AddMemory("target", workspaceMemory("target-memory", "target", "same", "body"))
	store.AddMemory("source", workspaceMemory("source-memory", "source", "same", "body"))
	store.AddMemory("source", workspaceMemory("conflict-memory", "source", "same", "different"))
	service := NewMergeService(store, func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) })
	plan, receipt, err := service.DryRun(context.Background(), "source", "target", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Aliases["source-memory"] != "target-memory" {
		t.Fatalf("aliases = %+v", plan.Aliases)
	}
	if !receipt.ReviewRequired || len(plan.ReviewFlags()) != 1 {
		t.Fatalf("review = %+v flags=%v", receipt, plan.ReviewFlags())
	}
	if err := service.Apply(context.Background(), plan, receipt); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(context.Background(), plan, receipt); err != nil {
		t.Fatal(err)
	}
}

func TestMergeReceiptExpiryAndCollision(t *testing.T) {
	store := NewMemoryStore()
	_ = store.CreateWorkspace(context.Background(), workspaceAt("source", "source"))
	_ = store.CreateWorkspace(context.Background(), workspaceAt("target", "target"))
	store.AddMemory("target", workspaceMemory("same-id", "target", "target", "body"))
	store.AddMemory("source", workspaceMemory("same-id", "source", "source", "body"))
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	service := NewMergeService(store, func() time.Time { return clock })
	if _, _, err := service.DryRun(context.Background(), "source", "target", time.Hour); !domain.IsCode(err, domain.CodeRevisionConflict) {
		t.Fatalf("collision error = %v", err)
	}
}
