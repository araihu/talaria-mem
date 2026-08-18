package commands

import (
	"context"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/maintenance"
)

type RestoreOperator interface {
	DryRun(context.Context, string) (maintenance.Receipt, string, error)
	Apply(context.Context, string) error
}

type DBCommands struct {
	Backup  *BackupCommands
	Restore RestoreOperator
}

func NewDBCommands(backup *BackupCommands, restore RestoreOperator) *DBCommands {
	return &DBCommands{Backup: backup, Restore: restore}
}

func (commands *DBCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || len(args) == 0 {
		return &cli.UsageError{Message: "usage: db backup reconcile|restore"}
	}
	switch args[0] {
	case "backup":
		if commands.Backup == nil || len(args) < 2 || args[1] != "reconcile" {
			return &cli.UsageError{Message: "usage: db backup reconcile --dry-run|--apply <receipt>"}
		}
		return commands.Backup.Reconcile(ctx, args[2:], output)
	case "restore":
		if commands.Restore == nil {
			return &cli.UsageError{Message: "database restore unavailable"}
		}
		return commands.runRestore(ctx, args[1:], output)
	default:
		return &cli.UsageError{Message: "usage: db backup reconcile|restore"}
	}
}

func (commands *DBCommands) runRestore(ctx context.Context, args []string, output cli.Output) error {
	var backupID, applyPath string
	dryRun := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--backup":
			if index+1 >= len(args) {
				return &cli.UsageError{Message: "backup ID is required"}
			}
			backupID = args[index+1]
			index++
		case "--dry-run":
			dryRun = true
		case "--apply":
			if index+1 >= len(args) {
				return &cli.UsageError{Message: "receipt path is required"}
			}
			applyPath = args[index+1]
			index++
		default:
			return &cli.UsageError{Message: "usage: db restore --backup <id> --dry-run|--apply <receipt>"}
		}
	}
	if dryRun {
		if backupID == "" || applyPath != "" {
			return &cli.UsageError{Message: "usage: db restore --backup <id> --dry-run"}
		}
		receipt, receiptPath, err := commands.Restore.DryRun(ctx, backupID)
		if err != nil {
			return err
		}
		return output.Result(map[string]any{"version": "talaria.restore.receipt.v1", "receipt": receiptPath, "expires_at": receipt.ExpiresAt}, "receipt=%s", receiptPath)
	}
	if applyPath == "" || backupID != "" {
		return &cli.UsageError{Message: "usage: db restore --apply <receipt>"}
	}
	if err := commands.Restore.Apply(ctx, applyPath); err != nil {
		return err
	}
	return output.Result(map[string]any{"version": "talaria.restore.v1", "receipt": applyPath, "status": "complete"}, "database restore complete")
}

func RegisterDB(registry *cli.Registry, commands *DBCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "database command unavailable"}
	}
	return registry.Register(cli.Command{Name: "db", Description: "database maintenance", Run: commands.Run, Build: dbCobraCommand})
}
