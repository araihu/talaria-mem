package ports

import "context"

type ProjectionRequest struct {
	ScopeID           string
	RevisionWatermark int64
}

type Projector interface {
	Project(ctx context.Context, request ProjectionRequest) error
}
