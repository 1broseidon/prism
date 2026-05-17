package adminauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const probeHTTPTimeout = 10 * time.Second

// ProbeIssuer runs OIDC discovery against the given issuer URL and returns
// the discovered authorization and token endpoints. Used by the admin
// "test connection" flow to validate config before committing it.
func ProbeIssuer(ctx context.Context, issuer string) (authURL, tokenURL string, err error) {
	if validateErr := validateProbeIssuerURL(issuer); validateErr != nil {
		return "", "", validateErr
	}
	probeCtx := oidc.ClientContext(ctx, safeProbeHTTPClient())
	provider, err := oidc.NewProvider(probeCtx, issuer)
	if err != nil {
		return "", "", err
	}
	ep := provider.Endpoint()
	return ep.AuthURL, ep.TokenURL, nil
}

func validateProbeIssuerURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("issuer is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse issuer: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("issuer scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("issuer host is required")
	}
	if u.User != nil {
		return fmt.Errorf("issuer must not include userinfo")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("issuer must not include query or fragment")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !isPublicProbeAddr(ip) {
		return fmt.Errorf("issuer host %s is not a public routable address", ip)
	}
	return nil
}

func safeProbeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: probeHTTPTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           publicOnlyDialContext,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}
}

func publicOnlyDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse dial address %q: %w", address, err)
	}
	if hostErr := requirePublicHost(ctx, host); hostErr != nil {
		return nil, hostErr
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		ip, ok := netip.AddrFromSlice(tcpAddr.IP)
		if ok && !isPublicProbeAddr(ip) {
			_ = conn.Close()
			return nil, fmt.Errorf("issuer host connected to non-public address %s", ip)
		}
	}
	return conn, nil
}

func requirePublicHost(ctx context.Context, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicProbeAddr(ip) {
			return fmt.Errorf("issuer host %s is not a public routable address", ip)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve issuer host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve issuer host %q: no addresses", host)
	}
	for _, ip := range ips {
		if !isPublicProbeAddr(ip) {
			return fmt.Errorf("issuer host %q resolves to non-public address %s", host, ip)
		}
	}
	return nil
}

func isPublicProbeAddr(ip netip.Addr) bool {
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	return ip.IsValid() &&
		ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsUnspecified()
}
