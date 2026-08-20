package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/providers/codex"
	"github.com/guilhermecastro/talaria-mem/internal/providers/codex/protocol"
	providerconfig "github.com/guilhermecastro/talaria-mem/internal/providers/config"
	"github.com/guilhermecastro/talaria-mem/internal/providers/openai"
)

// runtimeSnapshotSource uses Codex thread/read when the host app-server is
// available. If host access is unavailable it degrades to the already bounded
// current-prompt capture; it never opens transcript_path or retains a raw host
// event. The worker can therefore keep local-only inference useful while
// status/doctor report the host degradation separately.
type runtimeSnapshotSource struct {
	configuration providerconfig.Provider
	scanner       curationScanner

	mu              sync.Mutex
	client          *codex.AppClient
	hostUnavailable bool
}

func newRuntimeSnapshotSource(configuration providerconfig.Provider, scanner curationScanner) *runtimeSnapshotSource {
	if configuration.Command == "" {
		configuration = providerconfig.Default().Providers["codex"]
	}
	return &runtimeSnapshotSource{configuration: configuration, scanner: scanner}
}

func (source *runtimeSnapshotSource) Capture(ctx context.Context, request curation.CaptureRequest) (curation.SanitizedSnapshot, error) {
	if source == nil || request.SessionID == "" {
		return curation.SanitizedSnapshot{}, errors.New("Codex host snapshot unavailable")
	}
	client, err := source.hostClient(ctx)
	if err == nil && client != nil {
		thread, readErr := client.ReadThread(ctx, request.SessionID)
		if readErr == nil {
			return source.snapshotFromThread(ctx, thread, request.SessionID)
		}
		source.markHostUnavailable()
	}
	if ctx != nil && ctx.Err() != nil {
		return curation.SanitizedSnapshot{}, ctx.Err()
	}
	// A host snapshot failure must not block a prompt or compaction. The
	// current prompt is appended by EnqueueService after this bounded envelope
	// is returned, giving a configured local provider a safe degraded input.
	return source.fallbackSnapshot(ctx, request.SessionID)
}

func (source *runtimeSnapshotSource) hostClient(ctx context.Context) (*codex.AppClient, error) {
	source.mu.Lock()
	if source.client != nil {
		client := source.client
		source.mu.Unlock()
		return client, nil
	}
	if source.hostUnavailable {
		source.mu.Unlock()
		return nil, errors.New("Codex host snapshot unavailable")
	}
	client, err := codex.StartClient(ctx, codex.ClientConfig{Command: source.configuration.Command, Model: source.configuration.Model, ReasoningEffort: source.configuration.ReasoningEffort, Timeout: source.configuration.Timeout})
	if err != nil {
		source.hostUnavailable = true
		source.mu.Unlock()
		return nil, err
	}
	source.client = client
	source.mu.Unlock()
	return client, nil
}

func (source *runtimeSnapshotSource) markHostUnavailable() {
	source.mu.Lock()
	source.hostUnavailable = true
	source.mu.Unlock()
}

func (source *runtimeSnapshotSource) HostUnavailable() bool {
	if source == nil {
		return true
	}
	source.mu.Lock()
	degraded := source.hostUnavailable
	source.mu.Unlock()
	return degraded
}

func (source *runtimeSnapshotSource) Close() error {
	if source == nil {
		return nil
	}
	source.mu.Lock()
	client := source.client
	source.client = nil
	source.mu.Unlock()
	return client.Close()
}

func (source *runtimeSnapshotSource) snapshotFromThread(ctx context.Context, thread protocol.ThreadSnapshot, fallbackThreadID string) (curation.SanitizedSnapshot, error) {
	threadID := thread.ID
	if threadID == "" {
		threadID = fallbackThreadID
	}
	envelope := snapshotEnvelope{Turns: make([]curation.SnapshotTurn, 0, len(thread.Turns)), Tools: make([]curation.SnapshotTool, 0)}
	turns := thread.Turns
	if len(turns) == 0 && len(thread.Items) > 0 {
		turns = []protocol.Turn{{Items: thread.Items}}
	}
	for _, turn := range turns {
		textParts := make([]string, 0, len(turn.Items))
		for _, item := range turn.Items {
			if !isTextItem(item.Type) {
				if !isToolItem(item.Type) {
					continue
				}
				name := interfaceString(item.Name)
				if name == "" {
					name = item.Type
				}
				envelope.Tools = append(envelope.Tools, curation.SnapshotTool{Name: boundedSnapshotText(name, 256), Status: boundedSnapshotText(interfaceString(item.Status), 128)})
				continue
			}
			for _, value := range []interface{}{item.Text, item.Content, item.Message} {
				if text := interfaceString(value); text != "" {
					textParts = append(textParts, text)
					break
				}
			}
		}
		if text := boundedSnapshotText(strings.Join(textParts, "\n"), 16*1024); text != "" {
			envelope.Turns = append(envelope.Turns, curation.SnapshotTurn{Role: "turn", Text: text})
		}
	}
	return source.encodeSnapshot(ctx, threadID, envelope)
}

func (source *runtimeSnapshotSource) fallbackSnapshot(ctx context.Context, threadID string) (curation.SanitizedSnapshot, error) {
	return source.encodeSnapshot(ctx, threadID, snapshotEnvelope{Turns: []curation.SnapshotTurn{}, Tools: []curation.SnapshotTool{}})
}

type snapshotEnvelope struct {
	Turns []curation.SnapshotTurn `json:"turns"`
	Tools []curation.SnapshotTool `json:"tools,omitempty"`
}

