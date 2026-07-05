package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

type fakeAPIKeyStore struct {
	createAPIKeyFn func(ctx context.Context, userID, label string) (store.CreatedAPIKey, error)
	listAPIKeysFn  func(ctx context.Context, userID string) ([]store.APIKey, error)
	revokeAPIKeyFn func(ctx context.Context, id, callerUserID string) error
}

func (f *fakeAPIKeyStore) CreateAPIKey(ctx context.Context, userID, label string) (store.CreatedAPIKey, error) {
	return f.createAPIKeyFn(ctx, userID, label)
}
func (f *fakeAPIKeyStore) ListAPIKeys(ctx context.Context, userID string) ([]store.APIKey, error) {
	return f.listAPIKeysFn(ctx, userID)
}
func (f *fakeAPIKeyStore) RevokeAPIKey(ctx context.Context, id, callerUserID string) error {
	return f.revokeAPIKeyFn(ctx, id, callerUserID)
}

func withChiRouteParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestAPIKeyHandler_Create_ReturnsPlaintextKeyOnce(t *testing.T) {
	fake := &fakeAPIKeyStore{
		createAPIKeyFn: func(ctx context.Context, userID, label string) (store.CreatedAPIKey, error) {
			if userID != "user-1" || label != "CI runner" {
				t.Fatalf("unexpected args: userID=%q label=%q", userID, label)
			}
			return store.CreatedAPIKey{
				APIKey: store.APIKey{
					ID:        "key-1",
					UserID:    userID,
					Label:     label,
					CreatedAt: time.Now(),
				},
				PlainKey: "pk_deadbeef",
			}, nil
		},
	}
	h := newAPIKeyHandler(fake, Deps{})

	body := strings.NewReader(`{"label":"CI runner"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/apikeys", body)
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rec := httptest.NewRecorder()

	h.create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp createAPIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Key != "pk_deadbeef" {
		t.Errorf("Key = %q, want pk_deadbeef", resp.Key)
	}
	if !strings.Contains(rec.Body.String(), `"key"`) {
		t.Error("create response must include the plaintext key field")
	}
}

func TestAPIKeyHandler_Create_EmptyLabelRejected(t *testing.T) {
	fake := &fakeAPIKeyStore{
		createAPIKeyFn: func(ctx context.Context, userID, label string) (store.CreatedAPIKey, error) {
			t.Fatal("store should not be called for an invalid request")
			return store.CreatedAPIKey{}, nil
		},
	}
	h := newAPIKeyHandler(fake, Deps{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/apikeys", strings.NewReader(`{"label":"  "}`))
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rec := httptest.NewRecorder()

	h.create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAPIKeyHandler_List_NeverIncludesKeyHashOrPlaintext(t *testing.T) {
	now := time.Now()
	fake := &fakeAPIKeyStore{
		listAPIKeysFn: func(ctx context.Context, userID string) ([]store.APIKey, error) {
			return []store.APIKey{
				{ID: "key-1", UserID: userID, Label: "prod", LastUsedAt: &now, CreatedAt: now},
			}, nil
		},
	}
	h := newAPIKeyHandler(fake, Deps{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/apikeys", nil)
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rec := httptest.NewRecorder()

	h.list(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "key_hash") || strings.Contains(rec.Body.String(), `"key"`) {
		t.Errorf("list response must never include a hash or plaintext key: %s", rec.Body.String())
	}
}

func TestAPIKeyHandler_Revoke_ForeignOrMissingKeyReturns404(t *testing.T) {
	fake := &fakeAPIKeyStore{
		revokeAPIKeyFn: func(ctx context.Context, id, callerUserID string) error {
			return testStoreError{msg: "resource not found", status: http.StatusNotFound}
		},
	}
	h := newAPIKeyHandler(fake, Deps{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/apikeys/other-key", nil)
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	req = withChiRouteParam(req, "id", "other-key")
	rec := httptest.NewRecorder()

	h.revoke(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIKeyHandler_Revoke_Success(t *testing.T) {
	revoked := false
	fake := &fakeAPIKeyStore{
		revokeAPIKeyFn: func(ctx context.Context, id, callerUserID string) error {
			revoked = true
			return nil
		},
	}
	h := newAPIKeyHandler(fake, Deps{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/apikeys/key-1", nil)
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	req = withChiRouteParam(req, "id", "key-1")
	rec := httptest.NewRecorder()

	h.revoke(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !revoked {
		t.Error("store.RevokeAPIKey was not called")
	}
}
