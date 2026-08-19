package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Method struct {
	Name   string
	Params string
	Result string
}

type Manifest struct {
	Methods []Method
}

var wireMethodName = regexp.MustCompile(`^[a-z][a-z0-9]*(/[a-z][a-z0-9]*)?$`)
var schemaName = regexp.MustCompile(`^(v1|v2)/[A-Z][A-Za-z0-9]*$`)

func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read method manifest: %w", err)
	}
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Manifest{}, fmt.Errorf("decode method manifest: %w", err)
	}
	if raw == nil {
		return Manifest{}, errors.New("method manifest must be an object")
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	manifest := Manifest{Methods: make([]Method, 0, len(names))}
	for _, name := range names {
		if !wireMethodName.MatchString(name) {
			return Manifest{}, fmt.Errorf("invalid wire method name %q", name)
		}
		var types []string
		if err := json.Unmarshal(raw[name], &types); err != nil || len(types) != 2 {
			return Manifest{}, fmt.Errorf("method %q must map to [params, result]", name)
		}
		if !schemaName.MatchString(types[0]) || !schemaName.MatchString(types[1]) {
			return Manifest{}, fmt.Errorf("method %q has invalid schema roots", name)
		}
		manifest.Methods = append(manifest.Methods, Method{Name: name, Params: types[0], Result: types[1]})
	}
	if len(manifest.Methods) == 0 {
		return Manifest{}, errors.New("method manifest is empty")
	}
	return manifest, nil
}

func BuildCuratedSchema(schemaDir string, manifest Manifest) ([]byte, error) {
	if schemaDir == "" {
		return nil, errors.New("schema directory is required")
	}
	absoluteSchemaDir, err := filepath.Abs(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("resolve schema directory: %w", err)
	}
	for _, method := range manifest.Methods {
		for _, root := range []string{method.Params, method.Result} {
			path := filepath.Join(absoluteSchemaDir, root+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read schema root %q: %w", root, err)
			}
			var document struct {
				Title string `json:"title"`
			}
			if err := json.Unmarshal(data, &document); err != nil {
				return nil, fmt.Errorf("decode schema root %q: %w", root, err)
			}
			wantTitle := filepath.Base(root)
			if document.Title != wantTitle {
				return nil, fmt.Errorf("schema root %q has title %q, want %q", root, document.Title, wantTitle)
			}
		}
	}

	definitions := map[string]any{}
	addObject := func(name string, properties map[string]any, required ...string) {
		node := map[string]any{"title": name, "type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			node["required"] = required
		}
		definitions[name] = node
	}
	stringField := func() any { return map[string]any{"type": "string"} }
	boolField := func() any { return map[string]any{"type": "boolean"} }
	intField := func() any { return map[string]any{"type": "integer", "format": "int64"} }
	ref := func(name string) any { return map[string]any{"$ref": "#/definitions/" + name} }
	array := func(item any) any { return map[string]any{"type": "array", "items": item} }
	mapField := func() any { return map[string]any{"type": "object", "additionalProperties": true} }
	optional := func(node any) any { return map[string]any{"anyOf": []any{node, map[string]any{"type": "null"}}} }

	addObject("ClientInfo", map[string]any{"name": stringField(), "title": optional(stringField()), "version": optional(stringField())}, "name")
	addObject("InitializeCapabilities", map[string]any{"experimentalApi": optional(boolField())})
	addObject("InitializeParams", map[string]any{"clientInfo": ref("ClientInfo"), "capabilities": ref("InitializeCapabilities")}, "clientInfo")
	addObject("InitializeResponse", map[string]any{"codexHome": stringField(), "platformFamily": stringField(), "platformOs": stringField(), "userAgent": stringField()}, "codexHome", "platformFamily", "platformOs", "userAgent")

	addObject("ModelListParams", map[string]any{"cursor": optional(stringField()), "includeHidden": optional(boolField()), "limit": optional(intField())})
	addObject("ReasoningEffortOption", map[string]any{"reasoningEffort": stringField()}, "reasoningEffort")
	addObject("Model", map[string]any{
		"id": stringField(), "model": stringField(), "displayName": stringField(), "hidden": boolField(), "isDefault": boolField(),
		"supportedReasoningEfforts": array(ref("ReasoningEffortOption")),
	}, "id", "model", "displayName")
	addObject("ModelListResponse", map[string]any{"data": array(ref("Model")), "nextCursor": optional(stringField())}, "data")

	addObject("ThreadReadParams", map[string]any{"threadId": stringField(), "includeTurns": optional(boolField())}, "threadId")
	addObject("ThreadItem", map[string]any{"type": stringField(), "text": optional(stringField()), "content": optional(stringField()), "message": optional(stringField()), "status": optional(stringField()), "name": optional(stringField())}, "type")
	addObject("Turn", map[string]any{"id": stringField(), "status": optional(stringField()), "items": array(ref("ThreadItem"))}, "id")
	addObject("ThreadSnapshot", map[string]any{"id": stringField(), "cwd": optional(stringField()), "turns": array(ref("Turn")), "items": array(ref("ThreadItem"))}, "id")
	addObject("ThreadReadResponse", map[string]any{"thread": ref("ThreadSnapshot")}, "thread")

	addObject("ThreadForkParams", map[string]any{
		"threadId": stringField(), "lastTurnId": optional(stringField()), "cwd": optional(stringField()), "model": optional(stringField()), "modelProvider": optional(stringField()), "ephemeral": optional(boolField()),
		"sandbox": optional(stringField()), "approvalPolicy": optional(stringField()), "baseInstructions": optional(stringField()), "developerInstructions": optional(stringField()), "config": optional(mapField()),
	}, "threadId")
	addObject("ThreadForkResponse", map[string]any{"thread": ref("ThreadSnapshot"), "model": optional(stringField()), "reasoningEffort": optional(stringField()), "cwd": optional(stringField())}, "thread")
	addObject("ThreadStartParams", map[string]any{
		"cwd": optional(stringField()), "model": optional(stringField()), "modelProvider": optional(stringField()), "ephemeral": optional(boolField()), "sandbox": optional(stringField()), "approvalPolicy": optional(stringField()), "baseInstructions": optional(stringField()), "developerInstructions": optional(stringField()), "config": optional(mapField()),
	})
	addObject("ThreadStartResponse", map[string]any{"thread": ref("ThreadSnapshot"), "model": optional(stringField()), "reasoningEffort": optional(stringField()), "cwd": optional(stringField())}, "thread")
	addObject("UserInput", map[string]any{"type": stringField(), "text": optional(stringField())}, "type")
	addObject("TurnStartParams", map[string]any{
		"threadId": stringField(), "input": array(ref("UserInput")), "cwd": optional(stringField()), "model": optional(stringField()), "effort": optional(stringField()), "approvalPolicy": optional(stringField()), "sandboxPolicy": optional(mapField()), "outputSchema": optional(mapField()),
	}, "threadId", "input")
	addObject("TurnStartResponse", map[string]any{"turn": ref("Turn")}, "turn")

	rootProperties := make(map[string]any, len(manifest.Methods)*2)
	for _, method := range manifest.Methods {
		rootProperties[strings.ReplaceAll(method.Name, "/", "_")+"Params"] = ref(filepath.Base(method.Params))
		rootProperties[strings.ReplaceAll(method.Name, "/", "_")+"Result"] = ref(filepath.Base(method.Result))
	}
	document := map[string]any{"$schema": "http://json-schema.org/draft-07/schema#", "title": "CuratedProtocol", "type": "object", "properties": rootProperties, "definitions": definitions}
	return json.MarshalIndent(document, "", "  ")
}

