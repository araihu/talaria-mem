package runtime

import (
	"context"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
)

type sqliteFTSRepairer struct{ database *sqlite.DB }

func (repairer sqliteFTSRepairer) Compare(ctx context.Context) (lifecycle.FTSReport, error) {
	diagnostic, err := repairer.database.FTSIntegrity(ctx)
	if err != nil {
		return lifecycle.FTSReport{}, err
	}
	return lifecycle.FTSReport{
		Tokenizer:      diagnostic.Tokenizer,
		SecureDelete:   diagnostic.SecureDelete,
		ExpectedRows:   diagnostic.ExpectedRows,
		ActualRows:     diagnostic.Rows,
		ExpectedHash:   diagnostic.ExpectedHash,
		ActualHash:     diagnostic.ActualHash,
		RowsMatch:      diagnostic.ExpectedRows == diagnostic.Rows,
		HashesMatch:    diagnostic.ExpectedHash == diagnostic.ActualHash,
		RepairRequired: diagnostic.Tokenizer != domain.FTS5Tokenizer || !diagnostic.SecureDelete || diagnostic.ExpectedRows != diagnostic.Rows || diagnostic.ExpectedHash != diagnostic.ActualHash,
	}, nil
}

func (repairer sqliteFTSRepairer) Repair(ctx context.Context) error {
	return repairer.database.RepairFTS(ctx)
}

var _ lifecycle.FTSRepairer = sqliteFTSRepairer{}
