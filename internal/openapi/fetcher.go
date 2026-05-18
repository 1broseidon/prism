package openapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FetcherConfig configures URL imports. Zero-value fields use safe defaults
// (15s timeout, no allowlist).
type FetcherConfig struct {
	// HostAllowlist permits hostnames that would otherwise be rejected by
	// the SSRF guard (localhost, link-local, RFC1918 ranges). Each entry is
	// an exact hostname match (case-insensitive). Empty means deny all
	// private destinations.
	HostAllowlist []string

	// Timeout caps both connect and total request duration. 15s by default.
	Timeout time.Duration

	// HTTPClient lets tests inject a transport. If nil, a new client with
	// the SSRF-guarded dialer is constructed.
	HTTPClient *http.Client

	// MaxBytes caps the response body size. Defaults to MaxSpecBytes.
	MaxBytes int64
}

// Fetcher pulls raw OpenAPI bytes from a URL, refusing to dial private or
// link-local destinations unless the host is allowlisted.
type Fetcher struct {
	cfg    FetcherConfig
	client *http.Client
}

// NewFetcher constructs a Fetcher with an SSRF-guarded dialer. The returned
// instance is safe for concurrent use.
func NewFetcher(cfg FetcherConfig) *Fetcher {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = MaxSpecBytes
	}

	allow := make(map[string]struct{}, len(cfg.HostAllowlist))
	for _, h := range cfg.HostAllowlist {
		allow[strings.ToLower(h)] = struct{}{}
	}

	client := cfg.HTTPClient
	if client == nil {
		dialer := &net.Dialer{Timeout: cfg.Timeout}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				if err := ssrfGuard(ctx, host, allow); err != nil {
					return nil, err
				}
				return dialer.DialContext(ctx, network, addr)
			},
		}
		client = &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
			// We allow redirects but re-apply the SSRF guard on each hop
			// (the dialer runs on every connection, so we are covered).
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if req.URL == nil {
					return errors.New("redirect with nil URL")
				}
				return preFlightURL(req.Context(), req.URL, allow)
			},
		}
	}
	return &Fetcher{cfg: cfg, client: client}
}

// Fetch retrieves the bytes at rawURL. Returns an error for non-http(s)
// schemes, SSRF-blocked hosts, HTTP failures, or bodies that exceed the
// size cap.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	allow := make(map[string]struct{}, len(f.cfg.HostAllowlist))
	for _, h := range f.cfg.HostAllowlist {
		allow[strings.ToLower(h)] = struct{}{}
	}
	if guardErr := preFlightURL(ctx, u, allow); guardErr != nil {
		return nil, guardErr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openapi spec: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch openapi spec: HTTP %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, f.cfg.MaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read openapi body: %w", err)
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return nil, fmt.Errorf("openapi spec body exceeds %d byte limit", f.cfg.MaxBytes)
	}
	return body, nil
}

// preFlightURL validates the scheme and resolves the host through the SSRF
// guard. Hostnames that resolve only to allowed IPs (after DNS) pass;
// loopback/link-local/private addresses are rejected unless the original
// hostname (case-insensitive) is in the allowlist.
func preFlightURL(ctx context.Context, u *url.URL, allow map[string]struct{}) error {
	if u == nil {
		return errors.New("nil url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q (only http and https allowed)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url has no host")
	}
	return ssrfGuard(ctx, host, allow)
}

// ssrfGuard returns an error if host (or any of its resolved IPs) maps to a
// blocked range unless host appears in allow.
func ssrfGuard(ctx context.Context, host string, allow map[string]struct{}) error {
	if _, ok := allow[strings.ToLower(host)]; ok {
		return nil
	}
	// Literal IP — check directly without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("ssrf guard: address %s is in a blocked range", ip)
		}
		return nil
	}
	// Hostname literal — block known synonyms for loopback.
	if isBlockedHostname(host) {
		return fmt.Errorf("ssrf guard: hostname %q is blocked", host)
	}
	resolver := net.DefaultResolver
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("ssrf guard: resolve %q: %w", host, err)
	}
	for _, addr := range addrs {
		if isBlockedIP(addr.IP) {
			return fmt.Errorf("ssrf guard: %s resolves to blocked address %s", host, addr.IP)
		}
	}
	return nil
}

// isBlockedHostname returns true for hostname literals known to map to
// loopback. We do not block ".local" mDNS — those typically resolve to LAN
// addresses and would be caught by IP blocking.
func isBlockedHostname(host string) bool {
	h := strings.ToLower(host)
	switch h {
	case "localhost", "ip6-localhost", "ip6-loopback":
		return true
	}
	return false
}

// isBlockedIP returns true for loopback, link-local, private (RFC1918 /
// ULA / IPv4-mapped private), and other non-public addresses.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}
	if ip.IsMulticast() {
		return true
	}
	// CGNAT 100.64.0.0/10
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && (ip4[1]&0xc0) == 64 {
			return true
		}
		// 0.0.0.0/8 (already covered by IsUnspecified for 0.0.0.0 only)
		if ip4[0] == 0 {
			return true
		}
	}
	return false
}
