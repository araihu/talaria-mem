package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type BindingKind string

const (
	BindingExplicit        BindingKind = "explicit"
	BindingGitRemote       BindingKind = "git_remote"
	BindingRootFingerprint BindingKind = "root_fingerprint"
	BindingAbsolutePath    BindingKind = "absolute_path"
)

func (kind BindingKind) Valid() bool {
	return kind == BindingExplicit || kind == BindingGitRemote || kind == BindingRootFingerprint || kind == BindingAbsolutePath
}

type Binding struct {
	Key                  string
	WorkspaceID          string
	Kind                 BindingKind
	FirstInferenceWarned bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ResolutionInput struct {
	ExplicitWorkspaceID   string
	ExplicitWorkspaceName string
	BindingKey            string
	GitRemote             string
	RepositoryRoot        string
	RootFingerprint       string
}

type Resolution struct {
	Workspace       domain.Workspace
	Binding         Binding
	Inferred        bool
	FirstUseWarning string
}

type Store interface {
	ReadWorkspace(ctx context.Context, idOrName string) (domain.Workspace, bool, error)
	CreateWorkspace(ctx context.Context, workspace domain.Workspace) error
	ReadBinding(ctx context.Context, key string) (Binding, bool, error)
	SaveBinding(ctx context.Context, binding Binding) error
}

type Resolver struct {
	Store Store
	Clock func() time.Time
}

func NewResolver(store Store, clock func() time.Time) *Resolver {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Resolver{Store: store, Clock: clock}
}

func (resolver *Resolver) Resolve(ctx context.Context, input ResolutionInput) (Resolution, error) {
	if resolver == nil || resolver.Store == nil {
		return Resolution{}, domain.NewError(domain.CodeUnavailable, "workspace store unavailable", true)
	}
	now := resolver.Clock().UTC()
	if input.ExplicitWorkspaceID != "" || input.ExplicitWorkspaceName != "" {
		key := input.ExplicitWorkspaceID
		if key == "" {
			key = input.ExplicitWorkspaceName
		}
		workspace, found, err := resolver.Store.ReadWorkspace(ctx, key)
		if err != nil {
			return Resolution{}, err
		}
		if !found {
			return Resolution{}, domain.NewError(domain.CodeNotFound, "workspace not found", false)
		}
		bindingKey := input.BindingKey
		if bindingKey == "" {
			bindingKey = "explicit:" + workspace.ID
		}
		binding := Binding{Key: bindingKey, WorkspaceID: workspace.ID, Kind: BindingExplicit, CreatedAt: now, UpdatedAt: now, FirstInferenceWarned: true}
		if existing, found, err := resolver.Store.ReadBinding(ctx, bindingKey); err == nil && found {
			binding = existing
			binding.WorkspaceID = workspace.ID
			binding.Kind = BindingExplicit
			binding.UpdatedAt = now
		}
		if err := resolver.Store.SaveBinding(ctx, binding); err != nil {
			return Resolution{}, err
		}
		return Resolution{Workspace: workspace, Binding: binding}, nil
	}
	key, kind, err := bindingIdentity(input)
	if err != nil {
		return Resolution{}, err
	}
	if key == "" {
		return Resolution{}, domain.NewError(domain.CodeValidation, "workspace binding input is required", false)
	}
	if binding, found, err := resolver.Store.ReadBinding(ctx, key); err != nil {
		return Resolution{}, err
	} else if found {
		workspace, found, err := resolver.Store.ReadWorkspace(ctx, binding.WorkspaceID)
		if err != nil {
			return Resolution{}, err
		}
		if !found {
			return Resolution{}, domain.NewError(domain.CodeUnavailable, "workspace binding target missing", false)
		}
		warning := ""
		if !binding.FirstInferenceWarned && binding.Kind != BindingExplicit {
			warning = fmt.Sprintf("Workspace inferred as %s. Use --workspace %s or workspace bind to select an existing workspace.", workspace.Name, workspace.Name)
			binding.FirstInferenceWarned = true
			binding.UpdatedAt = now
			if err := resolver.Store.SaveBinding(ctx, binding); err != nil {
				return Resolution{}, err
			}
		}
		return Resolution{Workspace: workspace, Binding: binding, FirstUseWarning: warning}, nil
	}
	name := key
	workspace, found, err := resolver.Store.ReadWorkspace(ctx, name)
	if err != nil {
		return Resolution{}, err
	}
	if !found {
		id, err := newWorkspaceID(now)
		if err != nil {
			return Resolution{}, err
		}
		workspace = domain.Workspace{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
		if err := resolver.Store.CreateWorkspace(ctx, workspace); err != nil {
			return Resolution{}, err
		}
	}
	binding := Binding{Key: key, WorkspaceID: workspace.ID, Kind: kind, CreatedAt: now, UpdatedAt: now}
	warning := fmt.Sprintf("Workspace inferred as %s. Use --workspace %s or workspace bind to select an existing workspace.", workspace.Name, workspace.Name)
	binding.FirstInferenceWarned = true
	if err := resolver.Store.SaveBinding(ctx, binding); err != nil {
		return Resolution{}, err
	}
	return Resolution{Workspace: workspace, Binding: binding, Inferred: true, FirstUseWarning: warning}, nil
}

func bindingIdentity(input ResolutionInput) (string, BindingKind, error) {
	if input.BindingKey != "" {
		return input.BindingKey, BindingExplicit, nil
	}
	if input.GitRemote != "" {
		normalized, err := NormalizeGitRemote(input.GitRemote)
		if err != nil {
			return "", "", err
		}
		return normalized, BindingGitRemote, nil
	}
	if input.RootFingerprint != "" {
		if len(input.RootFingerprint) > 256 {
			return "", "", domain.NewError(domain.CodeValidation, "root fingerprint too long", false)
		}
		return "root:" + input.RootFingerprint, BindingRootFingerprint, nil
	}
	if input.RepositoryRoot != "" {
		absolute, err := filepath.Abs(input.RepositoryRoot)
		if err != nil {
			return "", "", domain.NewError(domain.CodeValidation, "repository root invalid", false)
		}
		return "path:" + filepath.Clean(absolute), BindingAbsolutePath, nil
	}
	return "", "", nil
}

func NormalizeGitRemote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return "", domain.NewError(domain.CodeValidation, "git remote invalid", false)
	}
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(raw[4:], ":", 2)
		if len(parts) != 2 {
			return "", domain.NewError(domain.CodeValidation, "git remote invalid", false)
		}
		raw = "ssh://" + parts[0] + "/" + parts[1]
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", domain.NewError(domain.CodeValidation, "git remote invalid", false)
	}
	if parsed.User != nil {
		return "", domain.NewError(domain.CodeValidation, "git remote credentials are forbidden", false)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return "", domain.NewError(domain.CodeValidation, "git remote host missing", false)
	}
	path := strings.Trim(parsed.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	segments := strings.Split(path, "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", domain.NewError(domain.CodeValidation, "git remote must contain owner and repository", false)
	}
	return strings.ToLower(host + "/" + segments[0] + "/" + segments[1]), nil
}

