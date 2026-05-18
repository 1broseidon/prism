package openapi

import (
	"strings"
	"testing"
)

// TestParseCurlBasicGET asserts the parser extracts method/URL/headers from a
// simple GET command, defaulting to GET when -X is absent.
func TestParseCurlBasicGET(t *testing.T) {
	input := `curl -H 'Authorization: Bearer xyz' https://api.example.com/v1/users`
	cmd, warnings, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v (warnings=%v)", err, warnings)
	}
	if cmd.Method != "GET" {
		t.Fatalf("method = %q, want GET", cmd.Method)
	}
	if cmd.URL != "https://api.example.com/v1/users" {
		t.Fatalf("url = %q", cmd.URL)
	}
	if len(cmd.Headers) != 1 || cmd.Headers[0].Name != "Authorization" {
		t.Fatalf("headers = %+v", cmd.Headers)
	}
	if !strings.HasPrefix(cmd.Headers[0].Value, "Bearer ") {
		t.Fatalf("auth header = %q", cmd.Headers[0].Value)
	}
}

// TestParseCurlBodyImpliesPOST asserts that supplying -d without -X promotes
// the method to POST (curl's default behavior).
func TestParseCurlBodyImpliesPOST(t *testing.T) {
	input := `curl -d '{"name":"alice"}' https://api.example.com/v1/users`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cmd.Method != "POST" {
		t.Fatalf("method = %q, want POST", cmd.Method)
	}
	if string(cmd.Body) != `{"name":"alice"}` {
		t.Fatalf("body = %q", cmd.Body)
	}
}

// TestParseCurlExplicitMethodOverridesDefault asserts -X wins over body-based
// inference.
func TestParseCurlExplicitMethodOverridesDefault(t *testing.T) {
	input := `curl -X PUT --data '{}' https://api.example.com/v1/x`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cmd.Method != "PUT" {
		t.Fatalf("method = %q", cmd.Method)
	}
}

// TestParseCurlMultilineContinuations verifies that backslash-newline folding
// produces a single logical command.
func TestParseCurlMultilineContinuations(t *testing.T) {
	input := "curl -X POST \\\n" +
		"  -H 'Authorization: Bearer xyz' \\\n" +
		"  -H 'Content-Type: application/json' \\\n" +
		"  -d '{\"name\":\"alice\"}' \\\n" +
		"  https://api.example.com/v1/users"
	cmd, warnings, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v warnings=%v", err, warnings)
	}
	if cmd.Method != "POST" {
		t.Fatalf("method = %q", cmd.Method)
	}
	if cmd.URL != "https://api.example.com/v1/users" {
		t.Fatalf("url = %q", cmd.URL)
	}
	if len(cmd.Headers) != 2 {
		t.Fatalf("headers = %+v", cmd.Headers)
	}
}

// TestParseCurlSingleAndDoubleQuotes verifies quote handling.
func TestParseCurlSingleAndDoubleQuotes(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantBody string
	}{
		{
			name:     "single quoted body",
			input:    `curl -d '{"key":"value with spaces"}' https://api.example.com/x`,
			wantBody: `{"key":"value with spaces"}`,
		},
		{
			name:     "double quoted body with escape",
			input:    `curl -d "{\"key\":\"value\"}" https://api.example.com/x`,
			wantBody: `{"key":"value"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, err := ParseCurl(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if string(cmd.Body) != tc.wantBody {
				t.Fatalf("body = %q want %q", cmd.Body, tc.wantBody)
			}
		})
	}
}

// TestParseCurlFileBodySkipped verifies @filename references emit a warning
// and don't attempt to read from disk.
func TestParseCurlFileBodySkipped(t *testing.T) {
	input := `curl -d @payload.json https://api.example.com/x`
	cmd, warnings, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cmd.FileBody {
		t.Fatal("expected FileBody=true")
	}
	if len(cmd.Body) != 0 {
		t.Fatalf("expected empty body for file ref, got %q", cmd.Body)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "file reference") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected file-reference warning, got %v", warnings)
	}
}

// TestParseCurlUnknownFlagEmitsWarning verifies that unrecognized flags don't
// stop parsing. We use a flag that looks like it takes a value (next token
// is non-flag) — the parser should swallow the value and proceed to the URL.
func TestParseCurlUnknownFlagEmitsWarning(t *testing.T) {
	input := `curl -A 'curl/8.0' https://api.example.com/x`
	cmd, warnings, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cmd.URL != "https://api.example.com/x" {
		t.Fatalf("url = %q", cmd.URL)
	}
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning for -A")
	}
}

