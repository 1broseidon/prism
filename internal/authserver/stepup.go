package authserver

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

const authSessionCookie = "prism_auth_session"

// stepUpStateTTL bounds how long a single-use step-up state token is valid.
// The token is issued by /authorize when step-up is required and consumed by
// POST /stepup; anything more than a few minutes is a usability regression
// without buying any additional security.
const stepUpStateTTL = 300 * time.Second

type authSession struct {
	AuthTime int64
	Acr      string
}

// stepUpState is the single-use gating token bound to one in-flight step-up
// redirect. Storing it server-side means the POST /stepup handler can refuse
// any request that wasn't preceded by a real /authorize -> redirect.
type stepUpState struct {
	Token     string
	ReturnURL string
	CreatedAt time.Time
	Used      bool
}

func (s *Server) authSessionFromRequest(r *http.Request) *authSession {
	c, err := r.Cookie(authSessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	return s.oauth.sessions[c.Value]
}

func (s *Server) needsStepUp(r *http.Request, grants []auth.IssuedGrant) bool {
	sess := s.authSessionFromRequest(r)
	now := s.now().Unix()
	for _, g := range grants {
		if g.AcrRequired != "" {
			if sess == nil || sess.Acr != g.AcrRequired {
				return true
			}
		}
		if g.AuthFreshnessMax > 0 {
			if sess == nil || sess.AuthTime == 0 || now-sess.AuthTime > g.AuthFreshnessMax {
				return true
			}
		}
	}
	return false
}

// issueStepUpState mints a single-use state token bound to the return URL.
// Caller must hold no oauth lock; this acquires s.oauth.mu internally.
func (s *Server) issueStepUpState(returnURL string) (string, error) {
	token, err := generateRandomString(32)
	if err != nil {
		var b [32]byte
		if _, readErr := rand.Read(b[:]); readErr != nil {
			return "", readErr
		}
		token = base64.RawURLEncoding.EncodeToString(b[:])
	}
	s.oauth.mu.Lock()
	if s.oauth.stepupStates == nil {
		s.oauth.stepupStates = make(map[string]*stepUpState)
	}
	s.oauth.stepupStates[token] = &stepUpState{
		Token:     token,
		ReturnURL: returnURL,
		CreatedAt: s.now(),
	}
	s.oauth.mu.Unlock()
	return token, nil
}

// consumeStepUpState validates and marks-used a state token. Returns the
// captured return URL on success. On any failure (missing, unknown, expired,
// already-used) returns ok=false with no session mutation.
func (s *Server) consumeStepUpState(token string) (returnURL string, ok bool) {
	if token == "" {
		return "", false
	}
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	st, exists := s.oauth.stepupStates[token]
	if !exists {
		return "", false
	}
	if st.Used {
		return "", false
	}
	if s.now().Sub(st.CreatedAt) > stepUpStateTTL {
		delete(s.oauth.stepupStates, token)
		return "", false
	}
	st.Used = true
	return st.ReturnURL, true
}

func (s *Server) redirectStepUp(w http.ResponseWriter, r *http.Request) {
	token, err := s.issueStepUpState(r.URL.RequestURI())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u := &url.URL{Path: "/stepup"}
	q := u.Query()
	q.Set("state", token)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) handleStepUp(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		state := r.URL.Query().Get("state")
		// We do not consume the state here — only validate it exists and is
		// fresh enough to render the form. Consumption happens on POST.
		if state == "" {
			http.Error(w, "missing state", http.StatusBadRequest)
			return
		}
		s.oauth.mu.Lock()
		st, exists := s.oauth.stepupStates[state]
		s.oauth.mu.Unlock()
		if !exists || st.Used || s.now().Sub(st.CreatedAt) > stepUpStateTTL {
			http.Error(w, "invalid step-up state", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<form method="POST"><input type="hidden" name="state" value="` + htmlEscape(state) + `"><button type="submit">Continue</button></form>`))
	case http.MethodPost:
		// Read state from form first (preferred), then fall back to query — the
		// form input is what the rendered stub uses; the query is a convenience
		// for direct POSTs in tests.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		state := r.FormValue("state")
		if state == "" {
			state = r.URL.Query().Get("state")
		}
		returnURL, ok := s.consumeStepUpState(state)
		if !ok {
			http.Error(w, "invalid step-up state", http.StatusBadRequest)
			return
		}
		id, err := generateRandomString(32)
		if err != nil {
			var b [32]byte
			if _, readErr := rand.Read(b[:]); readErr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			id = base64.RawURLEncoding.EncodeToString(b[:])
		}
		s.oauth.mu.Lock()
		s.oauth.sessions[id] = &authSession{AuthTime: s.now().Unix(), Acr: "urn:prism:mfa"}
		s.oauth.mu.Unlock()
		http.SetCookie(w, &http.Cookie{
			Name:     authSessionCookie,
			Value:    id,
			Path:     "/",
			MaxAge:   600,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		if returnURL == "" {
			returnURL = "/authorize"
		}
		http.Redirect(w, r, returnURL, http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// htmlEscape is a tiny escaper for the single attribute we inject into the
// step-up stub form. The state token is generated server-side from random
// bytes, so the only characters we expect are URL-safe base64 — but defense
// in depth costs us a handful of lines.
func htmlEscape(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '<':
			b = append(b, []byte("&lt;")...)
		case '>':
			b = append(b, []byte("&gt;")...)
		case '&':
			b = append(b, []byte("&amp;")...)
		case '"':
			b = append(b, []byte("&quot;")...)
		case '\'':
			b = append(b, []byte("&#39;")...)
		default:
			b = append(b, c)
		}
	}
	return string(b)
}
