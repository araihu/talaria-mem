# Environment Variables

## Environment

Environment is the small, non-secret path boundary used by acceptance
composition. Runtime code receives one parsed value; it does not call
os.Getenv while starting a server or opening storage.

The variables are intentionally required when this type is parsed. The
normal installation path uses DefaultEnvironment and creates its private
directories explicitly, while acceptance fixtures inject all three paths.

- `TALARIA_STATE_DIR` (**required**, non-empty) - Private state directory for the database, root-owned lifecycle state, and receipts.
- `TALARIA_CONFIG_DIR` (**required**, non-empty) - Private configuration directory for the bearer token, root key, and Codex setup metadata.
- `TALARIA_BACKUP_DIR` (**required**, non-empty) - Private directory containing authenticated database backups and sidecars.
