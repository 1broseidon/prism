package authserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

const (
	grantTemplateKeyPrefix     = "grant_templates/"
	grantTemplateHashKeyPrefix = "grant_template_hashes/"
	grantBindingKeyPrefix      = "grant_bindings/"
	grantBindingIndexPrefix    = "grant_bindings_by_template/"
	grantTokenRefKeyPrefix     = "grant_token_refs/"
	grantTokenRefByHashPrefix  = "grant_token_refs_by_hash/"
)

var grantIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type grantHashRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type grantTokenRef struct {
	JTI            string    `json:"jti"`
	TemplateHashes []string  `json:"template_hashes"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// SaveGrantTemplate validates a spec and writes a new immutable template
// version. Editing a template is represented by calling this again with the
// same ID and a changed Spec.
func (s *Server) SaveGrantTemplate(t auth.GrantTemplate) (auth.GrantTemplate, error) {
	if s.store == nil {
		return auth.GrantTemplate{}, errors.New("no KV store configured")
	}
	t.ID = strings.TrimSpace(t.ID)
	if !grantIDRE.MatchString(t.ID) {
		return auth.GrantTemplate{}, errors.New("template id must be 1-128 chars of [A-Za-z0-9_.-]")
	}
	if t.Spec.Type == "" {
		t.Spec.Type = auth.GrantTypeMCPCall
	}
	if err := t.Spec.Validate(); err != nil {
		return auth.GrantTemplate{}, err
	}
	hash, err := auth.CanonicalGrantHash(t.Spec)
	if err != nil {
		return auth.GrantTemplate{}, err
	}
	latest := s.latestGrantTemplateVersion(t.ID)
	t.Version = latest + 1
	t.Hash = hash
	if latestTemplate, err := s.GetGrantTemplate(t.ID, latest); err == nil {
		t.Supersedes = latestTemplate.Hash
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}

	data, err := json.Marshal(t)
	if err != nil {
		return auth.GrantTemplate{}, err
	}
	if err := s.store.Set(grantTemplateKey(t.ID, t.Version), data); err != nil {
		return auth.GrantTemplate{}, err
	}
	ref, _ := json.Marshal(grantHashRef{ID: t.ID, Version: t.Version})
	if err := s.store.Set(grantTemplateHashKeyPrefix+t.Hash, ref); err != nil {
		return auth.GrantTemplate{}, err
	}
	return t, nil
}

// ListGrantTemplates returns every stored template version.
func (s *Server) ListGrantTemplates() []auth.GrantTemplate {
	if s.store == nil {
		return nil
	}
	keys, err := s.store.List(grantTemplateKeyPrefix)
	if err != nil {
		return nil
	}
	out := make([]auth.GrantTemplate, 0, len(keys))
	for _, key := range keys {
		data, err := s.store.Get(key)
		if err != nil {
			continue
		}
		var t auth.GrantTemplate
		if json.Unmarshal(data, &t) == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// GetGrantTemplate returns a template by ID/version. Version 0 means latest.
func (s *Server) GetGrantTemplate(id string, version int) (auth.GrantTemplate, error) {
	if s.store == nil {
		return auth.GrantTemplate{}, errors.New("no KV store configured")
	}
	if version == 0 {
		version = s.latestGrantTemplateVersion(id)
	}
	if version <= 0 {
		return auth.GrantTemplate{}, errors.New("template not found")
	}
	data, err := s.store.Get(grantTemplateKey(id, version))
	if err != nil {
		return auth.GrantTemplate{}, err
	}
	var t auth.GrantTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return auth.GrantTemplate{}, err
	}
	return t, nil
}

// GetGrantTemplateByHash returns the frozen template version referenced by a token.
func (s *Server) GetGrantTemplateByHash(hash string) (auth.GrantTemplate, error) {
	if s.store == nil {
		return auth.GrantTemplate{}, errors.New("no KV store configured")
	}
	data, err := s.store.Get(grantTemplateHashKeyPrefix + hash)
	if err != nil {
		return auth.GrantTemplate{}, err
	}
	var ref grantHashRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return auth.GrantTemplate{}, err
	}
	return s.GetGrantTemplate(ref.ID, ref.Version)
}

// DeleteGrantTemplate deletes a stored template version if no binding points at it.
func (s *Server) DeleteGrantTemplate(id string, version int) error {
	if s.store == nil {
		return errors.New("no KV store configured")
	}
	t, err := s.GetGrantTemplate(id, version)
	if err != nil {
		return err
	}
	for _, b := range s.ListGrantBindings() {
		if b.TemplateHash == t.Hash {
			return errors.New("template version is still referenced by a binding")
		}
	}
	if s.ActiveGrantTokenCount(t.Hash) > 0 {
		return errors.New("template version is still referenced by active tokens")
	}
	if err := s.store.Delete(grantTemplateKey(t.ID, t.Version)); err != nil {
		return err
	}
	return s.store.Delete(grantTemplateHashKeyPrefix + t.Hash)
}

// SetGrantBinding creates or replaces a binding to an existing template hash.
func (s *Server) SetGrantBinding(b auth.GrantBinding) (auth.GrantBinding, error) {
	if s.store == nil {
		return auth.GrantBinding{}, errors.New("no KV store configured")
	}
	b.ID = strings.TrimSpace(b.ID)
	if !grantIDRE.MatchString(b.ID) {
		return auth.GrantBinding{}, errors.New("binding id must be 1-128 chars of [A-Za-z0-9_.-]")
	}
	if b.TemplateHash == "" {
		return auth.GrantBinding{}, errors.New("template_hash is required")
	}
	t, err := s.GetGrantTemplateByHash(b.TemplateHash)
	if err != nil {
		return auth.GrantBinding{}, fmt.Errorf("template_hash not found: %w", err)
	}
	if b.TemplateID == "" {
		b.TemplateID = t.ID
	}
	if b.TemplateID != t.ID {
		return auth.GrantBinding{}, errors.New("template_id does not match template_hash")
	}
	if !selectorConfigured(b.Subjects) {
		return auth.GrantBinding{}, errors.New("subjects selector must not be empty")
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(b)
	if err != nil {
		return auth.GrantBinding{}, err
	}
	if err := s.store.Set(grantBindingKeyPrefix+b.ID, data); err != nil {
		return auth.GrantBinding{}, err
	}
	_ = s.store.Set(grantBindingIndexKey(b.TemplateID, b.ID), []byte(b.ID))
	return b, nil
}

// GetGrantBinding returns a binding by ID.
func (s *Server) GetGrantBinding(id string) (auth.GrantBinding, error) {
	if s.store == nil {
		return auth.GrantBinding{}, errors.New("no KV store configured")
	}
	data, err := s.store.Get(grantBindingKeyPrefix + id)
	if err != nil {
		return auth.GrantBinding{}, err
	}
	var b auth.GrantBinding
	if err := json.Unmarshal(data, &b); err != nil {
		return auth.GrantBinding{}, err
	}
	return b, nil
}

// ListGrantBindings returns every stored binding.
func (s *Server) ListGrantBindings() []auth.GrantBinding {
	if s.store == nil {
		return nil
	}
	keys, err := s.store.List(grantBindingKeyPrefix)
	if err != nil {
		return nil
	}
	out := make([]auth.GrantBinding, 0, len(keys))
	for _, key := range keys {
		data, err := s.store.Get(key)
		if err != nil {
			continue
		}
		var b auth.GrantBinding
		if json.Unmarshal(data, &b) == nil {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListGrantBindingsForSubject returns bindings that match the authenticated subject.
func (s *Server) ListGrantBindingsForSubject(agentID string, groups, roles []string) []auth.GrantBinding {
	all := s.ListGrantBindings()
	out := make([]auth.GrantBinding, 0, len(all))
	for _, b := range all {
		if b.Subjects.Matches(agentID, groups, roles) {
			out = append(out, b)
		}
	}
	return out
}

// DeleteGrantBinding removes one binding and its template index.
func (s *Server) DeleteGrantBinding(id string) error {
	if s.store == nil {
		return errors.New("no KV store configured")
	}
	b, err := s.GetGrantBinding(id)
	if err == nil {
		_ = s.store.Delete(grantBindingIndexKey(b.TemplateID, b.ID))
	}
	return s.store.Delete(grantBindingKeyPrefix + id)
}

func (s *Server) latestGrantTemplateVersion(id string) int {
	if s.store == nil || id == "" {
		return 0
	}
	keys, err := s.store.List(grantTemplateKeyPrefix + id + "/")
	if err != nil {
		return 0
	}
	latest := 0
	for _, key := range keys {
		raw := strings.TrimPrefix(key, grantTemplateKeyPrefix+id+"/")
		v, err := strconv.Atoi(raw)
		if err == nil && v > latest {
			latest = v
		}
	}
	return latest
}

func grantTemplateKey(id string, version int) string {
	return grantTemplateKeyPrefix + id + "/" + strconv.Itoa(version)
}

func grantBindingIndexKey(templateID, bindingID string) string {
	return grantBindingIndexPrefix + templateID + "/" + bindingID
}

func grantTokenRefKey(jti string) string {
	return grantTokenRefKeyPrefix + jti
}

func grantTokenRefByHashKey(hash, jti string) string {
	return grantTokenRefByHashPrefix + hash + "/" + jti
}

func (s *Server) trackGrantTokenRefs(jti string, grants []auth.IssuedGrant, expiresAt time.Time) {
	if s == nil || s.store == nil || jti == "" || len(grants) == 0 {
		return
	}
	seen := map[string]struct{}{}
	hashes := make([]string, 0, len(grants))
	for _, grant := range grants {
		hash := strings.TrimSpace(grant.TemplateHash)
		if hash == "" {
			continue
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	if len(hashes) == 0 {
		return
	}
	sort.Strings(hashes)
	ref := grantTokenRef{JTI: jti, TemplateHashes: hashes, ExpiresAt: expiresAt.UTC()}
	data, err := json.Marshal(ref)
	if err != nil {
		s.logger.Warn("failed to marshal grant token ref", "jti", jti, "error", err)
		return
	}
	if err := s.store.Set(grantTokenRefKey(jti), data); err != nil {
		s.logger.Warn("failed to persist grant token ref", "jti", jti, "error", err)
		return
	}
	for _, hash := range hashes {
		_ = s.store.Set(grantTokenRefByHashKey(hash, jti), []byte(ref.ExpiresAt.Format(time.RFC3339Nano)))
	}
}

// ActiveGrantTokenCount returns the number of unexpired access-token JTIs
// currently indexed for a template hash.
func (s *Server) ActiveGrantTokenCount(templateHash string) int {
	if s == nil || s.store == nil || strings.TrimSpace(templateHash) == "" {
		return 0
	}
	now := s.now()
	keys, err := s.store.List(grantTokenRefByHashPrefix + templateHash + "/")
	if err != nil {
		return 0
	}
	count := 0
	for _, key := range keys {
		jti := strings.TrimPrefix(key, grantTokenRefByHashPrefix+templateHash+"/")
		if s.grantTokenRefIsActive(jti, now) {
			count++
		} else {
			_ = s.store.Delete(key)
		}
	}
	return count
}

func (s *Server) grantTokenRefIsActive(jti string, now time.Time) bool {
	data, err := s.store.Get(grantTokenRefKey(jti))
	if err != nil {
		return false
	}
	var ref grantTokenRef
	if err := json.Unmarshal(data, &ref); err != nil {
		_ = s.store.Delete(grantTokenRefKey(jti))
		return false
	}
	if !ref.ExpiresAt.IsZero() && !now.Before(ref.ExpiresAt) {
		s.deleteGrantTokenRef(ref)
		return false
	}
	return true
}

func (s *Server) deleteGrantTokenRef(ref grantTokenRef) {
	if ref.JTI != "" {
		_ = s.store.Delete(grantTokenRefKey(ref.JTI))
	}
	for _, hash := range ref.TemplateHashes {
		if hash != "" && ref.JTI != "" {
			_ = s.store.Delete(grantTokenRefByHashKey(hash, ref.JTI))
		}
	}
}

func selectorConfigured(s auth.SubjectSelector) bool {
	return len(s.Groups) > 0 || len(s.Roles) > 0 || len(s.AgentIDs) > 0
}