// TestParseCurlNoArgFlagsIgnoredSilently verifies that common no-arg flags
// (like --insecure, -L) don't emit warnings and don't disturb parsing.
func TestParseCurlNoArgFlagsIgnoredSilently(t *testing.T) {
	input := `curl -L --insecure -s https://api.example.com/x`
	cmd, warnings, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cmd.URL != "https://api.example.com/x" {
		t.Fatalf("url = %q", cmd.URL)
	}
	for _, w := range warnings {
		if strings.Contains(w, "-L") || strings.Contains(w, "insecure") || strings.Contains(w, "-s ") {
			t.Fatalf("did not expect warning for known no-arg flag: %s", w)
		}
	}
}

// TestScaffoldFromCurlEmitsBearerScheme verifies an Authorization: Bearer
// header surfaces as bearerAuth + a security requirement.
func TestScaffoldFromCurlEmitsBearerScheme(t *testing.T) {
	input := `curl -H 'Authorization: Bearer xyz' https://api.example.com/v1/users`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, warnings, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v warnings=%v", err, warnings)
	}
	s := string(out)
	if !strings.Contains(s, "bearerAuth:") {
		t.Fatalf("missing bearerAuth in output:\n%s", s)
	}
	if !strings.Contains(s, "scheme: bearer") {
		t.Fatalf("missing scheme: bearer in output:\n%s", s)
	}
	if !strings.Contains(s, "type: http") {
		t.Fatalf("missing type: http in output:\n%s", s)
	}
	if !strings.Contains(s, "openapi: 3.1.0") {
		t.Fatalf("missing openapi version:\n%s", s)
	}
}

// TestScaffoldFromCurlEmitsAPIKeyScheme verifies an X-API-Key header becomes
// an apiKeyAuth scheme rather than a plain header parameter.
func TestScaffoldFromCurlEmitsAPIKeyScheme(t *testing.T) {
	input := `curl -H 'X-API-Key: abc123' https://api.example.com/things`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, _, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "apiKeyAuth:") {
		t.Fatalf("missing apiKeyAuth scheme:\n%s", s)
	}
	if !strings.Contains(s, "type: apiKey") {
		t.Fatalf("missing type apiKey:\n%s", s)
	}
	if !strings.Contains(s, "in: header") {
		t.Fatalf("missing in: header:\n%s", s)
	}
	if !strings.Contains(s, "name: X-API-Key") {
		t.Fatalf("missing X-API-Key name in output:\n%s", s)
	}
}

// TestScaffoldFromCurlUnsupportedAuthBecomesHeaderParam verifies a Basic auth
// header is emitted as a regular header parameter with a comment.
func TestScaffoldFromCurlUnsupportedAuthBecomesHeaderParam(t *testing.T) {
	input := `curl -H 'Authorization: Basic dXNlcjpwYXNz' https://api.example.com/v1/x`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, warnings, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "bearerAuth:") {
		t.Fatalf("should not have emitted bearerAuth for Basic auth:\n%s", s)
	}
	if !strings.Contains(s, "unsupported auth scheme") {
		t.Fatalf("expected unsupported-auth comment:\n%s", s)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "not a recognized scheme") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unsupported auth warning, got %v", warnings)
	}
}

// TestScaffoldFromCurlInfersJSONBodySchema asserts that a JSON body produces
// an object schema with the expected top-level properties and types.
func TestScaffoldFromCurlInfersJSONBodySchema(t *testing.T) {
	input := `curl -X POST -d '{"name":"alice","age":42,"active":true,"tags":["admin","ops"]}' https://api.example.com/v1/users`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, _, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"requestBody:",
		"application/json:",
		"name:",
		"type: string",
		"age:",
		"type: integer",
		"active:",
		"type: boolean",
		"tags:",
		"type: array",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in output:\n%s", want, s)
		}
	}
}

