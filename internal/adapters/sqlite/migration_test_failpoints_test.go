package sqlite

// This file is test-only. It supplies a subprocess crash adapter without
// adding process controls, callbacks, or environment inspection to the
// production migration path.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/golang-migrate/migrate/v4"
	migratedatabase "github.com/golang-migrate/migrate/v4/database"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	migrateiofs "github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/guilhermecastro/talaria-mem/db"
)

type testMigrationFailpoints struct {
	BeforeRun                func(version uint) error
	AfterRun                 func(version uint) error
	KillAfterDirtySetVersion func(version uint) error
	AfterCleanVersion        func(version uint) error
	BeforeUserVersion        func(version uint) error
	AfterUserVersion         func(version uint) error
	BeforeFinalJournal       func(version uint) error
	AfterFinalJournal        func(version uint) error
	KillBeforeCommit         func(version uint) error
	KillAfterDDL             func(version uint) error
}

type testMigrationOptions struct {
	failpoints testMigrationFailpoints
}

// testMigrationBaseDriver places test-only callbacks inside the production
// journal driver's Run boundary. It is compiled only with *_test.go files.
type testMigrationBaseDriver struct {
	delegate   migratedatabase.Driver
	failpoints *testMigrationFailpoints
}

func (driver *testMigrationBaseDriver) Open(url string) (migratedatabase.Driver, error) {
	opened, err := driver.delegate.Open(url)
	if err != nil {
		return nil, err
	}
	return &testMigrationBaseDriver{delegate: opened, failpoints: driver.failpoints}, nil
}

func (driver *testMigrationBaseDriver) Close() error  { return driver.delegate.Close() }
func (driver *testMigrationBaseDriver) Lock() error   { return driver.delegate.Lock() }
func (driver *testMigrationBaseDriver) Unlock() error { return driver.delegate.Unlock() }
func (driver *testMigrationBaseDriver) SetVersion(version int, dirty bool) error {
	return driver.delegate.SetVersion(version, dirty)
}
func (driver *testMigrationBaseDriver) Version() (int, bool, error) {
	return driver.delegate.Version()
}
func (driver *testMigrationBaseDriver) Drop() error { return driver.delegate.Drop() }
func (driver *testMigrationBaseDriver) Run(migration io.Reader) error {
	version, dirty, err := driver.delegate.Version()
	if err != nil {
		return err
	}
	if !dirty || version < 0 {
		return fmt.Errorf("test migration driver entered Run without dirty target version")
	}
	target := uint(version)
	if driver.failpoints.KillBeforeCommit != nil {
		if err := driver.failpoints.KillBeforeCommit(target); err != nil {
			return err
		}
	}
	if driver.failpoints.BeforeRun != nil {
		if err := driver.failpoints.BeforeRun(target); err != nil {
			return err
		}
	}
	if err := driver.delegate.Run(migration); err != nil {
		return err
	}
	if driver.failpoints.KillAfterDDL != nil {
		if err := driver.failpoints.KillAfterDDL(target); err != nil {
			return err
		}
	}
	if driver.failpoints.AfterRun != nil {
		if err := driver.failpoints.AfterRun(target); err != nil {
			return err
		}
	}
	return nil
}

type testMigrationDriver struct {
	delegate   migratedatabase.Driver
	failpoints *testMigrationFailpoints
}

func (driver *testMigrationDriver) Open(url string) (migratedatabase.Driver, error) {
	opened, err := driver.delegate.Open(url)
	if err != nil {
		return nil, err
	}
	return &testMigrationDriver{delegate: opened, failpoints: driver.failpoints}, nil
}

func (driver *testMigrationDriver) Close() error  { return driver.delegate.Close() }
func (driver *testMigrationDriver) Lock() error   { return driver.delegate.Lock() }
func (driver *testMigrationDriver) Unlock() error { return driver.delegate.Unlock() }
func (driver *testMigrationDriver) SetVersion(version int, dirty bool) error {
	if err := driver.delegate.SetVersion(version, dirty); err != nil {
		return err
	}
	if dirty && driver.failpoints.KillAfterDirtySetVersion != nil {
		if err := driver.failpoints.KillAfterDirtySetVersion(uint(version)); err != nil {
			return err
		}
	}
	if !dirty && version >= 0 && driver.failpoints.AfterCleanVersion != nil {
		if err := driver.failpoints.AfterCleanVersion(uint(version)); err != nil {
			// Keep the durable protocol dirty for an injected in-process error;
			// a real subprocess death is intentionally exercised separately and
			// observes the clean-version window before this repair can run.
			_ = setTestDirtyVersion(driver.delegate, version)
			return err
		}
	}
	return nil
}

