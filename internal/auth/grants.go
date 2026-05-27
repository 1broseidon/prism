package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	GrantTypeMCPCall = "prism.mcp.call"

	GrantDenyNone           = ""
	GrantDenyNoGrant        = "no_grant"
	GrantDenyArgs           = "args"
	GrantDenyNotYet         = "not_yet"
	GrantDenyExpired        = "expired"
	GrantDenyOutOfWindow    = "out_of_window"
	GrantDenyNeedsStepUp    = "needs_step_up"
	GrantDenyACRRequired    = "acr_required"
	GrantDenyWorkspaceDrift = "workspace_drift"
	GrantDenyToolDisabled   = "tool_disabled"
)

// GrantTemplate is an immutable per-version capability definition.
type GrantTemplate struct {
	ID         string    `json:"id"`
	Version    int       `json:"version"`
	Hash       string    `json:"hash"`
	Supersedes string    `json:"supersedes,omitempty"`
	Spec       GrantSpec `json:"spec"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by,omitempty"`
}

// GrantSpec is the operator-authored grant shape. It is resolved into an
// IssuedGrant during authorization.
type GrantSpec struct {
	Type    string               `json:"type"`
	Tool    string               `json:"tool"`
	Backend string               `json:"backend"`
	Args    map[string]Predicate `json:"args,omitempty"`

	Workspace WorkspaceConstraint `json:"workspace,omitempty"`

	Hours            string `json:"hours,omitempty"`
	NotBefore        int64  `json:"not_before,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	AuthFreshnessMax int64  `json:"auth_freshness_max,omitempty"`
	CnfRequired      bool   `json:"cnf_required,omitempty"`
	AcrRequired      string `json:"acr_required,omitempty"`
}

// WorkspaceConstraint constrains the live workspace resolved for a call.
// Predicates are optional and AND-composed across set fields.
type WorkspaceConstraint struct {
	ID        *Predicate `json:"id,omitempty"`
	Type      *Predicate `json:"type,omitempty"`
	WriteMode *Predicate `json:"write_mode,omitempty"`
}

// WorkspaceInstance is the concrete workspace tuple pinned into an issued grant.
type WorkspaceInstance struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	WriteMode string `json:"write_mode,omitempty"`
}

// GrantBinding links subjects to a pinned template hash.
type GrantBinding struct {
	ID           string          `json:"id"`
	TemplateID   string          `json:"template_id"`
	TemplateHash string          `json:"template_hash"`
	Subjects     SubjectSelector `json:"subjects"`
	Conditions   []EnvCondition  `json:"conditions,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	CreatedBy    string          `json:"created_by,omitempty"`
}

// SubjectSelector controls who can instantiate a template. Groups, Roles, and
// AgentIDs are OR-combined; RoleRequired is AND-combined on top.
type SubjectSelector struct {
	Groups       []string `json:"groups,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	AgentIDs     []string `json:"agent_ids,omitempty"`
	RoleRequired string   `json:"role_required,omitempty"`
}

// SubjectIdentity is the subject shape used by grant binding lookups.
type SubjectIdentity struct {
	AgentID  string
	ClientID string
	Groups   []string
	Roles    []string
}

// EnvCondition is reserved for v1 storage compatibility. Enforcement is not
// wired until the spec defines concrete condition types.
type EnvCondition struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

// IssuedGrant is the resolved RAR object carried in access tokens.
type IssuedGrant struct {
	Type         string   `json:"type"`
	TemplateID   string   `json:"template_id"`
	TemplateHash string   `json:"template_hash"`
	Actions      []string `json:"actions,omitempty"`

	Tool    string               `json:"tool"`
	Backend string               `json:"backend"`
	Args    map[string]Predicate `json:"args,omitempty"`

	Workspace *WorkspaceInstance `json:"workspace,omitempty"`

	Hours            string `json:"hours,omitempty"`
	NotBefore        int64  `json:"not_before,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	AuthFreshnessMax int64  `json:"auth_freshness_max,omitempty"`
	CnfRequired      bool   `json:"cnf_required,omitempty"`
	AcrRequired      string `json:"acr_required,omitempty"`
}

