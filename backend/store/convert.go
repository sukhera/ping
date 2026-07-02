package store

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgUUID parses a hyphenated UUID string into a pgtype.UUID for query params.
func pgUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("store: parse uuid %q: %w", s, err)
	}
	return u, nil
}
