package store

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

func testUUID(t *testing.T) (pgtype.UUID, string) {
	t.Helper()
	u := pgtype.UUID{Valid: true}
	u.Bytes[0] = 1
	return u, u.String()
}

func TestCreateMonitor_RetriesOnSlugCollision(t *testing.T) {
	userUUID, userID := testUUID(t)
	attempts := 0
	var seenSlugs []string

	q := &fakeQuerier{
		createMonitorFn: func(ctx context.Context, arg db.CreateMonitorParams) (db.Monitor, error) {
			attempts++
			seenSlugs = append(seenSlugs, arg.Slug)
			if attempts < 3 {
				return db.Monitor{}, &pgconn.PgError{Code: "23505"}
			}
			return db.Monitor{ID: userUUID, UserID: userUUID, Kind: "heartbeat", Slug: arg.Slug, Name: arg.Name, Tz: "UTC"}, nil
		},
	}
	s := newTestStore(q)

	m, err := s.CreateMonitor(context.Background(), CreateMonitorParams{
		UserID: userID, Kind: "heartbeat", Name: "nightly backup", TZ: "UTC",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (retried past 2 collisions)", attempts)
	}
	if len(seenSlugs) != len(uniqueStrings(seenSlugs)) {
		t.Errorf("slugs = %v, want each retry to use a fresh slug", seenSlugs)
	}
	if m.Slug != seenSlugs[len(seenSlugs)-1] {
		t.Errorf("returned slug = %q, want the slug from the successful attempt", m.Slug)
	}
}

func TestCreateMonitor_ExhaustsRetriesReturnsError(t *testing.T) {
	_, userID := testUUID(t)
	q := &fakeQuerier{
		createMonitorFn: func(ctx context.Context, arg db.CreateMonitorParams) (db.Monitor, error) {
			return db.Monitor{}, &pgconn.PgError{Code: "23505"}
		},
	}
	s := newTestStore(q)

	_, err := s.CreateMonitor(context.Background(), CreateMonitorParams{UserID: userID, Kind: "heartbeat", Name: "x", TZ: "UTC"})
	if err == nil {
		t.Fatal("expected error after exhausting slug retries, got nil")
	}
}

func TestGetMonitor_ForeignOwnerReturns403NotFoundMasking(t *testing.T) {
	ownerUUID, _ := testUUID(t)
	_, id := testUUID(t)
	callerUUID := pgtype.UUID{Valid: true}
	callerUUID.Bytes[0] = 2
	callerID := callerUUID.String()

	q := &fakeQuerier{
		getMonitorByIDFn: func(ctx context.Context, gotID pgtype.UUID) (db.Monitor, error) {
			return db.Monitor{ID: gotID, UserID: ownerUUID, Kind: "heartbeat"}, nil
		},
	}
	s := newTestStore(q)

	_, err := s.GetMonitor(context.Background(), id, callerID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden (403, not 404-masking a foreign resource)", err)
	}
	var se storeErrorForTest
	if !errors.As(err, &se) || se.HTTPStatus() != http.StatusForbidden {
		t.Error("expected HTTPStatus() == 403")
	}
}

func TestGetMonitor_MissingReturns404(t *testing.T) {
	_, id := testUUID(t)
	q := &fakeQuerier{
		getMonitorByIDFn: func(ctx context.Context, gotID pgtype.UUID) (db.Monitor, error) {
			return db.Monitor{}, pgx.ErrNoRows
		},
	}
	s := newTestStore(q)

	_, err := s.GetMonitor(context.Background(), id, "whoever")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	var se storeErrorForTest
	if !errors.As(err, &se) || se.HTTPStatus() != http.StatusNotFound {
		t.Error("expected HTTPStatus() == 404")
	}
}

func TestGetMonitor_OwnerSucceeds(t *testing.T) {
	ownerUUID, ownerID := testUUID(t)
	q := &fakeQuerier{
		getMonitorByIDFn: func(ctx context.Context, gotID pgtype.UUID) (db.Monitor, error) {
			return db.Monitor{ID: gotID, UserID: ownerUUID, Kind: "heartbeat", Name: "mine", Tz: "UTC"}, nil
		},
	}
	s := newTestStore(q)

	m, err := s.GetMonitor(context.Background(), ownerID, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "mine" {
		t.Errorf("Name = %q, want mine", m.Name)
	}
}

func TestDeleteMonitor_ZeroRowsAffectedReturns404(t *testing.T) {
	_, id := testUUID(t)
	_, callerID := testUUID(t)
	q := &fakeQuerier{
		deleteMonitorFn: func(ctx context.Context, arg db.DeleteMonitorParams) (int64, error) {
			return 0, nil
		},
	}
	s := newTestStore(q)

	err := s.DeleteMonitor(context.Background(), id, callerID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteMonitor_Succeeds(t *testing.T) {
	_, id := testUUID(t)
	_, callerID := testUUID(t)
	q := &fakeQuerier{
		deleteMonitorFn: func(ctx context.Context, arg db.DeleteMonitorParams) (int64, error) {
			return 1, nil
		},
	}
	s := newTestStore(q)

	if err := s.DeleteMonitor(context.Background(), id, callerID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListMonitors_SetsNextCursorOnlyWhenPageIsFull(t *testing.T) {
	_, userID := testUUID(t)

	rowUUID := pgtype.UUID{Valid: true}
	rowUUID.Bytes[0] = 9
	fullPage := []db.Monitor{
		{ID: rowUUID, UserID: rowUUID, Kind: "heartbeat", Tz: "UTC", CreatedAt: pgtype.Timestamptz{Valid: true}},
		{ID: rowUUID, UserID: rowUUID, Kind: "heartbeat", Tz: "UTC", CreatedAt: pgtype.Timestamptz{Valid: true}},
	}

	q := &fakeQuerier{
		listMonitorsByUserPgFn: func(ctx context.Context, arg db.ListMonitorsByUserPageParams) ([]db.Monitor, error) {
			if arg.PageLimit == 2 {
				return fullPage, nil
			}
			return fullPage[:1], nil
		},
	}
	s := newTestStore(q)

	full, err := s.ListMonitors(context.Background(), userID, "", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if full.NextCursor == "" {
		t.Error("expected NextCursor to be set when the page is full (more results may exist)")
	}

	partial, err := s.ListMonitors(context.Background(), userID, "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if partial.NextCursor != "" {
		t.Error("expected NextCursor to be empty when the page is short (no more results)")
	}
}

func TestListMonitors_DecodesCursorIntoQueryParams(t *testing.T) {
	_, userID := testUUID(t)
	var gotCursor pgtype.Timestamptz

	q := &fakeQuerier{
		listMonitorsByUserPgFn: func(ctx context.Context, arg db.ListMonitorsByUserPageParams) ([]db.Monitor, error) {
			gotCursor = arg.CursorCreatedAt
			return nil, nil
		},
	}
	s := newTestStore(q)

	first, err := s.ListMonitors(context.Background(), userID, "", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = first

	rowUUID := pgtype.UUID{Valid: true}
	rowUUID.Bytes[0] = 3
	cursor := encodeCursor(time.Now(), rowUUID)

	if _, err := s.ListMonitors(context.Background(), userID, cursor, 1); err != nil {
		t.Fatalf("unexpected error decoding valid cursor: %v", err)
	}
	if !gotCursor.Valid {
		t.Error("expected CursorCreatedAt to be set from the decoded cursor")
	}
}

func TestListMonitors_InvalidCursorReturns400(t *testing.T) {
	_, userID := testUUID(t)
	s := newTestStore(&fakeQuerier{})

	_, err := s.ListMonitors(context.Background(), userID, "not-a-valid-cursor", 10)
	if err == nil {
		t.Fatal("expected error for malformed cursor")
	}
	var se storeErrorForTest
	if !errors.As(err, &se) || se.HTTPStatus() != http.StatusBadRequest {
		t.Error("expected HTTPStatus() == 400 for a malformed cursor")
	}
}

func uniqueStrings(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