// TestScaffoldFromCurlFallsBackOnNonJSONBody verifies a non-JSON body emits
// the object placeholder schema rather than failing.
func TestScaffoldFromCurlFallsBackOnNonJSONBody(t *testing.T) {
	input := `curl -X POST -d 'not json at all' https://api.example.com/v1/x`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, warnings, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !strings.Contains(string(out), "type: object") {
		t.Fatalf("expected object placeholder:\n%s", out)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "valid JSON") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected non-JSON warning, got %v", warnings)
	}
}

// TestScaffoldFromCurlEmitsQueryParameters asserts query params appear as
// `in: query` parameters with the typed schema inferred from values.
func TestScaffoldFromCurlEmitsQueryParameters(t *testing.T) {
	input := `curl 'https://api.example.com/v1/search?q=test&limit=20&active=true'`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, _, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"in: query",
		"name: q",
		"name: limit",
		"type: integer",
		"name: active",
		"type: boolean",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in output:\n%s", want, s)
		}
	}
}

// TestScaffoldFromCurlEmitsCustomHeaderParameters verifies non-special,
// non-API-key headers appear as `in: header` parameters.
func TestScaffoldFromCurlEmitsCustomHeaderParameters(t *testing.T) {
	input := `curl -H 'X-Request-ID: abc' -H 'X-Tenant: acme' https://api.example.com/v1/x`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, _, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: X-Request-ID") {
		t.Fatalf("missing X-Request-ID parameter:\n%s", s)
	}
	if !strings.Contains(s, "name: X-Tenant") {
		t.Fatalf("missing X-Tenant parameter:\n%s", s)
	}
}

// TestScaffoldFromCurlServerStripsDefaultPort asserts default ports are
// dropped from servers[].url.
func TestScaffoldFromCurlServerStripsDefaultPort(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSrv string
	}{
		{"https default", `curl https://api.example.com:443/x`, "url: https://api.example.com"},
		{"http default", `curl http://api.example.com:80/x`, "url: http://api.example.com"},
		{"non-default kept", `curl https://api.example.com:8443/x`, "url: https://api.example.com:8443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, err := ParseCurl(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			out, _, err := ScaffoldFromCurl(cmd)
			if err != nil {
				t.Fatalf("scaffold: %v", err)
			}
			if !strings.Contains(string(out), tc.wantSrv) {
				t.Fatalf("missing %q in output:\n%s", tc.wantSrv, out)
			}
		})
	}
}

// TestScaffoldFromCurlOutputParsesAsValidSpec asserts that the YAML produced
// round-trips through the OpenAPI parser without errors — the whole point of
// the feature is to give the operator a working starting point.
func TestScaffoldFromCurlOutputParsesAsValidSpec(t *testing.T) {
	input := `curl -X POST \
  -H 'Authorization: Bearer xyz' \
  -H 'X-Tenant: acme' \
  -d '{"name":"alice","age":42}' \
  'https://api.example.com/v1/users?notify=true'`
	cmd, _, err := ParseCurl(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, _, err := ScaffoldFromCurl(cmd)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	spec, err := NewParser().Parse(out)
	if err != nil {
		t.Fatalf("parse scaffold output: %v\n--- yaml ---\n%s", err, out)
	}
	if len(spec.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d (output:\n%s)", len(spec.Operations), out)
	}
	op := spec.Operations[0]
	if op.Method != "POST" {
		t.Fatalf("method = %q, want POST", op.Method)
	}
	if op.Path != "/v1/users" {
		t.Fatalf("path = %q, want /v1/users", op.Path)
	}
}

// TestParseCurlRejectsUnterminatedQuote ensures malformed input fails fast
// rather than silently dropping characters.
func TestParseCurlRejectsUnterminatedQuote(t *testing.T) {
	_, _, err := ParseCurl(`curl -H 'no closing https://api.example.com/x`)
	if err == nil {
		t.Fatal("expected error for unterminated quote")
	}
}

// TestParseCurlRejectsEmptyInput ensures empty input is rejected explicitly.
func TestParseCurlRejectsEmptyInput(t *testing.T) {
	_, _, err := ParseCurl("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// TestScaffoldFromCurlRejectsRelativeURL ensures URLs without a scheme/host
// produce an error rather than a malformed spec.
func TestScaffoldFromCurlRejectsRelativeURL(t *testing.T) {
	cmd := &CurlCommand{Method: "GET", URL: "/v1/users"}
	_, _, err := ScaffoldFromCurl(cmd)
	if err == nil {
		t.Fatal("expected error for relative URL")
	}
}
