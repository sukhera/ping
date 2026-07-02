package store

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/sukhera/ping/db"
)

func TestRegister_ClosedReturns403(t *testing.T) {
	s := newTestStore(&fakeQuerier{})

	_, err := s.Register(context.Background(), "a@example.com", "correcthorsebatterystaple", false)
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("err = %v, want ErrRegistrationClosed", err)
	}
	var se storeErrorForTest
	if !errors.As(err, &se) {
		t.Fatal("expected error implementing HTTPStatus()")
	}
	if se.HTTPStatus() != http.StatusForbidden {
		t.Errorf("status = %d, want 403", se.HTTPStatus())
	}
}

func TestRegister_DuplicateEmailReturns409(t *testing.T) {
	q := &fakeQuerier{
		createUserFn: func(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
			return db.User{}, &pgconn.PgError{Code: "23505"}
		},
	}
	s := newTestStore(q)

	_, err := s.Register(context.Background(), "a@example.com", "correcthorsebatterystaple", true)
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
	var se storeErrorForTest
	if !errors.As(err, &se) || se.HTTPStatus() != http.StatusConflict {
		t.Error("expected HTTPStatus() == 409")
	}
}

func TestRegister_NormalizesEmailAndHashesPassword(t *testing.T) {
	var gotEmail, gotHash string
	q := &fakeQuerier{
		createUserFn: func(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
			gotEmail = arg.Email
			gotHash = arg.PasswordHash
			return db.User{ID: pgtype.UUID{Valid: true}, Email: arg.Email}, nil
		},
	}
	s := newTestStore(q)

	_, err := s.Register(context.Background(), "  Foo@Example.com ", "correcthorsebatterystaple", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEmail != "foo@example.com" {
		t.Errorf("email = %q, want normalized foo@example.com", gotEmail)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(gotHash), []byte("correcthorsebatterystaple")); err != nil {
		t.Errorf("stored hash does not match password: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(gotHash))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost != bcryptCost {
		t.Errorf("bcrypt cost = %d, want %d", cost, bcryptCost)
	}
}

func TestAuthenticate_UnknownEmailReturns401(t *testing.T) {
	q := &fakeQuerier{
		getUserByEmailFn: func(ctx context.Context, email string) (db.User, error) {
			return db.User{}, pgx.ErrNoRows
		},
	}
	s := newTestStore(q)

	_, err := s.Authenticate(context.Background(), "nobody@example.com", "whatever12345")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthenticate_WrongPasswordReturns401(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correcthorsebatterystaple"), bcryptCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	q := &fakeQuerier{
		getUserByEmailFn: func(ctx context.Context, email string) (db.User, error) {
			return db.User{ID: pgtype.UUID{Valid: true}, Email: email, PasswordHash: string(hash)}, nil
		},
	}
	s := newTestStore(q)

	_, err = s.Authenticate(context.Background(), "a@example.com", "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthenticate_CorrectPasswordSucceeds(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correcthorsebatterystaple"), bcryptCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	q := &fakeQuerier{
		getUserByEmailFn: func(ctx context.Context, email string) (db.User, error) {
			return db.User{ID: pgtype.UUID{Valid: true}, Email: email, PasswordHash: string(hash)}, nil
		},
	}
	s := newTestStore(q)

	u, err := s.Authenticate(context.Background(), "A@Example.com", "correcthorsebatterystaple")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "a@example.com" {
		t.Errorf("email = %q, want a@example.com", u.Email)
	}
}

// storeErrorForTest mirrors the storeError interface server/response.go
// expects, redeclared here so store's tests don't import server.
type storeErrorForTest interface {
	error
	HTTPStatus() int
}
