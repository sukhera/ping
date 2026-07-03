package server

import (
	"context"
	"crypto/rsa"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/sukhera/ping/alert"
	"github.com/sukhera/ping/store"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 20 * time.Second
)

// Deps holds the dependencies handlers need. Nil fields degrade gracefully
// where the corresponding feature is not required yet.
type Deps struct {
	DB            *pgxpool.Pool
	Redis         *redis.Client
	AllowedOrigin string
	// BaseURL is the externally reachable API origin (PING_BASE_URL), used to
	// build full ping URLs (e.g. "<BaseURL>/p/<slug>") returned to clients.
	BaseURL string

	JWTPrivateKey    *rsa.PrivateKey
	JWTPublicKey     *rsa.PublicKey
	JWTAccessTTL     time.Duration
	JWTRefreshTTL    time.Duration
	RegistrationOpen bool
	// CookieSecure sets the refresh cookie's Secure attribute; true in
	// production, false so cookies work over plain http in local dev.
	CookieSecure bool

	// AlertChannel delivers emails for the "send test email" endpoint. Nil
	// when SMTP is unconfigured; the handler reports that clearly.
	AlertChannel alert.Channel
}

func New(addr string, deps Deps) *http.Server {
	r := chi.NewRouter()

	r.Use(requestIDMiddleware)
	r.Use(loggingMiddleware)
	r.Use(recoverMiddleware)
	r.Use(securityHeadersMiddleware)
	r.Use(corsMiddleware(deps.AllowedOrigin))

	r.Get("/health", healthHandler(deps))

	st := store.New(deps.DB, deps.Redis)

	ah := newAuthHandler(st, deps)
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/register", ah.register)
		r.Post("/login", ah.login)
		r.Post("/refresh", ah.refresh)
		r.Post("/logout", ah.logout)
	})

	// Ping ingestion (PING-008): public, unauthenticated, GET/POST/HEAD. The
	// static /start and /fail routes are registered before the /{code}
	// catch-all so chi matches them first.
	ch := newCheckinHandler(st, deps)
	r.Route("/p/{slug}", func(r chi.Router) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
			r.MethodFunc(method, "/", ch.success)
			r.MethodFunc(method, "/start", ch.start)
			r.MethodFunc(method, "/fail", ch.fail)
			r.MethodFunc(method, "/{code}", ch.exitCode)
		}
	})

	mh := newMonitorHandler(st, deps)
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(deps.JWTPublicKey))

		r.Post("/api/v1/schedule/describe", mh.describeSchedule)

		alh := newAlertingHandler(st, deps)
		r.Post("/api/v1/alerting/test", alh.sendTest)

		r.Get("/api/v1/events", mh.listEvents)

		r.Route("/api/v1/monitors", func(r chi.Router) {
			r.Post("/", mh.create)
			r.Get("/", mh.list)
			r.Get("/{id}", mh.get)
			r.Patch("/{id}", mh.update)
			r.Delete("/{id}", mh.delete)

			r.Post("/{id}/pause", mh.pause)
			r.Post("/{id}/resume", mh.resume)
			r.Post("/{id}/mute", mh.mute)
			r.Post("/{id}/unmute", mh.unmute)
			r.Get("/{id}/events", mh.listMonitorEvents)
		})
	})

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
