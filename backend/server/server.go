package server

import (
	"context"
	"crypto/rsa"
	"net/http"
	"net/netip"
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

	// Env is the process environment (PING_ENV: "development"|"test"|"production").
	// Used to runtime-gate test-only surfaces (e.g. the e2e time-warp endpoint)
	// in addition to their compile-time build tag — belt and suspenders.
	Env string

	// SSRFAllowlist mirrors config.Config.SSRFAllowlist; only used to drive the
	// prober from the e2e-only /test/advance-clock endpoint (backend/server/testclock.go).
	SSRFAllowlist []netip.Prefix

	JWTPrivateKey    *rsa.PrivateKey
	JWTPublicKey     *rsa.PublicKey
	JWTAccessTTL     time.Duration
	JWTRefreshTTL    time.Duration
	RegistrationOpen bool
	// CookieSecure sets the refresh cookie's Secure attribute; true in
	// production, false so cookies work over plain http in local dev.
	CookieSecure bool

	// AuthRateLimitDisabled turns off the per-IP auth rate limiter
	// (register/login). Set ONLY in the e2e test environment (PING_ENV=test):
	// the Playwright suite shares one IP across all workers, so a growing set
	// of spec files quickly exhausts the tight 5/min per-IP budget. Never
	// enabled in dev or production — the limiter is a real abuse control there.
	AuthRateLimitDisabled bool

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

	// Test-only time-warp endpoint (PING-022): registerTestRoutes is a no-op
	// unless this binary was built with `-tags e2e` (see testclock_e2e.go /
	// testclock_notag.go) AND deps.Env == "test" — both the build tag and the
	// runtime check must hold, so an e2e-tagged binary accidentally deployed
	// anywhere but the test environment still exposes nothing extra.
	registerTestRoutes(r, st, deps)

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
	// The management API: accepts a JWT (web session) or a "pk_..." API key
	// (PING-016), so the full monitor CRUD surface is scriptable with curl and
	// just a key, per its AC.
	r.Group(func(r chi.Router) {
		r.Use(requireAuthOrAPIKey(deps.JWTPublicKey, st))

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
			r.Get("/{id}/checkins", mh.listMonitorCheckins)
			r.Get("/{id}/probe-results", mh.listMonitorProbeResults)
			r.Get("/{id}/latency", mh.getMonitorLatencySeries)
		})
	})

	// API key management is JWT-only (a logged-in web session): a leaked key
	// must not be able to mint or revoke other keys for the account.
	kh := newAPIKeyHandler(st, deps)
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(deps.JWTPublicKey))

		r.Route("/api/v1/apikeys", func(r chi.Router) {
			r.Post("/", kh.create)
			r.Get("/", kh.list)
			r.Delete("/{id}", kh.revoke)
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
