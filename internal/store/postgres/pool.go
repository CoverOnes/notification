// Package postgres provides pgxpool-based store implementations for the notification service.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig holds optional tuning parameters for NewPool.
// Zero values fall back to safe defaults (MaxConns=10, MinConns=2).
type PoolConfig struct {
	MaxConns int32
	MinConns int32
}

// NewPool creates and validates a pgxpool.Pool with sensible production defaults.
// Connection budget per CONVENTIONS §12 and backend-security-design §5.3.
//
// If schema is non-empty, the pool will:
//  1. Create the schema (CREATE SCHEMA IF NOT EXISTS) once on startup.
//  2. Set search_path=<schema> for every connection via AfterConnect so all
//     queries resolve against the schema without explicit qualification.
//
// Identifier quoting uses pgx.Identifier.Sanitize() so reserved words such as
// "user" are safely double-quoted and cannot cause PG error 42601.
//
// If schema is empty the pool behaves identically to before (public schema).
// The caller is responsible for validating that schema matches [a-zA-Z0-9_]+
// before passing it here (config.validate() enforces this).
func NewPool(ctx context.Context, dsn, schema string, opts ...PoolConfig) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}

	// Apply tuning; fall back to safe defaults when callers omit opts or pass zero.
	var pc PoolConfig
	if len(opts) > 0 {
		pc = opts[0]
	}

	if pc.MaxConns > 0 {
		cfg.MaxConns = pc.MaxConns
	} else {
		cfg.MaxConns = 10
	}

	if pc.MinConns > 0 {
		cfg.MinConns = pc.MinConns
	} else {
		cfg.MinConns = 2
	}

	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	if schema != "" {
		// quotedSchema is the safely double-quoted identifier for the schema name.
		// pgx.Identifier.Sanitize() handles reserved words (e.g. "user") that would
		// otherwise cause PG syntax error 42601 with bare string concatenation.
		quotedSchema := pgx.Identifier{schema}.Sanitize()

		// AfterConnect sets the search_path for every new connection.
		cfg.AfterConnect = func(connectCtx context.Context, conn *pgx.Conn) error {
			_, execErr := conn.Exec(connectCtx, "SET search_path = "+quotedSchema)
			if execErr != nil {
				return fmt.Errorf("set search_path=%s: %w", schema, execErr)
			}

			return nil
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}

	if schema != "" {
		quotedSchema := pgx.Identifier{schema}.Sanitize()

		// Create the schema once on startup (idempotent).
		// quotedSchema is safely double-quoted via pgx.Identifier.Sanitize().
		if _, execErr := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quotedSchema); execErr != nil {
			pool.Close()
			return nil, fmt.Errorf("create schema %q: %w", schema, execErr)
		}
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}