// CanonicalGrantHash returns the stable version hash for a GrantSpec.
//
// The encoder uses canonicalJSONEncode rather than json.Marshal to ensure
// the byte sequence depends only on the field values and the field-name
// alphabet — not on the struct field declaration order. That guarantee lets
// us refactor GrantSpec safely; existing template hashes remain valid as
// long as the JSON tag names and the value semantics are unchanged.
func CanonicalGrantHash(spec GrantSpec) (string, error) {
	if spec.Type == "" {
		spec.Type = GrantTypeMCPCall
	}
	data, err := canonicalJSONEncode(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}

// ComputeTemplateHash is the task-level public name for CanonicalGrantHash.
func ComputeTemplateHash(spec GrantSpec) (string, error) {
	return CanonicalGrantHash(spec)
}

// canonicalJSONEncode marshals v with object keys sorted lexicographically at
// every level. Arrays preserve element order. The output is a stable byte
// sequence safe to hash.
func canonicalJSONEncode(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var decoded any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	return marshalCanonical(decoded)
}

func marshalCanonical(v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		// Lexicographic sort is the canonical order.
		slices.Sort(keys)
		var buf strings.Builder
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			vb, err := marshalCanonical(x[k])
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return []byte(buf.String()), nil
	case []any:
		var buf strings.Builder
		buf.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			vb, err := marshalCanonical(el)
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte(']')
		return []byte(buf.String()), nil
	default:
		return json.Marshal(v)
	}
}

// Validate checks a GrantSpec for v1-compatible fields.
func (s GrantSpec) Validate() error {
	if s.Type == "" {
		s.Type = GrantTypeMCPCall
	}
	if s.Type != GrantTypeMCPCall {
		return fmt.Errorf("unsupported grant type %q", s.Type)
	}
	if strings.TrimSpace(s.Tool) == "" {
		return errors.New("grant tool is required")
	}
	if strings.TrimSpace(s.Backend) == "" {
		return errors.New("grant backend is required")
	}
	for field, pred := range s.Args {
		if err := pred.Validate(); err != nil {
			return fmt.Errorf("args.%s: %w", field, err)
		}
	}
	if err := s.Workspace.Validate(); err != nil {
		return err
	}
	if s.AuthFreshnessMax < 0 {
		return errors.New("auth_freshness_max must be >= 0")
	}
	if s.NotBefore != 0 && s.ExpiresAt != 0 && s.NotBefore > s.ExpiresAt {
		return errors.New("not_before must be <= expires_at")
	}
	if s.Hours != "" {
		if _, err := parseHoursWindow(s.Hours); err != nil {
			return fmt.Errorf("hours: %w", err)
		}
	}
	return nil
}

// Validate checks a workspace constraint.
func (w WorkspaceConstraint) Validate() error {
	if w.ID != nil {
		if err := w.ID.Validate(); err != nil {
			return fmt.Errorf("workspace.id: %w", err)
		}
	}
	if w.Type != nil {
		if err := w.Type.Validate(); err != nil {
			return fmt.Errorf("workspace.type: %w", err)
		}
	}
	if w.WriteMode != nil {
		if err := w.WriteMode.Validate(); err != nil {
			return fmt.Errorf("workspace.write_mode: %w", err)
		}
	}
	return nil
}

// Matches reports whether the selector covers the subject.
func (s SubjectSelector) Matches(agentID string, groups, roles []string) bool {
	eligible := false
	for _, want := range s.AgentIDs {
		if want != "" && want == agentID {
			eligible = true
			break
		}
	}
	if !eligible && intersects(s.Groups, groups) {
		eligible = true
	}
	if !eligible && intersects(s.Roles, roles) {
		eligible = true
	}
	if !eligible {
		return false
	}
	if s.RoleRequired != "" && !slices.Contains(roles, s.RoleRequired) {
		return false
	}
	return true
}

