package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// minimalSpec returns a valid OpenAPI 3.0 spec covering one accepted GET
// operation. Helpers compose larger fixtures from this baseline.
func minimalSpec() string {
	return `{
  "openapi": "3.0.0",
  "info": {"title":"Test","version":"1.0.0"},
  "servers":[{"url":"https://api.example.com"}],
  "paths": {
    "/things/{id}": {
      "get": {
        "operationId":"getThing",
        "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
        "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"}}}}}}}
      }
    }
  }
}`
}

func TestParse_AcceptsMinimalSpec(t *testing.T) {
	p := NewParser()
	spec, err := p.Parse([]byte(minimalSpec()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Title != "Test" || spec.Version != "1.0.0" {
		t.Errorf("metadata mismatch: %+v", spec)
	}
	if spec.BaseURL != "https://api.example.com" {
		t.Errorf("base url mismatch: %q", spec.BaseURL)
	}
	if len(spec.Operations) != 1 {
		t.Fatalf("want 1 op, got %d (skipped=%v)", len(spec.Operations), spec.Skipped)
	}
	op := spec.Operations[0]
	if op.Name != "getThing" || op.Method != "GET" || op.Path != "/things/{id}" {
		t.Errorf("op identity mismatch: %+v", op)
	}
	if op.Fingerprint == "" {
		t.Error("fingerprint missing")
	}
	if !op.Annotations.ReadOnly || !op.Annotations.Idempotent || op.Annotations.Destructive {
		t.Errorf("annotations wrong for GET: %+v", op.Annotations)
	}
	if !op.Annotations.OpenWorld {
		t.Error("openWorld must always be true")
	}
}

func TestParse_RejectsOversizedSpec(t *testing.T) {
	big := make([]byte, MaxSpecBytes+1)
	for i := range big {
		big[i] = ' '
	}
	_, err := NewParser().Parse(big)
	if err == nil || !strings.Contains(err.Error(), "5242880") {
		t.Fatalf("want size-limit error, got %v", err)
	}
}

func TestParse_RejectsEmpty(t *testing.T) {
	_, err := NewParser().Parse(nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty error, got %v", err)
	}
}

func TestParse_RejectsSwagger2(t *testing.T) {
	swagger := `{"swagger":"2.0","info":{"title":"old","version":"1"},"paths":{}}`
	_, err := NewParser().Parse([]byte(swagger))
	if err == nil || !strings.Contains(err.Error(), "Swagger 2.0") && !strings.Contains(err.Error(), "swagger 2.0") {
		t.Fatalf("want swagger-rejection error, got %v", err)
	}
}

func TestParse_RejectsUnsupportedVersion(t *testing.T) {
	spec := `{"openapi":"4.0.0","info":{"title":"x","version":"1"},"paths":{}}`
	_, err := NewParser().Parse([]byte(spec))
	if err == nil || !strings.Contains(err.Error(), "unsupported openapi version") {
		t.Fatalf("want version-rejection error, got %v", err)
	}
}

func TestParse_RejectsMissingVersion(t *testing.T) {
	spec := `{"info":{"title":"x","version":"1"},"paths":{}}`
	_, err := NewParser().Parse([]byte(spec))
	if err == nil {
		t.Fatal("want error for missing openapi version")
	}
}

func TestParse_AcceptsOpenAPI31(t *testing.T) {
	spec := `{
  "openapi": "3.1.0",
  "info":{"title":"x","version":"1"},
  "paths":{
    "/p":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}
  }
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatalf("3.1 should be supported: %v", err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("want 1 op, got %d", len(out.Operations))
	}
}

func TestParse_RejectsExternalRefs(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"get":{"responses":{"200":{"$ref":"https://example.com/other.yaml#/components/responses/ok"}}}}}
}`
	_, err := NewParser().Parse([]byte(spec))
	if err == nil {
		t.Fatal("want error for external $ref")
	}
}

func TestParse_SkipsNonJSONRequest(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"post":{
    "operationId":"upload",
    "requestBody":{"required":true,"content":{"multipart/form-data":{"schema":{"type":"object"}}}},
    "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
  }}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 0 || len(out.Skipped) != 1 {
		t.Fatalf("want skip, got ops=%v skipped=%v", out.Operations, out.Skipped)
	}
	if out.Skipped[0].Reason != SkipReasonUnsupportedRequestContentType {
		t.Errorf("wrong skip reason: %q", out.Skipped[0].Reason)
	}
}

func TestParse_SkipsNonJSONResponse(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"get":{
    "operationId":"download",
    "responses":{"200":{"description":"ok","content":{"application/xml":{"schema":{"type":"object"}}}}}
  }}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Reason != SkipReasonUnsupportedResponseContentType {
		t.Fatalf("want unsupported response skip, got %v", out.Skipped)
	}
}

func TestParse_Accepts204NoContent(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"delete":{"operationId":"del","responses":{"204":{"description":"no content"}}}}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("want 1 op, got %d (skipped=%v)", len(out.Operations), out.Skipped)
	}
}

