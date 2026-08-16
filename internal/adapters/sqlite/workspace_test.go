package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	workspacepkg "github.com/guilhermecastro/talaria-mem/internal/workspace"
)

func TestWorkspaceStorePersistsIdentityAndBindingAcrossAdapters(t *testing.T) {
	database := openTestDB(t)
	first := NewWorkspaceStore(database)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	workspace := domain.Workspace{ID: "workspace-id", Name: "workspace-name", CreatedAt: now, UpdatedAt: now}
	if err := first.CreateWorkspace(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	binding := workspacepkg.Binding{Key: "path:/tmp/repo", WorkspaceID: workspace.ID, Kind: workspacepkg.BindingAbsolutePath, CreatedAt: now, UpdatedAt: now}
	if err := first.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}

	second := NewWorkspaceStore(database)
	byName, found, err := second.ReadWorkspace(context.Background(), workspace.Name)
	if err != nil || !found {
		t.Fatalf("read by name found=%t err=%v", found, err)
	}
	if byName.ID != workspace.ID {
		t.Fatalf("workspace = %+v", byName)
	}
	gotBinding, found, err := second.ReadBinding(context.Background(), binding.Key)
	if err != nil || !found {
		t.Fatalf("read binding found=%t err=%v", found, err)
	}
	if gotBinding.WorkspaceID != workspace.ID || gotBinding.Kind != binding.Kind {
		t.Fatalf("binding = %+v", gotBinding)
	}
	items, err := second.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != workspace.ID {
		t.Fatalf("workspaces = %+v", items)
	}
}

func TestWorkspaceStoreMergePersistsRedirectAndWatermarks(t *testing.T) {
	database := openTestDB(t)
	store := NewWorkspaceStore(database)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for _, item := range []domain.Workspace{
		{ID: "source", Name: "source", CreatedAt: now, UpdatedAt: now},
		{ID: "target", Name: "target", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateWorkspace(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	service := workspacepkg.NewMergeService(store, func() time.Time { return now })
	plan, receipt, err := service.DryRun(context.Background(), "source", "target", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(context.Background(), plan, receipt); err != nil {
		t.Fatal(err)
	}
	redirect, found, err := store.ReadRedirect(context.Background(), "source")
	if err != nil || !found || redirect != "target" {
		t.Fatalf("redirect=%q found=%t err=%v", redirect, found, err)
	}
	for _, id := range []string{"source", "target"} {
		item, found, err := store.ReadWorkspace(context.Background(), id)
		if err != nil || !found {
			t.Fatalf("workspace %s found=%t err=%v", id, found, err)
		}
		if item.RevisionWatermark != 1 {
			t.Fatalf("workspace %s watermark=%d", id, item.RevisionWatermark)
		}
	}
	if _, _, err := workspacepkg.NewMergeService(store, func() time.Time { return now }).DryRun(context.Background(), "source", "target", time.Hour); err == nil {
		t.Fatal("redirected source accepted for another merge")
	}
}
