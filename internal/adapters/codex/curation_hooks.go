package codex

import (
	"context"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const CurationHookResponseVersion = "talaria.curation-hook.v1"

type HookResponse struct {
	Version     string        `json:"version"`
	HookName    string        `json:"hook_event_name"`
	WorkspaceID string        `json:"workspace_id,omitempty"`
	Accepted    bool          `json:"accepted"`
	Enqueued    bool          `json:"enqueued"`
	Recall      *RecallResult `json:"recall,omitempty"`
}

type HookEnqueuer interface {
	Enqueue(context.Context, curation.EnqueueRequest) (curation.EnqueueResult, error)
}

type PromptCounter interface {
	IncrementPrompt(context.Context, curation.SessionCounter) (curation.SessionCounter, error)
	EndSession(context.Context, []byte) error
}

type SessionDigestor interface {
	SessionDigest(context.Context, []byte) ([]byte, error)
}

type CurationHookConfig struct {
	Resolver WorkspaceResolver
	Recall   RecallProvider
	Enqueue  HookEnqueuer
	Counter  PromptCounter
	Digest   SessionDigestor
	Clock    func() time.Time
}

type CurationHooks struct {
	config CurationHookConfig
}

func NewCurationHooks(config CurationHookConfig) *CurationHooks {
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &CurationHooks{config: config}
}

func (hooks *CurationHooks) Handle(ctx context.Context, event HookEvent) (HookResponse, error) {
	response := HookResponse{Version: CurationHookResponseVersion, HookName: event.HookName, Accepted: true}
	if hooks == nil || !event.Valid() {
		return response, nil
	}
	if event.HookName == HookName {
		return response, nil
	}
	if hooks.config.Resolver == nil {
		return response, nil
	}
	resolved, err := hooks.config.Resolver.ResolveBound(ctx, event.WorkingDirectory)
	if err != nil || resolved.Workspace.ID == "" {
		return response, nil
	}
	response.WorkspaceID = resolved.Workspace.ID
	if event.HookName == HookUserPromptSubmit {
		response.Recall = &RecallResult{Items: []RecallItem{}}
		if hooks.config.Recall != nil {
			if recall, recallErr := hooks.config.Recall.Recall(ctx, resolved.Workspace.ID, event.CurrentPrompt); recallErr == nil {
				response.Recall = &recall
			}
		}
		digest, digestErr := hooks.sessionDigest(ctx, event.SessionID)
		if digestErr == nil && hooks.config.Counter != nil {
			expires := hooks.config.Clock().UTC().Add(curation.CurationJobRetention)
			counter, counterErr := hooks.config.Counter.IncrementPrompt(ctx, curation.SessionCounter{SessionDigest: digest, LastWatermark: event.SourceWatermark, ExpiresAt: expires})
			if counterErr == nil && counter.PromptCount > 0 && counter.PromptCount%10 == 0 {
				response.Enqueued = hooks.enqueue(ctx, resolved.Workspace.ID, event, curation.ReasonPeriodic)
			}
		}
		return response, nil
	}
	response.Enqueued = hooks.enqueue(ctx, resolved.Workspace.ID, event, reasonForHook(event.HookName))
	if event.HookName == HookSessionEnd {
		if digest, digestErr := hooks.sessionDigest(ctx, event.SessionID); digestErr == nil && hooks.config.Counter != nil {
			_ = hooks.config.Counter.EndSession(ctx, digest)
		}
	}
	return response, nil
}

func (hooks *CurationHooks) enqueue(ctx context.Context, workspaceID string, event HookEvent, reason curation.Reason) bool {
	if hooks.config.Enqueue == nil {
		return false
	}
	result, err := hooks.config.Enqueue.Enqueue(ctx, curation.EnqueueRequest{WorkspaceID: workspaceID, SessionID: event.SessionID, Reason: reason, SourceWatermark: event.SourceWatermark, CurrentPrompt: event.CurrentPrompt})
	return err == nil && result.Enqueued
}

func (hooks *CurationHooks) sessionDigest(ctx context.Context, sessionID string) ([]byte, error) {
	if hooks.config.Digest == nil || sessionID == "" {
		return nil, domain.NewError(domain.CodeUnavailable, "session digest unavailable", true)
	}
	return hooks.config.Digest.SessionDigest(ctx, []byte(sessionID))
}

func reasonForHook(name string) curation.Reason {
	if name == HookPreCompact {
		return curation.ReasonPreCompact
	}
	return curation.ReasonSessionEnd
}