// GrantToolName returns Prism's v1 capability tool identifier.
func GrantToolName(namespace, tool string) string {
	if namespace == "" {
		return tool
	}
	return namespace + "." + tool
}

// IssueGrantFromTemplate resolves a template into a token grant. The caller is
// responsible for checking subject binding eligibility before calling this.
func IssueGrantFromTemplate(t GrantTemplate, vars map[string]string, workspace *WorkspaceInstance) (IssuedGrant, error) {
	spec, err := SubstituteGrantSpec(t.Spec, vars)
	if err != nil {
		return IssuedGrant{}, err
	}
	if err := spec.Validate(); err != nil {
		return IssuedGrant{}, err
	}
	if workspace != nil && !spec.Workspace.Match(*workspace) {
		return IssuedGrant{}, errors.New("workspace does not satisfy grant template")
	}
	hash := t.Hash
	if hash == "" {
		hash, err = CanonicalGrantHash(spec)
		if err != nil {
			return IssuedGrant{}, err
		}
	}
	return IssuedGrant{
		Type:             GrantTypeMCPCall,
		TemplateID:       t.ID,
		TemplateHash:     hash,
		Actions:          []string{"tools/call"},
		Tool:             spec.Tool,
		Backend:          spec.Backend,
		Args:             spec.Args,
		Workspace:        workspace,
		Hours:            spec.Hours,
		NotBefore:        spec.NotBefore,
		ExpiresAt:        spec.ExpiresAt,
		AuthFreshnessMax: spec.AuthFreshnessMax,
		CnfRequired:      spec.CnfRequired,
		AcrRequired:      spec.AcrRequired,
	}, nil
}

// SubstituteGrantSpec performs single-pass ${var} substitution across string
// fields in a grant spec.
func SubstituteGrantSpec(spec GrantSpec, vars map[string]string) (GrantSpec, error) {
	if err := rejectUnknownSubstitutionVars(spec, vars); err != nil {
		return GrantSpec{}, err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return GrantSpec{}, err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return GrantSpec{}, err
	}
	v = substituteStrings(v, vars)
	data, err = json.Marshal(v)
	if err != nil {
		return GrantSpec{}, err
	}
	var out GrantSpec
	if err := json.Unmarshal(data, &out); err != nil {
		return GrantSpec{}, err
	}
	return out, nil
}

// SubVars contains the v1 template-substitution values.
type SubVars struct {
	AgentPrismID  string
	AgentClientID string
}

// SubstituteVars applies v1 substitution variables in a single pass.
func SubstituteVars(spec GrantSpec, ctx SubVars) (GrantSpec, error) {
	return SubstituteGrantSpec(spec, map[string]string{
		"agent.prism_id":  ctx.AgentPrismID,
		"agent.client_id": ctx.AgentClientID,
	})
}

// Match checks a concrete workspace against the constraint.
func (w WorkspaceConstraint) Match(inst WorkspaceInstance) bool {
	if w.ID != nil && !w.ID.Match(inst.ID) {
		return false
	}
	if w.Type != nil && !w.Type.Match(inst.Type) {
		return false
	}
	if w.WriteMode != nil && !w.WriteMode.Match(inst.WriteMode) {
		return false
	}
	return true
}

func rejectUnknownSubstitutionVars(spec GrantSpec, vars map[string]string) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	matches := regexp.MustCompile(`\$\{([^}]+)\}`).FindAllSubmatch(data, -1)
	for _, m := range matches {
		name := string(m[1])
		if _, ok := vars[name]; !ok {
			return fmt.Errorf("unknown substitution variable %q", name)
		}
	}
	return nil
}

func jsonValueEqual(a, b any) bool {
	if af, ok := jsonNumber(a); ok {
		if bf, ok := jsonNumber(b); ok {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

func jsonNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
