package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

// withChiURLParams attaches multiple chi URL params in one route context, since
// withChiURLParam replaces the whole context on each call (only the last param
// would survive otherwise).
func withChiURLParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

type fakeCheckinStore struct {
	recordFn func(ctx context.Context, p store.RecordCheckinParams) (store.RecordCheckinResult, error)
	allowFn  func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

func (f *fakeCheckinStore) RecordCheckin(ctx context.Context, p store.RecordCheckinParams) (store.RecordCheckinResult, error) {
	return f.recordFn(ctx, p)
}

func (f *fakeCheckinStore) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	if f.allowFn == nil {
		return true, 0, nil
	}
	return f.allowFn(ctx, key, limit, window)
}

// okStore records every call and always succeeds, letting tests assert what
// the handler passed to RecordCheckin.
type okStore struct {
	fakeCheckinStore
	calls []store.RecordCheckinParams
}

func newOKStore() *okStore {
	s := &okStore{}
	s.recordFn = func(_ context.Context, p store.RecordCheckinParams) (store.RecordCheckinResult, error) {
		s.calls = append(s.calls, p)
		return store.RecordCheckinResult{MonitorID: "m1", State: "up"}, nil
	}
	return s
}

func TestCheckin_KindFromRoute(t *testing.T) {
	cases := []struct {
		name     string
		method   func(*checkinHandler, http.ResponseWriter, *http.Request)
		urlParam map[string]string
		wantKind store.CheckinKind
	}{
		{"success", (*checkinHandler).success, nil, store.CheckinSuccess},
		{"start", (*checkinHandler).start, nil, store.CheckinStart},
		{"fail", (*checkinHandler).fail, nil, store.CheckinFail},
		{"exit-zero", (*checkinHandler).exitCode, map[string]string{"code": "0"}, store.CheckinSuccess},
		{"exit-nonzero", (*checkinHandler).exitCode, map[string]string{"code": "42"}, store.CheckinFail},
		{"exit-nonnumeric", (*checkinHandler).exitCode, map[string]string{"code": "oops"}, store.CheckinSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newOKStore()
			h := newCheckinHandler(st, testDeps(t))

			params := map[string]string{"slug": "slug1"}
			for k, v := range tc.urlParam {
				params[k] = v
			}
			req := withChiURLParams(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/p/slug1", nil), params)
			rec := httptest.NewRecorder()
			tc.method(h, rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if len(st.calls) != 1 {
				t.Fatalf("RecordCheckin called %d times, want 1", len(st.calls))
			}
			if st.calls[0].Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", st.calls[0].Kind, tc.wantKind)
			}
			if st.calls[0].Slug != "slug1" {
				t.Errorf("slug = %q, want slug1", st.calls[0].Slug)
			}
		})
	}
}

func TestCheckin_UnknownSlugReturns200(t *testing.T) {
	st := &fakeCheckinStore{
		recordFn: func(_ context.Context, _ store.RecordCheckinParams) (store.RecordCheckinResult, error) {
			return store.RecordCheckinResult{}, store.ErrNotFound
		},
	}
	h := newCheckinHandler(st, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/p/ghost", nil)
	req = withChiURLParam(req, "slug", "ghost")
	rec := httptest.NewRecorder()
	h.success(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unknown slug", rec.Code)
	}
}

func TestCheckin_StoreErrorStillReturns200(t *testing.T) {
	st := &fakeCheckinStore{
		recordFn: func(_ context.Context, _ store.RecordCheckinParams) (store.RecordCheckinResult, error) {
			return store.RecordCheckinResult{}, context.DeadlineExceeded
		},
	}
	h := newCheckinHandler(st, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/p/slug1", strings.NewReader("body"))
	req = withChiURLParam(req, "slug", "slug1")
	rec := httptest.NewRecorder()
	h.success(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even on store error", rec.Code)
	}
}

func TestCheckin_RateLimitedReturns429(t *testing.T) {
	st := &fakeCheckinStore{
		recordFn: func(_ context.Context, _ store.RecordCheckinParams) (store.RecordCheckinResult, error) {
			t.Fatal("RecordCheckin should not be called when rate limited")
			return store.RecordCheckinResult{}, nil
		},
		allowFn: func(_ context.Context, _ string, _ int, _ time.Duration) (bool, time.Duration, error) {
			return false, 30 * time.Second, nil
		},
	}
	h := newCheckinHandler(st, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/p/slug1", nil)
	req = withChiURLParam(req, "slug", "slug1")
	rec := httptest.NewRecorder()
	h.success(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
}

func TestCheckin_BodyTruncatedAtCap(t *testing.T) {
	st := newOKStore()
	h := newCheckinHandler(st, testDeps(t))

	oversized := strings.Repeat("x", maxCheckinBody+5000)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/p/slug1", strings.NewReader(oversized))
	req = withChiURLParam(req, "slug", "slug1")
	rec := httptest.NewRecorder()
	h.success(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := len(st.calls[0].Body); got != maxCheckinBody {
		t.Errorf("stored body length = %d, want %d (truncated)", got, maxCheckinBody)
	}
}

func TestCheckin_HEADSkipsBody(t *testing.T) {
	st := newOKStore()
	h := newCheckinHandler(st, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodHead, "/p/slug1", strings.NewReader("ignored"))
	req = withChiURLParam(req, "slug", "slug1")
	rec := httptest.NewRecorder()
	h.success(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if st.calls[0].Body != "" {
		t.Errorf("HEAD body = %q, want empty", st.calls[0].Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD response body = %q, want empty", rec.Body.String())
	}
}
