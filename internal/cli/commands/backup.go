package commands

import (
	"context"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/maintenance"
)

type BackupReconciler interface {
	ReconcileDryRun(context.Context) (maintenance.Receipt, string, error)
	ReconcileApply(context.Context, string) error
}

type BackupCommands struct {
	Reconciler BackupReconciler
}

func NewBackupCommands(reconciler BackupReconciler) *BackupCommands {
	return &BackupCommands{Reconciler: reconciler}
}

func (commands *BackupCommands) Reconcile(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Reconciler == nil {
		return &cli.UsageError{Message: "backup reconciliation unavailable"}
	}
	if len(args) == 0 {
		return &cli.UsageError{Message: "usage: db backup reconcile --dry-run|--apply <receipt>"}
	}
	switch args[0] {
	case "--dry-run":
		if len(args) != 1 {
			return &cli.UsageError{Message: "usage: db backup reconcile --dry-run"}
		}
		receipt, path, err := commands.Reconciler.ReconcileDryRun(ctx)
		if err != nil {
			return err
		}
		return output.Result(map[string]any{
			"version":    "talaria.maintenance.receipt.v1",
			"kind":       receipt.Kind,
			"receipt":    path,
			"expires_at": receipt.ExpiresAt,
		}, "receipt=%s", path)
	case "--apply":
		if len(args) != 2 || args[1] == "" {
			return &cli.UsageError{Message: "usage: db backup reconcile --apply <receipt>"}
		}
		if err := commands.Reconciler.ReconcileApply(ctx, args[1]); err != nil {
			return err
		}
		return output.Result(map[string]any{"version": "talaria.maintenance.apply.v1", "receipt": args[1], "status": "complete"}, "backup reconciliation complete")
	default:
		return &cli.UsageError{Message: "usage: db backup reconcile --dry-run|--apply <receipt>"}
	}
}
