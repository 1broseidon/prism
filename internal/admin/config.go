package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/1broseidon/prism/internal/adminauth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
)

// adminAuthView is the JSON shape returned to the SPA. The client_secret is
// replaced by a boolean — secrets should never roundtrip through the UI.
type adminAuthView struct {
	Config          *adminAuthConfigView `json:"config"`
	ClientSecretSet bool                 `json:"client_secret_set"`
	Enabled         bool                 `json:"enabled"`
	Active          bool                 `json:"active"`
	ActiveDiscovery string               `json:"active_issuer,omitempty"`
}

type adminAuthConfigView struct {
	Issuer       string                 `json:"issuer"`
	ClientID     string                 `json:"client_id"`
	RedirectURL  string                 `json:"redirect_url"`
	Scopes       []string               `json:"scopes,omitempty"`
	GroupsClaim  string                 `json:"groups_claim,omitempty"`
	SessionTTL   string                 `json:"session_ttl,omitempty"`
	CookieDomain string                 `json:"cookie_domain,omitempty"`
	CookieSecure bool                   `json:"cookie_secure,omitempty"`
	Rules        []config.AdminAuthRule `json:"rules"`
}

// adminAuthPutPayload is the PUT body. ClientSecret is a pointer so the
// client can omit it to "keep existing"; explicit empty string would clear it.
type adminAuthPutPayload struct {
	Issuer       string                 `json:"issuer"`
	ClientID     string                 `json:"client_id"`
	ClientSecret *string                `json:"client_secret"`
	RedirectURL  string                 `json:"redirect_url"`
	Scopes       []string               `json:"scopes"`
	GroupsClaim  string                 `json:"groups_claim"`
	SessionTTL   string                 `json:"session_ttl"`
	CookieDomain string                 `json:"cookie_domain"`
	CookieSecure bool                   `json:"cookie_secure"`
	Rules        []config.AdminAuthRule `json:"rules"`
}

// handleGetAdminAuth returns the current draft config + enabled/active status.
func (a *API) handleGetAdminAuth(w http.ResponseWriter, _ *http.Request) {
	if a.auth == nil || a.auth.KV() == nil {
		http.Error(w, "admin auth config not available", http.StatusServiceUnavailable)
		return
	}
	st, err := adminauth.LoadState(a.auth.KV())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, viewFromState(st, a.auth.Get()))
}

