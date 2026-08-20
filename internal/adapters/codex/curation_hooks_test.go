package codex

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type hookResolver struct{ workspace domain.Workspace }

func (resolver hookResolver) ResolveBound(context.Context, string) (WorkspaceResolution, error) {
	return WorkspaceResolution{Workspace: resolver.workspace}, nil
}

type hookRecall struct{ calls int }

func (recall *hookRecall) Recall(context.Context, string, string) (RecallResult, error) {
	recall.calls++
	return RecallResult{WorkspaceID: "workspace", Items: []RecallItem{{MemoryID: "memory"}}, Included: 1}, nil
}

type hookEnqueue struct {
	requests []curation.EnqueueRequest
}

func (enqueue *hookEnqueue) Enqueue(_ context.Context, request curation.EnqueueRequest) (curation.EnqueueResult, error) {
	enqueue.requests = append(enqueue.requests, request)
	return curation.EnqueueResult{Enqueued: true}, nil
}

type hookCounter struct {
	count int64
	ended bool
}

func (counter *hookCounter) IncrementPrompt(_ context.Context, value curation.SessionCounter) (curation.SessionCounter, error) {
	counter.count++
	value.PromptCount = counter.count
	return value, nil
}
func (counter *hookCounter) EndSession(context.Context, []byte) error {
	counter.ended = true
	return nil
}

type hookDigest struct{}

func (hookDigest) SessionDigest(context.Context, []byte) ([]byte, error) {
	return []byte("digest"), nil
}

func TestCurationHooksRecallEveryPromptAndEnqueueEveryTen(t *testing.T) {
	recall := &hookRecall{}
	enqueue := &hookEnqueue{}
	counter := &hookCounter{}
	hooks := NewCurationHooks(CurationHookConfig{Resolver: hookResolver{workspace: domain.Workspace{ID: "workspace"}}, Recall: recall, Enqueue: enqueue, Counter: counter, Digest: hookDigest{}, Clock: func() time.Time { return time.Unix(0, 0).UTC() }})
	for index := 0; index < 10; index++ {
		response, err := hooks.Handle(context.Background(), HookEvent{SessionID: "session", HookName: HookUserPromptSubmit, WorkingDirectory: "/tmp", CurrentPrompt: "prompt", SourceWatermark: int64(index)})
		if err != nil || response.Recall == nil {
			t.Fatalf("prompt %d response=%+v err=%v", index, response, err)
		}
	}
	if recall.calls != 10 || len(enqueue.requests) != 1 || enqueue.requests[0].Reason != curation.ReasonPeriodic {
		t.Fatalf("calls=%d requests=%+v", recall.calls, enqueue.requests)
	}
}

func TestCurationHooksPreCompactAndSessionEndEnqueueAndEndCounter(t *testing.T) {
	enqueue := &hookEnqueue{}
	counter := &hookCounter{}
	hooks := NewCurationHooks(CurationHookConfig{Resolver: hookResolver{workspace: domain.Workspace{ID: "workspace"}}, Enqueue: enqueue, Counter: counter, Digest: hookDigest{}})
	for _, name := range []string{HookPreCompact, HookSessionEnd} {
		response, err := hooks.Handle(context.Background(), HookEvent{SessionID: "session", HookName: name, WorkingDirectory: "/tmp", SourceWatermark: 1})
		if err != nil || !response.Enqueued {
			t.Fatalf("%s response=%+v err=%v", name, response, err)
		}
	}
	if len(enqueue.requests) != 2 || enqueue.requests[0].Reason != curation.ReasonPreCompact || enqueue.requests[1].Reason != curation.ReasonSessionEnd || !counter.ended {
		t.Fatalf("requests=%+v ended=%v", enqueue.requests, counter.ended)
	}
}

func TestCurationHooksInvalidLocalStateReturnsEmptyValidResponse(t *testing.T) {
	hooks := NewCurationHooks(CurationHookConfig{})
	response, err := hooks.Handle(context.Background(), HookEvent{HookName: HookUserPromptSubmit})
	if err != nil || !response.Accepted || response.Version != CurationHookResponseVersion || response.Recall != nil {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}