func TestParse_FiltersUnsupportedSecuritySchemes(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "components":{"securitySchemes":{
    "bearerAuth":{"type":"http","scheme":"bearer"},
    "apiKeyHeader":{"type":"apiKey","in":"header","name":"X-API-Key"},
    "apiKeyQuery":{"type":"apiKey","in":"query","name":"k"},
    "basicAuth":{"type":"http","scheme":"basic"},
    "oauth2":{"type":"oauth2","flows":{"implicit":{"authorizationUrl":"https://x.test","scopes":{}}}}
  }},
  "paths":{
    "/ok":{"get":{"security":[{"bearerAuth":[]}],"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}},
    "/skip":{"get":{"security":[{"basicAuth":[]}],"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}
  }
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.SecuritySchemes["bearerAuth"]; !ok {
		t.Error("bearerAuth should be kept")
	}
	if _, ok := out.SecuritySchemes["apiKeyHeader"]; !ok {
		t.Error("apiKeyHeader should be kept")
	}
	if _, ok := out.SecuritySchemes["apiKeyQuery"]; ok {
		t.Error("apiKey-in-query must be dropped")
	}
	if _, ok := out.SecuritySchemes["basicAuth"]; ok {
		t.Error("basic must be dropped")
	}
	if _, ok := out.SecuritySchemes["oauth2"]; ok {
		t.Error("oauth2 must be dropped")
	}
	if len(out.Operations) != 1 || out.Operations[0].Path != "/ok" {
		t.Errorf("want only /ok accepted, got %+v", out.Operations)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Reason != SkipReasonNoSupportedSecurityScheme {
		t.Errorf("want 1 security-skip, got %+v", out.Skipped)
	}
}

func TestParse_AcceptsOpenAuthRequirement(t *testing.T) {
	// Empty SecurityRequirement {} means "no auth required" — that
	// alternative must always be acceptable even without supported schemes.
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "components":{"securitySchemes":{"basicAuth":{"type":"http","scheme":"basic"}}},
  "paths":{"/p":{"get":{"security":[{},{"basicAuth":[]}],"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("want 1 op (open auth path), got %+v", out)
	}
}

func TestParse_SkipsParamNameCollision(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p/{id}":{"get":{
    "parameters":[
      {"name":"id","in":"path","required":true,"schema":{"type":"string"}},
      {"name":"id","in":"query","schema":{"type":"string"}}
    ],
    "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
  }}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 0 || len(out.Skipped) != 1 {
		t.Fatalf("want collision skip, got ops=%v skipped=%v", out.Operations, out.Skipped)
	}
	if out.Skipped[0].Reason != SkipReasonParameterNameCollision {
		t.Errorf("wrong skip reason: %q", out.Skipped[0].Reason)
	}
}

func TestParse_GeneratesNameFromMethodAndPath(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/v1/Users/{userId}/posts":{"post":{
    "requestBody":{"content":{"application/json":{"schema":{"type":"object"}}}},
    "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
  }}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("want 1 op, got %d", len(out.Operations))
	}
	if got, want := out.Operations[0].Name, "post_v1_users_userid_posts"; got != want {
		t.Errorf("generated name: got %q want %q", got, want)
	}
}

func TestParse_EmitsSoftWarningOver500Ops(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{`)
	for i := 0; i < 501; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"/p%d":{"get":{"operationId":"op%d","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}`, i, i)
	}
	b.WriteString("}}")
	out, err := NewParser().Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 501 {
		t.Errorf("want 501 ops, got %d", len(out.Operations))
	}
	if len(out.Warnings) == 0 || !strings.Contains(out.Warnings[0], "soft limit") {
		t.Errorf("want soft-limit warning, got %v", out.Warnings)
	}
}

func TestParse_FlattensBodyAndParamsIntoInputSchema(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"post":{
    "operationId":"create",
    "parameters":[
      {"name":"trace_id","in":"header","schema":{"type":"string"}},
      {"name":"verbose","in":"query","schema":{"type":"boolean"}}
    ],
    "requestBody":{"required":true,"content":{"application/json":{"schema":{
      "type":"object",
      "required":["name"],
      "properties":{"name":{"type":"string"},"qty":{"type":"integer"}}
    }}}},
    "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
  }}}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("want 1 op, got %d (skipped=%v)", len(out.Operations), out.Skipped)
	}
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
		MCP        map[string]bool            `json:"x-mcp-annotations"`
	}
	if err := json.Unmarshal(out.Operations[0].InputSchema, &schema); err != nil {
		t.Fatalf("input schema unmarshal: %v", err)
	}
	for _, want := range []string{"trace_id", "verbose", "name", "qty"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Errorf("flat schema missing %q (props=%v)", want, keys(schema.Properties))
		}
	}
	if got := mustString(schema.Required); got != "name" {
		t.Errorf("required wrong: %v", schema.Required)
	}
	if !schema.MCP["destructive"] || schema.MCP["readOnly"] || !schema.MCP["openWorld"] {
		t.Errorf("annotations wrong: %+v", schema.MCP)
	}
}

