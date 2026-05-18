package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/openapi"
)

// fakeOpenAPIBackendManager is a BackendManager + OpenAPIBackendManager
// double. It captures whatever the admin layer hands it so individual tests
// can assert on the side effects.
type fakeOpenAPIBackendManager struct {
	saveID         string
	saveParams     OpenAPISaveParams
	saveErr        error
	loadResult     *PersistedOpenAPIBackend
	loadErr        error
	reimportID     string
	reimportParams OpenAPIReimportParams
	reimportErr    error
}

func (f *fakeOpenAPIBackendManager) AddBackend(context.Context, string, BackendConfig) error {
	return nil
}
func (f *fakeOpenAPIBackendManager) RemoveBackend(string) error { return nil }
func (f *fakeOpenAPIBackendManager) NotifyToolsChanged()        {}

func (f *fakeOpenAPIBackendManager) SaveOpenAPIBackend(_ context.Context, id string, params OpenAPISaveParams) error {
	f.saveID = id
	f.saveParams = params
	return f.saveErr
}

func (f *fakeOpenAPIBackendManager) LoadOpenAPIBackend(string) (*PersistedOpenAPIBackend, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.loadResult, nil
}

func (f *fakeOpenAPIBackendManager) ReimportOpenAPIBackend(_ context.Context, id string, params OpenAPIReimportParams) error {
	f.reimportID = id
	f.reimportParams = params
	return f.reimportErr
}

// inlineOpenAPISpec returns a minimal 3.0 spec with two operations: a GET
// listing and a POST creator. Useful for preview + diff + reimport tests.
func inlineOpenAPISpec(t *testing.T) string {
	t.Helper()
	return `{
"openapi":"3.0.0",
"info":{"title":"Things","version":"1.0.0"},
"servers":[{"url":"https://api.example.com"}],
"components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
"security":[{"bearerAuth":[]}],
"paths":{
  "/things":{
    "get":{"operationId":"listThings","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}},
    "post":{"operationId":"createThing","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"}}}}}},"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}
  }
}}`
}

// inlineOpenAPISpecRenamed is the same shape as inlineOpenAPISpec but with
// listThings -> list_things. Reimport/diff tests use the rename to verify
// fingerprint-based matching.
func inlineOpenAPISpecRenamed(t *testing.T) string {
	t.Helper()
	return `{
"openapi":"3.0.0",
"info":{"title":"Things","version":"1.0.1"},
"servers":[{"url":"https://api.example.com"}],
"components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
"security":[{"bearerAuth":[]}],
"paths":{
  "/things":{
    "get":{"operationId":"list_things","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}},
    "post":{"operationId":"createThing","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"}}}}}},"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}
  }
}}`
}

func encodedSource(t *testing.T, raw string) openAPISpecSource {
	t.Helper()
	return openAPISpecSource{File: base64.StdEncoding.EncodeToString([]byte(raw))}
}

func decodeJSON(t *testing.T, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode response: %v body=%s", err, body)
	}
}

