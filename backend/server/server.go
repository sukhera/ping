package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 20 * time.Second
)

// Deps holds the dependencies handlers need. Nil fields degrade gracefully
// where the corresponding feature is not required yet (e.g. no store package until PING-007).
type Deps struct {
	DB            *pgxpool.Pool
	Redis         *redis.Client
	AllowedOrigin string
}

func New(addr string, deps Deps) *http.Server {
	r := chi.NewRouter()

	r.Use(requestIDMiddleware)
	r.Use(loggingMiddleware)
	r.Use(recoverMiddleware)
	r.Use(securityHeadersMiddleware)
	r.Use(corsMiddleware(deps.AllowedOrigin))

	r.Get("/health", healthHandler(deps))

	return &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// Shutdown gracefully drains in-flight requests, bounded by shutdownTimeout.
func Shutdown(ctx context.Context, srv *http.Server) error {
	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
