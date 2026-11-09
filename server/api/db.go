package main

import (
	"context"

	"github.com/iamsorenl/arcade-soccer/server/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplySchema runs schema.sql. Every statement in it is safe to re-run.
func ApplySchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, db.Schema)
	return err
}
