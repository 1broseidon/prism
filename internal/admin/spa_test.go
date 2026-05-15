package admin

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAServesIndex(t *testing.T) {
	api := &API{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	api.handleSPA(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<div id=\"app\">") {
		t.Fatalf("expected SPA root element in response, got: %s", body)
	}
}

func TestSPAFallbackToIndex(t *testing.T) {
	api := &API{}
	// Unknown SPA route — should still return index.html so client-side routing handles it.
	r := httptest.NewRequest(http.MethodGet, "/identity", nil)
	w := httptest.NewRecorder()
	api.handleSPA(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html for SPA fallback, got %q", ct)
	}
}

func TestSPAServesEmbeddedAsset(t *testing.T) {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	// Find a built JS asset in dist/assets to request through the handler.
	assetsDir, err := fs.ReadDir(sub, "assets")
	if err != nil {
		t.Fatalf("ReadDir(assets): %v", err)
	}
	var jsName string
	for _, e := range assetsDir {
		if strings.HasSuffix(e.Name(), ".js") {
			jsName = e.Name()
			break
		}
	}
	if jsName == "" {
		t.Skip("no js asset in dist/assets — run npm build first")
	}

	api := &API{}
	r := httptest.NewRequest(http.MethodGet, "/assets/"+jsName, nil)
	w := httptest.NewRecorder()
	api.handleSPA(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("expected application/javascript, got %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("expected immutable cache header for hashed asset, got %q", cc)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected non-empty asset body")
	}
}
