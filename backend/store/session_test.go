package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

func validRow(id, userID, familyID string) db.RefreshToken {
	idUUID, _ := pgUUID(id)
	userUUID, _ := pgUUID(userID)
	familyUUID, _ := pgUUID(familyID)
	return db.RefreshToken{
		ID:        idUUID,
		UserID:    userUUID,
		FamilyID:  familyUUID,
		TokenHash: hashToken("plain-token"),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}
}

func TestRotateRefreshToken_UnknownTokenReturnsInvalid(t *testing.T) {
	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return db.RefreshToken{}, pgx.ErrNoRows
		},
	}
	s := newTestStore(q)

	_, _, err := s.RotateRefreshToken(context.Background(), "plain-token", time.Hour)
	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("err = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestRotateRefreshToken_RevokedReturnsInvalid(t *testing.T) {
	row := validRow("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")
	row.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}

	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return row, nil
		},
	}
	s := newTestStore(q)

	_, _, err := s.RotateRefreshToken(context.Background(), "plain-token", time.Hour)
	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("err = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestRotateRefreshToken_AlreadyRotatedRevokesFamilyAndReturnsReuse(t *testing.T) {
	row := validRow("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")
	row.RotatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}

	var revokedFamily pgtype.UUID
	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return row, nil
		},
		revokeRefreshTokenFamFn: func(ctx context.Context, familyID pgtype.UUID) error {
			revokedFamily = familyID
			return nil
		},
	}
	s := newTestStore(q)

	_, _, err := s.RotateRefreshToken(context.Background(), "plain-token", time.Hour)
	if !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("err = %v, want ErrRefreshReuse", err)
	}
	if revokedFamily.String() != row.FamilyID.String() {
		t.Errorf("revoked family = %s, want %s", revokedFamily.String(), row.FamilyID.String())
	}
}

func TestRotateRefreshToken_ExpiredReturnsInvalid(t *testing.T) {
	row := validRow("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")
	row.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}

	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return row, nil
		},
	}
	s := newTestStore(q)

	_, _, err := s.RotateRefreshToken(context.Background(), "plain-token", time.Hour)
	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("err = %v, want ErrInvalidRefreshToken", err)
	}
}

// TestRotateRefreshToken_ConcurrentRaceLoserRevokesFamily simulates two
// concurrent rotation attempts on the same still-valid token: the read
// (GetRefreshTokenByHash) sees an unrotated row for both, but the atomic
// RotateRefreshTokenIfUnrotated UPDATE can only succeed for one caller. The
// loser must see pgx.ErrNoRows from that call and treat it as reuse,
// revoking the family — this is what closes the TOCTOU race.
func TestRotateRefreshToken_ConcurrentRaceLoserRevokesFamily(t *testing.T) {
	row := validRow("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")

	var revokedFamily pgtype.UUID
	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return row, nil // both racers see the unrotated row
		},
		rotateRefreshTokenIfUnrotFn: func(ctx context.Context, id pgtype.UUID) (db.RefreshToken, error) {
			// Simulates losing the atomic UPDATE race: another request
			// already rotated this row, so WHERE rotated_at IS NULL no
			// longer matches.
			return db.RefreshToken{}, pgx.ErrNoRows
		},
		revokeRefreshTokenFamFn: func(ctx context.Context, familyID pgtype.UUID) error {
			revokedFamily = familyID
			return nil
		},
	}
	s := newTestStore(q)

	_, _, err := s.RotateRefreshToken(context.Background(), "plain-token", time.Hour)
	if !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("err = %v, want ErrRefreshReuse", err)
	}
	if revokedFamily.String() != row.FamilyID.String() {
		t.Errorf("revoked family = %s, want %s", revokedFamily.String(), row.FamilyID.String())
	}
}

func TestRotateRefreshToken_SuccessIssuesNewTokenInSameFamily(t *testing.T) {
	row := validRow("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")

	var createdParams db.CreateRefreshTokenParams
	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return row, nil
		},
		rotateRefreshTokenIfUnrotFn: func(ctx context.Context, id pgtype.UUID) (db.RefreshToken, error) {
			return row, nil
		},
		createRefreshTokenFn: func(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
			createdParams = arg
			return db.RefreshToken{}, nil
		},
	}
	s := newTestStore(q)

	next, userID, err := s.RotateRefreshToken(context.Background(), "plain-token", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != row.UserID.String() {
		t.Errorf("userID = %q, want %q", userID, row.UserID.String())
	}
	if next.PlainToken == "" {
		t.Error("expected a new plaintext token")
	}
	if next.PlainToken == "plain-token" {
		t.Error("expected a freshly generated token, not the old one")
	}
	if createdParams.FamilyID.String() != row.FamilyID.String() {
		t.Errorf("new token family = %s, want same family %s", createdParams.FamilyID.String(), row.FamilyID.String())
	}
}

func TestRevokeFamilyByToken_UnknownTokenIsIdempotent(t *testing.T) {
	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return db.RefreshToken{}, pgx.ErrNoRows
		},
	}
	s := newTestStore(q)

	if err := s.RevokeFamilyByToken(context.Background(), "already-gone"); err != nil {
		t.Fatalf("unexpected error for already-gone token: %v", err)
	}
}

func TestRevokeFamilyByToken_RevokesFamily(t *testing.T) {
	row := validRow("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")

	var revokedFamily pgtype.UUID
	q := &fakeQuerier{
		getRefreshTokenByHashFn: func(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
			return row, nil
		},
		revokeRefreshTokenFamFn: func(ctx context.Context, familyID pgtype.UUID) error {
			revokedFamily = familyID
			return nil
		},
	}
	s := newTestStore(q)

	if err := s.RevokeFamilyByToken(context.Background(), "plain-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if revokedFamily.String() != row.FamilyID.String() {
		t.Errorf("revoked family = %s, want %s", revokedFamily.String(), row.FamilyID.String())
	}
}

func TestIssueRefreshToken_GeneratesFreshFamily(t *testing.T) {
	var createdParams db.CreateRefreshTokenParams
	q := &fakeQuerier{
		createRefreshTokenFn: func(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
			createdParams = arg
			return db.RefreshToken{}, nil
		},
	}
	s := newTestStore(q)

	rt, err := s.IssueRefreshToken(context.Background(), "22222222-2222-4222-8222-222222222222", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.PlainToken == "" {
		t.Error("expected non-empty plaintext token")
	}
	if createdParams.FamilyID.String() == "" {
		t.Error("expected a generated family id")
	}
}
