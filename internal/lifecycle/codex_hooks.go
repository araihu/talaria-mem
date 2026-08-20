package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	codexHookName           = "SessionStart"
	codexHooksMatcher       = "startup|resume|clear|compact"
	codexHooksStatusMessage = "Loading Talaria-Mem context"
)

var managedCodexHookEvents = []struct {
	name    string
	matcher string
}{
	{name: "SessionStart", matcher: codexHooksMatcher},
	{name: "UserPromptSubmit"},
	{name: "PreCompact"},
	{name: "SessionEnd"},
}

var (
	ErrCodexHooksInvalid = errors.New("Codex hooks configuration is invalid")
	ErrCodexHooksUnsafe  = errors.New("Codex hooks path is unsafe")
)

// CodexHooksInstaller owns only the exact Talaria-Mem SessionStart group in
// Codex's official hooks JSON. Unknown top-level settings, hook groups, and
// command entries remain untouched semantically; collisions fail closed.
type CodexHooksInstaller struct{}

func NewCodexHooksInstaller() *CodexHooksInstaller { return &CodexHooksInstaller{} }

type codexHooksPlan struct {
	path         string
	before       []byte
	beforeExists bool
	beforeMode   fs.FileMode
	after        []byte
	changed      bool
	action       string
	fingerprint  string
}

func (installer *CodexHooksInstaller) Plan(ctx context.Context, request SetupRequest, fingerprint string, remove bool) ([]SetupChange, error) {
	if installer == nil {
		return nil, errors.New("Codex hooks installer unavailable")
	}
	plan, err := installer.prepare(ctx, request, fingerprint, remove)
	if err != nil {
		return nil, err
	}
	if !plan.changed {
		return nil, nil
	}
	return []SetupChange{{Path: plan.path, Action: plan.action, Fingerprint: plan.fingerprint}}, nil
}

func (installer *CodexHooksInstaller) Install(ctx context.Context, request SetupRequest, fingerprint string) error {
	if installer == nil {
		return errors.New("Codex hooks installer unavailable")
	}
	plan, err := installer.prepare(ctx, request, fingerprint, false)
	if err != nil {
		return err
	}
	return installer.apply(ctx, plan)
}

func (installer *CodexHooksInstaller) Remove(ctx context.Context, request SetupRequest, fingerprint string) error {
	if installer == nil {
		return errors.New("Codex hooks installer unavailable")
	}
	plan, err := installer.prepare(ctx, request, fingerprint, true)
	if err != nil {
		return err
	}
	return installer.apply(ctx, plan)
}

func (installer *CodexHooksInstaller) prepare(ctx context.Context, request SetupRequest, fingerprint string, remove bool) (codexHooksPlan, error) {
	if err := contextError(ctx); err != nil {
		return codexHooksPlan{}, err
	}
	if request.CodexHooksPath == "" {
		return codexHooksPlan{}, nil
	}
	if err := validateHookPath(request.HookPath); err != nil {
		return codexHooksPlan{}, err
	}
	if err := validateCodexHooksPath(request.CodexHooksPath); err != nil {
		return codexHooksPlan{}, err
	}
	before, exists, mode, err := readCodexHooks(request.CodexHooksPath)
	if err != nil {
		return codexHooksPlan{}, err
	}
	plan := codexHooksPlan{
		path:         request.CodexHooksPath,
		before:       append([]byte(nil), before...),
		beforeExists: exists,
		beforeMode:   mode,
		fingerprint:  fingerprint,
	}
	if !exists && remove {
		return plan, nil
	}

	var document map[string]json.RawMessage
	var hooks map[string]json.RawMessage
	if !exists {
		document = make(map[string]json.RawMessage)
		hooks = make(map[string]json.RawMessage)
	} else {
		document, hooks, err = decodeCodexHooks(before)
		if err != nil {
			return codexHooksPlan{}, err
		}
	}
	changed := false
	for _, event := range managedCodexHookEvents {
		groups, exists := hooks[event.name]
		var rawGroups []json.RawMessage
		if exists {
			if err := json.Unmarshal(groups, &rawGroups); err != nil || rawGroups == nil {
				return codexHooksPlan{}, ErrCodexHooksInvalid
			}
		}
		managedIndex := -1
		for index, rawGroup := range rawGroups {
			group, err := decodeCodexHookGroup(rawGroup)
			if err != nil {
				return codexHooksPlan{}, err
			}
			managed, err := exactManagedCodexHookForEvent(group, request.HookPath, event.name, event.matcher)
			if err != nil {
				return codexHooksPlan{}, err
			}
			if managed {
				if managedIndex >= 0 {
					return codexHooksPlan{}, ErrSetupCollision
				}
				managedIndex = index
				continue
			}
			collides, err := codexHookCommandCollision(group, request.HookPath)
			if err != nil {
				return codexHooksPlan{}, err
			}
			if collides {
				return codexHooksPlan{}, ErrSetupCollision
			}
		}
		if remove {
			if managedIndex < 0 {
				continue
			}
			rawGroups = append(rawGroups[:managedIndex], rawGroups[managedIndex+1:]...)
			if len(rawGroups) == 0 {
				delete(hooks, event.name)
			} else if encoded, err := json.Marshal(rawGroups); err != nil {
				return codexHooksPlan{}, ErrCodexHooksInvalid
			} else {
				hooks[event.name] = encoded
			}
			changed = true
			plan.action = "unregister"
			continue
		}
		if managedIndex >= 0 {
			continue
		}
		rawGroup, err := json.Marshal(managedCodexHookGroupForEvent(request.HookPath, event.name, event.matcher))
		if err != nil {
			return codexHooksPlan{}, ErrCodexHooksInvalid
		}
		rawGroups = append(rawGroups, rawGroup)
		encoded, err := json.Marshal(rawGroups)
		if err != nil {
			return codexHooksPlan{}, ErrCodexHooksInvalid
		}
		hooks[event.name] = encoded
		changed = true
		plan.action = "register"
	}
	if !changed {
		return plan, nil
	}

	encodedHooks, err := json.Marshal(hooks)
	if err != nil {
		return codexHooksPlan{}, ErrCodexHooksInvalid
	}
	document["hooks"] = encodedHooks
	after, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return codexHooksPlan{}, ErrCodexHooksInvalid
	}
	plan.after = append(after, '\n')
	plan.changed = !bytes.Equal(plan.before, plan.after)
	return plan, nil
}

