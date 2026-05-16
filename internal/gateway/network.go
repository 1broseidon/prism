package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/store"
)

// networkKVKey is where the persisted runtime network settings live.
const networkKVKey = "gateway/network/v1"

// LoadNetworkSettings reads the persisted settings, falling back to a zero
// value when no record exists yet.
func LoadNetworkSettings(kv store.Store) (*admin.NetworkSettings, error) {
	if kv == nil {
		return &admin.NetworkSettings{}, nil
	}
	data, err := kv.Get(networkKVKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &admin.NetworkSettings{}, nil
		}
		return nil, fmt.Errorf("load network settings: %w", err)
	}
	var s admin.NetworkSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse network settings: %w", err)
	}
	return &s, nil
}

// SaveNetworkSettings persists settings to KV.
func SaveNetworkSettings(kv store.Store, s *admin.NetworkSettings) error {
	if kv == nil {
		return errors.New("kv store is required to persist network settings")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal network settings: %w", err)
	}
	return kv.Set(networkKVKey, data)
}

// networkRuntime is the gateway-internal runtime view of network settings.
// Held in an atomic pointer so the OAuth callback and Settings PUT can
// coexist without locking on the hot path.
type networkRuntime struct {
	v atomic.Pointer[admin.NetworkSettings]
}

func newNetworkRuntime(initial *admin.NetworkSettings) *networkRuntime {
	r := &networkRuntime{}
	if initial == nil {
		initial = &admin.NetworkSettings{}
	}
	r.v.Store(initial)
	return r
}

func (r *networkRuntime) Get() *admin.NetworkSettings {
	if r == nil {
		return &admin.NetworkSettings{}
	}
	if s := r.v.Load(); s != nil {
		return s
	}
	return &admin.NetworkSettings{}
}

func (r *networkRuntime) Set(s *admin.NetworkSettings) {
	if r == nil {
		return
	}
	if s == nil {
		s = &admin.NetworkSettings{}
	}
	r.v.Store(s)
}

// AdminCallbackURL returns the OAuth redirect_uri to register with providers,
// or "" when no admin URL is configured. The host-aware request fallback
// kicks in inside ProbeBackendAuth when this is empty.
func (r *networkRuntime) AdminCallbackURL() string {
	a := r.Get().AdminPublicURL
	if a == "" {
		return ""
	}
	return strings.TrimRight(a, "/") + "/oauth/callback"
}