func Generate(schemaDir, manifestPath, outputDir string) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	curated, err := BuildCuratedSchema(schemaDir, manifest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary, err := os.MkdirTemp("", "talaria-codex-schema-")
	if err != nil {
		return fmt.Errorf("create generator temp directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	schemaPath := filepath.Join(temporary, "curated.schema.json")
	if err := os.WriteFile(schemaPath, curated, 0o600); err != nil {
		return fmt.Errorf("write curated schema: %w", err)
	}
	modelsPath := filepath.Join(outputDir, "models.gen.go")
	command := exec.Command("go", "tool", "github.com/atombender/go-jsonschema", "--only-models", "--struct-name-from-title", "--capitalization", "ID", "--package", "protocol", "-o", modelsPath, schemaPath)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run go-jsonschema: %w", err)
	}
	if err := writeFile(filepath.Join(outputDir, "client.gen.go"), renderClient(manifest)); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outputDir, "notifications.gen.go"), renderNotifications()); err != nil {
		return err
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write generated file %s: %w", path, err)
	}
	return nil
}

func renderClient(manifest Manifest) []byte {
	var builder strings.Builder
	builder.WriteString("// Code generated by cmd/codexrpcgen; DO NOT EDIT.\n\npackage protocol\n\nimport (\n\t\"context\"\n\n\t\"github.com/sourcegraph/jsonrpc2\"\n)\n\ntype Client struct {\n\tconn *jsonrpc2.Conn\n}\n\nfunc NewClient(conn *jsonrpc2.Conn) *Client { return &Client{conn: conn} }\n\n")
	for _, method := range manifest.Methods {
		params := filepath.Base(method.Params)
		result := filepath.Base(method.Result)
		name := exportedMethodName(method.Name)
		fmt.Fprintf(&builder, "func (client *Client) %s(ctx context.Context, params %s) (%s, error) {\n\tvar result %s\n\tif err := client.conn.Call(ctx, %q, params, &result); err != nil {\n\t\treturn %s{}, err\n\t}\n\treturn result, nil\n}\n\n", name, params, result, result, method.Name, result)
	}
	return []byte(builder.String())
}

func exportedMethodName(method string) string {
	parts := strings.Split(method, "/")
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func renderNotifications() []byte {
	return []byte("// Code generated by cmd/codexrpcgen; DO NOT EDIT.\n\n" +
		"package protocol\n\n" +
		"import \"encoding/json\"\n\n" +
		"const (\n" +
		"\tNotificationTurnStarted      = \"turn/started\"\n" +
		"\tNotificationAgentMessageDone = \"item/completed\"\n" +
		"\tNotificationTurnCompleted   = \"turn/completed\"\n" +
		"\tNotificationError           = \"error\"\n" +
		")\n\n" +
		"type Notification struct {\n" +
		"\tMethod string          `json:\"method\"`\n" +
		"\tParams json.RawMessage `json:\"params\"`\n" +
		"}\n\n" +
		"func (notification Notification) IsCompletion() bool {\n" +
		"\treturn notification.Method == NotificationAgentMessageDone || notification.Method == NotificationTurnCompleted\n" +
		"}\n")
}

func main() {
	schemaDir := flag.String("schema", "", "checked-in Codex schema directory")
	manifestPath := flag.String("manifest", "", "method manifest")
	outputDir := flag.String("out", "", "generated output directory")
	flag.Parse()
	if *schemaDir == "" || *manifestPath == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "usage: codexrpcgen -schema DIR -manifest FILE -out DIR")
		os.Exit(2)
	}
	if err := Generate(*schemaDir, *manifestPath, *outputDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
