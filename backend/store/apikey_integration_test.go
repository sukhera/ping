//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sukhera/ping/db"
)

// seedUser creates a bare user row (no monitor) for tests that only need an
// account to own API keys.
func (e *checkinTestEnv) seedUser(t *testing.T) string {
	t.Helper()
	email := "apikey+" + strings.ReplaceAll(t.Name(), "/", "_") + time.Now().Format("150405.000000000") + "@example.test"
	user, err := e.q.CreateUser(context.Background(), db.CreateUserParams{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return user.ID.String()
}

// TestCreateAPIKey_PlaintextOnlyReturnedOnce is the AC's "key visible exactly
// once; only hash in DB" requirement: CreateAPIKey returns the plaintext, but
// the persisted row only ever has a hash, and the hash never equals the
// plaintext token (so a DB leak alone can't be replayed as a bearer token).
func TestCreateAPIKey_PlaintextOnlyReturnedOnce(t *testing.T) {
	e := newCheckinTestEnv(t)
	userID := e.seedUser(t)

	created, err := e.store.CreateAPIKey(context.Background(), userID, "CI runner")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !strings.HasPrefix(created.PlainKey, "pk_") {
		t.Errorf("PlainKey = %q, want pk_ prefix", created.PlainKey)
	}

	row, err := e.q.GetAPIKeyByHash(context.Background(), hashToken(created.PlainKey))
	if err != nil {
		t.Fatalf("GetAPIKeyByHash: %v", err)
	}
	if row.KeyHash == created.PlainKey {
		t.Error("stored key_hash must not equal the plaintext key")
	}
	if row.KeyHash != hashToken(created.PlainKey) {
		t.Error("stored key_hash must be the SHA-256 hash of the plaintext key")
	}
}

func TestAuthenticateAPIKey_UnknownKeyRejected(t *testing.T) {
	e := newCheckinTestEnv(t)

	if _, err := e.store.AuthenticateAPIKey(context.Background(), "pk_doesnotexist"); err == nil {
		t.Error("AuthenticateAPIKey should reject an unknown key")
	}
}

// TestAuthenticateAPIKey_RevokedKeyRejectedImmediately is the AC's "revoked
// key -> 401 within one request (no cache window)": the very next
// authentication attempt after RevokeAPIKey must fail, with no TTL or
// propagation delay.
func TestAuthenticateAPIKey_RevokedKeyRejectedImmediately(t *testing.T) {
	e := newCheckinTestEnv(t)
	userID := e.seedUser(t)

	created, err := e.store.CreateAPIKey(context.Background(), userID, "to be revoked")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if _, err := e.store.AuthenticateAPIKey(context.Background(), created.PlainKey); err != nil {
		t.Fatalf("AuthenticateAPIKey before revoke: %v", err)
	}

	if err := e.store.RevokeAPIKey(context.Background(), created.ID, userID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}

	if _, err := e.store.AuthenticateAPIKey(context.Background(), created.PlainKey); err == nil {
		t.Error("AuthenticateAPIKey must reject a revoked key on the very next request")
	}
}

func TestAuthenticateAPIKey_TouchesLastUsedAt(t *testing.T) {
	e := newCheckinTestEnv(t)
	userID := e.seedUser(t)

	created, err := e.store.CreateAPIKey(context.Background(), userID, "touch test")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	keys, err := e.store.ListAPIKeys(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].LastUsedAt != nil {
		t.Fatalf("expected one key with nil LastUsedAt before use, got %+v", keys)
	}

	if _, err := e.store.AuthenticateAPIKey(context.Background(), created.PlainKey); err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}

	keys, err = e.store.ListAPIKeys(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListAPIKeys after auth: %v", err)
	}
	if len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("expected LastUsedAt to be set after authentication, got %+v", keys)
	}
}

func TestRevokeAPIKey_ForeignUserReturnsNotFound(t *testing.T) {
	e := newCheckinTestEnv(t)
	owner := e.seedUser(t)
	other := e.seedUser(t)

	created, err := e.store.CreateAPIKey(context.Background(), owner, "owner's key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	err = e.store.RevokeAPIKey(context.Background(), created.ID, other)
	if err == nil {
		t.Fatal("RevokeAPIKey should fail when the caller doesn't own the key")
	}
}

func TestListAPIKeys_ScopedToUser(t *testing.T) {
	e := newCheckinTestEnv(t)
	userA := e.seedUser(t)
	userB := e.seedUser(t)

	if _, err := e.store.CreateAPIKey(context.Background(), userA, "a-key"); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), userB, "b-key"); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	keysA, err := e.store.ListAPIKeys(context.Background(), userA)
	if err != nil {
		t.Fatalf("ListAPIKeys A: %v", err)
	}
	if len(keysA) != 1 || keysA[0].Label != "a-key" {
		t.Fatalf("ListAPIKeys A = %+v, want exactly [a-key]", keysA)
	}
}
