package commands

import (
	"context"
	"errors"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
)

type DaemonOperator interface {
	Run(context.Context) error
}

type DaemonCommands struct {
	Daemon DaemonOperator
}

func NewDaemonCommands(daemon DaemonOperator) *DaemonCommands {
	return &DaemonCommands{Daemon: daemon}
}

func (commands *DaemonCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Daemon == nil {
		return errors.New("daemon unavailable")
	}
	if len(args) > 1 || (len(args) == 1 && args[0] != "--foreground" && args[0] != "--diagnostic") {
		return &cli.UsageError{Message: "usage: daemon [--foreground|--diagnostic]"}
	}
	if err := commands.Daemon.Run(ctx); err != nil {
		return err
	}
	return output.Result(map[string]any{"version": "talaria.daemon.v1", "status": "stopped"}, "daemon stopped")
}

func RegisterDaemon(registry *cli.Registry, commands *DaemonCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "daemon command unavailable"}
	}
	return registry.Register(cli.Command{Name: "daemon", Description: "run local daemon", Run: commands.Run})
}

type SetupOperator interface {
	Plan(context.Context, lifecycle.SetupRequest) (lifecycle.SetupResult, error)
	Apply(context.Context, lifecycle.SetupRequest) (lifecycle.SetupResult, error)
}

type SetupCommands struct {
	Setup   SetupOperator
	Request lifecycle.SetupRequest
}

func NewSetupCommands(setup SetupOperator, request lifecycle.SetupRequest) *SetupCommands {
	return &SetupCommands{Setup: setup, Request: request}
}

func (commands *SetupCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Setup == nil {
		return errors.New("setup unavailable")
	}
	if len(args) == 0 || args[0] != "codex" {
		return &cli.UsageError{Message: "usage: setup codex --dry-run|--apply [--remove]"}
	}
	request := commands.Request
	request.DryRun = false
	request.Remove = false
	dryRun, apply := false, false
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--dry-run":
			dryRun = true
		case "--apply":
			apply = true
		case "--remove":
			request.Remove = true
		case "--config", "--hook", "--token", "--binary", "--endpoint", "--fingerprint":
			if index+1 >= len(args) {
				return &cli.UsageError{Message: "setup option requires a value"}
			}
			value := args[index+1]
			index++
			switch args[index-1] {
			case "--config":
				request.ConfigPath = value
			case "--hook":
				request.HookPath = value
			case "--token":
				request.TokenPath = value
			case "--binary":
				request.BinaryPath = value
			case "--endpoint":
				request.Endpoint = value
			case "--fingerprint":
				request.Fingerprint = value
			}
		default:
			return &cli.UsageError{Message: "unknown setup option"}
		}
	}
	if dryRun == apply {
		return &cli.UsageError{Message: "choose exactly one of --dry-run or --apply"}
	}
	if dryRun {
		request.DryRun = true
		result, err := commands.Setup.Plan(ctx, request)
		if err != nil {
			return err
		}
		return output.Result(result, "setup dry-run: %d changes", len(result.Changes))
	}
	result, err := commands.Setup.Apply(ctx, request)
	if err != nil {
		return err
	}
	return output.Result(result, "setup applied: %d changes", len(result.Changes))
}

func RegisterSetup(registry *cli.Registry, commands *SetupCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "setup command unavailable"}
	}
	return registry.Register(cli.Command{Name: "setup", Description: "install local integrations", Run: commands.Run})
}

type StatusOperator interface {
	Status(context.Context) (lifecycle.StatusReport, error)
}

type StatusCommands struct{ Provider StatusOperator }

func NewStatusCommands(provider StatusOperator) *StatusCommands {
	return &StatusCommands{Provider: provider}
}

func (commands *StatusCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Provider == nil {
		return errors.New("status unavailable")
	}
	if len(args) != 0 {
		return &cli.UsageError{Message: "usage: status"}
	}
	report, err := commands.Provider.Status(ctx)
	if err != nil {
		return err
	}
	return output.Result(report, "ready=%t database=%d bytes", report.Ready, report.Storage.DatabaseSize)
}

func RegisterStatus(registry *cli.Registry, commands *StatusCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "status command unavailable"}
	}
	return registry.Register(cli.Command{Name: "status", Description: "show daemon status", Run: commands.Run})
}

type DoctorOperator interface {
	Check(context.Context) (lifecycle.DoctorReport, error)
	RepairFTS(context.Context, lifecycle.FTSRepairRequest) (lifecycle.FTSRepairResult, error)
}

type DoctorCommands struct{ Doctor DoctorOperator }

func NewDoctorCommands(doctor DoctorOperator) *DoctorCommands {
	return &DoctorCommands{Doctor: doctor}
}

func (commands *DoctorCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Doctor == nil {
		return errors.New("doctor unavailable")
	}
	if len(args) == 0 {
		report, err := commands.Doctor.Check(ctx)
		if err != nil {
			return err
		}
		return output.Result(report, "healthy=%t ready=%t", report.Healthy, report.Readiness.Ready)
	}
	if len(args) < 2 || args[0] != "--repair=fts" {
		return &cli.UsageError{Message: "usage: doctor [--repair=fts --dry-run|--apply <receipt>]"}
	}
	request := lifecycle.FTSRepairRequest{}
	switch args[1] {
	case "--dry-run":
		request.DryRun = true
		if len(args) != 2 {
			return &cli.UsageError{Message: "usage: doctor --repair=fts --dry-run"}
		}
	case "--apply":
		if len(args) != 3 || args[2] == "" {
			return &cli.UsageError{Message: "usage: doctor --repair=fts --apply <receipt>"}
		}
		request.Receipt = args[2]
	default:
		return &cli.UsageError{Message: "usage: doctor --repair=fts --dry-run|--apply <receipt>"}
	}
	result, err := commands.Doctor.RepairFTS(ctx, request)
	if err != nil {
		return err
	}
	return output.Result(result, "fts repair dry-run=%t receipt=%s", result.DryRun, result.Receipt)
}

func RegisterDoctor(registry *cli.Registry, commands *DoctorCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "doctor command unavailable"}
	}
	return registry.Register(cli.Command{Name: "doctor", Description: "diagnose local state", Run: commands.Run})
}

type TokenOperator interface{ Rotate(context.Context) error }

type TokenCommands struct{ Operator TokenOperator }

func NewTokenCommands(operator TokenOperator) *TokenCommands {
	return &TokenCommands{Operator: operator}
}

func (commands *TokenCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Operator == nil {
		return errors.New("token rotation unavailable")
	}
	if len(args) != 1 || args[0] != "rotate" {
		return &cli.UsageError{Message: "usage: token rotate"}
	}
	if err := commands.Operator.Rotate(ctx); err != nil {
		return err
	}
	return output.Result(map[string]any{"version": "talaria.token.v1", "status": "rotated"}, "token rotated")
}

func RegisterToken(registry *cli.Registry, commands *TokenCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "token command unavailable"}
	}
	return registry.Register(cli.Command{Name: "token", Description: "manage local bearer token", Run: commands.Run})
}
