// Package config loads and validates process configuration from the environment.
// This is the only package where os.Getenv appears.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port              int
	Env               string
	DatabaseURL       string
	RedisURL          string
	CORSAllowedOrigin string

	JWTPrivateKeyPath string
	JWTPublicKeyPath  string
	JWTAccessTTL      time.Duration
	JWTRefreshTTL     time.Duration
	RegistrationOpen  bool
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

	return cfg, nil
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
