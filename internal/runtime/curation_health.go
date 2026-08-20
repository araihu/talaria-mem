package runtime

import (
	"context"
	"os/exec"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite"
	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	providerconfig "github.com/guilhermecastro/talaria-mem/internal/providers/config"
)

// curationHealthSource translates provider configuration and aggregate queue
// metadata into the closed lifecycle diagnostic contract. It never reads or
// returns provider payloads, prompts, locators, credentials, or model output.
type curationHealthSource struct {
	configuration    providerconfig.Configuration
	configurationErr error
	store            *sqlite.CurationStore
	snapshot         *runtimeSnapshotSource
	now              func() time.Time
}

func newCurationHealthSource(configuration providerconfig.Configuration, configurationErr error, store *sqlite.CurationStore, snapshot *runtimeSnapshotSource, clock func() time.Time) *curationHealthSource {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &curationHealthSource{configuration: configuration, configurationErr: configurationErr, store: store, snapshot: snapshot, now: clock}
}

func (source *curationHealthSource) CurationStatus(ctx context.Context) (lifecycle.CurationHealth, error) {
	if source == nil {
		return lifecycle.CurationHealth{Degraded: true, LastErrorClass: string(curation.ErrorUnavailable)}, nil
	}
	health := lifecycle.CurationHealth{Enabled: source.configuration.Enabled}
	if source.configurationErr != nil {
		health.Enabled = false
		health.Degraded = true
		health.LastErrorClass = string(curation.ErrorUnavailable)
	}
	if source.snapshot != nil && source.snapshot.HostUnavailable() {
		health.Degraded = true
		if health.LastErrorClass == "" {
			health.LastErrorClass = string(curation.ErrorUnavailable)
		}
	}
	for _, name := range source.configuration.Chain {
		provider, ok := source.configuration.Providers[name]
		if !ok {
			health.Degraded = true
			continue
		}
		health.Providers = append(health.Providers, lifecycle.CurationProviderStatus{
			Name:       name,
			Type:       provider.Type,
			Configured: true,
			Available:  providerAvailable(provider),
		})
		if !providerAvailable(provider) {
			health.Degraded = true
			if health.LastErrorClass == "" {
				health.LastErrorClass = string(curation.ErrorUnavailable)
			}
		}
	}
	if source.store == nil {
		health.Degraded = true
		if health.LastErrorClass == "" {
			health.LastErrorClass = string(curation.ErrorPersistence)
		}
		return health, nil
	}
	queue, err := source.store.Health(ctx, source.now().UTC())
	if err != nil {
		return lifecycle.CurationHealth{}, err
	}
	health.QueueDepth = queue.QueueDepth
	health.Running = queue.Running
	if !queue.OldestAt.IsZero() {
		age := source.now().UTC().Sub(queue.OldestAt)
		if age > 0 {
			health.OldestJobAgeSeconds = int64(age / time.Second)
		}
	}
	if queue.LastErrorClass != "" {
		health.LastErrorClass = string(queue.LastErrorClass)
	}
	return health, nil
}

func providerAvailable(provider providerconfig.Provider) bool {
	switch provider.Type {
	case providerconfig.TypeCodex:
		if provider.Command == "" {
			return false
		}
		_, err := exec.LookPath(provider.Command)
		return err == nil
	case providerconfig.TypeOpenAICompatible:
		// URL policy and credentials were validated while loading the owner-only
		// config. Avoid a live probe: availability is determined on demand by the
		// provider adapter and reported as a safe degraded error there.
		return true
	default:
		return false
	}
}