func TestParse_FingerprintStableAcrossReparses(t *testing.T) {
	data := []byte(minimalSpec())
	p := NewParser()
	a, err := p.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if a.Operations[0].Fingerprint == "" {
		t.Fatal("fingerprint empty")
	}
	if a.Operations[0].Fingerprint != b.Operations[0].Fingerprint {
		t.Errorf("fingerprint not stable across re-parses: %q vs %q",
			a.Operations[0].Fingerprint, b.Operations[0].Fingerprint)
	}
}

func TestParse_FingerprintChangesOnSignatureChange(t *testing.T) {
	base := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"post":{"operationId":"create","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"a":{"type":"string"}}}}}},"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}}}`
	changed := strings.Replace(base, `"a":{"type":"string"}`, `"b":{"type":"string"}`, 1)
	p := NewParser()
	a, err := p.Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Parse([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if a.Operations[0].Fingerprint == c.Operations[0].Fingerprint {
		t.Errorf("fingerprint should differ when input keys change")
	}
}

func TestParse_FingerprintChangesOnResponseShapeChange(t *testing.T) {
	base := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p":{"get":{"operationId":"get","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"x":{"type":"string"}}}}}}}}}}}`
	changed := strings.Replace(base, `"x":{"type":"string"}`, `"x":{"type":"string"},"y":{"type":"integer"}`, 1)
	p := NewParser()
	a, err := p.Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Parse([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if a.Operations[0].Fingerprint == c.Operations[0].Fingerprint {
		t.Errorf("fingerprint should change when response keys change")
	}
}

func TestParse_InternalRefsResolved(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "components":{"schemas":{"Thing":{"type":"object","properties":{"id":{"type":"string"}}}}},
  "paths":{"/p":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Thing"}}}}}}}}}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatalf("internal refs should resolve: %v", err)
	}
	if len(out.Operations) != 1 {
		t.Fatal("want 1 op")
	}
}

func TestParse_PathAndOpParameterMerge(t *testing.T) {
	spec := `{
  "openapi":"3.0.0","info":{"title":"x","version":"1"},
  "paths":{"/p/{id}":{
    "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],
    "get":{
      "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
      "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
    }
  }}
}`
	out, err := NewParser().Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("want 1 op, got %+v", out)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(out.Operations[0].InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	idJSON := string(schema.Properties["id"])
	// op-level wins → type:string
	if !strings.Contains(idJSON, `"string"`) {
		t.Errorf("op-level param did not override path-level: %s", idJSON)
	}
}

// -----------------------------------------------------------------------------
// Fetcher tests
// -----------------------------------------------------------------------------

func TestFetcher_RejectsLoopbackLiteral(t *testing.T) {
	f := NewFetcher(FetcherConfig{})
	_, err := f.Fetch(context.Background(), "http://127.0.0.1/spec.json")
	if err == nil || !strings.Contains(err.Error(), "ssrf guard") {
		t.Fatalf("want SSRF rejection, got %v", err)
	}
}

func TestFetcher_RejectsLocalhostHostname(t *testing.T) {
	f := NewFetcher(FetcherConfig{})
	_, err := f.Fetch(context.Background(), "http://localhost/spec.json")
	if err == nil || !strings.Contains(err.Error(), "ssrf guard") {
		t.Fatalf("want SSRF rejection, got %v", err)
	}
}

func TestFetcher_RejectsPrivateAddresses(t *testing.T) {
	cases := []string{
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://172.16.0.1/x",
		"http://169.254.1.1/x", // link-local
		"http://0.0.0.0/x",
	}
	f := NewFetcher(FetcherConfig{})
	for _, c := range cases {
		_, err := f.Fetch(context.Background(), c)
		if err == nil {
			t.Errorf("%s should be rejected", c)
		}
	}
}

func TestFetcher_RejectsUnsupportedScheme(t *testing.T) {
	f := NewFetcher(FetcherConfig{})
	_, err := f.Fetch(context.Background(), "file:///etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("want scheme rejection, got %v", err)
	}
}

func TestFetcher_AllowsAllowlistedLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, minimalSpec())
	}))
	defer srv.Close()

	// httptest server listens on 127.0.0.1; allow that exact host literal.
	host, _, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	f := NewFetcher(FetcherConfig{
		HostAllowlist: []string{host},
		Timeout:       2 * time.Second,
	})
	body, err := f.Fetch(context.Background(), srv.URL+"/openapi.json")
	if err != nil {
		t.Fatalf("allowlisted host should fetch: %v", err)
	}
	if !strings.Contains(string(body), `"openapi"`) {
		t.Errorf("body missing spec: %s", body)
	}
}

func TestFetcher_EnforcesSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 200))
	}))
	defer srv.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	f := NewFetcher(FetcherConfig{
		HostAllowlist: []string{host},
		Timeout:       2 * time.Second,
		MaxBytes:      100,
	})
	_, err := f.Fetch(context.Background(), srv.URL+"/spec")
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("want size-cap error, got %v", err)
	}
}

func TestFetcher_ReturnsHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	f := NewFetcher(FetcherConfig{
		HostAllowlist: []string{host},
		Timeout:       2 * time.Second,
	})
	_, err := f.Fetch(context.Background(), srv.URL+"/spec")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want HTTP 500, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustString(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
