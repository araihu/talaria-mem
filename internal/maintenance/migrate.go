package maintenance

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

type MigrationStep struct {
	Version uint
	Name    string
	SQL     []byte
}

func ValidateMigrationSQL(name string, sql []byte) error {
	if err := validateMetadata(name, 256, true); err != nil {
		return err
	}
	if len(sql) == 0 {
		return ErrMigrationRejected
	}
	// VACUUM, journal mode changes, and explicit transaction control are not
	// valid inside an ordinary forward migration.  They belong to the
	// maintenance protocol around the migration, not inside its transaction.
	normalized := strings.ToLower(string(sql))
	for _, token := range []string{"vacuum", "pragma journal_mode", "pragma wal_checkpoint", "begin transaction", "commit;", "rollback;"} {
		if strings.Contains(normalized, token) {
			return fmt.Errorf("%w: %s", ErrMigrationRejected, name)
		}
	}
	return nil
}

var migrationVersionPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,10})(?:[^0-9]|$)`)

func ValidateMigrationSet(steps []MigrationStep) error {
	seen := make(map[uint]struct{}, len(steps))
	var previous uint
	for _, step := range steps {
		if step.Version == 0 || (previous != 0 && step.Version <= previous) {
			return ErrMigrationRejected
		}
		if _, ok := seen[step.Version]; ok {
			return ErrMigrationRejected
		}
		seen[step.Version] = struct{}{}
		if err := ValidateMigrationSQL(step.Name, step.SQL); err != nil {
			return err
		}
		if match := migrationVersionPattern.FindStringSubmatch(step.Name); len(match) > 0 && strings.TrimLeft(match[1], "0") != strings.TrimLeft(fmt.Sprint(step.Version), "0") {
			return ErrMigrationRejected
		}
		previous = step.Version
	}
	return nil
}

type MigrationDriver interface {
	CurrentVersion(ctx context.Context) (uint, error)
	Apply(ctx context.Context, step MigrationStep) error
	Integrity(ctx context.Context) error
}

type MigrationRun struct {
	ID               string    `json:"id"`
	TargetVersion    uint      `json:"target_version"`
	CurrentVersion   uint      `json:"current_version"`
	FailedVersion    uint      `json:"failed_version,omitempty"`
	CompletedVersion uint      `json:"completed_version,omitempty"`
	BackupID         string    `json:"backup_id,omitempty"`
	FailureStage     string    `json:"failure_stage,omitempty"`
	SafeError        string    `json:"safe_error,omitempty"`
	Dirty            bool      `json:"dirty"`
	StartedAt        time.Time `json:"started_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type MigrationManager struct {
	Driver    MigrationDriver
	Backup    func(context.Context) (string, error)
	LockPath  string
	Clock     interface{ Now() time.Time }
	Failpoint func(string) error
	mu        sync.Mutex
	run       MigrationRun
	ready     bool
}

func NewMigrationManager(driver MigrationDriver, backup func(context.Context) (string, error), lockPath string, clock interface{ Now() time.Time }) *MigrationManager {
	return &MigrationManager{Driver: driver, Backup: backup, LockPath: lockPath, Clock: clock, ready: true}
}

func (manager *MigrationManager) Ready() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.ready
}

func (manager *MigrationManager) Run(ctx context.Context, steps []MigrationStep) (MigrationRun, error) {
	if manager == nil || manager.Driver == nil {
		return MigrationRun{}, ErrMigrationUnready
	}
	if err := ValidateMigrationSet(steps); err != nil {
		return MigrationRun{}, err
	}
	if len(steps) == 0 {
		version, err := manager.Driver.CurrentVersion(ctx)
		return MigrationRun{CurrentVersion: version, TargetVersion: version}, err
	}
	lockPath := manager.LockPath
	if lockPath == "" {
		return MigrationRun{}, ErrUnsafePath
	}
	var result MigrationRun
	err := WithLock(ctx, lockPath, func(ctx context.Context) error {
		current, err := manager.Driver.CurrentVersion(ctx)
		if err != nil {
			return err
		}
		now := migrationNow(manager.Clock)
		manager.mu.Lock()
		previousRun := manager.run
		manager.mu.Unlock()
		run := previousRun
		if !run.Dirty || run.TargetVersion != steps[len(steps)-1].Version || run.ID == "" {
			run = MigrationRun{ID: operationID(), TargetVersion: steps[len(steps)-1].Version, CurrentVersion: current, StartedAt: now, UpdatedAt: now, Dirty: true}
		} else {
			run.CurrentVersion, run.UpdatedAt, run.Dirty = current, now, true
			run.FailedVersion, run.FailureStage, run.SafeError = 0, "", ""
		}
		if run.BackupID == "" && manager.Backup != nil {
			run.BackupID, err = manager.Backup(ctx)
			if err != nil {
				run.FailureStage, run.SafeError = "backup", SafeError(err)
				manager.setRun(run, false)
				return err
			}
		}
		manager.setRun(run, false)
		for _, step := range steps {
			if step.Version <= current {
				continue
			}
			if err := manager.fail("before_" + step.Name); err != nil {
				run.FailedVersion, run.FailureStage, run.SafeError = step.Version, "before_apply", SafeError(err)
				manager.setRun(run, false)
				return err
			}
			if err := manager.Driver.Apply(ctx, step); err != nil {
				run.FailedVersion, run.FailureStage, run.SafeError = step.Version, "apply", SafeError(err)
				manager.setRun(run, false)
				return errors.Join(ErrMigrationUnready, err)
			}
			current = step.Version
			run.CurrentVersion, run.CompletedVersion, run.UpdatedAt = current, current, migrationNow(manager.Clock)
			manager.setRun(run, false)
			if err := manager.fail("after_" + step.Name); err != nil {
				run.FailedVersion, run.FailureStage, run.SafeError = step.Version, "after_apply", SafeError(err)
				manager.setRun(run, false)
				return err
			}
		}
		if err := manager.Driver.Integrity(ctx); err != nil {
			run.FailureStage, run.SafeError = "post_integrity", SafeError(err)
			manager.setRun(run, false)
			return errors.Join(ErrMigrationUnready, err)
		}
		run.Dirty, run.UpdatedAt, run.FailureStage = false, migrationNow(manager.Clock), ""
		manager.setRun(run, true)
		result = run
		return nil
	})
	if err != nil {
		manager.mu.Lock()
		result = manager.run
		manager.mu.Unlock()
	}
	return result, err
}

func (manager *MigrationManager) Resume(ctx context.Context, steps []MigrationStep) (MigrationRun, error) {
	return manager.Run(ctx, steps)
}

func (manager *MigrationManager) Journal() MigrationRun {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.run
}

func (manager *MigrationManager) setRun(run MigrationRun, ready bool) {
	manager.mu.Lock()
	manager.run, manager.ready = run, ready
	manager.mu.Unlock()
}

func (manager *MigrationManager) fail(phase string) error {
	if manager.Failpoint == nil {
		return nil
	}
	return manager.Failpoint(phase)
}

func migrationNow(clock interface{ Now() time.Time }) time.Time {
	if clock == nil {
		return time.Now().UTC()
	}
	return clock.Now().UTC()
}