func setTestDirtyVersion(driver migratedatabase.Driver, version int) error {
	if journaled, ok := driver.(*journaledMigrationDriver); ok {
		return journaled.delegate.SetVersion(version, true)
	}
	return driver.SetVersion(version, true)
}
func (driver *testMigrationDriver) Version() (int, bool, error) {
	return driver.delegate.Version()
}
func (driver *testMigrationDriver) Drop() error { return driver.delegate.Drop() }
func (driver *testMigrationDriver) Run(migration io.Reader) error {
	return driver.delegate.Run(migration)
}

func applyMigrationsWithTestOptions(ctx context.Context, handle *sql.DB, options testMigrationOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateEmbeddedMigrations(); err != nil {
		return err
	}
	legacyVersion, version, dirty, present, err := validateMigrationAuthority(ctx, handle)
	if err != nil {
		return err
	}
	if !present {
		if err := ensureCanonicalMigrationTable(ctx, handle); err != nil {
			return err
		}
	}
	source, err := migrateiofs.New(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("initialize embedded migrations: %w", err)
	}
	baseDriver, err := migratesqlite.WithInstance(handle, &migratesqlite.Config{
		MigrationsTable: "schema_migrations",
		NoTxWrap:        false,
	})
	if err != nil {
		return fmt.Errorf("initialize sqlite migration driver: %w", err)
	}
	runID, err := newMigrationRunID()
	if err != nil {
		return err
	}
	failpoints := &options.failpoints
	base := &testMigrationBaseDriver{delegate: baseDriver, failpoints: failpoints}
	journaled := &journaledMigrationDriver{delegate: base, handle: handle, runID: runID}
	driver := &testMigrationDriver{delegate: journaled, failpoints: failpoints}
	migration, err := migrate.NewWithInstance("embedded", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("initialize migration protocol: %w", err)
	}
	if present && dirty {
		journalPresent, journalErr := migrationJournalExists(ctx, handle)
		if journalErr != nil {
			return journalErr
		}
		if version == 1 && !journalPresent && legacyVersion == 0 {
			if err := resetPreMigrationOneAuthority(ctx, handle); err != nil {
				return fmt.Errorf("restart pre-DDL migration 1: %w", err)
			}
		} else {
			evidence, evidenceErr := latestMigrationEvidence(ctx, handle)
			if evidenceErr != nil {
				return evidenceErr
			}
			stage, classifyErr := classifyDirtyMigration(ctx, handle, version, evidence)
			if classifyErr != nil {
				return classifyErr
			}
			resumeVersion := int(version)
			if stage == migrationStageRollbackBeforeCommit && resumeVersion > 0 {
				resumeVersion--
			} else if stage != migrationStageAppliedDDLBeforeClean && stage != migrationStageRollbackBeforeCommit {
				return fmt.Errorf("migration dirty version %d has unknown recovery stage %q", version, stage)
			}
			if err := migration.Force(resumeVersion); err != nil {
				return fmt.Errorf("resume dirty migration at version %d: %w", resumeVersion, err)
			}
		}
	}
	if err := migration.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return recordMigrationFailure(ctx, handle, migration, err)
	}
	version, dirty, err = migration.Version()
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		return recordMigrationFailure(ctx, handle, migration, errors.New("migration protocol left a dirty version"))
	}
	if options.failpoints.BeforeUserVersion != nil {
		if err := options.failpoints.BeforeUserVersion(uint(version)); err != nil {
			return err
		}
	}
	if err := setSQLiteUserVersion(ctx, handle, uint(version)); err != nil {
		return err
	}
	if options.failpoints.AfterUserVersion != nil {
		if err := options.failpoints.AfterUserVersion(uint(version)); err != nil {
			return err
		}
	}
	if options.failpoints.BeforeFinalJournal != nil {
		if err := options.failpoints.BeforeFinalJournal(uint(version)); err != nil {
			return err
		}
	}
	fingerprint, err := migrationSchemaFingerprint(ctx, handle)
	if err != nil {
		return err
	}
	completed := uint(version)
	if err := recordMigrationStateWithStage(ctx, handle, runID, uint(version), nil, &completed, "", "", false, fingerprint); err != nil {
		return err
	}
	if options.failpoints.AfterFinalJournal != nil {
		if err := options.failpoints.AfterFinalJournal(uint(version)); err != nil {
			return err
		}
	}
	if err := validateMigrationJournalSchema(ctx, handle); err != nil {
		return fmt.Errorf("validate migration journal after maintenance: %w", err)
	}
	return nil
}
