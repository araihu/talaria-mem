package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	runtimegraph "github.com/guilhermecastro/talaria-mem/internal/runtime"
	"github.com/guilhermecastro/talaria-mem/internal/security"
	"github.com/guilhermecastro/talaria-mem/internal/workspace"
)

func TestAutomaticMemoryLifecycle(t *testing.T) {
	environment := acceptanceEnvironment(t)
	if _, err := security.CreateRootKey(filepath.Join(environment.ConfigDir, "root.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := security.CreateBearerToken(filepath.Join(environment.ConfigDir, "token")); err != nil {
		t.Fatal(err)
	}
	// A local-only provider topology is valid offline; this test never invokes
	// it, proving that recall and inline/generated storage do not require a
	// provider call.
	providers := []byte(`version = 1
enabled = true
chain = ["local"]

[providers.local]
type = "openai_compatible"
base_url = "http://127.0.0.1:11434/v1"
model = "offline-test"
timeout = "1s"
`)
	if err := os.WriteFile(filepath.Join(environment.ConfigDir, "providers.toml"), providers, lifecycle.ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	composition, err := runtimegraph.New(context.Background(), runtimegraph.Config{Environment: environment, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = composition.Close() })

	ctx := context.Background()
	workspaceStore := sqlite.NewWorkspaceStore(composition.DB)
	workspaceID := "018f1f61-7b5c-7abc-8def-1123456789ab"
	if err := workspaceStore.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, Name: "acceptance"}); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if _, err := workspace.NewBinder(workspaceStore, nil).Bind(ctx, "path:"+cwd, workspaceID); err != nil {
		t.Fatal(err)
	}
	generated, err := composition.Memory.CreateGenerated(ctx, application.GeneratedMutationRequest{WorkspaceID: workspaceID, Kind: domain.MemoryKindState, Title: "offline recovery", Content: "recover the local worker after restart", Tags: []string{"acceptance"}}, application.GeneratedSourceInline)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Trust != domain.TrustGenerated {
		t.Fatalf("generated trust=%s", generated.Trust)
	}

	token, err := security.LoadBearerToken(filepath.Join(environment.ConfigDir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	hook := func(event string, prompt string, watermark int) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"session_id": "acceptance-session", "hook_event_name": event, "cwd": cwd, "current_prompt": prompt, "source_watermark": watermark})
		request := httptest.NewRequest(http.MethodPost, "http://"+composition.Address+"/control/v1/"+hookRoute(event), strings.NewReader(string(body)))
		request.RemoteAddr = "127.0.0.1:12345"
		request.Host = composition.Address
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		composition.HTTP.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", event, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	if body := hook("UserPromptSubmit", "recover local worker", 1); !strings.Contains(body, "offline recovery") || !strings.Contains(body, "generated/unconfirmed") {
		t.Fatalf("prompt recall omitted generated context: %s", body)
	}
	for count := 2; count <= 10; count++ {
		hook("UserPromptSubmit", "periodic curation", count)
	}
	hook("PreCompact", "", 10)
	hook("SessionEnd", "", 10)
	health, err := composition.CurationStore.Health(ctx, timeNow())
	if err != nil {
		t.Fatal(err)
	}
	if health.QueueDepth != 1 || health.Running != 0 {
		t.Fatalf("unexpected durable queue health: %+v", health)
	}
	status, err := composition.Status.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || !status.Curation.Enabled || status.Curation.QueueDepth != 1 {
		t.Fatalf("unexpected safe status: %+v", status)
	}
}

func hookRoute(event string) string {
	switch event {
	case "UserPromptSubmit":
		return "user-prompt-submit"
	case "PreCompact":
		return "pre-compact"
	case "SessionEnd":
		return "session-end"
	default:
		return "session-start"
	}
}

func acceptanceEnvironment(t *testing.T) lifecycle.Environment {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	config := filepath.Join(root, "config")
	backup := filepath.Join(state, "backups")
	for _, path := range []string{state, config, backup} {
		if err := os.Mkdir(path, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return lifecycle.Environment{StateDir: state, ConfigDir: config, BackupDir: backup}
}

func timeNow() time.Time { return time.Now().UTC() }