// TestHandlePreviewOpenAPIHappyPath asserts the wire shape returned by the
// preview endpoint: title, version, base URL, security schemes, operations,
// and skipped/warnings buckets all populated from the parsed spec.
func TestHandlePreviewOpenAPIHappyPath(t *testing.T) {
	api := &API{backendMgr: &fakeOpenAPIBackendManager{}}
	body := mustEncodeBody(t, openAPIPreviewRequest{Source: encodedSource(t, inlineOpenAPISpec(t))})
	req := httptest.NewRequest(http.MethodPost, "/backends/preview-openapi", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePreviewOpenAPI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp openAPIPreviewResponse
	decodeJSON(t, rec.Body.Bytes(), &resp)
	if resp.Title != "Things" || resp.Version != "1.0.0" {
		t.Fatalf("metadata = %+v", resp)
	}
	if resp.BaseURL != "https://api.example.com" {
		t.Fatalf("base url = %q", resp.BaseURL)
	}
	if len(resp.SecuritySchemes) != 1 || resp.SecuritySchemes[0].Name != "bearerAuth" {
		t.Fatalf("schemes = %+v", resp.SecuritySchemes)
	}
	if len(resp.Operations) != 2 {
		t.Fatalf("ops = %+v", resp.Operations)
	}
}

func TestHandlePreviewOpenAPIRejectsOversizedSpec(t *testing.T) {
	api := &API{backendMgr: &fakeOpenAPIBackendManager{}}
	// Build a spec that exceeds MaxSpecBytes after base64 decoding. We pad an
	// otherwise-valid spec with whitespace inside the description; once it
	// crosses the parser's cap Parse returns an error.
	pad := strings.Repeat(" ", openapi.MaxSpecBytes+1024)
	oversize := fmt.Sprintf(`{"openapi":"3.0.0","info":{"title":"x","version":"1","description":%q},"paths":{}}`, pad)
	body := mustEncodeBody(t, openAPIPreviewRequest{Source: encodedSource(t, oversize)})
	req := httptest.NewRequest(http.MethodPost, "/backends/preview-openapi", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePreviewOpenAPI(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "byte limit") {
		t.Fatalf("expected size limit error, got %s", rec.Body.String())
	}
}

func TestHandlePreviewOpenAPIRejectsExternalRefs(t *testing.T) {
	api := &API{backendMgr: &fakeOpenAPIBackendManager{}}
	spec := `{
"openapi":"3.0.0","info":{"title":"x","version":"1"},
"paths":{"/p":{"get":{"responses":{"200":{"$ref":"https://example.com/other.yaml#/components/responses/ok"}}}}}
}`
	body := mustEncodeBody(t, openAPIPreviewRequest{Source: encodedSource(t, spec)})
	req := httptest.NewRequest(http.MethodPost, "/backends/preview-openapi", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePreviewOpenAPI(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "external") {
		t.Fatalf("expected external-ref error, got %s", rec.Body.String())
	}
}

func TestHandlePreviewOpenAPIRejectsSwagger2(t *testing.T) {
	api := &API{backendMgr: &fakeOpenAPIBackendManager{}}
	spec := `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{}}`
	body := mustEncodeBody(t, openAPIPreviewRequest{Source: encodedSource(t, spec)})
	req := httptest.NewRequest(http.MethodPost, "/backends/preview-openapi", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePreviewOpenAPI(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "swagger") && !strings.Contains(rec.Body.String(), "Swagger") {
		t.Fatalf("expected swagger 2 rejection, got %s", rec.Body.String())
	}
}

// TestHandleSaveOpenAPIBackendPersistsRawBytes verifies the admin layer hands
// the parser the verbatim source bytes — no normalization round-trip — by
// asserting the SpecRaw passed to the manager matches the raw payload.
func TestHandleSaveOpenAPIBackendPersistsRawBytes(t *testing.T) {
	mgr := &fakeOpenAPIBackendManager{}
	api := &API{backendMgr: mgr}
	raw := inlineOpenAPISpec(t)
	save := openAPISaveRequest{
		Type:           "openapi",
		Source:         encodedSource(t, raw),
		SecurityScheme: "bearerAuth",
		Credential:     &CredentialConfig{Type: "static", Header: "Authorization", Value: "Bearer x"},
		DisabledTools:  []string{"createThing"},
	}
	body := mustEncodeBody(t, save)
	req := httptest.NewRequest(http.MethodPost, "/backends/things", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleAddBackend(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.saveID != "things" {
		t.Fatalf("save id = %q", mgr.saveID)
	}
	if string(mgr.saveParams.SpecRaw) != raw {
		t.Fatalf("raw bytes mismatch:\nwant: %s\n got: %s", raw, mgr.saveParams.SpecRaw)
	}
	if mgr.saveParams.SecurityScheme != "bearerAuth" {
		t.Fatalf("scheme = %q", mgr.saveParams.SecurityScheme)
	}
	if mgr.saveParams.Credential == nil || mgr.saveParams.Credential.Value != "Bearer x" {
		t.Fatalf("credential = %+v", mgr.saveParams.Credential)
	}
	if len(mgr.saveParams.DisabledTools) != 1 || mgr.saveParams.DisabledTools[0] != "createThing" {
		t.Fatalf("disabled = %+v", mgr.saveParams.DisabledTools)
	}
	if mgr.saveParams.Spec == nil {
		t.Fatal("spec not parsed before forwarding to manager")
	}
}

func TestHandleSaveOpenAPIBackendRejectsUnknownSecurityScheme(t *testing.T) {
	mgr := &fakeOpenAPIBackendManager{}
	api := &API{backendMgr: mgr}
	save := openAPISaveRequest{
		Type:           "openapi",
		Source:         encodedSource(t, inlineOpenAPISpec(t)),
		SecurityScheme: "doesNotExist",
	}
	body := mustEncodeBody(t, save)
	req := httptest.NewRequest(http.MethodPost, "/backends/things", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleAddBackend(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.saveID != "" {
		t.Fatal("manager should not have been called when scheme is invalid")
	}
}

// TestHandleOpenAPIDiffIdentifiesRenames feeds the diff endpoint a previous
// spec (with listThings) and a next spec (with list_things), and asserts the
// rename is picked up via fingerprint match.
func TestHandleOpenAPIDiffIdentifiesRenames(t *testing.T) {
	mgr := &fakeOpenAPIBackendManager{
		loadResult: &PersistedOpenAPIBackend{
			SpecRaw: []byte(inlineOpenAPISpec(t)),
		},
	}
	api := &API{backendMgr: mgr}
	body := mustEncodeBody(t, openAPIDiffRequest{Source: encodedSource(t, inlineOpenAPISpecRenamed(t))})
	req := httptest.NewRequest(http.MethodPost, "/backends/things/openapi-diff", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIDiff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp openAPIDiffResponse
	decodeJSON(t, rec.Body.Bytes(), &resp)
	if len(resp.Renamed) != 1 || resp.Renamed[0].From != "listThings" || resp.Renamed[0].To != "list_things" {
		t.Fatalf("renamed = %+v", resp.Renamed)
	}
	if len(resp.Added) != 0 {
		t.Fatalf("expected no adds, got %+v", resp.Added)
	}
	if len(resp.Removed) != 0 {
		t.Fatalf("expected no removes, got %+v", resp.Removed)
	}
	if resp.UnchangedCount != 1 {
		t.Fatalf("unchanged = %d", resp.UnchangedCount)
	}
}

// TestResolveReimportDisabledToolsPreservesFingerprintMatches asserts the
// resolver carries an operator's per-tool toggle across a rename when the
// "preserve" strategy is requested.
func TestResolveReimportDisabledToolsPreservesFingerprintMatches(t *testing.T) {
	prev, err := openapi.NewParser().Parse([]byte(inlineOpenAPISpec(t)))
	if err != nil {
		t.Fatalf("parse prev: %v", err)
	}
	next, err := openapi.NewParser().Parse([]byte(inlineOpenAPISpecRenamed(t)))
	if err != nil {
		t.Fatalf("parse next: %v", err)
	}
	got := ResolveReimportDisabledTools(prev, next, []string{"listThings", "createThing"}, true)
	// listThings renamed to list_things; createThing unchanged.
	want := []string{"createThing", "list_things"}
	if !equalSorted(got, want) {
		t.Fatalf("disabled = %+v, want %+v", got, want)
	}
}

func TestResolveReimportDisabledToolsDefaultEnabledClearsList(t *testing.T) {
	prev, err := openapi.NewParser().Parse([]byte(inlineOpenAPISpec(t)))
	if err != nil {
		t.Fatalf("parse prev: %v", err)
	}
	next, err := openapi.NewParser().Parse([]byte(inlineOpenAPISpecRenamed(t)))
	if err != nil {
		t.Fatalf("parse next: %v", err)
	}
	got := ResolveReimportDisabledTools(prev, next, []string{"listThings", "createThing"}, false)
	if len(got) != 0 {
		t.Fatalf("default_enabled should clear list, got %+v", got)
	}
}

// TestHandleOpenAPIReimportRoundTripsResolution asserts the reimport endpoint
// forwards the PreserveDisabled bool to the gateway based on the wire field.
func TestHandleOpenAPIReimportRoundTripsResolution(t *testing.T) {
	mgr := &fakeOpenAPIBackendManager{}
	api := &API{backendMgr: mgr}
	rb := openAPIReimportRequest{
		Source:                  encodedSource(t, inlineOpenAPISpecRenamed(t)),
		DisabledToolsResolution: "preserve",
	}
	body := mustEncodeBody(t, rb)
	req := httptest.NewRequest(http.MethodPost, "/backends/things/reimport", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIReimport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.reimportID != "things" {
		t.Fatalf("reimport id = %q", mgr.reimportID)
	}
	if !mgr.reimportParams.PreserveDisabled {
		t.Fatal("expected preserve=true")
	}
	if mgr.reimportParams.Spec == nil {
		t.Fatal("expected reimport spec to be parsed")
	}
}

func TestHandleOpenAPIReimportRejectsUnknownResolution(t *testing.T) {
	mgr := &fakeOpenAPIBackendManager{}
	api := &API{backendMgr: mgr}
	rb := openAPIReimportRequest{
		Source:                  encodedSource(t, inlineOpenAPISpec(t)),
		DisabledToolsResolution: "wat",
	}
	body := mustEncodeBody(t, rb)
	req := httptest.NewRequest(http.MethodPost, "/backends/things/reimport", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleOpenAPIReimport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.reimportID != "" {
		t.Fatal("manager should not have been called with invalid resolution")
	}
}

// TestSSRFGuardOnURLSource feeds the preview endpoint a localhost URL and
// asserts it's rejected before any HTTP request is made. The fetcher's SSRF
// guard runs in resolveOpenAPISource, so the test exercises the end-to-end
// path through the handler.
func TestSSRFGuardOnURLSource(t *testing.T) {
	api := &API{backendMgr: &fakeOpenAPIBackendManager{}}
	body := mustEncodeBody(t, openAPIPreviewRequest{Source: openAPISpecSource{URL: "http://127.0.0.1:9999/openapi.json"}})
	req := httptest.NewRequest(http.MethodPost, "/backends/preview-openapi", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePreviewOpenAPI(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ssrf") && !strings.Contains(rec.Body.String(), "blocked") {
		t.Fatalf("expected SSRF rejection, got %s", rec.Body.String())
	}
}

// TestRequiresOpenAPIBackendManager verifies that admin endpoints fail with
// 503 when the wired BackendManager does not implement the optional
// OpenAPIBackendManager interface — which is the production contract for an
// out-of-process gateway split.
func TestRequiresOpenAPIBackendManager(t *testing.T) {
	api := &API{backendMgr: &reconnectTestBackendManager{}}
	// Save endpoint: openapi-typed body should 503 when manager can't handle it.
	save := openAPISaveRequest{Type: "openapi", Source: encodedSource(t, inlineOpenAPISpec(t))}
	body := mustEncodeBody(t, save)
	req := httptest.NewRequest(http.MethodPost, "/backends/things", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleAddBackend(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("save status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// equalSorted returns true when a and b contain the same strings, regardless
// of order. Used by tests that don't care about the resolver's output order.
func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}

func mustEncodeBody(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return out
}
