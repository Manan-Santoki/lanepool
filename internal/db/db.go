// Package db opens the Postgres pool and runs migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects to Postgres and migrates the schema to the latest version.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	cfg.MaxConns = 20
	var pool *pgxpool.Pool
	// The database may still be starting (docker compose); retry for a while.
	for attempt := 0; ; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			err = pool.Ping(ctx)
		}
		if err == nil {
			break
		}
		if pool != nil {
			pool.Close()
		}
		if attempt >= 30 || ctx.Err() != nil {
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		time.Sleep(time.Second)
	}
	if err := Migrate(pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	return migrate(sqlDB)
}

func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
