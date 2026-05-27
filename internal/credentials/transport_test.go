package credentials

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInjectingTransportRoundTrip401InsufficientScope(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="fs:write_file"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"insufficient_scope"}`))
	}))
	defer upstream.Close()

	tr := &InjectingTransport{
		Base:      http.DefaultTransport,
		Store:     NewStore(),
		BackendID: "upstream-fs",
	}
	req, err := http.NewRequest(http.MethodGet, upstream.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if resp == nil {
		t.Fatal("RoundTrip dropped the response on a challenge")
	}
	defer resp.Body.Close()
	var challenge *UpstreamAuthChallenge
	if !errors.As(err, &challenge) {
		t.Fatalf("error = %v, want *UpstreamAuthChallenge", err)
	}
	if challenge.RequiredScope != "fs:write_file" {
		t.Fatalf("scope = %q", challenge.RequiredScope)
	}
	if challenge.BackendID != "upstream-fs" {
		t.Fatalf("backend = %q", challenge.BackendID)
	}
	// Body must still be readable — RoundTrip must not consume it.
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "insufficient_scope") {
		t.Fatalf("body = %q", body)
	}
}

func TestInjectingTransportRoundTrip401InsufficientUserAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `DPoP error="insufficient_user_authentication", acr_values="urn:prism:mfa"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	tr := &InjectingTransport{
		Base:      http.DefaultTransport,
		Store:     NewStore(),
		BackendID: "upstream-mfa",
	}
	req, err := http.NewRequest(http.MethodGet, upstream.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if resp == nil {
		t.Fatal("RoundTrip dropped the response on a challenge")
	}
	defer resp.Body.Close()
	var challenge *UpstreamAuthChallenge
	if !errors.As(err, &challenge) {
		t.Fatalf("error = %v, want *UpstreamAuthChallenge", err)
	}
	if challenge.AcrValues != "urn:prism:mfa" {
		t.Fatalf("acr_values = %q", challenge.AcrValues)
	}
}

func TestInjectingTransportRoundTrip200NoError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	tr := &InjectingTransport{
		Base:      http.DefaultTransport,
		Store:     NewStore(),
		BackendID: "upstream-ok",
	}
	req, err := http.NewRequest(http.MethodGet, upstream.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestInjectingTransportRoundTrip403WithoutChallengeNoError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer upstream.Close()

	tr := &InjectingTransport{
		Base:      http.DefaultTransport,
		Store:     NewStore(),
		BackendID: "upstream-403",
	}
	req, err := http.NewRequest(http.MethodGet, upstream.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("plain 403 must not produce an UpstreamAuthChallenge error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestExtractParam(t *testing.T) {
	cases := []struct {
		header string
		name   string
		want   string
	}{
		{`Bearer scope="fs:write_file"`, "scope", "fs:write_file"},
		{`Bearer error="insufficient_scope", scope="fs:write_file"`, "scope", "fs:write_file"},
		{`DPoP acr_values="urn:prism:mfa"`, "acr_values", "urn:prism:mfa"},
		{`Bearer realm="upstream"`, "scope", ""},
		{`scope=foo`, "scope", "foo"},
		{``, "scope", ""},
	}
	for _, tc := range cases {
		if got := extractParam(tc.header, tc.name); got != tc.want {
			t.Errorf("extractParam(%q, %q) = %q, want %q", tc.header, tc.name, got, tc.want)
		}
	}
}
