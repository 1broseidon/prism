package authserver

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"time"
)

//go:embed consent.html
var consentHTML embed.FS

var consentTmpl = template.Must(template.ParseFS(consentHTML, "consent.html"))

const (
	consentCSRFCookie = "prism_consent_csrf"
	consentCSRFField  = "_csrf"
)

type consentData struct {
	ClientName      string
	ClientID        string
	ResponseType    string
	RedirectURI     string
	State           string
	CodeChallenge   string
	ChallengeMethod string
	Scopes          string
	CSRFToken       string
}

func (s *Server) renderConsent(w http.ResponseWriter, data *consentData) {
	// Issue a fresh CSRF token via double-submit cookie. The cookie is
	// SameSite=Strict + HttpOnly so a cross-origin auto-submit POST can't
	// include it; the form field is reflected to the operator's browser so
	// a same-origin submission can echo it back. POST handler rejects when
	// either is missing or the two don't match.
	token, err := newConsentCSRFToken()
	if err != nil {
		s.logger.Error("generate consent csrf token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure intentionally omitted for http://*.localhost dev; SameSite=Strict is the loopback safeguard, Caddy adds Secure in production
		Name:     consentCSRFCookie,
		Value:    token,
		Path:     "/authorize",
		Expires:  time.Now().Add(10 * time.Minute),
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure: omitted — operator may be on http://*.localhost. Browsers
		// allow SameSite=Strict to suffice on the loopback origins we
		// support today; production deployments behind Caddy add Secure
		// automatically via the reverse-proxy hop.
	})
	data.CSRFToken = token

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := consentTmpl.Execute(w, data); err != nil {
		s.logger.Error("failed to render consent page", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// verifyConsentCSRF enforces the double-submit cookie pattern on POST
// /authorize. The form field must be non-empty and exactly equal to the
// cookie value; missing either is a 403.
func verifyConsentCSRF(r *http.Request) error {
	c, err := r.Cookie(consentCSRFCookie)
	if err != nil {
		return errors.New("missing consent csrf cookie — consent form expired or session was cross-origin")
	}
	form := r.FormValue(consentCSRFField)
	if form == "" {
		return errors.New("missing csrf form field")
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(form)) != 1 {
		return errors.New("csrf token mismatch")
	}
	return nil
}

func newConsentCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