func (installer *CodexHooksInstaller) apply(ctx context.Context, plan codexHooksPlan) error {
	if !plan.changed {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	current, exists, mode, err := readCodexHooks(plan.path)
	if err != nil {
		return err
	}
	if exists != plan.beforeExists || !bytes.Equal(current, plan.before) || (exists && mode.Perm() != plan.beforeMode.Perm()) {
		return ErrSetupCollision
	}
	if !exists {
		return writeNewCodexHooksFile(ctx, plan.path, plan.after)
	}
	return replaceCodexHooksFile(ctx, plan.path, plan.after, ManagedFileMode, plan.before)
}

// restore reverts one successful official hooks write only when the target is
// still exactly the bytes this installer wrote. It therefore cannot erase a
// concurrent user edit while the caller is rolling back a second artifact.
func (installer *CodexHooksInstaller) restore(ctx context.Context, plan codexHooksPlan) error {
	if installer == nil || !plan.changed {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	current, exists, _, err := readCodexHooks(plan.path)
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(current, plan.after) {
		return ErrSetupCollision
	}
	if !plan.beforeExists {
		if err := os.Remove(plan.path); err != nil {
			return err
		}
		return syncCodexHooksParent(ctx, plan.path)
	}
	return replaceCodexHooksFile(ctx, plan.path, plan.before, plan.beforeMode, plan.after)
}

func decodeCodexHooks(data []byte) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	if len(data) == 0 || domain.RejectDuplicateJSONKeys(data) != nil {
		return nil, nil, ErrCodexHooksInvalid
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil || document == nil {
		return nil, nil, ErrCodexHooksInvalid
	}
	hooks := make(map[string]json.RawMessage)
	if raw, ok := document["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return nil, nil, ErrCodexHooksInvalid
		}
	}
	return document, hooks, nil
}

func decodeCodexHookGroup(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var group map[string]json.RawMessage
	if err := json.Unmarshal(raw, &group); err != nil || group == nil {
		return nil, ErrCodexHooksInvalid
	}
	return group, nil
}

func decodeCodexHookList(group map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	raw, ok := group["hooks"]
	if !ok {
		return nil, ErrCodexHooksInvalid
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, ErrCodexHooksInvalid
	}
	result := make([]map[string]json.RawMessage, 0, len(values))
	for _, value := range values {
		item, err := decodeCodexHookGroup(value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func exactManagedCodexHook(group map[string]json.RawMessage, command string) (bool, error) {
	return exactManagedCodexHookForEvent(group, command, codexHookName, codexHooksMatcher)
}

func exactManagedCodexHookForEvent(group map[string]json.RawMessage, command, event, matcher string) (bool, error) {
	expectedFields := 1
	if matcher != "" {
		expectedFields = 2
	}
	if len(group) != expectedFields {
		return false, nil
	}
	if matcher != "" {
		var configuredMatcher string
		if err := json.Unmarshal(group["matcher"], &configuredMatcher); err != nil || configuredMatcher != matcher {
			return false, nil
		}
	}
	hooks, err := decodeCodexHookList(group)
	if err != nil {
		return false, err
	}
	if len(hooks) != 1 || len(hooks[0]) != 3 {
		return false, nil
	}
	var hookType, hookCommand, statusMessage string
	if json.Unmarshal(hooks[0]["type"], &hookType) != nil || json.Unmarshal(hooks[0]["command"], &hookCommand) != nil || json.Unmarshal(hooks[0]["statusMessage"], &statusMessage) != nil {
		return false, nil
	}
	return hookType == "command" && hookCommand == command && statusMessage == codexHooksStatusMessage, nil
}

func codexHookCommandCollision(group map[string]json.RawMessage, command string) (bool, error) {
	hooks, err := decodeCodexHookList(group)
	if err != nil {
		return false, err
	}
	for _, hook := range hooks {
		var hookType, hookCommand string
		if raw, ok := hook["type"]; ok {
			if err := json.Unmarshal(raw, &hookType); err != nil {
				return false, ErrCodexHooksInvalid
			}
		}
		if raw, ok := hook["command"]; ok {
			if err := json.Unmarshal(raw, &hookCommand); err != nil {
				return false, ErrCodexHooksInvalid
			}
		}
		if hookCommand == command {
			return true, nil
		}
	}
	return false, nil
}

func managedCodexHookGroup(command string) map[string]any {
	return managedCodexHookGroupForEvent(command, codexHookName, codexHooksMatcher)
}

func managedCodexHookGroupForEvent(command, event, matcher string) map[string]any {
	group := map[string]any{
		"hooks": []map[string]string{{
			"type":          "command",
			"command":       command,
			"statusMessage": codexHooksStatusMessage,
		}},
	}
	if matcher != "" {
		group["matcher"] = matcher
	}
	return group
}

func validateCodexHooksPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrCodexHooksUnsafe
	}
	parentInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() || parentInfo.Mode().Perm()&0o022 != 0 || !currentUserOwns(parentInfo) {
		return ErrCodexHooksUnsafe
	}
	return nil
}

func readCodexHooks(path string) ([]byte, bool, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, ErrCodexHooksUnsafe
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !currentUserOwns(info) || info.Mode().Perm()&0o022 != 0 {
		return nil, false, 0, ErrCodexHooksUnsafe
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, ErrCodexHooksUnsafe
	}
	return value, true, info.Mode(), nil
}

func replaceCodexHooksFile(ctx context.Context, path string, value []byte, finalMode fs.FileMode, expectedValue []byte) error {
	current, exists, mode, err := readCodexHooks(path)
	if err != nil || !exists {
		return ErrSetupCollision
	}
	if expectedValue != nil && !bytes.Equal(current, expectedValue) {
		return ErrSetupCollision
	}
	if err := os.Chmod(path, ManagedFileMode.Perm()); err != nil {
		return ErrCodexHooksUnsafe
	}
	replaced := false
	defer func() {
		if !replaced && mode.Perm() != ManagedFileMode.Perm() {
			_ = os.Chmod(path, mode.Perm())
		}
	}()
	current, exists, _, err = readCodexHooks(path)
	if err != nil || !exists || (expectedValue != nil && !bytes.Equal(current, expectedValue)) {
		return ErrSetupCollision
	}
	temporary, err := writeCodexHooksTemp(ctx, path, value)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporary)
		}
	}()
	current, exists, _, err = readCodexHooks(path)
	if err != nil || !exists || (expectedValue != nil && !bytes.Equal(current, expectedValue)) {
		return ErrSetupCollision
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("Codex hooks replace failed")
	}
	keep = true
	replaced = true
	if err := os.Chmod(path, finalMode.Perm()); err != nil {
		return ErrCodexHooksUnsafe
	}
	return syncCodexHooksParent(ctx, path)
}

