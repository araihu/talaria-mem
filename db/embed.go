package db

import "embed"

// Migrations contains the reviewed SQL migration sources.
//
//go:embed migrations/*.sql
var Migrations embed.FS
