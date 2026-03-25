package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
)

// KeyManager holds the RSA signing key and its derived JWK metadata.
// It is safe for concurrent use after construction.
type KeyManager struct {
	privateKey *rsa.PrivateKey
	// kid is the JWK thumbprint (RFC 7638) used as the "kid" JWT header.
	kid string
	// jwks is the pre-serialized JWKS JSON served at /.well-known/jwks.json.
	jwks []byte
}

// JWK represents a JSON Web Key (RFC 7517) for an RSA public key.
type JWK struct {
	// Kty is the key type — always "RSA" for this server.
	Kty string `json:"kty"`
	// Kid is the key ID (JWK thumbprint per RFC 7638).
	Kid string `json:"kid"`
	// Use indicates the intended key usage — "sig" for signing.
	Use string `json:"use"`
	// Alg is the signing algorithm — "RS256".
	Alg string `json:"alg"`
	// N is the base64url-encoded RSA modulus.
	N string `json:"n"`
	// E is the base64url-encoded RSA public exponent.
	E string `json:"e"`
}

// JWKSet is a JSON Web Key Set (RFC 7517 §5).
type JWKSet struct {
	// Keys is the list of JSON Web Keys.
	Keys []JWK `json:"keys"`
}

// newKeyManager constructs a KeyManager.
// If path is non-empty, the RSA private key is loaded from that PEM file.
// If path is empty, a 2048-bit ephemeral key is generated (dev mode).
func newKeyManager(path string) (*KeyManager, error) {
	var (
		privateKey *rsa.PrivateKey
		err        error
	)
	if path != "" {
		privateKey, err = loadRSAKey(path)
	} else {
		privateKey, err = generateRSAKey()
	}
	if err != nil {
		return nil, err
	}

	kid := computeKID(&privateKey.PublicKey)

	jwks, err := buildJWKS(&privateKey.PublicKey, kid)
	if err != nil {
		return nil, fmt.Errorf("build JWKS: %w", err)
	}

	return &KeyManager{
		privateKey: privateKey,
		kid:        kid,
		jwks:       jwks,
	}, nil
}

// loadRSAKey reads a PEM-encoded RSA private key from a file.
// Supports both PKCS#8 and PKCS#1 encoding.
func loadRSAKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Key path is from config file
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block from key file")
	}
	return parseRSAKeyBlock(block)
}

// parseRSAKeyBlock attempts PKCS#8 then PKCS#1 parsing.
func parseRSAKeyBlock(block *pem.Block) (*rsa.PrivateKey, error) {
	// Try PKCS#8 first (openssl genpkey output)
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("key file does not contain an RSA private key")
		}
		return rsaKey, nil
	}
	// Fall back to PKCS#1 (openssl genrsa output)
	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key (tried PKCS#8 and PKCS#1): %w", err)
	}
	return rsaKey, nil
}

// generateRSAKey generates a fresh 2048-bit RSA key pair using crypto/rand.
func generateRSAKey() (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate RSA key: %w", err)
	}
	return key, nil
}

// computeKID returns the JWK thumbprint of the public key per RFC 7638.
// The thumbprint is SHA-256 of the canonical JSON representation of the key,
// base64url-encoded without padding.
func computeKID(pub *rsa.PublicKey) string {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	// RFC 7638 §3.3: required members in lexicographic order, no whitespace.
	canonical := fmt.Sprintf(`{"e":%q,"kty":"RSA","n":%q}`, e, n)
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// buildJWKS constructs and marshals the JWKS document for the given public key.
func buildJWKS(pub *rsa.PublicKey, kid string) ([]byte, error) {
	jwk := JWK{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
	return json.Marshal(JWKSet{Keys: []JWK{jwk}})
}
