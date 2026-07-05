package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

// maxAPIKeyBodyBytes bounds the create-key request body — just a label.
const maxAPIKeyBodyBytes = 1 << 10 // 1 KiB

const maxAPIKeyLabelLen = 100

// apiKeyStore is the subset of *store.Store the API key handlers need, kept
// as an interface so handler tests can inject a fake without touching
// Postgres, matching monitorStore/authStore.
type apiKeyStore interface {
	CreateAPIKey(ctx context.Context, userID, label string) (store.CreatedAPIKey, error)
	ListAPIKeys(ctx context.Context, userID string) ([]store.APIKey, error)
	RevokeAPIKey(ctx context.Context, id, callerUserID string) error
}

type apiKeyHandler struct {
	store apiKeyStore
	deps  Deps
}

func newAPIKeyHandler(s apiKeyStore, deps Deps) *apiKeyHandler {
	return &apiKeyHandler{store: s, deps: deps}
}

type createAPIKeyRequest struct {
	Label string `json:"label"`
}

// createAPIKeyResponse includes Key (the plaintext "pk_..." token) only on
// this one response — apiKeyResponse (list) never carries it, matching the
// "shown exactly once" AC.
type createAPIKeyResponse struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
}

type apiKeyResponse struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
	CreatedAt  string  `json:"created_at"`
}

func (h *apiKeyHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if !decodeBoundedJSON(w, r, &req, maxAPIKeyBodyBytes) {
		return
	}

	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		writeFieldError(w, "label", "label is required")
		return
	}
	if len(req.Label) > maxAPIKeyLabelLen {
		writeFieldError(w, "label", "label must be 100 characters or fewer")
		return
	}

	userID := userIDFromContext(r.Context())
	created, err := h.store.CreateAPIKey(r.Context(), userID, req.Label)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, createAPIKeyResponse{
		ID:        created.ID,
		Label:     created.Label,
		Key:       created.PlainKey,
		CreatedAt: created.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (h *apiKeyHandler) list(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	keys, err := h.store.ListAPIKeys(r.Context(), userID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := make([]apiKeyResponse, len(keys))
	for i, k := range keys {
		resp[i] = apiKeyResponse{
			ID:         k.ID,
			Label:      k.Label,
			LastUsedAt: formatTimePtr(k.LastUsedAt),
			RevokedAt:  formatTimePtr(k.RevokedAt),
			CreatedAt:  k.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *apiKeyHandler) revoke(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := userIDFromContext(r.Context())

	if err := h.store.RevokeAPIKey(r.Context(), id, userID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
