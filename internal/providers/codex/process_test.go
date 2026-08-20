package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMinimalEnvironmentDropsAmbientSecretsAndProxySettings(t *testing.T) {
	got := minimalEnvironment([]string{
		"PATH=/bin",
		"HOME=/tmp/home",
		"LC_ALL=en_US.UTF-8",
		"OPENAI_API_KEY=secret",
		"HTTP_PROXY=http://proxy.invalid",
		"TALARIA_SECRET=secret",
	})
	joined := strings.Join(got, "\n")
	for _, value := range []string{"OPENAI_API_KEY=secret", "HTTP_PROXY=http://proxy.invalid", "TALARIA_SECRET=secret"} {
		if strings.Contains(joined, value) {
			t.Fatalf("minimalEnvironment retained %q: %v", value, got)
		}
	}
	for _, value := range []string{"PATH=/bin", "HOME=/tmp/home", "LC_ALL=en_US.UTF-8"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("minimalEnvironment dropped %q: %v", value, got)
		}
	}
}

func TestProcessStartsConfiguredJSONLChild(t *testing.T) {
	process, err := StartProcess(context.Background(), ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1"),
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	if process == nil || process.Connection() == nil {
		t.Fatal("StartProcess() returned incomplete process")
	}
	if err := process.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestProcessMapsMissingExecutableToUnavailable(t *testing.T) {
	_, err := StartProcess(context.Background(), ProcessConfig{Command: "/path/does/not/exist", Timeout: time.Second})
	if !IsClass(err, ErrorUnavailable) {
		t.Fatalf("StartProcess() error = %v, want unavailable", err)
	}
}

func TestProcessMapsChildExitToUnavailable(t *testing.T) {
	process, err := StartProcess(context.Background(), ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1", "TALARIA_CODEX_PROCESS_EXIT=1"),
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	if !IsClass(process.Err(), ErrorUnavailable) {
		t.Fatalf("process.Err() = %v, want unavailable", process.Err())
	}
	_ = process.Close()
}

func TestCodexProcessHelper(t *testing.T) {
	if os.Getenv("TALARIA_CODEX_PROCESS_HELPER") != "1" {
		return
	}
	if os.Getenv("TALARIA_CODEX_PROCESS_EXIT") == "1" {
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var request map[string]any
		if json.Unmarshal(line, &request) != nil {
			continue
		}
		if _, ok := request["id"]; !ok {
			continue
		}
		if os.Getenv("TALARIA_CODEX_PROCESS_HOLD") == "1" {
			time.Sleep(2 * time.Second)
		}
		if os.Getenv("TALARIA_CODEX_PROCESS_NOTIFY") == "1" {
			notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "turn/started", "params": map[string]any{"threadId": "thread"}})
			_, _ = writer.Write(append(notification, '\n'))
			_ = writer.Flush()
		}
		if os.Getenv("TALARIA_CODEX_PROCESS_REQUEST") == "1" {
			requestMessage, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "command/exec", "params": map[string]any{}})
			_, _ = writer.Write(append(requestMessage, '\n'))
			_ = writer.Flush()
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{}}
		if request["method"] == "initialize" {
			response["result"] = map[string]any{"codexHome": "/tmp", "platformFamily": "unix", "platformOs": "darwin", "userAgent": "test"}
		}
		if request["method"] == "model/list" {
			response["result"] = map[string]any{"data": []map[string]any{{"id": "gpt-5.6-luna", "model": "gpt-5.6-luna", "displayName": "Luna"}}}
		}
		if request["method"] == "thread/fork" {
			response["result"] = map[string]any{"thread": map[string]any{"id": "forked-thread", "turns": []any{}, "items": []any{}}}
		}
		if request["method"] == "thread/read" {
			params, _ := request["params"].(map[string]any)
			threadID, _ := params["threadId"].(string)
			response["result"] = map[string]any{"thread": map[string]any{"id": threadID, "turns": []any{}, "items": []any{}}}
		}
		if request["method"] == "turn/start" {
			mode := os.Getenv("TALARIA_CODEX_CURATOR_MODE")
			if mode == "tool" {
				requestMessage, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 101, "method": "command/exec", "params": map[string]any{}})
				_, _ = writer.Write(append(requestMessage, '\n'))
				_ = writer.Flush()
			}
			output := `{"candidates":[{"kind":"state","title":"Keep this","content":"Remember this detail","tags":["test"]}]}`
			if mode == "invalid" {
				output = `{"candidates":[{"kind":"state","title":"Keep this","content":"Remember this detail"}],"unexpected":true}`
			}
			item, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "item/completed", "params": map[string]any{"item": map[string]any{"type": "agent_message", "text": output}}})
			_, _ = writer.Write(append(item, '\n'))
			completed, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "turn", "items": []any{}}}})
			_, _ = writer.Write(append(completed, '\n'))
			_ = writer.Flush()
			response["result"] = map[string]any{"turn": map[string]any{"id": "turn", "items": []any{}}}
		}
		data, _ := json.Marshal(response)
		_, _ = writer.Write(append(data, '\n'))
		_ = writer.Flush()
	}
	os.Exit(0)
}
