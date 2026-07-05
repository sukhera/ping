// Package config loads and validates process configuration from the environment.
// This is the only package where os.Getenv appears.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port              int
	Env               string
	BaseURL           string
	DatabaseURL       string
	RedisURL          string
	CORSAllowedOrigin string

	JWTPrivateKeyPath string
	JWTPublicKeyPath  string
	JWTAccessTTL      time.Duration
	JWTRefreshTTL     time.Duration
	RegistrationOpen  bool

	SMTP SMTPConfig

	// SSRFAllowlist is the prober's dial-time SSRF guard override (PING-017):
	// CIDR prefixes that bypass the private/loopback/link-local/metadata
	// block, for self-hosters genuinely monitoring internal targets. Empty
	// means the guard is fully enforced.
	SSRFAllowlist []netip.Prefix

	// RetentionDays is how long raw checkins/probe_results/events are kept
	// before the rollup worker prunes them (PRD F6.4, PING-020). daily_stats
	// rollups are kept indefinitely regardless of this setting.
	RetentionDays int
}

// SMTPConfig holds outbound email settings. It is optional: a fresh install
// boots without SMTP configured, and alert delivery / the "send test email"
// endpoint report a clear "not configured" error until SMTP_HOST is set.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Configured reports whether enough SMTP settings are present to attempt a
// send. Host and From are the minimum; auth is optional (some relays accept
// unauthenticated submission from trusted networks).
func (c SMTPConfig) Configured() bool {
	return c.Host != "" && c.From != ""
}

// Load reads required configuration from the environment and validates it.
// It returns an error naming the first missing or invalid variable so callers
// can fail fast with a clear message.
func Load() (Config, error) {
	var cfg Config
	var err error

	port, err := requireInt("PING_PORT")
	if err != nil {
		return Config{}, err
	}
	cfg.Port = port

	cfg.Env, err = require("PING_ENV")
	if err != nil {
		return Config{}, err
	}

	cfg.BaseURL, err = require("PING_BASE_URL")
	if err != nil {
		return Config{}, err
	}

	cfg.DatabaseURL, err = require("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}

	cfg.RedisURL, err = require("REDIS_URL")
	if err != nil {
		return Config{}, err
	}

	cfg.CORSAllowedOrigin, err = require("CORS_ALLOWED_ORIGIN")
	if err != nil {
		return Config{}, err
	}

	cfg.JWTPrivateKeyPath, err = require("JWT_PRIVATE_KEY_PATH")
	if err != nil {
		return Config{}, err
	}

	cfg.JWTPublicKeyPath, err = require("JWT_PUBLIC_KEY_PATH")
	if err != nil {
		return Config{}, err
	}

	cfg.JWTAccessTTL, err = requireDuration("JWT_ACCESS_TTL")
	if err != nil {
		return Config{}, err
	}

	cfg.JWTRefreshTTL, err = requireDuration("JWT_REFRESH_TTL")
	if err != nil {
		return Config{}, err
	}

	cfg.RegistrationOpen, err = requireBool("REGISTRATION_OPEN")
	if err != nil {
		return Config{}, err
	}

	// SMTP is optional. Port defaults to 587 (submission/STARTTLS) when unset
	// or blank; only a non-integer value is an error.
	cfg.SMTP.Host = os.Getenv("SMTP_HOST")
	cfg.SMTP.Username = os.Getenv("SMTP_USERNAME")
	cfg.SMTP.Password = os.Getenv("SMTP_PASSWORD")
	cfg.SMTP.From = os.Getenv("SMTP_FROM")
	cfg.SMTP.Port, err = optionalInt("SMTP_PORT", 587)
	if err != nil {
		return Config{}, err
	}

	cfg.SSRFAllowlist, err = parseAllowlist(os.Getenv("SSRF_ALLOWLIST"))
	if err != nil {
		return Config{}, err
	}

	cfg.RetentionDays, err = optionalInt("RETENTION_DAYS", 90)
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// parseAllowlist parses a comma-separated list of CIDR prefixes or bare IPs
// (treated as a /32 or /128) from SSRF_ALLOWLIST. Empty input yields an empty
// (fully-enforced) allowlist.
func parseAllowlist(v string) ([]netip.Prefix, error) {
	if v == "" {
		return nil, nil
	}
	var prefixes []netip.Prefix
	for entry := range strings.SplitSeq(v, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			prefixes = append(prefixes, prefix)
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("config: SSRF_ALLOWLIST entry %q is not a valid IP or CIDR", entry)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

func require(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("config: required environment variable %s is not set", name)
	}
	return v, nil
}

func requireInt(name string) (int, error) {
	v, err := require(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: environment variable %s must be an integer, got %q", name, v)
	}
	return n, nil
}

// optionalInt returns the integer value of an environment variable, or def if
// it is unset or empty. A present-but-non-integer value is an error.
func optionalInt(name string, def int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: environment variable %s must be an integer, got %q", name, v)
	}
	return n, nil
}

func requireDuration(name string) (time.Duration, error) {
	v, err := require(name)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: environment variable %s must be a duration, got %q", name, v)
	}
	return d, nil
}

func requireBool(name string) (bool, error) {
	v, err := require(name)
	if err != nil {
		return false, err
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: environment variable %s must be a boolean, got %q", name, v)
	}
	return b, nil
}