func writeNewCodexHooksFile(ctx context.Context, path string, value []byte) error {
	temporary, err := writeCodexHooksTemp(ctx, path, value)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporary)
		}
	}()
	if err := contextError(ctx); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !currentUserOwns(info) {
			return ErrCodexHooksUnsafe
		}
		return ErrSetupCollision
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrCodexHooksUnsafe
	}
	if err := os.Link(temporary, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrSetupCollision
		}
		return errors.New("Codex hooks create failed")
	}
	if err := os.Remove(temporary); err != nil {
		return errors.New("Codex hooks create failed")
	}
	keep = true
	return syncCodexHooksParent(ctx, path)
}

func writeCodexHooksTemp(ctx context.Context, path string, value []byte) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".talaria-codex-hooks-")
	if err != nil {
		return "", ErrCodexHooksUnsafe
	}
	temporary := file.Name()
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(ManagedFileMode.Perm()); err != nil {
		return "", ErrCodexHooksUnsafe
	}
	if _, err := file.Write(value); err != nil {
		return "", errors.New("Codex hooks write failed")
	}
	if err := file.Sync(); err != nil {
		return "", errors.New("Codex hooks write failed")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("Codex hooks write failed")
	}
	keep = true
	return temporary, nil
}

func syncCodexHooksParent(ctx context.Context, path string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errors.New("Codex hooks directory sync failed")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("Codex hooks directory sync failed")
	}
	return nil
}
