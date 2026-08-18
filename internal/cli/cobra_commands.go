package cli

import (
	"strconv"

	"github.com/spf13/cobra"
)

func invokeCobra(context CobraContext, command *cobra.Command, args []string) error {
	if context.Run == nil {
		return errCommandUnavailable
	}
	return context.Run(command.Context(), args, context.Output(command))
}

func groupCommand(use, short string, run func(*cobra.Command) error) *cobra.Command {
	command := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
	if run != nil {
		command.RunE = func(command *cobra.Command, _ []string) error {
			return run(command)
		}
	}
	return command
}

func addStringFlag(command *cobra.Command, name, usage string) {
	command.Flags().String(name, "", usage)
}

func addBoolFlag(command *cobra.Command, name, usage string) {
	command.Flags().Bool(name, false, usage)
}

func changedString(args []string, command *cobra.Command, name string) []string {
	if !command.Flags().Changed(name) {
		return args
	}
	value, _ := command.Flags().GetString(name)
	return append(args, "--"+name, value)
}

func changedBool(args []string, command *cobra.Command, name string) []string {
	if !command.Flags().Changed(name) {
		return args
	}
	value, _ := command.Flags().GetBool(name)
	if value {
		return append(args, "--"+name)
	}
	return args
}

func memoryCobraCommand(core *MemoryCore, output func(*cobra.Command) Output) *cobra.Command {
	command := groupCommand("memory", "create, inspect, and search local memory", func(command *cobra.Command) error {
		return output(command).Result(map[string]any{
			"version":  "talaria.memory.v1",
			"commands": []string{"add", "list", "review", "search", "get", "update", "confirm", "pin", "forget", "restore", "explain"},
		}, "memory commands: add list review search get update confirm pin forget restore explain")
	})

	add := &cobra.Command{Use: "add", Short: "create a memory", Args: cobra.NoArgs}
	for _, name := range []string{"workspace", "kind", "title", "content", "tags", "idempotency-key", "resolution-state", "source", "source-locator"} {
		addStringFlag(add, name, "")
	}
	for _, name := range []string{"global", "verified", "pinned"} {
		addBoolFlag(add, name, "")
	}
	add.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"add"}
		for _, name := range []string{"workspace", "kind", "title", "content", "tags", "idempotency-key", "resolution-state", "source", "source-locator"} {
			args = changedString(args, command, name)
		}
		for _, name := range []string{"global", "verified", "pinned"} {
			args = changedBool(args, command, name)
		}
		return core.Run(command.Context(), args, output(command))
	}

	update := &cobra.Command{Use: "update", Short: "create a new memory revision", Args: cobra.NoArgs}
	for _, name := range []string{"memory", "expected-revision", "workspace", "kind", "title", "content", "tags", "idempotency-key", "resolution-state", "source", "source-locator"} {
		addStringFlag(update, name, "")
	}
	addBoolFlag(update, "verified", "")
	update.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"update"}
		for _, name := range []string{"memory", "expected-revision", "workspace", "kind", "title", "content", "tags", "idempotency-key", "resolution-state", "source", "source-locator"} {
			args = changedString(args, command, name)
		}
		args = changedBool(args, command, "verified")
		return core.Run(command.Context(), args, output(command))
	}

	for _, name := range []string{"confirm", "pin", "forget"} {
		child := &cobra.Command{Use: name, Short: name + " a memory revision", Args: cobra.NoArgs}
		for _, flagName := range []string{"memory", "expected-revision", "idempotency-key"} {
			addStringFlag(child, flagName, "")
		}
		child.RunE = func(command *cobra.Command, _ []string) error {
			args := []string{name}
			for _, flagName := range []string{"memory", "expected-revision", "idempotency-key"} {
				args = changedString(args, command, flagName)
			}
			return core.Run(command.Context(), args, output(command))
		}
		command.AddCommand(child)
	}

	restore := &cobra.Command{Use: "restore", Short: "restore a forgotten memory revision", Args: cobra.NoArgs}
	for _, name := range []string{"memory", "expected-revision", "idempotency-key"} {
		addStringFlag(restore, name, "")
	}
	addBoolFlag(restore, "verified", "")
	restore.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"restore"}
		for _, name := range []string{"memory", "expected-revision", "idempotency-key"} {
			args = changedString(args, command, name)
		}
		args = changedBool(args, command, "verified")
		return core.Run(command.Context(), args, output(command))
	}

	review := &cobra.Command{Use: "review [memory-id]", Short: "review an unverified memory", Args: cobra.MaximumNArgs(1)}
	addStringFlag(review, "memory", "")
	addStringFlag(review, "workspace", "")
	review.RunE = func(command *cobra.Command, positional []string) error {
		args := append([]string{"review"}, positional...)
		args = changedString(args, command, "memory")
		args = changedString(args, command, "workspace")
		return core.Run(command.Context(), args, output(command))
	}

	list := &cobra.Command{Use: "list", Short: "list memories awaiting review", Args: cobra.NoArgs}
	addStringFlag(list, "workspace", "")
	addStringFlag(list, "cursor", "")
	list.Flags().Int("limit", 0, "maximum number of memories")
	list.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"list"}
		args = changedString(args, command, "workspace")
		args = changedString(args, command, "cursor")
		if command.Flags().Changed("limit") {
			value, _ := command.Flags().GetInt("limit")
			args = append(args, "--limit", strconv.Itoa(value))
		}
		return core.Run(command.Context(), args, output(command))
	}

	search := &cobra.Command{Use: "search", Short: "search confirmed memory", Args: cobra.NoArgs}
	for _, name := range []string{"query", "workspace", "session"} {
		addStringFlag(search, name, "")
	}
	search.Flags().Int("limit", 0, "maximum number of results")
	search.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"search"}
		for _, name := range []string{"query", "workspace", "session"} {
			args = changedString(args, command, name)
		}
		if command.Flags().Changed("limit") {
			value, _ := command.Flags().GetInt("limit")
			args = append(args, "--limit", strconv.Itoa(value))
		}
		return core.Run(command.Context(), args, output(command))
	}

	get := &cobra.Command{Use: "get [memory-id]", Short: "read one memory", Args: cobra.MaximumNArgs(1)}
	for _, name := range []string{"memory", "workspace", "session"} {
		addStringFlag(get, name, "")
	}
	get.RunE = func(command *cobra.Command, positional []string) error {
		args := append([]string{"get"}, positional...)
		for _, name := range []string{"memory", "workspace", "session"} {
			args = changedString(args, command, name)
		}
		return core.Run(command.Context(), args, output(command))
	}

	explain := &cobra.Command{Use: "explain [memory-id]", Short: "explain memory trust and lifecycle", Args: cobra.MaximumNArgs(1)}
	addStringFlag(explain, "memory", "")
	explain.RunE = func(command *cobra.Command, positional []string) error {
		args := append([]string{"explain"}, positional...)
		args = changedString(args, command, "memory")
		return core.Run(command.Context(), args, output(command))
	}

	command.AddCommand(add, update, restore, review, list, search, get, explain)
	return command
}

