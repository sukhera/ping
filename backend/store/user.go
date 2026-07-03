package store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/sukhera/ping/db"
)

const bcryptCost = 12

// dummyHash is compared against on a login attempt for an email that doesn't
// exist, so responding to "unknown email" takes roughly the same time as
// "wrong password" and doesn't leak account existence via timing.
var dummyHash = mustHash("not-a-real-password-used-only-for-timing")

func mustHash(password string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		panic("store: failed to generate dummy hash: " + err.Error())
	}
	return h
}

type AuthUser struct {
	ID    string
	Email string
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Register creates a new account. It returns ErrRegistrationClosed if
// registrationOpen is false, or ErrEmailTaken if the email is already used.
func (s *Store) Register(ctx context.Context, email, password string, registrationOpen bool) (AuthUser, error) {
	if !registrationOpen {
		return AuthUser{}, newHTTPError(ErrRegistrationClosed, http.StatusForbidden)
	}

	email = normalizeEmail(email)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return AuthUser{}, fmt.Errorf("store: hash password: %w", err)
	}

	u, err := s.q.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: string(hash),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return AuthUser{}, newHTTPError(ErrEmailTaken, http.StatusConflict)
		}
		return AuthUser{}, fmt.Errorf("store: create user: %w", err)
	}

	return AuthUser{ID: u.ID.String(), Email: u.Email}, nil
}

// UserEmailByID returns the email address for a user id, or ErrNotFound if no
// such user exists. Used by the alerting test endpoint to address the message
// to the authenticated caller's own account.
func (s *Store) UserEmailByID(ctx context.Context, userID string) (string, error) {
	id, err := pgUUID(userID)
	if err != nil {
		return "", newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	u, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", newHTTPError(ErrNotFound, http.StatusNotFound)
		}
		return "", fmt.Errorf("store: get user by id: %w", err)
	}
	return u.Email, nil
}

// Authenticate verifies email/password and returns the user on success, or
// ErrInvalidCredentials if the email is unknown or the password is wrong.
func (s *Store) Authenticate(ctx context.Context, email, password string) (AuthUser, error) {
	email = normalizeEmail(email)

	u, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return AuthUser{}, newHTTPError(ErrInvalidCredentials, http.StatusUnauthorized)
		}
		return AuthUser{}, fmt.Errorf("store: get user by email: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return AuthUser{}, newHTTPError(ErrInvalidCredentials, http.StatusUnauthorized)
	}

	return AuthUser{ID: u.ID.String(), Email: u.Email}, nil
}
