package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pingTimeout = 5 * time.Second

type Postgres struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}

	pingContext, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := pool.Ping(pingContext); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}

	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	if p != nil && p.pool != nil {
		p.pool.Close()
	}
}

func (p *Postgres) Ping(ctx context.Context) error            { return p.pool.Ping(ctx) }
func (p *Postgres) Begin(ctx context.Context) (pgx.Tx, error) { return p.pool.Begin(ctx) }
func (p *Postgres) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}
func (p *Postgres) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}
func (p *Postgres) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}
