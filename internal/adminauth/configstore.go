package adminauth

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
)

// configKVKey is where the persisted admin auth state lives.
// Versioned in the key so future migrations can swap formats.
const configKVKey = "adminauth/state/v1"

// State is the persisted admin auth record: the draft config (may be empty
// before first save) and whether auth is currently enabled.
type State struct {
	Config  *config.AdminAuthConfig `json:"config,omitempty"`
	Enabled bool                    `json:"enabled"`
}

// LoadState reads admin auth state from the KV store. Returns a zero-value
// State (Config=nil, Enabled=false) when no record exists yet — the run-open
// default for fresh installs.
func LoadState(kv store.Store) (*State, error) {
	if kv == nil {
		return &State{}, nil
	}
	data, err := kv.Get(configKVKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("load admin auth state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse admin auth state: %w", err)
	}
	return &st, nil
}

// SaveState writes the admin auth state to the KV store.
func SaveState(kv store.Store, st *State) error {
	if kv == nil {
		return errors.New("kv store is required to persist admin auth state")
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal admin auth state: %w", err)
	}
	return kv.Set(configKVKey, data)
}
