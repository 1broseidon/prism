package admin

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/adminauth"
	"github.com/1broseidon/prism/internal/store"
)

func TestAdminAuthProbeRateLimit(t *testing.T) {
	api := NewAPI(
		func() any { return nil },
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	body := `{"issuer":"http://127.0.0.1:5555"}`
	for i := 0; i < adminProbeRateBucketSize; i++ {
		r := httptest.NewRequest(http.MethodPost, "/config/admin-auth/test", strings.NewReader(body))
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited too early", i+1)
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/config/admin-auth/test", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPutAdminAuthPayloadSizeLimit(t *testing.T) {
	auth := adminauth.NewHolder(store.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	api := NewAPI(
		func() any { return nil },
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		auth,
	)

	body := `{"issuer":"` + strings.Repeat("a", maxAdminAuthPayloadBytes+1) + `"}`
	r := httptest.NewRequest(http.MethodPut, "/config/admin-auth", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized payload, got %d: %s", w.Code, w.Body.String())
	}
}
