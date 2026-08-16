package db

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate -f sqlc.yaml
//go:generate sh ../scripts/update-sqlc-manifest.sh

import "embed"

// Migrations contains the reviewed SQL migration sources.
//
//go:embed migrations/*.sql
var Migrations embed.FS
