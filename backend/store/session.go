package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// RefreshToken is the plaintext token handed to the client (in the httpOnly
// cookie) alongside metadata the caller needs to set the cookie correctly.
// The plaintext is never persisted — only its SHA-256 hash is stored.
type RefreshToken struct {
	PlainToken string
	FamilyID   string
	ExpiresAt  time.Time
}

// IssueRefreshToken starts a brand-new token family (fresh login).
func (s *Store) IssueRefreshToken(ctx context.Context, userID string, ttl time.Duration) (RefreshToken, error) {
	familyID, err := newRandomUUID()
	if err != nil {
		return RefreshToken{}, fmt.Errorf("store: generate family id: %w", err)
	}
	return s.issueInFamily(ctx, userID, familyID, ttl)
}

// RotateRefreshToken validates plainToken and, if it is the current,
// unrotated token in its family, marks it rotated and issues a new token in
// the same family (sliding expiration: the new token gets the full ttl).
//
// If plainToken has already been rotated (i.e. it is being replayed after a
// legitimate rotation already happened) or the family has been revoked, this
// is treated as possible theft: the entire family is revoked and
// ErrRefreshReuse is returned, invalidating every token issued in that
// lineage, including ones not yet used.
//
// The rotate-if-unrotated step is a single conditional UPDATE (see
// RotateRefreshTokenIfUnrotated), not a separate read-then-write, so two
// concurrent requests replaying the same token cannot both win: only one
// UPDATE can match WHERE rotated_at IS NULL before the first commits.
func (s *Store) RotateRefreshToken(ctx context.Context, plainToken string, ttl time.Duration) (RefreshToken, string, error) {
	hash := hashToken(plainToken)

	row, err := s.q.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RefreshToken{}, "", newHTTPError(ErrInvalidRefreshToken, http.StatusUnauthorized)
		}
		return RefreshToken{}, "", fmt.Errorf("store: get refresh token: %w", err)
	}

	switch {
	case row.RevokedAt.Valid:
		return RefreshToken{}, "", newHTTPError(ErrInvalidRefreshToken, http.StatusUnauthorized)
	case row.RotatedAt.Valid:
		if revokeErr := s.q.RevokeRefreshTokenFamily(ctx, row.FamilyID); revokeErr != nil {
			return RefreshToken{}, "", fmt.Errorf("store: revoke family on reuse: %w", revokeErr)
		}
		return RefreshToken{}, "", newHTTPError(ErrRefreshReuse, http.StatusUnauthorized)
	case row.ExpiresAt.Time.Before(time.Now()):
		return RefreshToken{}, "", newHTTPError(ErrInvalidRefreshToken, http.StatusUnauthorized)
	}

	rotated, err := s.q.RotateRefreshTokenIfUnrotated(ctx, row.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Lost the race: another concurrent request rotated (or a
			// concurrent logout revoked) this token first. Treat exactly
			// like a reuse attempt and revoke the family.
			if revokeErr := s.q.RevokeRefreshTokenFamily(ctx, row.FamilyID); revokeErr != nil {
				return RefreshToken{}, "", fmt.Errorf("store: revoke family on race: %w", revokeErr)
			}
			return RefreshToken{}, "", newHTTPError(ErrRefreshReuse, http.StatusUnauthorized)
		}
		return RefreshToken{}, "", fmt.Errorf("store: mark rotated: %w", err)
	}

	userID := rotated.UserID.String()
	next, err := s.issueInFamily(ctx, userID, rotated.FamilyID.String(), ttl)
	if err != nil {
		return RefreshToken{}, "", err
	}
	return next, userID, nil
}

// RevokeFamilyByToken revokes every token in plainToken's family. It is
// idempotent: if the token is already gone, logout still succeeds.
func (s *Store) RevokeFamilyByToken(ctx context.Context, plainToken string) error {
	row, err := s.q.GetRefreshTokenByHash(ctx, hashToken(plainToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("store: get refresh token for logout: %w", err)
	}
	if err := s.q.RevokeRefreshTokenFamily(ctx, row.FamilyID); err != nil {
		return fmt.Errorf("store: revoke family on logout: %w", err)
	}
	return nil
}

func (s *Store) issueInFamily(ctx context.Context, userID, familyID string, ttl time.Duration) (RefreshToken, error) {
	plain, err := newRandomToken()
	if err != nil {
		return RefreshToken{}, fmt.Errorf("store: generate refresh token: %w", err)
	}
	expiresAt := time.Now().Add(ttl)

	userUUID, err := pgUUID(userID)
	if err != nil {
		return RefreshToken{}, err
	}
	familyUUID, err := pgUUID(familyID)
	if err != nil {
		return RefreshToken{}, err
	}

	_, err = s.q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    userUUID,
		FamilyID:  familyUUID,
		TokenHash: hashToken(plain),
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return RefreshToken{}, fmt.Errorf("store: create refresh token: %w", err)
	}

	return RefreshToken{PlainToken: plain, FamilyID: familyID, ExpiresAt: expiresAt}, nil
}

func newRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newRandomUUID generates a UUIDv4 string using crypto/rand. family_id is
// generated application-side (rather than via Postgres's gen_random_uuid())
// because it must be known before the first insert in a family and reused
// across rotations without a round trip.
func newRandomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
