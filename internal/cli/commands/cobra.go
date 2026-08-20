package commands

import (
	"github.com/spf13/cobra"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
)

func invoke(context cli.CobraContext, command *cobra.Command, args []string) error {
	return context.Run(command.Context(), args, context.Output(command))
}

func appendStringFlag(args []string, command *cobra.Command, name string) []string {
	if !command.Flags().Changed(name) {
		return args
	}
	value, _ := command.Flags().GetString(name)
	return append(args, "--"+name, value)
}

func appendBoolFlag(args []string, command *cobra.Command, name string) []string {
	if !command.Flags().Changed(name) {
		return args
	}
	value, _ := command.Flags().GetBool(name)
	if value {
		return append(args, "--"+name)
	}
	return args
}

func daemonCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{
		Use:   "daemon",
		Short: "run the local loopback daemon",
		Args:  cobra.NoArgs,
	}
	command.Flags().Bool("foreground", false, "run in the foreground")
	command.Flags().Bool("diagnostic", false, "run with diagnostic output")
	command.Flags().String("address", "", "loopback listen address")
	command.RunE = func(command *cobra.Command, _ []string) error {
		if command.Flags().Changed("address") {
			return &cli.UsageError{Message: "daemon --address must be supplied before runtime composition"}
		}
		args := []string{}
		args = appendBoolFlag(args, command, "foreground")
		args = appendBoolFlag(args, command, "diagnostic")
		return invoke(context, command, args)
	}
	return command
}

func setupCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{Use: "setup", Short: "install local integrations", Args: cobra.NoArgs}
	codex := &cobra.Command{Use: "codex", Short: "install the Codex SessionStart hook", Args: cobra.NoArgs}
	for _, name := range []string{"config", "talaria-config", "codex-config", "hook", "codex-hooks", "provider-config", "token", "binary", "endpoint", "fingerprint"} {
		codex.Flags().String(name, "", "")
	}
	for _, name := range []string{"dry-run", "apply", "remove"} {
		codex.Flags().Bool(name, false, "")
	}
	codex.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"codex"}
		for _, name := range []string{"config", "talaria-config", "codex-config", "hook", "codex-hooks", "provider-config", "token", "binary", "endpoint", "fingerprint"} {
			args = appendStringFlag(args, command, name)
		}
		for _, name := range []string{"dry-run", "apply", "remove"} {
			args = appendBoolFlag(args, command, name)
		}
		return invoke(context, command, args)
	}
	command.AddCommand(codex)
	return command
}

func statusCobraCommand(context cli.CobraContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "show daemon status",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return invoke(context, command, nil)
		},
	}
}

func doctorCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{Use: "doctor", Short: "diagnose local state", Args: cobra.NoArgs}
	command.Flags().String("repair", "", "repair subsystem (currently: fts)")
	command.Flags().Bool("dry-run", false, "plan a repair without applying it")
	command.Flags().String("apply", "", "apply a repair receipt")
	command.RunE = func(command *cobra.Command, _ []string) error {
		repair, _ := command.Flags().GetString("repair")
		args := []string{}
		if repair != "" {
			args = append(args, "--repair="+repair)
		}
		args = appendBoolFlag(args, command, "dry-run")
		args = appendStringFlag(args, command, "apply")
		return invoke(context, command, args)
	}
	return command
}

func tokenCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{Use: "token", Short: "manage the local bearer token", Args: cobra.NoArgs}
	rotate := &cobra.Command{
		Use:  "rotate",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return invoke(context, command, []string{"rotate"})
		},
	}
	command.AddCommand(rotate)
	return command
}

func dbCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{Use: "db", Short: "database maintenance", Args: cobra.NoArgs}
	backup := &cobra.Command{Use: "backup", Short: "manage backup inventory", Args: cobra.NoArgs}
	reconcile := &cobra.Command{Use: "reconcile", Short: "reconcile backup inventory", Args: cobra.NoArgs}
	reconcile.Flags().Bool("dry-run", false, "plan reconciliation")
	reconcile.Flags().String("apply", "", "apply a reconciliation receipt")
	reconcile.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"backup", "reconcile"}
		args = appendBoolFlag(args, command, "dry-run")
		args = appendStringFlag(args, command, "apply")
		return invoke(context, command, args)
	}
	backup.AddCommand(reconcile)

	restore := &cobra.Command{Use: "restore", Short: "restore a database backup", Args: cobra.NoArgs}
	restore.Flags().String("backup", "", "backup identifier")
	restore.Flags().Bool("dry-run", false, "create a restore receipt")
	restore.Flags().String("apply", "", "apply a restore receipt")
	restore.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"restore"}
		args = appendStringFlag(args, command, "backup")
		args = appendBoolFlag(args, command, "dry-run")
		args = appendStringFlag(args, command, "apply")
		return invoke(context, command, args)
	}

	command.AddCommand(backup, restore)
	return command
}

func scannerCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{Use: "scanner", Short: "scanner rule maintenance", Args: cobra.NoArgs}
	rules := &cobra.Command{Use: "rules", Short: "manage scanner rules", Args: cobra.NoArgs}
	upgrade := &cobra.Command{Use: "upgrade", Short: "upgrade scanner rules", Args: cobra.NoArgs}
	upgrade.Flags().String("candidate", "", "candidate rules path")
	upgrade.Flags().Bool("dry-run", false, "plan the rule upgrade")
	upgrade.Flags().String("apply", "", "apply a rule-upgrade receipt")
	upgrade.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"rules", "upgrade"}
		args = appendStringFlag(args, command, "candidate")
		args = appendBoolFlag(args, command, "dry-run")
		args = appendStringFlag(args, command, "apply")
		return invoke(context, command, args)
	}
	rules.AddCommand(upgrade)
	command.AddCommand(rules)
	return command
}
