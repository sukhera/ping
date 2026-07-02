// Command ping runs the ping API server and background workers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"github.com/sukhera/ping/internal/config"
	"github.com/sukhera/ping/server"
)

func main() {
	role := flag.String("role", "all", "which components to run: api|worker|all")
	flag.Parse()

	if err := run(*role); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(role string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	setupLogger(cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	dbPool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("run: connect postgres: %w", err)
	}
	defer dbPool.Close()

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("run: parse REDIS_URL: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)
	defer func() {
		if err := redisClient.Close(); err != nil {
			slog.Error("run: close redis client", "error", err)
		}
	}()

	g, ctx := errgroup.WithContext(ctx)

	if role == "api" || role == "all" {
		srv := server.New(fmt.Sprintf(":%d", cfg.Port), server.Deps{
			DB:            dbPool,
			Redis:         redisClient,
			AllowedOrigin: cfg.CORSAllowedOrigin,
		})

		g.Go(func() error {
			slog.Info("server starting", "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("server: %w", err)
			}
			return nil
		})

		g.Go(func() error {
			<-ctx.Done()
			slog.Info("server shutting down")
			return server.Shutdown(context.Background(), srv)
		})
	}

	// role == "worker" is a no-op until PING-009+ introduces the scheduler,
	// prober, and alerter loops.

	return g.Wait()
}

func setupLogger(env string) {
	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	} else {
		handler = slog.NewTextHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(handler))
}
