package maintenance

import "context"

// Stable aliases keep composition code readable while the concrete service
// names remain descriptive in package documentation.
type MaintenanceLock = Lock
type BackupInventory = Inventory
type BackupInventoryReader = InventoryReader
type PurgeService = PurgeManager
type BackupService = BackupManager
type RestoreService = RestoreManager
type MigrationService = MigrationManager
type RuleUpgradeService = RuleUpgradeManager

func AcquireMaintenanceLock(ctx context.Context, path string) (*Lock, error) {
	return AcquireLock(ctx, path)
}
