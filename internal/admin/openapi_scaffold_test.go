package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/openapi"
)

// TestHandleOpenAPIScaffoldHappyPath verifies a realistic curl input produces
// a YAML spec containing the expected method, path, and security scheme.
func TestHandleOpenAPIScaffoldHappyPath(t *testing.T) {
	api := &API{}
	body := mustEncodeBody(t, openAPIScaffoldRequest{
		Curl: `curl -X POST -H 'Authorization: Bearer xyz' -d '{"name":"alice"}' https://api.example.com/v1/users`,
	})
	req := httptest.NewRequest(http.MethodPost, "/openapi/scaffold-from-curl", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIScaffold(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp openAPIScaffoldResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Spec, "openapi: 3.1.0") {
		t.Fatalf("missing version header in spec:\n%s", resp.Spec)
	}
	if !strings.Contains(resp.Spec, "post:") {
		t.Fatalf("missing POST method in spec:\n%s", resp.Spec)
	}
	if !strings.Contains(resp.Spec, "bearerAuth:") {
		t.Fatalf("missing bearerAuth in spec:\n%s", resp.Spec)
	}
	// The generated spec must round-trip through the parser without errors.
	if _, err := openapi.NewParser().Parse([]byte(resp.Spec)); err != nil {
		t.Fatalf("generated spec failed to parse: %v\n%s", err, resp.Spec)
	}
}

// TestHandleOpenAPIScaffoldRejectsEmptyCurl ensures an empty curl string is
// rejected with a 400 rather than silently scaffolding an empty spec.
func TestHandleOpenAPIScaffoldRejectsEmptyCurl(t *testing.T) {
	api := &API{}
	body := mustEncodeBody(t, openAPIScaffoldRequest{Curl: ""})
	req := httptest.NewRequest(http.MethodPost, "/openapi/scaffold-from-curl", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIScaffold(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOpenAPIScaffoldRejectsOversizedInput ensures the curl-length cap
// is enforced.
func TestHandleOpenAPIScaffoldRejectsOversizedInput(t *testing.T) {
	api := &API{}
	pad := strings.Repeat("a", openapi.MaxCurlInputBytes+10)
	body := mustEncodeBody(t, openAPIScaffoldRequest{Curl: pad})
	req := httptest.NewRequest(http.MethodPost, "/openapi/scaffold-from-curl", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIScaffold(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOpenAPIScaffoldReturnsWarnings verifies the warnings array makes
// it into the response when the parser drops flags.
func TestHandleOpenAPIScaffoldReturnsWarnings(t *testing.T) {
	api := &API{}
	body := mustEncodeBody(t, openAPIScaffoldRequest{
		Curl: `curl -d @payload.json https://api.example.com/x`,
	})
	req := httptest.NewRequest(http.MethodPost, "/openapi/scaffold-from-curl", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIScaffold(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp openAPIScaffoldResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected warnings for @file reference, got none. spec:\n%s", resp.Spec)
	}
}

// TestHandleOpenAPIScaffoldRejectsMalformedJSON makes sure the handler
// returns 400 for non-JSON bodies rather than panicking.
func TestHandleOpenAPIScaffoldRejectsMalformedJSON(t *testing.T) {
	api := &API{}
	req := httptest.NewRequest(http.MethodPost, "/openapi/scaffold-from-curl", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	api.handleOpenAPIScaffold(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}
