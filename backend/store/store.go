// Package store is the business logic layer between server and db: it wraps
// sqlc-generated queries with domain rules (password hashing, token rotation,
// rate limiting) and returns errors the server package maps to HTTP.
package store

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/sukhera/ping/db"
)

type Store struct {
	q     db.Querier
	redis *redis.Client
}

func New(pool *pgxpool.Pool, redisClient *redis.Client) *Store {
	return &Store{
		q:     db.New(pool),
		redis: redisClient,
	}
}
