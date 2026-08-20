package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/golang-migrate/migrate/v4"
	migratedatabase "github.com/golang-migrate/migrate/v4/database"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	migrateiofs "github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/guilhermecastro/talaria-mem/db"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	_ "modernc.org/sqlite"
)

// DB owns one already-initialized SQLite handle. Open never creates files,
// changes journal/schema state, or applies migrations. All schema/filesystem
// preparation belongs to OpenForMaintenance/Migrate, which T13 must call only
// while holding its lifecycle lock and after its backup/journal/readiness
// sequencing.
type DB struct {
	sql          *sql.DB
	beforeCommit func() error
	pathPolicy   ports.ManagedPathPolicy
}

const (
	embeddedMigrationTarget             = uint(4)
	canonicalSchemaContractFingerprint  = "b0e8099395f3aa64608f605929d89f741a40bd9823959685d806eabca95e10f5"
	canonicalPreMigrationOneFingerprint = "9fc386a71a70afaa72cfb2c9fca0fba0e2489b33a74f8d8ac8cd0767996fd09d"
	migrationStageStarted               = "started"
	migrationStageRollbackBeforeCommit  = "rollback_before_commit"
	migrationStageAppliedDDLBeforeClean = "applied_ddl_before_clean"
)

type journaledMigrationDriver struct {
	delegate  migratedatabase.Driver
	handle    *sql.DB
	runID     string
	startedID int64
}

func (driver *journaledMigrationDriver) Open(url string) (migratedatabase.Driver, error) {
	opened, err := driver.delegate.Open(url)
	if err != nil {
		return nil, err
	}
	return &journaledMigrationDriver{delegate: opened, handle: driver.handle, runID: driver.runID}, nil
}

func (driver *journaledMigrationDriver) Close() error  { return driver.delegate.Close() }
func (driver *journaledMigrationDriver) Lock() error   { return driver.delegate.Lock() }
func (driver *journaledMigrationDriver) Unlock() error { return driver.delegate.Unlock() }
func (driver *journaledMigrationDriver) SetVersion(version int, dirty bool) error {
	if dirty {
		target := uint(version)
		completed := target
		if completed > 0 {
			completed--
		}
		fingerprint, err := migrationSchemaFingerprint(context.Background(), driver.handle)
		if err != nil {
			return err
		}
		id, err := insertMigrationStateWithStage(
			context.Background(), driver.handle, driver.runID, target,
			&target, &completed, "", migrationStageStarted, true, fingerprint,
		)
		if err != nil {
			return err
		}
		if err := driver.delegate.SetVersion(version, dirty); err != nil {
			return err
		}
		driver.startedID = id
		return nil
	}
	return driver.delegate.SetVersion(version, dirty)
}
func (driver *journaledMigrationDriver) Version() (int, bool, error) {
	version, dirty, err := driver.delegate.Version()
	if err != nil {
		return 0, false, err
	}
	if version == 0 && !dirty {
		return migratedatabase.NilVersion, false, nil
	}
	return version, dirty, nil
}
func (driver *journaledMigrationDriver) Drop() error { return driver.delegate.Drop() }

func (driver *journaledMigrationDriver) Run(migration io.Reader) error {
	version, dirty, err := driver.delegate.Version()
	if err != nil {
		return err
	}
	if !dirty || version < 0 {
		return fmt.Errorf("migration driver entered Run without dirty target version")
	}
	target := uint(version)
	completed := target
	if completed > 0 {
		completed--
	}
	preFingerprint, err := migrationSchemaFingerprint(context.Background(), driver.handle)
	if err != nil {
		return err
	}
	if err := driver.writeMigrationState(target, &target, &completed, "", migrationStageStarted, true, preFingerprint); err != nil {
		return err
	}
	if err := driver.delegate.Run(migration); err != nil {
		journalErr := driver.writeMigrationState(target, &target, &completed,
			safeMigrationError(err), migrationStageRollbackBeforeCommit, true, preFingerprint)
		return errors.Join(err, journalErr)
	}
	postFingerprint, fingerprintErr := migrationSchemaFingerprint(context.Background(), driver.handle)
	if fingerprintErr != nil {
		return fingerprintErr
	}
	if err := driver.writeMigrationState(target, &target, &completed,
		"", migrationStageAppliedDDLBeforeClean, true, postFingerprint); err != nil {
		return err
	}
	return nil
}

func (driver *journaledMigrationDriver) writeMigrationState(current uint, failed, completed *uint, safeError, failureStage string, dirty bool, fingerprint string) error {
	if driver.startedID != 0 {
		return updateMigrationStateWithStage(context.Background(), driver.handle, driver.startedID, driver.runID, current, failed, completed, safeError, failureStage, dirty, fingerprint)
	}
	id, err := insertMigrationStateWithStage(context.Background(), driver.handle, driver.runID, current, failed, completed, safeError, failureStage, dirty, fingerprint)
	if err == nil {
		driver.startedID = id
	}
	return err
}

// downMigrationDriver keeps the migrate library's internal NilVersion (-1)
// sentinel out of the durable authority table. Reverse migrations are test
// fixtures only; the production maintenance path is forward-only.
type downMigrationDriver struct {
	delegate migratedatabase.Driver
}

func (driver *downMigrationDriver) Open(url string) (migratedatabase.Driver, error) {
	opened, err := driver.delegate.Open(url)
	if err != nil {
		return nil, err
	}
	return &downMigrationDriver{delegate: opened}, nil
}

func (driver *downMigrationDriver) Close() error  { return driver.delegate.Close() }
func (driver *downMigrationDriver) Lock() error   { return driver.delegate.Lock() }
func (driver *downMigrationDriver) Unlock() error { return driver.delegate.Unlock() }
func (driver *downMigrationDriver) Version() (int, bool, error) {
	return driver.delegate.Version()
}
func (driver *downMigrationDriver) Drop() error { return driver.delegate.Drop() }
func (driver *downMigrationDriver) Run(migration io.Reader) error {
	return driver.delegate.Run(migration)
}
func (driver *downMigrationDriver) SetVersion(version int, dirty bool) error {
	if version < 0 {
		version = 0
	}
	return driver.delegate.SetVersion(version, dirty)
}

// Open is the ordinary daemon boundary. The managed path policy is an
// explicit dependency supplied by the T14 composition root; this adapter
// never discovers a concrete filesystem through package initialization.
func Open(ctx context.Context, path string, policy ports.ManagedPathPolicy) (*DB, error) {
	return OpenWithManagedPathPolicy(ctx, path, policy)
}

