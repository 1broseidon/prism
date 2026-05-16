package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ValidateNetworkSettings rejects malformed URLs before they reach KV.
func ValidateNetworkSettings(s *NetworkSettings) error {
	if s == nil {
		return errors.New("settings are nil")
	}
	for label, raw := range map[string]string{
		"public_url":       s.PublicURL,
		"admin_public_url": s.AdminPublicURL,
	} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%s: scheme must be http or https", label)
		}
		if u.Host == "" {
			return fmt.Errorf("%s: host is required", label)
		}
	}
	return nil
}

// NetworkSettings are the runtime network knobs editable from the Settings
// page. They override file config at startup and can change at runtime.
//
// Defined in admin to avoid an admin↔gateway import cycle; the gateway
// stores and applies these values via NetworkSettingsManager.
type NetworkSettings struct {
	// PublicURL is the externally-reachable base URL for the MCP gateway.
	PublicURL string `json:"public_url,omitempty"`
	// AdminPublicURL is the externally-reachable base URL for the admin API.
	// Pins the OAuth redirect_uri — required for providers that reject
	// http+non-localhost or that need a stable callback URL.
	AdminPublicURL string `json:"admin_public_url,omitempty"`
	// TrustProxyHeaders honors X-Forwarded-Proto / X-Forwarded-Host on
	// inbound admin requests when deriving OAuth callbacks. Enable when
	// prism sits behind a reverse proxy (Caddy, nginx, Cloudflare).
	TrustProxyHeaders bool `json:"trust_proxy_headers,omitempty"`
}

// NetworkSettingsManager is implemented by backend managers that own the
// runtime network settings record. Used by /config/network to read and
// atomically apply changes.
type NetworkSettingsManager interface {
	NetworkSettings() *NetworkSettings
	SetNetworkSettings(*NetworkSettings)
	// PersistNetworkSettings writes the settings to KV; the admin handler
	// calls this after a successful in-memory swap so the change survives
	// restarts.
	PersistNetworkSettings(*NetworkSettings) error
}

func (a *API) handleGetNetwork(w http.ResponseWriter, _ *http.Request) {
	mgr, ok := a.backendMgr.(NetworkSettingsManager)
	if !ok {
		http.Error(w, "network settings not available", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, mgr.NetworkSettings())
}

func (a *API) handlePutNetwork(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.backendMgr.(NetworkSettingsManager)
	if !ok {
		http.Error(w, "network settings not available", http.StatusServiceUnavailable)
		return
	}
	var next NetworkSettings
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := ValidateNetworkSettings(&next); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := mgr.PersistNetworkSettings(&next); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mgr.SetNetworkSettings(&next)
	writeJSON(w, http.StatusOK, &next)
}
