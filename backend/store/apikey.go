package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sukhera/ping/db"
)

// apiKeyPrefix marks a plaintext API key so requireAuth can distinguish it
// from a JWT at a glance before hashing and looking it up.
const apiKeyPrefix = "pk_"

// APIKey is the store's domain representation of an api_keys row. KeyHash is
// never exposed outside this package — the server layer only ever sees the
// plaintext once, at creation, via CreatedAPIKey.PlainKey.
type APIKey struct {
	ID         string
	UserID     string
	Label      string
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// CreatedAPIKey is returned only from CreateAPIKey: PlainKey is the full
// "pk_..." token the caller must copy now — it cannot be recovered later,
// since only its SHA-256 hash is persisted.
type CreatedAPIKey struct {
	APIKey
	PlainKey string
}

// CreateAPIKey generates a new random key for userID, storing only its hash.
func (s *Store) CreateAPIKey(ctx context.Context, userID, label string) (CreatedAPIKey, error) {
	userUUID, err := pgUUID(userID)
	if err != nil {
		return CreatedAPIKey{}, err
	}

	plain, err := newAPIKeyToken()
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("store: generate api key: %w", err)
	}

	row, err := s.q.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:  userUUID,
		KeyHash: hashToken(plain),
		Label:   label,
	})
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("store: create api key: %w", err)
	}

	return CreatedAPIKey{APIKey: toAPIKey(row), PlainKey: plain}, nil
}

// ListAPIKeys returns userID's API keys, newest first. KeyHash is never
// included in the domain type, so there is nothing sensitive to leak here.
func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	userUUID, err := pgUUID(userID)
	if err != nil {
		return nil, err
	}

	rows, err := s.q.ListAPIKeysByUser(ctx, userUUID)
	if err != nil {
		return nil, fmt.Errorf("store: list api keys: %w", err)
	}

	keys := make([]APIKey, len(rows))
	for i, row := range rows {
		keys[i] = toAPIKey(row)
	}
	return keys, nil
}

// RevokeAPIKey revokes id if it is owned by callerUserID. Returns
// ErrNotFound if id doesn't exist or isn't owned by callerUserID — like
// DeleteMonitor, the WHERE id+user_id can't distinguish the two.
func (s *Store) RevokeAPIKey(ctx context.Context, id, callerUserID string) error {
	idUUID, err := pgUUID(id)
	if err != nil {
		return newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	userUUID, err := pgUUID(callerUserID)
	if err != nil {
		return newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	rowsAffected, err := s.q.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{ID: idUUID, UserID: userUUID})
	if err != nil {
		return fmt.Errorf("store: revoke api key: %w", err)
	}
	if rowsAffected == 0 {
		return newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	return nil
}

// AuthenticatedAPIKey identifies the user and key behind a successful
// AuthenticateAPIKey call. KeyID lets the caller (requireAuth) rate-limit
// per-key without re-hashing or ever handling the plaintext again.
type AuthenticatedAPIKey struct {
	UserID string
	KeyID  string
}

// AuthenticateAPIKey looks up plainKey (a full "pk_..." token) by its hash,
// rejecting unknown or revoked keys, and touches last_used_at on success so
// the request that authenticates also records use — no separate write path
// can skip it.
func (s *Store) AuthenticateAPIKey(ctx context.Context, plainKey string) (AuthenticatedAPIKey, error) {
	row, err := s.q.GetAPIKeyByHash(ctx, hashToken(plainKey))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthenticatedAPIKey{}, newHTTPError(errors.New("invalid or revoked api key"), http.StatusUnauthorized)
		}
		return AuthenticatedAPIKey{}, fmt.Errorf("store: get api key by hash: %w", err)
	}

	if err := s.q.TouchAPIKeyLastUsed(ctx, row.ID); err != nil {
		return AuthenticatedAPIKey{}, fmt.Errorf("store: touch api key last used: %w", err)
	}

	return AuthenticatedAPIKey{UserID: row.UserID.String(), KeyID: row.ID.String()}, nil
}

func toAPIKey(row db.ApiKey) APIKey {
	return APIKey{
		ID:         row.ID.String(),
		UserID:     row.UserID.String(),
		Label:      row.Label,
		LastUsedAt: timePtr(row.LastUsedAt),
		RevokedAt:  timePtr(row.RevokedAt),
		CreatedAt:  row.CreatedAt.Time,
	}
}

// newAPIKeyToken generates a "pk_" prefixed key with 32 bytes (256 bits) of
// crypto/rand entropy, hex-encoded — the same construction as refresh tokens
// (session.go's newRandomToken), plus the prefix requireAuth keys off of.
func newAPIKeyToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return apiKeyPrefix + hex.EncodeToString(b), nil
}
