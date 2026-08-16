package workspace

import (
	"context"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

// Binder performs explicit binding and never infers a similarly named
// workspace. It is intentionally separate from Resolver so CLI `workspace
// bind` can make user intent visible.
type Binder struct {
	Store Store
	Clock func() time.Time
}

func NewBinder(store Store, clock func() time.Time) *Binder {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Binder{Store: store, Clock: clock}
}

func (binder *Binder) Bind(ctx context.Context, bindingKey, workspaceID string) (Binding, error) {
	if binder == nil || binder.Store == nil {
		return Binding{}, domain.NewError(domain.CodeUnavailable, "workspace store unavailable", true)
	}
	if bindingKey == "" || workspaceID == "" {
		return Binding{}, domain.NewError(domain.CodeValidation, "binding key and workspace are required", false)
	}
	workspace, found, err := binder.Store.ReadWorkspace(ctx, workspaceID)
	if err != nil {
		return Binding{}, err
	}
	if !found {
		return Binding{}, domain.NewError(domain.CodeNotFound, "workspace not found", false)
	}
	now := binder.Clock().UTC()
	binding := Binding{Key: bindingKey, WorkspaceID: workspace.ID, Kind: BindingExplicit, FirstInferenceWarned: true, CreatedAt: now, UpdatedAt: now}
	if existing, found, err := binder.Store.ReadBinding(ctx, bindingKey); err == nil && found {
		binding.CreatedAt = existing.CreatedAt
	}
	if err := binder.Store.SaveBinding(ctx, binding); err != nil {
		return Binding{}, err
	}
	return binding, nil
}
