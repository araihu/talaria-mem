package curation

import (
	"context"
	"errors"
	"fmt"
)

type ProviderEntry struct {
	Name    string
	Curator Curator
}

type Router struct {
	Enabled bool
	Chain   []ProviderEntry
}

func (router Router) Validate() error {
	if !router.Enabled {
		return nil
	}
	if len(router.Chain) == 0 {
		return fmt.Errorf("enabled curation requires a non-empty provider chain")
	}
	seen := make(map[string]struct{}, len(router.Chain))
	for index, entry := range router.Chain {
		if entry.Name == "" {
			return fmt.Errorf("provider chain entry %d has empty name", index)
		}
		if entry.Curator == nil {
			return fmt.Errorf("provider %q has no curator", entry.Name)
		}
		if _, ok := seen[entry.Name]; ok {
			return fmt.Errorf("provider chain contains duplicate %q", entry.Name)
		}
		seen[entry.Name] = struct{}{}
	}
	return nil
}

func (router Router) Curate(ctx context.Context, request CurationRequest) (CurationResult, error) {
	if !router.Enabled {
		return CurationResult{}, ErrCurationDisabled
	}
	if err := router.Validate(); err != nil {
		return CurationResult{}, err
	}
	var last error
	for _, entry := range router.Chain {
		result, err := entry.Curator.Curate(ctx, request)
		if err == nil {
			if result.Provider == "" {
				result.Provider = entry.Name
			}
			return result, nil
		}
		last = err
		var providerErr *ProviderError
		if !errors.As(err, &providerErr) || !providerErr.Class.Retryable() {
			return CurationResult{}, err
		}
	}
	if last != nil {
		return CurationResult{}, last
	}
	return CurationResult{}, ErrNoProvider
}
