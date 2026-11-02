// Package db carries the League schema so the API can apply it on startup.
package db

import _ "embed"

// Schema is server/db/schema.sql, idempotent: safe to apply on every start.
//
//go:embed schema.sql
var Schema string
