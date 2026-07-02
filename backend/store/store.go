// Package store is the business logic layer between server and db: it wraps
// sqlc-generated queries with domain rules (password hashing, token rotation,
// rate limiting) and returns errors the server package maps to HTTP.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/sukhera/ping/db"
)

type Store struct {
	// pool backs multi-statement transactions via withTx; q wraps the same
	// pool for the single-statement queries that make up most of this package.
	pool  *pgxpool.Pool
	q     db.Querier
	redis *redis.Client
}

func New(pool *pgxpool.Pool, redisClient *redis.Client) *Store {
	return &Store{
		pool:  pool,
		q:     db.New(pool),
		redis: redisClient,
	}
}

// withTx runs fn inside a single database transaction, passing it a *db.Queries
// bound to that transaction. It rolls back on any error or panic and commits
// otherwise; the deferred rollback is a no-op once Commit has succeeded.
func (s *Store) withTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(db.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit tx: %w", err)
	}
	return nil
}