func workspaceCobraCommand(commands *WorkspaceCommands, output func(*cobra.Command) Output) *cobra.Command {
	command := groupCommand("workspace", "manage workspace bindings", func(command *cobra.Command) error {
		return output(command).Result(map[string]any{
			"version":  "talaria.workspace.v1",
			"commands": []string{"create", "list", "show", "bind", "merge"},
		}, "workspace commands: create list show bind merge")
	})

	create := &cobra.Command{Use: "create [name]", Short: "create a workspace", Args: cobra.MaximumNArgs(1)}
	addStringFlag(create, "name", "workspace name")
	addStringFlag(create, "id", "workspace identifier")
	create.RunE = func(command *cobra.Command, positional []string) error {
		args := append([]string{"create"}, positional...)
		args = changedString(args, command, "name")
		args = changedString(args, command, "id")
		return commands.Run(command.Context(), args, output(command))
	}

	list := &cobra.Command{Use: "list", Short: "list workspaces", Args: cobra.NoArgs}
	list.RunE = func(command *cobra.Command, _ []string) error {
		return commands.Run(command.Context(), []string{"list"}, output(command))
	}

	show := &cobra.Command{Use: "show [id-or-name]", Short: "show a workspace", Args: cobra.MaximumNArgs(1)}
	addStringFlag(show, "workspace", "workspace identifier")
	show.RunE = func(command *cobra.Command, positional []string) error {
		args := append([]string{"show"}, positional...)
		args = changedString(args, command, "workspace")
		return commands.Run(command.Context(), args, output(command))
	}

	bind := &cobra.Command{Use: "bind", Short: "bind a lookup key to a workspace", Args: cobra.NoArgs}
	addStringFlag(bind, "key", "binding key")
	addStringFlag(bind, "workspace", "workspace identifier")
	bind.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"bind"}
		args = changedString(args, command, "key")
		args = changedString(args, command, "workspace")
		return commands.Run(command.Context(), args, output(command))
	}

	merge := &cobra.Command{Use: "merge [source] [target]", Short: "merge two workspaces", Args: cobra.MaximumNArgs(2)}
	for _, name := range []string{"source", "target"} {
		addStringFlag(merge, name, "workspace identifier")
	}
	addBoolFlag(merge, "dry-run", "plan the merge without applying it")
	addBoolFlag(merge, "apply", "apply the merge plan")
	merge.RunE = func(command *cobra.Command, positional []string) error {
		args := append([]string{"merge"}, positional...)
		for _, name := range []string{"source", "target"} {
			args = changedString(args, command, name)
		}
		args = changedBool(args, command, "dry-run")
		args = changedBool(args, command, "apply")
		return commands.Run(command.Context(), args, output(command))
	}

	command.AddCommand(create, list, show, bind, merge)
	return command
}

func projectionCobraCommand(commands *ProjectionCommands, output func(*cobra.Command) Output) *cobra.Command {
	command := groupCommand("projection", "rebuild derived workspace projections", func(command *cobra.Command) error {
		return output(command).Result(map[string]any{"version": "talaria.projection.v1", "commands": []string{"rebuild"}}, "projection commands: rebuild")
	})
	rebuild := &cobra.Command{Use: "rebuild", Short: "rebuild a workspace projection", Args: cobra.NoArgs}
	addStringFlag(rebuild, "workspace", "workspace identifier")
	rebuild.RunE = func(command *cobra.Command, _ []string) error {
		args := []string{"rebuild"}
		args = changedString(args, command, "workspace")
		return commands.Run(command.Context(), args, output(command))
	}
	command.AddCommand(rebuild)
	return command
}
