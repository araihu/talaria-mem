package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	workspacepkg "github.com/guilhermecastro/talaria-mem/internal/workspace"
)

type WorkspaceClient interface {
	List(context.Context) ([]domain.Workspace, error)
	Show(context.Context, string) (domain.Workspace, error)
	Bind(context.Context, string, string) (workspacepkg.Binding, error)
	Resolve(context.Context, workspacepkg.ResolutionInput) (workspacepkg.Resolution, error)
	MergeDryRun(context.Context, string, string) (workspacepkg.MergePlan, workspacepkg.MergeReceipt, error)
	MergeApply(context.Context, workspacepkg.MergePlan, workspacepkg.MergeReceipt) error
}

type WorkspaceCommands struct{ Client WorkspaceClient }

func NewWorkspaceCommands(client WorkspaceClient) *WorkspaceCommands {
	return &WorkspaceCommands{Client: client}
}

func (commands *WorkspaceCommands) Run(ctx context.Context, args []string, output Output) error {
	if commands == nil || commands.Client == nil {
		return errCommandUnavailable
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		return output.Result(map[string]any{"version": "talaria.workspace.v1", "commands": []string{"list", "show", "bind", "merge"}}, "workspace commands: list show bind merge")
	}
	flags, positional, err := parseFlags(args[1:])
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		items, err := commands.Client.List(ctx)
		if err != nil {
			return err
		}
		return output.Result(map[string]any{"version": "talaria.workspace-list.v1", "items": items}, "workspaces: %d", len(items))
	case "show":
		id := flags["workspace"]
		if id == "" && len(positional) > 0 {
			id = positional[0]
		}
		if id == "" {
			return &UsageError{Message: "workspace show requires an ID or name"}
		}
		item, err := commands.Client.Show(ctx, id)
		if err != nil {
			return err
		}
		return output.Result(item, "%s (%s)", item.Name, item.ID)
	case "bind":
		key, workspaceID := flags["key"], flags["workspace"]
		if key == "" || workspaceID == "" {
			return &UsageError{Message: "workspace bind requires --key and --workspace"}
		}
		binding, err := commands.Client.Bind(ctx, key, workspaceID)
		if err != nil {
			return err
		}
		return output.Result(binding, "bound %s to %s", binding.Key, binding.WorkspaceID)
	case "merge":
		return commands.merge(ctx, flags, positional, output)
	default:
		return &UsageError{Message: fmt.Sprintf("unknown workspace command %q", args[0])}
	}
}

func (commands *WorkspaceCommands) merge(ctx context.Context, flags map[string]string, positional []string, output Output) error {
	source, target := flags["source"], flags["target"]
	if source == "" && len(positional) > 0 {
		source = positional[0]
	}
	if target == "" && len(positional) > 1 {
		target = positional[1]
	}
	if source == "" || target == "" {
		return &UsageError{Message: "workspace merge requires source and target workspaces"}
	}
	plan, receipt, err := commands.Client.MergeDryRun(ctx, source, target)
	if err != nil {
		return err
	}
	if !boolFlag(flags, "apply") || boolFlag(flags, "dry-run") {
		return output.Result(map[string]any{"plan": plan, "receipt": receipt}, "merge dry-run %s expires %s", receipt.ID, receipt.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	if err := commands.Client.MergeApply(ctx, plan, receipt); err != nil {
		return err
	}
	receipt.Applied = true
	return output.Result(receipt, "merged %s into %s", receipt.SourceWorkspaceID, receipt.TargetWorkspaceID)
}

// StoreClient adapts the explicit workspace ports and merge service. Listing
// remains optional because Store intentionally exposes only read/write seams.
type StoreClient struct {
	Store    workspacepkg.Store
	Resolver *workspacepkg.Resolver
	Binder   *workspacepkg.Binder
	Merge    *workspacepkg.MergeService
	ListFn   func(context.Context) ([]domain.Workspace, error)
}

func (client StoreClient) List(ctx context.Context) ([]domain.Workspace, error) {
	if client.ListFn == nil {
		return nil, errCommandUnavailable
	}
	return client.ListFn(ctx)
}
func (client StoreClient) Show(ctx context.Context, id string) (domain.Workspace, error) {
	if client.Store == nil {
		return domain.Workspace{}, errCommandUnavailable
	}
	item, found, err := client.Store.ReadWorkspace(ctx, id)
	if err != nil {
		return domain.Workspace{}, err
	}
	if !found {
		return domain.Workspace{}, domain.NewError(domain.CodeNotFound, "workspace not found", false)
	}
	return item, nil
}
func (client StoreClient) Bind(ctx context.Context, key, id string) (workspacepkg.Binding, error) {
	if client.Binder == nil {
		return workspacepkg.Binding{}, errCommandUnavailable
	}
	return client.Binder.Bind(ctx, key, id)
}
func (client StoreClient) Resolve(ctx context.Context, input workspacepkg.ResolutionInput) (workspacepkg.Resolution, error) {
	if client.Resolver == nil {
		return workspacepkg.Resolution{}, errCommandUnavailable
	}
	return client.Resolver.Resolve(ctx, input)
}
func (client StoreClient) MergeDryRun(ctx context.Context, source, target string) (workspacepkg.MergePlan, workspacepkg.MergeReceipt, error) {
	if client.Merge == nil {
		return workspacepkg.MergePlan{}, workspacepkg.MergeReceipt{}, errCommandUnavailable
	}
	return client.Merge.DryRun(ctx, source, target, 0)
}
func (client StoreClient) MergeApply(ctx context.Context, plan workspacepkg.MergePlan, receipt workspacepkg.MergeReceipt) error {
	if client.Merge == nil {
		return errCommandUnavailable
	}
	return client.Merge.Apply(ctx, plan, receipt)
}
