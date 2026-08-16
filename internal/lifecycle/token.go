package lifecycle

import (
	"context"
	"errors"

	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type TokenService struct{ Path string }

func NewTokenService(path string) *TokenService { return &TokenService{Path: path} }

func (service *TokenService) Rotate(ctx context.Context) error {
	if service == nil || service.Path == "" {
		return errors.New("token path is required")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	store, err := security.NewTokenStore(service.Path)
	if err != nil {
		return err
	}
	_, err = store.Rotate()
	return err
}

func (service *TokenService) Validate(ctx context.Context) error {
	if service == nil || service.Path == "" {
		return errors.New("token path is required")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	_, err := security.LoadBearerToken(service.Path)
	return err
}
