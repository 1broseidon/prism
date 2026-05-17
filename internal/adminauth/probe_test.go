package adminauth

import (
	"context"
	"strings"
	"testing"
)

func TestProbeIssuerRejectsPrivateAddressLiteral(t *testing.T) {
	_, _, err := ProbeIssuer(context.Background(), "http://127.0.0.1:5555")
	if err == nil {
		t.Fatal("expected private-address rejection")
	}
	if !strings.Contains(err.Error(), "not a public routable address") {
		t.Fatalf("expected public-address error, got %v", err)
	}
}

func TestProbeIssuerRejectsPrivateDNSResolution(t *testing.T) {
	_, _, err := ProbeIssuer(context.Background(), "http://localhost:5555")
	if err == nil {
		t.Fatal("expected private DNS resolution rejection")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("expected non-public DNS error, got %v", err)
	}
}

func TestProbeIssuerRejectsInvalidIssuerShape(t *testing.T) {
	tests := []string{
		"",
		"ftp://issuer.example",
		"https://user:pass@issuer.example",
		"https://issuer.example?x=1",
		"https://issuer.example#fragment",
	}

	for _, issuer := range tests {
		t.Run(issuer, func(t *testing.T) {
			if _, _, err := ProbeIssuer(context.Background(), issuer); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
