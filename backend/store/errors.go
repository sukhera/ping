package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrEmailTaken          = errors.New("email already registered")
	ErrRegistrationClosed  = errors.New("registration is closed")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("invalid or expired refresh token")
	ErrRefreshReuse        = errors.New("refresh token reuse detected")
	ErrNotFound            = errors.New("resource not found")
	// ErrForbidden signals a resource exists but is owned by a different
	// user. Callers must return 403, never mask this as ErrNotFound (404) —
	// see the security checklist's IDOR guidance (TECH-PLAN PING-007 AC).
	ErrForbidden = errors.New("forbidden")
)

// httpError wraps a sentinel error with the HTTP status the server layer
// should map it to, implementing the storeError interface server/response.go
// expects. Unwrap is implemented so errors.Is still matches the sentinel.
type httpError struct {
	err    error
	status int
}

func (e httpError) Error() string   { return e.err.Error() }
func (e httpError) Unwrap() error   { return e.err }
func (e httpError) HTTPStatus() int { return e.status }

func newHTTPError(err error, status int) httpError {
	return httpError{err: err, status: status}
}

// isUniqueViolation reports whether err is a Postgres unique_violation (23505).
func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
}
