package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	codexMCPBegin = "# talaria-mem:mcp-begin"
	codexMCPEnd   = "# talaria-mem:mcp-end"
)

type codexMCPPlan struct {
	path         string
	before       []byte
	beforeExists bool
	beforeMode   fs.FileMode
	after        []byte
	changed      bool
	remove       bool
}

type CodexMCPInstaller struct{}

func NewCodexMCPInstaller() *CodexMCPInstaller { return &CodexMCPInstaller{} }

func (installer *CodexMCPInstaller) Plan(ctx context.Context, request SetupRequest, fingerprint string, remove bool) ([]SetupChange, error) {
	plan, err := installer.prepare(ctx, request, fingerprint, remove)
	if err != nil {
		return nil, err
	}
	if !plan.changed {
		return nil, nil
	}
	action := "install"
	if remove {
		action = "remove"
	}
	return []SetupChange{{Path: plan.path, Action: action, Fingerprint: fingerprint}}, nil
}

func (installer *CodexMCPInstaller) Install(ctx context.Context, request SetupRequest, fingerprint string) error {
	plan, err := installer.prepare(ctx, request, fingerprint, false)
	if err != nil {
		return err
	}
	return installer.apply(ctx, plan)
}

func (installer *CodexMCPInstaller) Remove(ctx context.Context, request SetupRequest, fingerprint string) error {
	plan, err := installer.prepare(ctx, request, fingerprint, true)
	if err != nil {
		return err
	}
	return installer.apply(ctx, plan)
}

func (installer *CodexMCPInstaller) prepare(ctx context.Context, request SetupRequest, fingerprint string, remove bool) (codexMCPPlan, error) {
	if installer == nil {
		return codexMCPPlan{}, errors.New("Codex MCP installer unavailable")
	}
	if request.CodexConfigPath == "" {
		return codexMCPPlan{}, nil
	}
	if err := contextError(ctx); err != nil {
		return codexMCPPlan{}, err
	}
	if err := validateCodexConfigPath(request.CodexConfigPath); err != nil {
		return codexMCPPlan{}, err
	}
	before, exists, mode, err := readCodexConfig(request.CodexConfigPath)
	if err != nil {
		return codexMCPPlan{}, err
	}
	plan := codexMCPPlan{path: request.CodexConfigPath, before: append([]byte(nil), before...), beforeExists: exists, beforeMode: mode, remove: remove}
	block := managedMCPBlock(request.BinaryPath, fingerprint)
	start, end, managedFingerprint := findManagedMCPBlock(before)
	if start >= 0 {
		if managedFingerprint != fingerprint || string(before[start:end]) != block {
			return codexMCPPlan{}, ErrSetupCollision
		}
		if remove {
			plan.after = append(append([]byte(nil), before[:start]...), before[end:]...)
			plan.changed = true
		}
		return plan, nil
	}
	if remove {
		return plan, nil
	}
	if bytes.Contains(before, []byte("[mcp_servers.talaria_mem]")) {
		return codexMCPPlan{}, ErrSetupCollision
	}
	plan.after = append([]byte(nil), before...)
	if len(plan.after) > 0 && plan.after[len(plan.after)-1] != '\n' {
		plan.after = append(plan.after, '\n')
	}
	plan.after = append(plan.after, []byte(block)...)
	plan.changed = true
	return plan, nil
}

func (installer *CodexMCPInstaller) apply(ctx context.Context, plan codexMCPPlan) error {
	if !plan.changed {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	current, exists, mode, err := readCodexConfig(plan.path)
	if err != nil || exists != plan.beforeExists || !bytes.Equal(current, plan.before) {
		return ErrSetupCollision
	}
	if plan.remove {
		if !exists {
			return ErrSetupCollision
		}
		if err := writeOwnerFileNoFollow(plan.path, plan.after, plan.beforeMode.Perm()); err != nil {
			return errors.New("Codex MCP config update failed")
		}
		return nil
	}
	if !exists {
		file, err := os.OpenFile(plan.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, ManagedFileMode.Perm())
		if err != nil {
			return ErrSetupCollision
		}
		if _, err := file.Write(plan.after); err != nil {
			_ = file.Close()
			_ = os.Remove(plan.path)
			return errors.New("Codex MCP config create failed")
		}
		if err := file.Close(); err != nil {
			return errors.New("Codex MCP config create failed")
		}
		return nil
	}
	if err := writeOwnerFileNoFollow(plan.path, plan.after, mode.Perm()); err != nil {
		return errors.New("Codex MCP config update failed")
	}
	return nil
}

func (installer *CodexMCPInstaller) restore(ctx context.Context, plan codexMCPPlan) error {
	if installer == nil || !plan.changed {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	current, exists, _, err := readCodexConfig(plan.path)
	if err != nil || !exists || !bytes.Equal(current, plan.after) {
		return ErrSetupCollision
	}
	if !plan.beforeExists {
		return os.Remove(plan.path)
	}
	return writeOwnerFileNoFollow(plan.path, plan.before, plan.beforeMode.Perm())
}

func writeOwnerFileNoFollow(path string, value []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func managedMCPBlock(binaryPath, fingerprint string) string {
	return strings.Join([]string{
		codexMCPBegin + " fingerprint=" + fingerprint,
		"[mcp_servers.talaria_mem]",
		"command = " + strconv.Quote(binaryPath),
		"args = [\"mcp\", \"proxy\"]",
		codexMCPEnd,
		"",
	}, "\n")
}

func findManagedMCPBlock(value []byte) (start, end int, fingerprint string) {
	text := string(value)
	start = strings.Index(text, codexMCPBegin)
	if start < 0 {
		return -1, -1, ""
	}
	endMarker := strings.Index(text[start:], codexMCPEnd)
	if endMarker < 0 {
		return -1, -1, ""
	}
	end = start + endMarker + len(codexMCPEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	lineEnd := strings.IndexByte(text[start:], '\n')
	if lineEnd < 0 {
		return -1, -1, ""
	}
	line := text[start : start+lineEnd]
	parts := strings.SplitN(line, "fingerprint=", 2)
	if len(parts) != 2 || len(parts[1]) != 64 {
		return -1, -1, ""
	}
	return start, end, parts[1]
}

func validateCodexConfigPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrCodexHooksUnsafe
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || parent.Mode()&os.ModeSymlink != 0 || !parent.IsDir() || parent.Mode().Perm()&0o022 != 0 || !currentUserOwns(parent) {
		return ErrCodexHooksUnsafe
	}
	return nil
}

func readCodexConfig(path string) ([]byte, bool, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || !currentUserOwns(info) {
		return nil, false, 0, ErrCodexHooksUnsafe
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, ErrCodexHooksUnsafe
	}
	return data, true, info.Mode(), nil
}