// handlePutAdminAuth persists a draft config (does NOT toggle enabled).
func (a *API) handlePutAdminAuth(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil || a.auth.KV() == nil {
		http.Error(w, "admin auth config not available", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminAuthPayloadBytes)
	var p adminAuthPutPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := checkPayloadLimits(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing, err := adminauth.LoadState(a.auth.KV())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg, err := buildConfig(&p, existing.Config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.ValidateAdminAuth(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing.Config = cfg
	if err := adminauth.SaveState(a.auth.KV(), existing); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// If currently enabled, reload so the change takes effect immediately.
	if existing.Enabled {
		if err := a.auth.Reload(r.Context(), cfg); err != nil {
			http.Error(w, "saved, but reload failed: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	writeJSON(w, http.StatusOK, viewFromState(existing, a.auth.Get()))
}

// handleTestAdminAuth probes the OIDC issuer for discovery and returns the
// authorization/token endpoints. Does not persist anything.
func (a *API) handleTestAdminAuth(w http.ResponseWriter, r *http.Request) {
	if a.adminProbeLimiter != nil && !a.adminProbeLimiter.allow(r) {
		http.Error(w, "too many discovery probes; try again in a moment", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminAuthPayloadBytes)
	var p adminAuthPutPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := checkPayloadLimits(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	authURL, tokenURL, err := adminauth.ProbeIssuer(ctx, p.Issuer)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"issuer":        p.Issuer,
		"authorize_url": authURL,
		"token_url":     tokenURL,
	})
}

// handleEnableAdminAuth toggles admin auth on, using the saved draft config.
func (a *API) handleEnableAdminAuth(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil || a.auth.KV() == nil {
		http.Error(w, "admin auth config not available", http.StatusServiceUnavailable)
		return
	}
	st, err := adminauth.LoadState(a.auth.KV())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if st.Config == nil {
		http.Error(w, "no admin auth config saved yet", http.StatusBadRequest)
		return
	}
	config.ApplyAdminAuthDefaults(st.Config)
	if err := config.ValidateAdminAuth(st.Config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.auth.Reload(r.Context(), st.Config); err != nil {
		http.Error(w, "enable failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	st.Enabled = true
	if err := adminauth.SaveState(a.auth.KV(), st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, viewFromState(st, a.auth.Get()))
}

// handleDisableAdminAuth toggles admin auth off (back to open mode).
func (a *API) handleDisableAdminAuth(w http.ResponseWriter, _ *http.Request) {
	if a.auth == nil || a.auth.KV() == nil {
		http.Error(w, "admin auth config not available", http.StatusServiceUnavailable)
		return
	}
	st, err := adminauth.LoadState(a.auth.KV())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.auth.Disable()
	st.Enabled = false
	if err := adminauth.SaveState(a.auth.KV(), st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, viewFromState(st, a.auth.Get()))
}

// buildConfig merges the PUT payload onto the existing config (so the
// client can keep the saved secret without re-typing it). Returns the
// merged config without validation — caller validates next.
func buildConfig(p *adminAuthPutPayload, existing *config.AdminAuthConfig) (*config.AdminAuthConfig, error) {
	out := &config.AdminAuthConfig{
		Issuer:       p.Issuer,
		ClientID:     p.ClientID,
		RedirectURL:  p.RedirectURL,
		Scopes:       p.Scopes,
		GroupsClaim:  p.GroupsClaim,
		CookieDomain: p.CookieDomain,
		CookieSecure: p.CookieSecure,
		Rules:        p.Rules,
	}
	switch {
	case p.ClientSecret == nil && existing != nil:
		out.ClientSecret = existing.ClientSecret
	case p.ClientSecret != nil:
		out.ClientSecret = *p.ClientSecret
	}
	if p.SessionTTL != "" {
		d, err := time.ParseDuration(p.SessionTTL)
		if err != nil {
			return nil, errors.New("session_ttl: " + err.Error())
		}
		out.SessionTTL = config.Duration(d)
	}
	return out, nil
}

func viewFromState(st *adminauth.State, active *adminauth.Service) adminAuthView {
	v := adminAuthView{
		Enabled: st.Enabled,
		Active:  active != nil,
	}
	if active != nil {
		v.ActiveDiscovery = active.Issuer()
	}
	if st.Config != nil {
		v.ClientSecretSet = st.Config.ClientSecret != ""
		ttl := ""
		if st.Config.SessionTTL != 0 {
			ttl = time.Duration(st.Config.SessionTTL).String()
		}
		v.Config = &adminAuthConfigView{
			Issuer:       st.Config.Issuer,
			ClientID:     st.Config.ClientID,
			RedirectURL:  st.Config.RedirectURL,
			Scopes:       st.Config.Scopes,
			GroupsClaim:  st.Config.GroupsClaim,
			SessionTTL:   ttl,
			CookieDomain: st.Config.CookieDomain,
			CookieSecure: st.Config.CookieSecure,
			Rules:        st.Config.Rules,
		}
	}
	return v
}

// hasKV is satisfied by *adminauth.Holder. Tiny interface so unit tests of
// config handlers don't need to spin up a Holder when one isn't relevant.
type hasKV interface {
	KV() store.Store
}

var _ hasKV = (*adminauth.Holder)(nil)

// Payload guards for /config/admin-auth — well under what's reasonable, well
// above what any human-written config needs.
const (
	maxAdminAuthPayloadBytes = 32 * 1024
	maxAdminAuthFieldLen     = 1024
	maxAdminAuthScopes       = 64
	maxAdminAuthRules        = 64
	maxAdminAuthMatchersPer  = 256
)

// checkPayloadLimits validates that the incoming admin-auth config payload
// doesn't contain absurdly large fields or array lengths. Keeps a buggy or
// compromised admin from wedging the KV / restart path.
func checkPayloadLimits(p *adminAuthPutPayload) error {
	fields := []string{p.Issuer, p.ClientID, p.RedirectURL, p.GroupsClaim, p.SessionTTL, p.CookieDomain}
	if p.ClientSecret != nil {
		fields = append(fields, *p.ClientSecret)
	}
	for _, f := range fields {
		if len(f) > maxAdminAuthFieldLen {
			return errors.New("admin_auth: field longer than max allowed")
		}
	}
	if err := checkScopeLimits(p.Scopes); err != nil {
		return err
	}
	if len(p.Rules) > maxAdminAuthRules {
		return errors.New("admin_auth: too many rules")
	}
	for i := range p.Rules {
		if err := checkRuleLimits(&p.Rules[i]); err != nil {
			return err
		}
	}
	return nil
}

func checkScopeLimits(scopes []string) error {
	if len(scopes) > maxAdminAuthScopes {
		return errors.New("admin_auth: too many scopes")
	}
	for _, s := range scopes {
		if len(s) > maxAdminAuthFieldLen {
			return errors.New("admin_auth: scope longer than max allowed")
		}
	}
	return nil
}

func checkRuleLimits(rule *config.AdminAuthRule) error {
	if len(rule.Role) > maxAdminAuthFieldLen {
		return errors.New("admin_auth: rule.role too long")
	}
	if len(rule.Emails) > maxAdminAuthMatchersPer ||
		len(rule.Domains) > maxAdminAuthMatchersPer ||
		len(rule.Groups) > maxAdminAuthMatchersPer {
		return errors.New("admin_auth: too many matchers in rule")
	}
	for _, vals := range [][]string{rule.Emails, rule.Domains, rule.Groups} {
		for _, v := range vals {
			if len(v) > maxAdminAuthFieldLen {
				return errors.New("admin_auth: rule matcher value too long")
			}
		}
	}
	return nil
}