// OpenWithManagedPathPolicy keeps the ordinary open boundary composable for
// callers that supply a storage policy. It never creates files, configures
// SQLite, or applies migrations.
func OpenWithManagedPathPolicy(ctx context.Context, path string, policy ports.ManagedPathPolicy) (*DB, error) {
	if policy == nil {
		return nil, domain.NewError(domain.CodeValidation, "managed path policy is required", false)
	}
	path, err := canonicalDatabasePath(path)
	if err != nil {
		return nil, err
	}
	if err := policy.ValidateManagedPathParent(path); err != nil {
		return nil, err
	}
	if err := policy.ValidateManagedPath(path, 0o600); err != nil {
		return nil, err
	}
	database, err := openHandle(ctx, path, false, policy)
	if err != nil {
		return nil, err
	}
	if err := validateOrdinaryOpenSchema(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := verifyDatabaseConfiguration(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// OpenForMaintenance is the explicit T13 migration/setup boundary. It may
// create private parents/database bytes, configure WAL/secure-delete, and
// apply forward migrations. Both the path policy and preparer are explicit
// dependencies supplied by the T14 composition root.
func OpenForMaintenance(ctx context.Context, path string, policy ports.ManagedPathPolicy, prepare ports.ManagedDatabasePreparer) (*DB, error) {
	return OpenForMaintenanceWithManagedPathPolicy(ctx, path, policy, prepare)
}

// OpenForMaintenanceWithManagedPathPolicy is the explicit T13 migration and
// setup boundary with injected path policy and preparation dependencies.
// Ordinary Open intentionally has no route to this preparation behavior.
func OpenForMaintenanceWithManagedPathPolicy(ctx context.Context, path string, policy ports.ManagedPathPolicy, prepare ports.ManagedDatabasePreparer) (*DB, error) {
	if policy == nil {
		return nil, domain.NewError(domain.CodeValidation, "managed path policy is required", false)
	}
	if prepare == nil {
		return nil, domain.NewError(domain.CodeValidation, "managed database preparer is required", false)
	}
	path, err := canonicalDatabasePath(path)
	if err != nil {
		return nil, err
	}
	if err := prepare(path, policy); err != nil {
		return nil, err
	}
	database, err := openHandle(ctx, path, true, policy)
	if err != nil {
		return nil, err
	}
	if _, _, _, _, err := validateMigrationAuthority(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := configureMaintenance(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := applyMigrations(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := validateSchemaContract(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := verifyDatabaseConfiguration(ctx, database.sql); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// Migrate is a path-based maintenance entrypoint for T13. It intentionally
// has no ordinary-open equivalent and closes the handle after forward
// migration. Down migrations are test-only and unexported.
func Migrate(ctx context.Context, path string, policy ports.ManagedPathPolicy, prepare ports.ManagedDatabasePreparer) error {
	database, err := OpenForMaintenance(ctx, path, policy, prepare)
	if err != nil {
		return err
	}
	return database.Close()
}

func openHandle(ctx context.Context, path string, maintenance bool, policy ports.ManagedPathPolicy) (*DB, error) {
	dsn := databaseDSN(path, maintenance)
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	poolSize := domain.MaxConcurrentReads + domain.MaxConcurrentWriters
	handle.SetMaxOpenConns(poolSize)
	handle.SetMaxIdleConns(poolSize)
	database := &DB{sql: handle, pathPolicy: policy}
	if err := handle.PingContext(ctx); err != nil {
		_ = handle.Close()
		return nil, domain.MapSQLiteError(err)
	}
	return database, nil
}

func canonicalDatabasePath(path string) (string, error) {
	if path == "" {
		return "", domain.NewError(domain.CodeValidation, "database path is required", false)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", domain.NewError(domain.CodeValidation, "invalid database path", false)
	}
	clean := filepath.Clean(absolute)
	if clean != absolute || !filepath.IsAbs(path) {
		return "", domain.NewError(domain.CodeValidation, "database path must be absolute and canonical", false)
	}
	return clean, nil
}

func databaseDSN(path string, maintenance bool) string {
	dsn := &url.URL{Scheme: "file", Path: path}
	query := dsn.Query()
	if maintenance {
		query.Set("mode", "rwc")
	} else {
		query.Set("mode", "rw")
	}
	// Connection-local pragmas only. journal_mode is intentionally absent;
	// maintenance configures WAL explicitly.
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "secure_delete(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func configureMaintenance(ctx context.Context, handle *sql.DB) error {
	for _, statement := range []string{
		"PRAGMA auto_vacuum = INCREMENTAL",
		"PRAGMA journal_mode = WAL",
		"PRAGMA secure_delete = ON",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := handle.ExecContext(ctx, statement); err != nil {
			return domain.MapSQLiteError(err)
		}
	}
	return nil
}

func verifyDatabaseConfiguration(ctx context.Context, handle *sql.DB) error {
	checks := []struct {
		pragma string
		want   int
	}{
		{pragma: "foreign_keys", want: 1},
		{pragma: "secure_delete", want: 1},
		{pragma: "auto_vacuum", want: 2},
	}
	for _, check := range checks {
		var got int
		if err := handle.QueryRowContext(ctx, "PRAGMA "+check.pragma).Scan(&got); err != nil {
			return domain.MapSQLiteError(err)
		}
		if got != check.want {
			return fmt.Errorf("sqlite configuration drift: %s=%d", check.pragma, got)
		}
	}
	var journalMode string
	if err := handle.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return domain.MapSQLiteError(err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("sqlite configuration drift: journal_mode=%s", journalMode)
	}
	var schema string
	if err := handle.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'memory_fts'",
	).Scan(&schema); err != nil {
		return domain.MapSQLiteError(err)
	}
	if !strings.Contains(schema, "tokenize='"+domain.FTS5Tokenizer+"'") {
		return domain.NewError(domain.CodeUnavailable, "FTS tokenizer configuration drift", false)
	}
	var ftsSecureDelete int
	if err := handle.QueryRowContext(ctx,
		"SELECT v FROM memory_fts_config WHERE k = 'secure-delete'",
	).Scan(&ftsSecureDelete); err != nil {
		return domain.MapSQLiteError(err)
	}
	if ftsSecureDelete != 1 {
		return domain.NewError(domain.CodeUnavailable, "FTS secure-delete configuration drift", false)
	}
	return nil
}

func validateMigrationAuthority(ctx context.Context, handle *sql.DB) (legacyVersion, version uint, dirty, present bool, err error) {
	legacy, err := sqliteUserVersion(ctx, handle)
	if err != nil {
		return 0, 0, false, false, err
	}
	if legacy < 0 {
		return 0, 0, false, false, fmt.Errorf("negative SQLite user_version")
	}
	legacyVersion = uint(legacy)
	tablePresent, err := migrationTableExists(ctx, handle)
	if err != nil {
		return 0, 0, false, false, err
	}
	if !tablePresent {
		journalPresent, err := migrationJournalExists(ctx, handle)
		if err != nil {
			return 0, 0, false, false, err
		}
		if err := validateMigrationAuthorityObjects(ctx, handle); err != nil {
			return 0, 0, false, false, err
		}
		if journalPresent {
			if err := validateMigrationJournalSchema(ctx, handle); err != nil {
				return 0, 0, false, false, err
			}
		}
		if legacyVersion != 0 {
			return 0, 0, false, false, fmt.Errorf("SQLite user_version %d has no schema_migrations authority", legacyVersion)
		}
		return legacyVersion, 0, false, false, nil
	}
	if err := validateSchemaMigrationsProtocol(ctx, handle); err != nil {
		return 0, 0, false, false, err
	}
	if err := validateMigrationAuthorityObjects(ctx, handle); err != nil {
		return 0, 0, false, false, err
	}
	version, dirty, present, err = migrationVersion(ctx, handle)
	if err != nil {
		return 0, 0, false, false, err
	}
	if !present {
		return 0, 0, false, false, fmt.Errorf("schema_migrations authority is absent")
	}
	if legacyVersion > embeddedMigrationTarget || version > embeddedMigrationTarget {
		return 0, 0, false, false, fmt.Errorf("migration authority exceeds embedded target: schema=%d user=%d target=%d", version, legacyVersion, embeddedMigrationTarget)
	}
	journalPresent, err := migrationJournalExists(ctx, handle)
	if err != nil {
		return 0, 0, false, false, err
	}
	if journalPresent {
		if err := validateMigrationJournalSchema(ctx, handle); err != nil {
			return 0, 0, false, false, err
		}
	}
	if dirty {
		if version == 0 || legacyVersion > version {
			return 0, 0, false, false, fmt.Errorf("dirty migration authorities diverge: schema=%d user=%d", version, legacyVersion)
		}
	} else if legacyVersion != version {
		// A process may die after golang-migrate has committed a clean
		// intermediate version but before the application-level user_version
		// write. Maintenance can resume that path only when the durable journal
		// proves the exact applied-DDL-before-clean window. Ordinary Open still
		// rejects it through validateSchemaContract's final-evidence check.
		if legacyVersion > version || !journalPresent || !migrationHasIntermediateCleanEvidence(ctx, handle, version) {
			return 0, 0, false, false, fmt.Errorf("clean migration authorities diverge: schema=%d user=%d", version, legacyVersion)
		}
	}
	if version > 0 && !journalPresent {
		if dirty && version == 1 && legacyVersion == 0 {
			if err := validateMigrationOnePreDDLState(ctx, handle); err != nil {
				return 0, 0, false, false, err
			}
		} else {
			return 0, 0, false, false, fmt.Errorf("migration journal is required for existing schema version %d", version)
		}
	}
	return legacyVersion, version, dirty, true, nil
}

func applyMigrations(ctx context.Context, handle *sql.DB) error {
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
	driver := &journaledMigrationDriver{delegate: baseDriver, handle: handle, runID: runID}
	migration, err := migrate.NewWithInstance("embedded", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("initialize migration protocol: %w", err)
	}
	// Do not call migration.Close here: the sqlite driver owns the supplied
	// handle and Close would terminate DB's shared connection pool. The iofs
	// source has no open resources after construction.
	if present && dirty {
		journalPresent, journalErr := migrationJournalExists(ctx, handle)
		if journalErr != nil {
			return journalErr
		}
		if version == 1 && !journalPresent && legacyVersion == 0 {
			// golang-migrate represents its pre-first-migration state as
			// database.NilVersion (-1), while the durable Talaria authority
			// permits only non-negative versions. The exact pre-DDL proof above
			// permits an atomic reset to the durable version-0 sentinel; the
			// journaled driver's Version maps that sentinel to NilVersion while
			// migration 1 runs.
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
			var resumeVersion int
			switch stage {
			case migrationStageRollbackBeforeCommit:
				resumeVersion = int(version)
				if resumeVersion > 0 {
					resumeVersion--
				}
			case migrationStageAppliedDDLBeforeClean:
				resumeVersion = int(version)
			default:
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
	if err := setSQLiteUserVersion(ctx, handle, version); err != nil {
		return err
	}
	fingerprint, err := migrationSchemaFingerprint(ctx, handle)
	if err != nil {
		return err
	}
	if err := recordMigrationStateWithStage(ctx, handle, runID, version, nil, &version, "", "", false, fingerprint); err != nil {
		return err
	}
	if err := validateMigrationJournalSchema(ctx, handle); err != nil {
		return fmt.Errorf("validate migration journal after maintenance: %w", err)
	}
	return nil
}

func validateOrdinaryOpenSchema(ctx context.Context, handle *sql.DB) error {
	_, version, dirty, present, err := validateMigrationAuthority(ctx, handle)
	if err != nil {
		return err
	}
	if !present || version != embeddedMigrationTarget {
		return fmt.Errorf("ordinary open rejects unsupported schema version")
	}
	if dirty {
		return fmt.Errorf("ordinary open rejects dirty schema version %d", version)
	}
	return validateSchemaContract(ctx, handle)
}

var canonicalSchemaObjects = map[string]string{
	"schema_migrations":                "table",
	"version_unique":                   "index",
	"workspaces":                       "table",
	"migration_journal":                "table",
	"workspace_bindings":               "table",
	"workspace_redirects":              "table",
	"memories":                         "table",
	"memory_revisions":                 "table",
	"memory_revisions_memory_id_id":    "index",
	"memories_current_revision_insert": "trigger",
	"memories_current_revision_update": "trigger",
	"memory_aliases":                   "table",
	"memory_fts":                       "table",
	"outbox":                           "table",
	"projection_state":                 "table",
	"usage_daily":                      "table",
	"usage_lifetime":                   "table",
	"usage_session_hits":               "table",
	"idempotency_requests":             "table",
	"idempotency_requests_expiry":      "index",
	"skill_promotions":                 "table",
	"deletion_receipts":                "table",
	"purge_operations":                 "table",
	"managed_backups":                  "table",
	"rule_activation_journal":          "table",
	"activation_epoch_audit":           "table",
}

func validateSchemaContract(ctx context.Context, handle *sql.DB) error {
	version, dirty, present, err := migrationVersion(ctx, handle)
	if err != nil {
		return err
	}
	if !present || dirty || version != embeddedMigrationTarget {
		return fmt.Errorf("schema contract requires clean migration version %d", embeddedMigrationTarget)
	}
	userVersion, err := sqliteUserVersion(ctx, handle)
	if err != nil {
		return err
	}
	if uint(userVersion) != embeddedMigrationTarget {
		return fmt.Errorf("schema contract user_version=%d, want %d", userVersion, embeddedMigrationTarget)
	}
	if err := validateLatestCleanMigrationEvidence(ctx, handle, version); err != nil {
		return err
	}
	for name, typ := range canonicalSchemaObjects {
		var count int
		if err := handle.QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", typ, name,
		).Scan(&count); err != nil {
			return domain.MapSQLiteError(err)
		}
		if count != 1 {
			return fmt.Errorf("schema contract missing %s %s", typ, name)
		}
	}
	got, err := schemaContractFingerprint(ctx, handle)
	if err != nil {
		return err
	}
	if canonicalSchemaContractFingerprint != "" && got != canonicalSchemaContractFingerprint {
		return fmt.Errorf("schema contract fingerprint mismatch")
	}
	return nil
}

// applyDownMigrations is intentionally unexported: reverse schema changes are
// test fixtures only and cannot be reached through ordinary maintenance.
func applyDownMigrations(ctx context.Context, handle *sql.DB) error {
	if err := ctx.Err(); err != nil {
		return err
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
	driver := &downMigrationDriver{delegate: baseDriver}
	migration, err := migrate.NewWithInstance("embedded", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("initialize migration protocol: %w", err)
	}
	legacyVersion, err := sqliteUserVersion(ctx, handle)
	if err != nil {
		return err
	}
	if _, _, present, err := migrationVersion(ctx, handle); err != nil {
		return err
	} else if !present && legacyVersion > 0 {
		if err := migration.Force(legacyVersion); err != nil {
			return fmt.Errorf("seed migration version %d: %w", legacyVersion, err)
		}
	}
	if err := migration.Steps(-4); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		// Reverse migrations are test fixtures only. A guard refusal must leave
		// migration authority clean at the forward schema version; the DDL
		// transaction itself has already rolled back.
		if restoreErr := migration.Force(int(embeddedMigrationTarget)); restoreErr != nil {
			return errors.Join(fmt.Errorf("reverse embedded migrations: %w", err), fmt.Errorf("restore migration authority: %w", restoreErr))
		}
		return fmt.Errorf("reverse embedded migrations: %w", err)
	}
	if _, err := handle.ExecContext(ctx, "DROP TABLE IF EXISTS schema_migrations"); err != nil {
		return domain.MapSQLiteError(err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 0"); err != nil {
		return domain.MapSQLiteError(err)
	}
	return nil
}

func validateEmbeddedMigrations() error {
	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		contents, err := db.Migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if err := validateMigrationSQL(entry.Name(), contents); err != nil {
			return err
		}
	}
	return nil
}

func validateMigrationSQL(name string, contents []byte) error {
	upper := strings.ToUpper(string(contents))
	for _, prohibited := range []string{"VACUUM", "PRAGMA JOURNAL_MODE", "PRAGMA WAL_CHECKPOINT"} {
		if strings.Contains(upper, prohibited) {
			return fmt.Errorf("ordinary migration %s contains prohibited non-transactional maintenance: %s", name, prohibited)
		}
	}
	return nil
}

func sqliteUserVersion(ctx context.Context, handle *sql.DB) (int, error) {
	var version int
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, domain.MapSQLiteError(err)
	}
	return version, nil
}

func setSQLiteUserVersion(ctx context.Context, handle *sql.DB, version uint) error {
	if version > uint(^uint(0)>>1) {
		return fmt.Errorf("migration version is too large")
	}
	if _, err := handle.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return domain.MapSQLiteError(err)
	}
	return nil
}

const (
	canonicalSchemaMigrationsTableDDL = "create table schema_migrations(version integer not null check(version >= 0),dirty integer not null check(dirty in(0,1)))"
	canonicalSchemaMigrationsIndexDDL = "create unique index version_unique on schema_migrations(version)"
)

func ensureCanonicalMigrationTable(ctx context.Context, handle *sql.DB) error {
	present, err := migrationTableExists(ctx, handle)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	tx, err := handle.BeginTx(ctx, nil)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER NOT NULL CHECK (version >= 0),
			dirty INTEGER NOT NULL CHECK (dirty IN (0,1))
		);
		CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
		INSERT INTO schema_migrations(version, dirty) VALUES (0, 0);
	`); err != nil {
		return domain.MapSQLiteError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.MapSQLiteError(err)
	}
	return nil
}

func resetPreMigrationOneAuthority(ctx context.Context, handle *sql.DB) error {
	result, err := handle.ExecContext(ctx, "UPDATE schema_migrations SET version = 0, dirty = 0")
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("pre-DDL migration-1 reset affected %d authority rows", rows)
	}
	return nil
}

func migrationTableExists(ctx context.Context, handle *sql.DB) (bool, error) {
	var count int
	if err := handle.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&count); err != nil {
		return false, domain.MapSQLiteError(err)
	}
	return count == 1, nil
}

func migrationJournalExists(ctx context.Context, handle *sql.DB) (bool, error) {
	var count int
	if err := handle.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&count); err != nil {
		return false, domain.MapSQLiteError(err)
	}
	return count == 1, nil
}

func normalizeSQLiteDDL(sqlText string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(sqlText)), " ")
	normalized = strings.ReplaceAll(normalized, ", ", ",")
	normalized = strings.ReplaceAll(normalized, " (", "(")
	normalized = strings.ReplaceAll(normalized, "( ", "(")
	normalized = strings.ReplaceAll(normalized, " )", ")")
	normalized = strings.ReplaceAll(normalized, "in (", "in(")
	return normalized
}

func validateSchemaMigrationsProtocol(ctx context.Context, handle *sql.DB) error {
	var tableDDL string
	if err := handle.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&tableDDL); err != nil {
		return domain.MapSQLiteError(err)
	}
	if got := normalizeSQLiteDDL(tableDDL); got != canonicalSchemaMigrationsTableDDL {
		return fmt.Errorf("schema_migrations table protocol drift: %q", got)
	}
	rows, err := handle.QueryContext(ctx, "PRAGMA table_info(schema_migrations)")
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer rows.Close()
	columns := make(map[string]struct {
		typ     string
		notNull int
	})
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		columns[name] = struct {
			typ     string
			notNull int
		}{typ: strings.ToLower(strings.TrimSpace(typ)), notNull: notNull}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	versionColumn, versionOK := columns["version"]
	dirtyColumn, dirtyOK := columns["dirty"]
	if len(columns) != 2 || !versionOK || !dirtyOK ||
		versionColumn.typ != "integer" || versionColumn.notNull != 1 ||
		dirtyColumn.typ != "integer" || dirtyColumn.notNull != 1 {
		return fmt.Errorf("schema_migrations columns drift")
	}
	var indexDDL string
	if err := handle.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'version_unique'").Scan(&indexDDL); err != nil {
		return domain.MapSQLiteError(err)
	}
	if got := normalizeSQLiteDDL(indexDDL); got != canonicalSchemaMigrationsIndexDDL {
		return fmt.Errorf("schema_migrations index protocol drift: %q", got)
	}
	var indexCount int
	if err := handle.QueryRowContext(ctx,
		"SELECT count(*) FROM pragma_index_list('schema_migrations') WHERE name = 'version_unique' AND \"unique\" = 1").Scan(&indexCount); err != nil {
		return domain.MapSQLiteError(err)
	}
	if indexCount != 1 {
		return fmt.Errorf("schema_migrations unique authority index is missing")
	}
	return nil
}

func validateMigrationAuthorityObjects(ctx context.Context, handle *sql.DB) error {
	rows, err := handle.QueryContext(ctx, `
		SELECT type, name, COALESCE(tbl_name, ''), COALESCE(sql, '')
		FROM sqlite_master
		WHERE type IN ('index', 'trigger', 'view')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name`)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var objectType, name, tableName, definition string
		if err := rows.Scan(&objectType, &name, &tableName, &definition); err != nil {
			return err
		}
		switch objectType {
		case "index":
			if strings.EqualFold(tableName, "migration_journal") {
				return fmt.Errorf("migration_journal has unexpected index %s", name)
			}
			if strings.EqualFold(tableName, "schema_migrations") {
				if name != "version_unique" || normalizeSQLiteDDL(definition) != canonicalSchemaMigrationsIndexDDL {
					return fmt.Errorf("schema_migrations has unexpected index %s", name)
				}
			}
		case "trigger":
			if strings.EqualFold(tableName, "migration_journal") || strings.EqualFold(tableName, "schema_migrations") {
				return fmt.Errorf("migration authority table %s has unexpected trigger %s", tableName, name)
			}
		case "view":
			if sqliteDefinitionReferencesMigrationAuthority(definition) {
				return fmt.Errorf("migration authority has unexpected view %s", name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func sqliteDefinitionReferencesMigrationAuthority(definition string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(definition), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	}) {
		if token == "migration_journal" || token == "schema_migrations" {
			return true
		}
	}
	return false
}

func migrationVersion(ctx context.Context, handle *sql.DB) (version uint, dirty, present bool, err error) {
	tablePresent, err := migrationTableExists(ctx, handle)
	if err != nil {
		return 0, false, false, err
	}
	if !tablePresent {
		return 0, false, false, nil
	}
	if err := validateSchemaMigrationsProtocol(ctx, handle); err != nil {
		return 0, false, false, err
	}
	var rowCount int
	if err := handle.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&rowCount); err != nil {
		return 0, false, false, domain.MapSQLiteError(err)
	}
	if rowCount == 0 {
		return 0, false, true, fmt.Errorf("schema_migrations has no authority row")
	}
	if rowCount != 1 {
		return 0, false, true, fmt.Errorf("schema_migrations has %d authority rows", rowCount)
	}
	var versionType, dirtyType string
	var row sql.NullInt64
	var dirtyValue sql.NullInt64
	err = handle.QueryRowContext(ctx,
		"SELECT typeof(version), typeof(dirty), version, dirty FROM schema_migrations",
	).Scan(&versionType, &dirtyType, &row, &dirtyValue)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, domain.MapSQLiteError(err)
	}
	if !row.Valid || row.Int64 < 0 {
		return 0, false, true, fmt.Errorf("invalid migration version")
	}
	if versionType != "integer" || dirtyType != "integer" {
		return 0, false, true, fmt.Errorf("migration authority values must use integer storage")
	}
	if !dirtyValue.Valid || (dirtyValue.Int64 != 0 && dirtyValue.Int64 != 1) {
		return 0, false, true, fmt.Errorf("invalid migration dirty value")
	}
	return uint(row.Int64), dirtyValue.Int64 == 1, true, nil
}

func validateMigrationOnePreDDLState(ctx context.Context, handle *sql.DB) error {
	rows, err := handle.QueryContext(ctx, `
		SELECT type, name, COALESCE(tbl_name, ''), COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT IN ('sqlite_sequence', 'schema_migrations', 'version_unique')
		ORDER BY type, name`)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer rows.Close()
	if rows.Next() {
		var typ, name, table, definition string
		if err := rows.Scan(&typ, &name, &table, &definition); err != nil {
			return err
		}
		return fmt.Errorf("migration-1 journal-less recovery is not pre-DDL: found %s %s", typ, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fingerprint, err := migrationSchemaFingerprint(ctx, handle)
	if err != nil {
		return err
	}
	if fingerprint != canonicalPreMigrationOneFingerprint {
		return fmt.Errorf("migration-1 journal-less recovery pre-DDL fingerprint mismatch")
	}
	return nil
}

type migrationEvidence struct {
	ID                int64
	RunID             string
	TargetVersion     int64
	CurrentVersion    int64
	FailedVersion     sql.NullInt64
	CompletedVersion  sql.NullInt64
	FailureStage      string
	SchemaFingerprint string
	Dirty             bool
}

func recordMigrationFailure(ctx context.Context, handle *sql.DB, migration *migrate.Migrate, migrationErr error) error {
	version, dirty, versionErr := migration.Version()
	if versionErr != nil {
		return fmt.Errorf("%w (read failed migration version: %v)", migrationErr, versionErr)
	}
	evidence, evidenceErr := latestMigrationEvidence(ctx, handle)
	if evidenceErr != nil {
		return errors.Join(migrationErr, evidenceErr)
	}
	if evidence.RunID == "" {
		return migrationErr
	}
	var failed *uint
	var completed *uint
	if dirty {
		failed = &version
		if version > 0 {
			completedVersion := version - 1
			completed = &completedVersion
		}
	} else {
		completed = &version
	}
	if err := recordMigrationStateWithStage(ctx, handle, evidence.RunID, version, failed, completed, safeMigrationError(migrationErr), evidence.FailureStage, dirty, evidence.SchemaFingerprint); err != nil {
		return errors.Join(migrationErr, err)
	}
	return migrationErr
}

func recordMigrationState(ctx context.Context, handle *sql.DB, runID string, current uint, failed, completed *uint, safeError string) error {
	fingerprint, err := migrationSchemaFingerprint(ctx, handle)
	if err != nil {
		return err
	}
	return recordMigrationStateWithStage(ctx, handle, runID, current, failed, completed, safeError, "", false, fingerprint)
}

func recordMigrationStateWithStage(ctx context.Context, handle *sql.DB, runID string, current uint, failed, completed *uint, safeError, failureStage string, dirty bool, fingerprint string) error {
	_, err := insertMigrationStateWithStage(ctx, handle, runID, current, failed, completed, safeError, failureStage, dirty, fingerprint)
	return err
}

func insertMigrationStateWithStage(ctx context.Context, handle *sql.DB, runID string, current uint, failed, completed *uint, safeError, failureStage string, dirty bool, fingerprint string) (int64, error) {
	started := time.Now().UTC().Format(time.RFC3339Nano)
	var failedValue any
	if failed != nil {
		failedValue = int64(*failed)
	}
	var completedValue any
	if completed != nil {
		completedValue = int64(*completed)
	}
	result, err := handle.ExecContext(ctx, `
		INSERT INTO migration_journal(
			run_id, target_version, current_version, failed_version, completed_version,
			failure_stage, schema_fingerprint, dirty, safe_error, started_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, embeddedMigrationTarget, current, failedValue, completedValue,
		failureStage, fingerprint, boolInt(dirty), safeError, started, started,
	)
	if err != nil {
		// Migration 1 can fail before its journal creates itself. There is no
		// durable row to corrupt in that window; every later write must surface
		// its error instead of silently relabeling a dirty schema.
		var table string
		if queryErr := handle.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&table); errors.Is(queryErr, sql.ErrNoRows) {
			if current == 1 {
				return 0, nil
			}
			return 0, fmt.Errorf("migration journal is absent before migration %d: %w", current, err)
		}
		return 0, domain.MapSQLiteError(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func updateMigrationStateWithStage(ctx context.Context, handle *sql.DB, id int64, runID string, current uint, failed, completed *uint, safeError, failureStage string, dirty bool, fingerprint string) error {
	var persistedStarted string
	if err := handle.QueryRowContext(ctx,
		"SELECT started_at FROM migration_journal WHERE id = ?", id,
	).Scan(&persistedStarted); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("migration journal row %d disappeared", id)
	} else if err != nil {
		return domain.MapSQLiteError(err)
	}
	startedAt, err := migrationJournalTimestamp(persistedStarted, "started_at")
	if err != nil {
		return fmt.Errorf("migration journal row %d has invalid started_at: %w", id, err)
	}
	updatedAt := time.Now().UTC()
	if updatedAt.Before(startedAt) {
		updatedAt = startedAt
	}
	updated := updatedAt.Format(time.RFC3339Nano)
	var failedValue any
	if failed != nil {
		failedValue = int64(*failed)
	}
	var completedValue any
	if completed != nil {
		completedValue = int64(*completed)
	}
	result, err := handle.ExecContext(ctx, `
		UPDATE migration_journal SET
			run_id = ?, target_version = ?, current_version = ?, failed_version = ?,
			completed_version = ?, failure_stage = ?, schema_fingerprint = ?, dirty = ?,
			safe_error = ?, updated_at = ?
		WHERE id = ?`,
		runID, embeddedMigrationTarget, current, failedValue, completedValue,
		failureStage, fingerprint, boolInt(dirty), safeError, updated, id)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("migration journal row %d disappeared", id)
	}
	return nil
}

func validateMigrationJournalSchema(ctx context.Context, handle *sql.DB) error {
	var tableDDL string
	if err := handle.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&tableDDL); err != nil {
		return domain.MapSQLiteError(err)
	}
	canonicalDDL, err := canonicalMigrationJournalDDL()
	if err != nil {
		return err
	}
	if got := normalizeSQLiteDDL(tableDDL); got != canonicalDDL {
		return fmt.Errorf("migration_journal table protocol drift: %q", got)
	}
	var strict int
	if err := handle.QueryRowContext(ctx,
		"SELECT strict FROM pragma_table_list WHERE type = 'table' AND name = 'migration_journal'").Scan(&strict); err != nil {
		return domain.MapSQLiteError(err)
	}
	if strict != 1 {
		return fmt.Errorf("migration_journal must be STRICT")
	}

	type columnContract struct {
		name       string
		typ        string
		notNull    int
		primaryKey int
		defaultSQL string
		hasDefault bool
	}
	want := []columnContract{
		{name: "id", typ: "INTEGER", primaryKey: 1},
		{name: "run_id", typ: "TEXT", notNull: 1, defaultSQL: "''", hasDefault: true},
		{name: "target_version", typ: "INTEGER", notNull: 1},
		{name: "current_version", typ: "INTEGER", notNull: 1},
		{name: "failed_version", typ: "INTEGER"},
		{name: "completed_version", typ: "INTEGER"},
		{name: "backup_id", typ: "TEXT"},
		{name: "failure_stage", typ: "TEXT", notNull: 1, defaultSQL: "''", hasDefault: true},
		{name: "schema_fingerprint", typ: "TEXT", notNull: 1, defaultSQL: "''", hasDefault: true},
		{name: "dirty", typ: "INTEGER", notNull: 1, defaultSQL: "0", hasDefault: true},
		{name: "safe_error", typ: "TEXT", notNull: 1, defaultSQL: "''", hasDefault: true},
		{name: "started_at", typ: "TEXT", notNull: 1},
		{name: "updated_at", typ: "TEXT", notNull: 1},
	}
	rows, err := handle.QueryContext(ctx, "PRAGMA table_info(migration_journal)")
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer rows.Close()
	for index := range want {
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return fmt.Errorf("migration_journal schema column %d is missing", index)
		}
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		contract := want[index]
		if cid != index || name != contract.name || strings.ToUpper(strings.TrimSpace(typ)) != contract.typ ||
			notNull != contract.notNull || primaryKey != contract.primaryKey {
			return fmt.Errorf("migration_journal column %d drift: name=%q type=%q not_null=%d primary_key=%d", index, name, typ, notNull, primaryKey)
		}
		if contract.hasDefault {
			if defaultValue == nil || fmt.Sprint(defaultValue) != contract.defaultSQL {
				return fmt.Errorf("migration_journal column %s default drift", name)
			}
		} else if defaultValue != nil {
			return fmt.Errorf("migration_journal column %s unexpectedly has a default", name)
		}
	}
	if rows.Next() {
		return fmt.Errorf("migration_journal has extra columns")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var foreignKeys int
	if err := handle.QueryRowContext(ctx,
		"SELECT count(*) FROM pragma_foreign_key_list('migration_journal')").Scan(&foreignKeys); err != nil {
		return domain.MapSQLiteError(err)
	}
	if foreignKeys != 0 {
		return fmt.Errorf("migration_journal has unexpected foreign keys")
	}

	var rowCount int
	if err := handle.QueryRowContext(ctx, "SELECT count(*) FROM migration_journal").Scan(&rowCount); err != nil {
		return domain.MapSQLiteError(err)
	}
	if rowCount == 0 {
		return fmt.Errorf("migration_journal has no durable evidence")
	}
	rows, err = handle.QueryContext(ctx, `
		SELECT typeof(id), typeof(run_id), typeof(target_version), typeof(current_version),
		       typeof(failed_version), typeof(completed_version), typeof(backup_id),
		       typeof(failure_stage), typeof(schema_fingerprint), typeof(dirty),
		       typeof(safe_error), typeof(started_at), typeof(updated_at),
		       id, run_id, target_version, current_version, failed_version, completed_version,
		       backup_id, failure_stage, schema_fingerprint, dirty, safe_error, started_at, updated_at
		FROM migration_journal ORDER BY id`)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer rows.Close()
	rowIndex := 0
	for rows.Next() {
		var types [13]string
		values := make([]any, len(types))
		destinations := make([]any, 0, len(types)*2)
		for index := range types {
			destinations = append(destinations, &types[index])
		}
		for index := range values {
			destinations = append(destinations, &values[index])
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		if err := validateMigrationJournalRow(rowIndex, types, values); err != nil {
			return err
		}
		rowIndex++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func canonicalMigrationJournalDDL() (string, error) {
	contents, err := db.Migrations.ReadFile("migrations/000001_core.up.sql")
	if err != nil {
		return "", err
	}
	start := strings.Index(string(contents), "CREATE TABLE migration_journal")
	if start < 0 {
		return "", fmt.Errorf("embedded migration journal DDL is missing")
	}
	remainder := string(contents)[start:]
	end := strings.Index(remainder, "\n\nCREATE TABLE workspace_bindings")
	if end < 0 {
		return "", fmt.Errorf("embedded migration journal DDL boundary is missing")
	}
	ddl := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(remainder[:end]), ";"))
	return normalizeSQLiteDDL(ddl), nil
}

func validateMigrationJournalRow(index int, types [13]string, values []any) error {
	expectedTypes := [...]string{"integer", "text", "integer", "integer", "null", "null", "null", "text", "text", "integer", "text", "text", "text"}
	for column, expected := range expectedTypes {
		actual := types[column]
		if column == 4 || column == 5 || column == 6 {
			if actual != "null" && actual != map[int]string{4: "integer", 5: "integer", 6: "text"}[column] {
				return fmt.Errorf("migration_journal row %d column %d has type %s", index, column, actual)
			}
			continue
		}
		if actual != expected {
			return fmt.Errorf("migration_journal row %d column %d has type %s, want %s", index, column, actual, expected)
		}
	}
	id, err := migrationJournalInteger(values[0], "id")
	if err != nil || id <= 0 {
		return fmt.Errorf("migration_journal row %d has invalid id", index)
	}
	runID, err := migrationJournalText(values[1], "run_id")
	if err != nil || runID == "" || len(runID) > 128 || strings.ContainsAny(runID, "\x00\r\n") {
		return fmt.Errorf("migration_journal row %d has invalid run_id", index)
	}
	target, err := migrationJournalInteger(values[2], "target_version")
	// Journal rows are append-only evidence.  Rows written by an older
	// binary legitimately retain that binary's target while a later binary
	// upgrades the same database.  Accept historical targets up to the
	// embedded target, but never accept zero, a future target, or a row whose
	// current schema version is ahead of the run it records.
	if err != nil || target <= 0 || target > int64(embeddedMigrationTarget) {
		return fmt.Errorf("migration_journal row %d has invalid target_version", index)
	}
	current, err := migrationJournalInteger(values[3], "current_version")
	if err != nil || current <= 0 || current > target {
		return fmt.Errorf("migration_journal row %d has invalid current_version", index)
	}
	failed, failedPresent, err := migrationJournalNullableInteger(values[4], "failed_version")
	if err != nil {
		return fmt.Errorf("migration_journal row %d has invalid failed_version", index)
	}
	completed, completedPresent, err := migrationJournalNullableInteger(values[5], "completed_version")
	if err != nil {
		return fmt.Errorf("migration_journal row %d has invalid completed_version", index)
	}
	backupID, backupPresent, err := migrationJournalNullableText(values[6], "backup_id")
	if err != nil || (backupPresent && backupID == "") {
		return fmt.Errorf("migration_journal row %d has invalid backup_id", index)
	}
	stage, err := migrationJournalText(values[7], "failure_stage")
	if err != nil || !map[string]bool{"": true, migrationStageStarted: true, migrationStageRollbackBeforeCommit: true, migrationStageAppliedDDLBeforeClean: true}[stage] {
		return fmt.Errorf("migration_journal row %d has invalid failure_stage", index)
	}
	fingerprint, err := migrationJournalText(values[8], "schema_fingerprint")
	if err != nil || len(fingerprint) != sha256.Size*2 || strings.ToLower(fingerprint) != fingerprint {
		return fmt.Errorf("migration_journal row %d has invalid schema_fingerprint", index)
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return fmt.Errorf("migration_journal row %d has invalid schema_fingerprint", index)
	}
	dirtyValue, err := migrationJournalInteger(values[9], "dirty")
	if err != nil || (dirtyValue != 0 && dirtyValue != 1) {
		return fmt.Errorf("migration_journal row %d has invalid dirty", index)
	}
	safeError, err := migrationJournalText(values[10], "safe_error")
	if err != nil || len(safeError) > 512 || strings.ContainsAny(safeError, "\x00\r\n") {
		return fmt.Errorf("migration_journal row %d has invalid safe_error", index)
	}
	startedAt, err := migrationJournalTimestamp(values[11], "started_at")
	if err != nil {
		return fmt.Errorf("migration_journal row %d has invalid started_at", index)
	}
	updatedAt, err := migrationJournalTimestamp(values[12], "updated_at")
	if err != nil || updatedAt.Before(startedAt) {
		return fmt.Errorf("migration_journal row %d has invalid updated_at", index)
	}
	if dirtyValue == 1 {
		if stage == "" || !failedPresent || failed != current || !completedPresent || completed != current-1 {
			return fmt.Errorf("migration_journal row %d has invalid dirty recovery invariant", index)
		}
		if backupPresent {
			return fmt.Errorf("migration_journal row %d has unexpected backup_id", index)
		}
		switch stage {
		case migrationStageStarted:
			if safeError != "" {
				return fmt.Errorf("migration_journal row %d has unexpected safe_error for stage %s", index, stage)
			}
		case migrationStageAppliedDDLBeforeClean:
			// The normal crash window has no safe error, while a maintenance
			// failure after DDL can retain a sanitized diagnostic for retry.
		case migrationStageRollbackBeforeCommit:
			if safeError == "" {
				return fmt.Errorf("migration_journal row %d has missing safe_error for rollback stage", index)
			}
		default:
			return fmt.Errorf("migration_journal row %d has invalid dirty recovery stage", index)
		}
	} else {
		if stage != "" || failedPresent || !completedPresent || completed != current {
			return fmt.Errorf("migration_journal row %d has invalid clean completion invariant", index)
		}
		if safeError != "" || backupPresent {
			return fmt.Errorf("migration_journal row %d has unexpected clean completion metadata", index)
		}
	}
	return nil
}

func migrationJournalInteger(value any, field string) (int64, error) {
	integer, ok := value.(int64)
	if !ok {
		return 0, fmt.Errorf("%s is not an integer", field)
	}
	return integer, nil
}

func migrationJournalText(value any, field string) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not text", field)
	}
	return text, nil
}

func migrationJournalNullableInteger(value any, field string) (int64, bool, error) {
	if value == nil {
		return 0, false, nil
	}
	integer, err := migrationJournalInteger(value, field)
	return integer, true, err
}

func migrationJournalNullableText(value any, field string) (string, bool, error) {
	if value == nil {
		return "", false, nil
	}
	text, err := migrationJournalText(value, field)
	return text, true, err
}

func migrationJournalTimestamp(value any, field string) (time.Time, error) {
	text, err := migrationJournalText(value, field)
	if err != nil || !strings.HasSuffix(text, "Z") {
		return time.Time{}, fmt.Errorf("%s is not canonical UTC RFC3339Nano", field)
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || parsed.UTC().Format(time.RFC3339Nano) != text {
		return time.Time{}, fmt.Errorf("%s is not canonical UTC RFC3339Nano", field)
	}
	return parsed, nil
}

func latestMigrationEvidence(ctx context.Context, handle *sql.DB) (migrationEvidence, error) {
	var evidence migrationEvidence
	err := handle.QueryRowContext(ctx, `
		SELECT id, run_id, target_version, current_version, failed_version,
		       completed_version, failure_stage, schema_fingerprint, dirty
		FROM migration_journal ORDER BY id DESC LIMIT 1`).Scan(
		&evidence.ID, &evidence.RunID, &evidence.TargetVersion,
		&evidence.CurrentVersion, &evidence.FailedVersion,
		&evidence.CompletedVersion, &evidence.FailureStage,
		&evidence.SchemaFingerprint, &evidence.Dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return migrationEvidence{}, fmt.Errorf("dirty migration has no durable run evidence")
	}
	if err != nil {
		return migrationEvidence{}, domain.MapSQLiteError(err)
	}
	return evidence, nil
}

func migrationHasIntermediateCleanEvidence(ctx context.Context, handle *sql.DB, version uint) bool {
	evidence, err := latestMigrationEvidence(ctx, handle)
	if err != nil || evidence.RunID == "" || evidence.TargetVersion != int64(embeddedMigrationTarget) ||
		evidence.CurrentVersion != int64(version) || !evidence.Dirty ||
		evidence.FailureStage != migrationStageAppliedDDLBeforeClean ||
		!evidence.FailedVersion.Valid || evidence.FailedVersion.Int64 != int64(version) ||
		version == 0 || !evidence.CompletedVersion.Valid || evidence.CompletedVersion.Int64 != int64(version-1) ||
		evidence.SchemaFingerprint == "" {
		return false
	}
	fingerprint, err := migrationSchemaFingerprint(ctx, handle)
	return err == nil && fingerprint == evidence.SchemaFingerprint
}

func validateLatestCleanMigrationEvidence(ctx context.Context, handle *sql.DB, version uint) error {
	evidence, err := latestMigrationEvidence(ctx, handle)
	if err != nil {
		return err
	}
	if evidence.RunID == "" || evidence.TargetVersion != int64(embeddedMigrationTarget) ||
		evidence.CurrentVersion != int64(version) || evidence.Dirty || evidence.FailureStage != "" ||
		evidence.FailedVersion.Valid || !evidence.CompletedVersion.Valid ||
		evidence.CompletedVersion.Int64 != int64(version) || evidence.SchemaFingerprint == "" {
		return fmt.Errorf("latest migration journal evidence is not a clean completion")
	}
	fingerprint, err := migrationSchemaFingerprint(ctx, handle)
	if err != nil {
		return err
	}
	if fingerprint != evidence.SchemaFingerprint {
		return fmt.Errorf("latest migration journal evidence fingerprint mismatch")
	}
	return nil
}

func classifyDirtyMigration(ctx context.Context, handle *sql.DB, version uint, evidence migrationEvidence) (string, error) {
	if evidence.RunID == "" || evidence.TargetVersion != int64(embeddedMigrationTarget) ||
		evidence.CurrentVersion != int64(version) || !evidence.Dirty ||
		!evidence.FailedVersion.Valid || evidence.FailedVersion.Int64 != int64(version) ||
		evidence.SchemaFingerprint == "" {
		return "", fmt.Errorf("dirty migration evidence is absent, stale, or inconsistent")
	}
	currentFingerprint, err := migrationSchemaFingerprint(ctx, handle)
	if err != nil {
		return "", err
	}
	postcondition, err := migrationPostcondition(ctx, handle, version)
	if err != nil {
		return "", err
	}
	switch evidence.FailureStage {
	case migrationStageRollbackBeforeCommit:
		if currentFingerprint != evidence.SchemaFingerprint || postcondition {
			return "", fmt.Errorf("rollback-before-commit evidence does not match schema state")
		}
		return migrationStageRollbackBeforeCommit, nil
	case migrationStageAppliedDDLBeforeClean:
		if currentFingerprint != evidence.SchemaFingerprint || !postcondition {
			return "", fmt.Errorf("applied-DDL-before-clean evidence does not match schema state")
		}
		return migrationStageAppliedDDLBeforeClean, nil
	case migrationStageStarted:
		// A process can die after the durable start row but before the DDL
		// transaction begins. Compare the pre-DDL fingerprint first; only a
		// complete target postcondition may classify the other crash window.
		if currentFingerprint == evidence.SchemaFingerprint && !postcondition {
			return migrationStageRollbackBeforeCommit, nil
		}
		if currentFingerprint != evidence.SchemaFingerprint && postcondition {
			return migrationStageAppliedDDLBeforeClean, nil
		}
		return "", fmt.Errorf("started migration evidence does not match a known crash position")
	default:
		return "", fmt.Errorf("migration dirty version %d has unknown recovery stage %q", version, evidence.FailureStage)
	}
}

func migrationPostcondition(ctx context.Context, handle *sql.DB, version uint) (bool, error) {
	names := []string{"workspaces"}
	switch version {
	case 2:
		names = []string{"usage_daily", "usage_lifetime", "usage_session_hits", "idempotency_requests", "skill_promotions", "deletion_receipts"}
	case 3:
		names = []string{"purge_operations", "managed_backups", "rule_activation_journal", "activation_epoch_audit"}
	case 4:
		names = []string{"curation_jobs", "curation_session_counters"}
	default:
		return false, fmt.Errorf("unsupported dirty migration version %d", version)
	}
	for _, name := range names {
		var exists int
		if err := handle.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", name).Scan(&exists); err != nil {
			return false, domain.MapSQLiteError(err)
		}
		if exists != 1 {
			return false, nil
		}
	}
	return true, nil
}

func migrationSchemaFingerprint(ctx context.Context, handle *sql.DB) (string, error) {
	return schemaFingerprint(ctx, handle, false, false)
}

func schemaContractFingerprint(ctx context.Context, handle *sql.DB) (string, error) {
	return schemaFingerprint(ctx, handle, true, true)
}

func schemaFingerprint(ctx context.Context, handle *sql.DB, includeMigrationJournal, includeSchemaMigrations bool) (string, error) {
	rows, err := handle.QueryContext(ctx, `
		SELECT type, name, COALESCE(tbl_name, ''), COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT IN ('sqlite_sequence')
		ORDER BY type, name`)
	if err != nil {
		return "", domain.MapSQLiteError(err)
	}
	defer rows.Close()
	var entries []string
	for rows.Next() {
		var typ, name, table, definition string
		if err := rows.Scan(&typ, &name, &table, &definition); err != nil {
			return "", err
		}
		if name == "migration_journal" && !includeMigrationJournal {
			continue
		}
		if name == "schema_migrations" && !includeSchemaMigrations {
			continue
		}
		entries = append(entries, typ+"\x00"+name+"\x00"+table+"\x00"+definition)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, entry := range entries {
		_, _ = hash.Write([]byte(entry))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func newMigrationRunID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create migration run identity: %w", err)
	}
	return "migration-" + hex.EncodeToString(raw[:]), nil
}

func safeMigrationError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', 0:
			return ' '
		default:
			return r
		}
	}, strings.TrimSpace(err.Error()))
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

func (database *DB) Close() error { return database.sql.Close() }
