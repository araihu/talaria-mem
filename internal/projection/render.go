package projection

import (
	"context"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type Renderer struct {
	Guard OutputGuard
}

func NewRenderer(guard OutputGuard) *Renderer { return &Renderer{Guard: guard} }

func (renderer *Renderer) Render(ctx context.Context, scope ScopeDocument) (RenderedDocument, error) {
	if renderer == nil || renderer.Guard == nil {
		return RenderedDocument{}, domain.NewError(domain.CodeUnavailable, "projection scanner guard unavailable", true)
	}
	for _, memory := range scope.Memories {
		fields := []ports.TextField{{Name: ports.FieldProjection, Value: memory.Revision.Title + "\n" + memory.Revision.Content}}
		for _, tag := range memory.Revision.Tags {
			fields = append(fields, ports.TextField{Name: ports.FieldProjection, Value: tag})
		}
		if _, err := renderer.Guard.Check(ctx, application.OutputRequest{Route: ports.FieldProjection, WorkspaceID: scope.ScopeID, MemoryID: memory.Memory.ID, RevisionID: memory.Revision.ID, Fields: fields}); err != nil {
			return RenderedDocument{}, err
		}
	}
	return Render(scope)
}
