package commands

import (
	"context"
	"os"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/maintenance"
	"github.com/guilhermecastro/talaria-mem/internal/scanner"
)

type RuleUpgradeOperator interface {
	DryRun(context.Context, maintenance.RuleUpgradeRequest) (maintenance.Receipt, string, error)
	Apply(context.Context, string, maintenance.RuleUpgradeRequest) error
}

type ScannerCommands struct {
	Upgrade       RuleUpgradeOperator
	Candidate     scanner.CandidateService
	CandidateLoad func(context.Context, maintenance.Receipt) (scanner.CandidateService, error)
	Fixtures      []scanner.ComparativeFixture
}

func NewScannerCommands(operator RuleUpgradeOperator, candidate scanner.CandidateService, fixtures []scanner.ComparativeFixture) *ScannerCommands {
	return &ScannerCommands{Upgrade: operator, Candidate: candidate, Fixtures: append([]scanner.ComparativeFixture(nil), fixtures...)}
}

func (commands *ScannerCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Upgrade == nil {
		return &cli.UsageError{Message: "scanner rules upgrade unavailable"}
	}
	if len(args) < 3 || args[0] != "rules" || args[1] != "upgrade" {
		return &cli.UsageError{Message: "usage: scanner rules upgrade --candidate <path> --dry-run|--apply <receipt>"}
	}
	var candidatePath, receiptPath string
	dryRun := false
	for index := 2; index < len(args); index++ {
		switch args[index] {
		case "--candidate":
			if index+1 >= len(args) {
				return &cli.UsageError{Message: "candidate path is required"}
			}
			candidatePath = args[index+1]
			index++
		case "--dry-run":
			dryRun = true
		case "--apply":
			if index+1 >= len(args) {
				return &cli.UsageError{Message: "receipt path is required"}
			}
			receiptPath = args[index+1]
			index++
		default:
			return &cli.UsageError{Message: "unknown scanner upgrade option"}
		}
	}
	if (dryRun && receiptPath != "") || (!dryRun && receiptPath == "") {
		return &cli.UsageError{Message: "choose exactly one of --dry-run or --apply <receipt>"}
	}
	request := maintenance.RuleUpgradeRequest{Candidate: commands.Candidate, Fixtures: commands.Fixtures}
	if candidatePath != "" {
		contents, err := os.ReadFile(candidatePath)
		if err != nil {
			return err
		}
		loaded, err := scanner.LoadCandidateRules(contents, scanner.GenerationForRules(contents))
		if err != nil {
			return err
		}
		request.Candidate = loaded
	}
	if !dryRun {
		if request.Candidate == nil && commands.CandidateLoad != nil {
			// The loader receives only the authenticated receipt metadata.  It
			// must not infer a path from user content.
			loaded, err := commands.CandidateLoad(ctx, maintenance.Receipt{ID: receiptPath, Kind: maintenance.ReceiptRuleUpgrade})
			if err != nil {
				return err
			}
			request.Candidate = loaded
		}
		if request.Candidate == nil {
			return &cli.UsageError{Message: "candidate rules are required for apply"}
		}
		if err := commands.Upgrade.Apply(ctx, receiptID(receiptPath), request); err != nil {
			return err
		}
		return output.Result(map[string]any{"version": "talaria.scanner.rules.v1", "status": "active", "receipt": receiptPath}, "scanner rules activated")
	}
	if request.Candidate == nil {
		return &cli.UsageError{Message: "candidate rules are required for dry-run"}
	}
	receipt, path, err := commands.Upgrade.DryRun(ctx, request)
	if err != nil {
		return err
	}
	return output.Result(map[string]any{"version": "talaria.scanner.rules.receipt.v1", "kind": receipt.Kind, "receipt": path, "expires_at": receipt.ExpiresAt}, "receipt=%s", path)
}

func receiptID(path string) string {
	// The CLI accepts an absolute receipt path, while the store API consumes
	// the authenticated basename.  Paths never enter logs as content.
	base := path
	for index := len(base) - 1; index >= 0; index-- {
		if base[index] == '/' || base[index] == '\\' {
			base = base[index+1:]
			break
		}
	}
	if len(base) > 5 && base[len(base)-5:] == ".json" {
		base = base[:len(base)-5]
	}
	return base
}

func RegisterScanner(registry *cli.Registry, commands *ScannerCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "scanner command unavailable"}
	}
	return registry.Register(cli.Command{Name: "scanner", Description: "scanner rule maintenance", Run: commands.Run, Build: scannerCobraCommand})
}
