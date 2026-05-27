package authserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

var errUseDPoPNonce = errors.New("use dpop nonce")

const dpopNonceWindow = 5 * time.Minute

func (s *Server) validateTokenDPoP(r *http.Request) (string, bool, error) {
	header := r.Header.Get("DPoP")
	if header == "" {
		return "", false, nil
	}
	now := s.now()
	proof, err := auth.ParseAndValidateProof(header, r.Method, absoluteRequestURL(r), s.currentDPoPNonce(now), "", now, s.dpopReplay)
	if err != nil && errors.Is(err, auth.ErrDPoPNonceMismatch) {
		proof, err = auth.ParseAndValidateProof(header, r.Method, absoluteRequestURL(r), s.previousDPoPNonce(now), "", now, s.dpopReplay)
	}
	if err != nil {
		if errors.Is(err, auth.ErrDPoPNonceMismatch) {
			return "", true, errUseDPoPNonce
		}
		return "", true, err
	}
	return proof.Thumbprint, true, nil
}

func (s *Server) setDPoPNonceHeader(w http.ResponseWriter, now time.Time) {
	w.Header().Set("DPoP-Nonce", s.currentDPoPNonce(now))
}

func (s *Server) currentDPoPNonce(now time.Time) string {
	return s.dpopNonceForBucket(dpopNonceBucket(now))
}

func (s *Server) previousDPoPNonce(now time.Time) string {
	return s.dpopNonceForBucket(dpopNonceBucket(now) - 1)
}

func dpopNonceBucket(now time.Time) int64 {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Unix() / int64(dpopNonceWindow/time.Second)
}

func (s *Server) dpopNonceForBucket(bucket int64) string {
	msg := []byte(fmt.Sprintf("prism-dpop-nonce:%s:%d", s.cfg.Issuer, bucket))
	mac := hmac.New(sha256.New, s.dpopNonceKey())
	_, _ = mac.Write(msg)
	sum := mac.Sum(nil)
	return strconv.FormatInt(bucket, 36) + "." + base64.RawURLEncoding.EncodeToString(sum)
}

func (s *Server) dpopNonceKey() []byte {
	if s.km != nil && s.km.privateKey != nil && s.km.privateKey.D != nil {
		sum := sha256.Sum256(s.km.privateKey.D.Bytes())
		return sum[:]
	}
	sum := sha256.Sum256([]byte(s.cfg.Issuer))
	return sum[:]
}

func absoluteRequestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, r.URL.Path)
}

func grantsRequireCnf(grants []auth.IssuedGrant) bool {
	for _, g := range grants {
		if g.CnfRequired {
			return true
		}
	}
	return false
}

func amrForDPoP(jkt string) []string {
	if jkt == "" {
		return nil
	}
	return []string{"hwk"}
}