type MemoryStore struct {
	mu         sync.Mutex
	workspaces map[string]domain.Workspace
	bindings   map[string]Binding
	redirects  map[string]string
	memories   map[string][]MemoryRecord
	aliases    map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{workspaces: map[string]domain.Workspace{}, bindings: map[string]Binding{}, redirects: map[string]string{}, memories: map[string][]MemoryRecord{}, aliases: map[string]string{}}
}

func (store *MemoryStore) ReadWorkspace(ctx context.Context, idOrName string) (domain.Workspace, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, workspace := range store.workspaces {
		if workspace.ID == idOrName || workspace.Name == idOrName {
			return workspace, true, nil
		}
	}
	return domain.Workspace{}, false, nil
}
func (store *MemoryStore) CreateWorkspace(ctx context.Context, workspace domain.Workspace) error {
	if workspace.ID == "" || workspace.Name == "" {
		return domain.NewError(domain.CodeValidation, "workspace identity is required", false)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, existing := range store.workspaces {
		if existing.ID == workspace.ID || existing.Name == workspace.Name {
			return domain.NewError(domain.CodeRevisionConflict, "workspace already exists", false)
		}
	}
	store.workspaces[workspace.ID] = workspace
	return nil
}
func (store *MemoryStore) ReadBinding(ctx context.Context, key string) (Binding, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	binding, ok := store.bindings[key]
	return binding, ok, nil
}
func (store *MemoryStore) SaveBinding(ctx context.Context, binding Binding) error {
	if binding.Key == "" || binding.WorkspaceID == "" || !binding.Kind.Valid() {
		return domain.NewError(domain.CodeValidation, "invalid workspace binding", false)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.bindings[binding.Key]; ok {
		binding.FirstInferenceWarned = existing.FirstInferenceWarned || binding.FirstInferenceWarned
		if binding.CreatedAt.IsZero() {
			binding.CreatedAt = existing.CreatedAt
		}
	}
	store.bindings[binding.Key] = binding
	return nil
}

func newWorkspaceID(now time.Time) (string, error) {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d", now.UnixNano())))
	return fmt.Sprintf("workspace-%s", hex.EncodeToString(digest[:12])), nil
}

var _ = errors.Is