func (source *runtimeSnapshotSource) encodeSnapshot(ctx context.Context, threadID string, envelope snapshotEnvelope) (curation.SanitizedSnapshot, error) {
	snapshot, err := json.Marshal(envelope)
	if err != nil {
		return curation.SanitizedSnapshot{}, err
	}
	if len(snapshot) > curation.MaxSnapshotBytes {
		return curation.SanitizedSnapshot{}, errors.New("Codex snapshot exceeds capture limit")
	}
	if source.scanner != nil {
		clean, scanErr := sanitizeRuntimeSnapshot(ctx, source.scanner, string(snapshot))
		if scanErr != nil {
			return curation.SanitizedSnapshot{}, scanErr
		}
		snapshot = []byte(clean)
	}
	locator, err := json.Marshal(struct {
		ThreadID string `json:"thread_id"`
	}{ThreadID: threadID})
	if err != nil {
		return curation.SanitizedSnapshot{}, err
	}
	return curation.SanitizedSnapshot{ThreadLocator: locator, Snapshot: snapshot}, nil
}

func sanitizeRuntimeSnapshot(ctx context.Context, scanner curationScanner, value string) (string, error) {
	value = strings.ToValidUTF8(value, "�")
	result := scanner.Scan(ctx, []ports.TextField{{Name: ports.FieldContent, Value: value}})
	if result.Status == ports.ScanClean {
		return value, nil
	}
	if result.Status != ports.ScanFinding {
		return "", errors.New("snapshot scanner unavailable")
	}
	redacted := value
	for index := len(result.Findings) - 1; index >= 0; index-- {
		finding := result.Findings[index]
		if finding.Field != ports.FieldContent || finding.Start < 0 || finding.End < finding.Start || finding.End > len(redacted) {
			return "", errors.New("snapshot scanner returned invalid finding")
		}
		redacted = redacted[:finding.Start] + "[REDACTED]" + redacted[finding.End:]
	}
	if scanner.Scan(ctx, []ports.TextField{{Name: ports.FieldContent, Value: redacted}}).Status != ports.ScanClean {
		return "", errors.New("snapshot scanner refused sanitized content")
	}
	return redacted, nil
}

func isToolItem(itemType string) bool {
	itemType = strings.ToLower(itemType)
	return strings.Contains(itemType, "tool") || strings.Contains(itemType, "command") || strings.Contains(itemType, "shell") || strings.Contains(itemType, "approval")
}

func isTextItem(itemType string) bool {
	switch strings.ToLower(itemType) {
	case "user_message", "assistant_message", "agent_message", "message", "text":
		return true
	default:
		return false
	}
}

func interfaceString(value interface{}) string {
	text, _ := value.(string)
	return strings.ToValidUTF8(text, "�")
}

func boundedSnapshotText(value string, maxBytes int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if len([]byte(value)) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

type lazyCodexCurator struct {
	configuration providerconfig.Provider
	mu            sync.Mutex
	client        *codex.AppClient
}

func (curator *lazyCodexCurator) Curate(ctx context.Context, request curation.CurationRequest) (curation.CurationResult, error) {
	if curator == nil {
		return curation.CurationResult{}, curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex provider unavailable"))
	}
	curator.mu.Lock()
	client := curator.client
	curator.mu.Unlock()
	if client == nil {
		started, err := codex.StartClient(ctx, codex.ClientConfig{Command: curator.configuration.Command, Model: curator.configuration.Model, ReasoningEffort: curator.configuration.ReasoningEffort, Timeout: curator.configuration.Timeout})
		if err != nil {
			return curation.CurationResult{}, err
		}
		curator.mu.Lock()
		if curator.client == nil {
			curator.client = started
			client = started
		} else {
			client = curator.client
			_ = started.Close()
		}
		curator.mu.Unlock()
	}
	return codex.NewCurator(client, codex.CuratorConfig{Model: curator.configuration.Model, ReasoningEffort: curator.configuration.ReasoningEffort, Timeout: curator.configuration.Timeout}).Curate(ctx, request)
}

func (curator *lazyCodexCurator) Close() error {
	if curator == nil {
		return nil
	}
	curator.mu.Lock()
	client := curator.client
	curator.client = nil
	curator.mu.Unlock()
	return client.Close()
}

func buildRouter(configuration providerconfig.Configuration, scanner curationScanner) (curation.Router, []func() error, error) {
	router := curation.Router{Enabled: configuration.Enabled}
	if !configuration.Enabled {
		return router, nil, nil
	}
	closers := make([]func() error, 0, len(configuration.Chain))
	for _, name := range configuration.Chain {
		provider := configuration.Providers[name]
		var curator curation.Curator
		switch provider.Type {
		case providerconfig.TypeCodex:
			lazy := &lazyCodexCurator{configuration: provider}
			curator = lazy
			closers = append(closers, lazy.Close)
		case providerconfig.TypeOpenAICompatible:
			client, err := openai.NewClient(openai.ClientConfig{BaseURL: provider.BaseURL, Model: provider.Model, Credential: provider.Credential, Timeout: provider.Timeout, Scanner: scanner})
			if err != nil {
				for _, close := range closers {
					_ = close()
				}
				return curation.Router{}, nil, err
			}
			curator = client
		default:
			return curation.Router{}, nil, errors.New("unsupported curation provider")
		}
		router.Chain = append(router.Chain, curation.ProviderEntry{Name: name, Curator: curator})
	}
	return router, closers, nil
}

type curationScanner interface {
	Scan(context.Context, []ports.TextField) ports.ScanResult
}
