package admin

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
)

// Subject types supported by /admin/policy.
const (
	subjectTypeGroups = "groups"
	subjectTypeRoles  = "roles"
	subjectTypeAgents = "agents"
)

// Capability source labels surfaced on CapabilityView.Source.
const (
	capabilitySourceScope = "scope"
	capabilitySourceGrant = "grant"
)

// Capability effect labels surfaced on CapabilityView.Effect (task-46).
// Default to allow; deny rows come from AgentPolicy.Deny scopes (and, in a
// future iteration, group/role deny shapes when those land).
const (
	capabilityEffectAllow = "allow"
	capabilityEffectDeny  = "deny"
)

// scopeDenyIDPrefix marks capability ids whose underlying storage is the
// subject's Deny list instead of the Grant list. The DELETE handler routes
// on the prefix so the wire surface stays a single endpoint (same path,
// different storage target).
const scopeDenyIDPrefix = "scope-deny-"

// MaxCapabilitiesPerSubject is the v1 cap from spec §15. The list endpoint
// truncates when the joined scope+binding count would exceed this, which is
// the only place capability count can be observed from the wire. Operators
// hitting the cap are expected to consolidate via verbs or Power Tools.
const MaxCapabilitiesPerSubject = 200

// PolicyAgentReader is the optional capability interface admin uses to read
// an agent's stored policy directly (scope strings + groups). The production
// authserver.Server implements it; tests can supply a mock. When unset, agent
// subject reads degrade to bindings-only.
type PolicyAgentReader interface {
	GetAgentPolicy(prismID string) (*AgentPolicy, error)
}

// CapabilitySpec is the operator-authored capability sentence. The UI POSTs
// this; the compile-down router picks the storage shape (see spec §7.1).
//
// Only the Action field is required. Each optional sub-field carries its
// own "no constraint" sentinel:
//   - Where: nil or Where.Mode == "anywhere"
//   - When: nil or When.Preset == "anytime"
//   - HowSecure: nil or HowSecure.Mode == "token"
//   - Advanced: nil
type CapabilitySpec struct {
	Action    ActionSpec     `json:"action"`
	Where     *WhereSpec     `json:"where,omitempty"`
	When      *WhenSpec      `json:"when,omitempty"`
	HowSecure *HowSecureSpec `json:"how_secure,omitempty"`
	Advanced  *AdvancedSpec  `json:"advanced,omitempty"`
}

// ActionSpec is the required "what can they do?" field. Exactly one of the
// three modes is populated.
type ActionSpec struct {
	// Mode discriminates: "verb" | "tool" | "backend_wildcard".
	Mode string `json:"mode"`
	// VerbSlug is set when Mode == "verb"; resolved at save time via
	// ResolveVerb against the currently-enabled backends.
	VerbSlug string `json:"verb_slug,omitempty"`
	// Backend + Tool are set when Mode == "tool" (specific tool).
	Backend string `json:"backend,omitempty"`
	Tool    string `json:"tool,omitempty"`
}

// WhereSpec narrows the request to a path prefix or a workspace category.
// Mode discriminates the storage compilation:
//   - "anywhere":     no constraint added
//   - "path_prefix":  args.path.prefix predicate (PathPrefix supplies the value)
//   - "agent_home":   args.path.prefix = "/workspace/${agent.prism_id}/"
//   - "shared":       workspace.type.equals = "virtual"
//   - "ephemeral":    workspace.type.equals = "ephemeral"
type WhereSpec struct {
	Mode       string `json:"mode"`
	PathPrefix string `json:"path_prefix,omitempty"`
}

// WhenSpec narrows the request to a time window.
//
//   - "anytime":  no constraint
//   - "business": Mon-Fri 09:00-18:00 in Timezone
//   - "window":   Hours grammar (forwarded verbatim to GrantSpec.Hours)
type WhenSpec struct {
	Mode     string `json:"mode"`
	Hours    string `json:"hours,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// HowSecureSpec narrows the request to a stronger auth posture.
//
//   - "token":       no constraint
//   - "mfa":         acr_required = "urn:prism:mfa" and auth_freshness_max = MfaFreshnessSec
//   - "mfa_dpop":    above + cnf_required = true
type HowSecureSpec struct {
	Mode            string `json:"mode"`
	MfaFreshnessSec int64  `json:"mfa_freshness_sec,omitempty"`
	AcrOverride     string `json:"acr_override,omitempty"`
	RequireDPoP     bool   `json:"require_dpop,omitempty"`
}

// AdvancedSpec exposes the full DSL editor surface inside the modal's
// "Show advanced fields" disclosure. None of these fields are required;
// they pass through verbatim to the GrantSpec. The full DSL editor still
// lives in /grants/templates (Power Tools); this is the inline surface.
type AdvancedSpec struct {
	Args         map[string]auth.Predicate `json:"args,omitempty"`
	Workspace    *auth.WorkspaceConstraint `json:"workspace,omitempty"`
	AcrRequired  string                    `json:"acr_required,omitempty"`
	RoleRequired string                    `json:"role_required,omitempty"`
}

// Chip is a pre-tokenized {kind, label, value} tuple the UI renders.
// Server-rendered to avoid per-row re-computation in the UI; ordering matches
// spec §5.2 (where → storage → time → freshness → auth manner).
type Chip struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

// CapabilityView is the read shape returned to the UI. See spec §10.1.
//
// InheritedFrom is populated only on agent subject reads. Each entry names a
// source (group, role, or "direct") that contributes the binding to the agent.
// A capability inherited via multiple group/role memberships gets one entry
// per source; the same capability appears only once in the listing (deduped
// by ID).
type CapabilityView struct {
	ID             string         `json:"id"`
	Source         string         `json:"source"`
	Spec           CapabilitySpec `json:"spec"`
	DisplaySummary string         `json:"display_summary"`
	Chips          []Chip         `json:"chips,omitempty"`
	SharedWith     []string       `json:"shared_with,omitempty"`
	// Effect is "allow" (default) or "deny" — populated for every row from
	// task-46 onward so the frontend can split the capability list into
	// ALLOWED / DENIED sections without re-deriving from the underlying
	// scope/binding shape. Bindings are always Effect="allow" today
	// (deny-shape bindings aren't a v1 primitive); scope rows derived from
	// AgentPolicy.Deny carry Effect="deny" and Source="scope".
	Effect        string              `json:"effect,omitempty"`
	InheritedFrom []InheritanceSource `json:"inherited_from,omitempty"`
}

// InheritanceSource names one upstream subject that contributes a binding to
// an agent. Type is one of {"group", "role", "direct"}; Name is the upstream
// subject's identifier ("" for direct grants).
type InheritanceSource struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// hasConstraints reports whether any constraint chip is set. This is the
// gate from spec §7.1: no constraints → scope; any constraint → grant.
func (c CapabilitySpec) hasConstraints() bool {
	if c.Where != nil && c.Where.Mode != "" && c.Where.Mode != "anywhere" {
		return true
	}
	if c.When != nil && c.When.Mode != "" && c.When.Mode != "anytime" {
		return true
	}
	if c.HowSecure != nil && c.HowSecure.Mode != "" && c.HowSecure.Mode != "token" {
		return true
	}
	if c.Advanced != nil {
		if len(c.Advanced.Args) > 0 {
			return true
		}
		if c.Advanced.Workspace != nil {
			ws := c.Advanced.Workspace
			if ws.ID != nil || ws.Type != nil || ws.WriteMode != nil {
				return true
			}
		}
		if c.Advanced.AcrRequired != "" || c.Advanced.RoleRequired != "" {
			return true
		}
	}
	return false
}

// registerPolicyRoutes wires the /policy/* endpoints. Called from
// registerAPIRoutes. Mutation requires admin auth; reads require session.
func (a *API) registerPolicyRoutes(mux *http.ServeMux) {
	mux.Handle("GET /policy/verbs", a.session(http.HandlerFunc(a.handleListVerbs)))
	mux.Handle("GET /policy/verbs/", a.session(http.HandlerFunc(a.handleResolveVerb)))
	mux.Handle("GET /policy/subjects/", a.session(a.policySubjectIdentityCompat(http.HandlerFunc(a.handlePolicySubjectsGet))))
	mux.Handle("POST /policy/subjects/", a.admin(a.policySubjectIdentityCompat(http.HandlerFunc(a.handlePolicySubjectsPost))))
	mux.Handle("PUT /policy/subjects/", a.admin(a.policySubjectIdentityCompat(http.HandlerFunc(a.handlePolicySubjectsPut))))
	mux.Handle("DELETE /policy/subjects/", a.admin(a.policySubjectIdentityCompat(http.HandlerFunc(a.handlePolicySubjectsDelete))))
	mux.Handle("GET /policy/health", a.session(http.HandlerFunc(a.handlePolicyHealth)))
	mux.Handle("GET /policy/access", a.session(http.HandlerFunc(a.handlePolicyAccess)))
}

// handleListVerbs handles GET /policy/verbs.
func (a *API) handleListVerbs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Verbs)
}

// handleResolveVerb handles GET /policy/verbs/{slug}/resolve?enabled_backends=fs,github.
func (a *API) handleResolveVerb(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/policy/verbs/")
	if !strings.HasSuffix(path, "/resolve") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	slug := strings.TrimSuffix(path, "/resolve")
	if !isValidID(slug) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid verb slug"})
		return
	}
	backends := parseEnabledBackends(r.URL.Query().Get("enabled_backends"))
	pairs, err := ResolveVerb(slug, backends)
	if err != nil {
		if errors.Is(err, ErrUnknownVerb) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown verb"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, pairs)
}

func parseEnabledBackends(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parsePolicySubjectsPath extracts (type, id, capabilityID) from the path.
// capabilityID is "" for the collection routes.
func parsePolicySubjectsPath(path string) (subjectType, subjectID, capID string, ok bool) {
	rest := strings.TrimPrefix(path, "/policy/subjects/")
	if rest == path {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	// Expected shapes:
	//   {type}/{id}/capabilities
	//   {type}/{id}/capabilities/{cap_id}
	if len(parts) < 3 || parts[2] != "capabilities" {
		return "", "", "", false
	}
	subjectType = parts[0]
	subjectID = parts[1]
	if !isValidSubjectType(subjectType) || !isValidID(subjectID) {
		return "", "", "", false
	}
	if len(parts) == 3 {
		return subjectType, subjectID, "", true
	}
	if len(parts) == 4 {
		return subjectType, subjectID, parts[3], true
	}
	return "", "", "", false
}

// resolveSubjectName maps a ULID subject id back to the dispatcher-tracked
// display name so downstream code (groupMgr, agentMgr) — which is still
// name-keyed in storage — can find the record. Agents are excluded: their
// prism_ids are opaque already and bypass the dispatcher.
func (a *API) resolveSubjectName(subjectType, subjectID string) string {
	if a.identity == nil || subjectID == "" {
		return subjectID
	}
	if !identity.IsULID(subjectID) {
		return subjectID
	}
	var kind identity.Kind
	switch subjectType {
	case subjectTypeGroups:
		kind = identity.KindGroup
	case subjectTypeRoles:
		kind = identity.KindRole
	default:
		return subjectID
	}
	ent, err := a.identity.Resolve(subjectID)
	if err != nil || ent.Kind != kind {
		return subjectID
	}
	return ent.DisplayName
}

func isValidSubjectType(t string) bool {
	switch t {
	case subjectTypeGroups, subjectTypeRoles, subjectTypeAgents:
		return true
	}
	return false
}

func (a *API) handlePolicySubjectsGet(w http.ResponseWriter, r *http.Request) {
	subjectType, subjectID, capID, ok := parsePolicySubjectsPath(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid policy subject path"})
		return
	}
	subjectID = a.resolveSubjectName(subjectType, subjectID)
	if capID != "" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET on single capability is not supported"})
		return
	}
	views, err := a.composeCapabilityViews(subjectType, subjectID)
	if err != nil {
		writeJSON(w, policyErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	if len(views) > MaxCapabilitiesPerSubject {
		views = views[:MaxCapabilitiesPerSubject]
	}
	writeJSON(w, http.StatusOK, views)
}

func (a *API) handlePolicySubjectsPost(w http.ResponseWriter, r *http.Request) {
	subjectType, subjectID, capID, ok := parsePolicySubjectsPath(r.URL.Path)
	if !ok || capID != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "POST path must be /policy/subjects/{type}/{id}/capabilities"})
		return
	}
	subjectID = a.resolveSubjectName(subjectType, subjectID)
	spec, err := decodeCapabilitySpec(w, r)
	if err != nil {
		return
	}
	view, err := a.createCapability(subjectType, subjectID, spec)
	if err != nil {
		writeJSON(w, policyErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	a.notifyToolsChanged()
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) handlePolicySubjectsPut(w http.ResponseWriter, r *http.Request) {
	subjectType, subjectID, capID, ok := parsePolicySubjectsPath(r.URL.Path)
	if !ok || capID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "PUT path must be /policy/subjects/{type}/{id}/capabilities/{cap_id}"})
		return
	}
	subjectID = a.resolveSubjectName(subjectType, subjectID)
	spec, err := decodeCapabilitySpec(w, r)
	if err != nil {
		return
	}
	view, err := a.editCapability(subjectType, subjectID, capID, spec)
	if err != nil {
		writeJSON(w, policyErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	a.notifyToolsChanged()
	writeJSON(w, http.StatusOK, view)
}

func (a *API) handlePolicySubjectsDelete(w http.ResponseWriter, r *http.Request) {
	subjectType, subjectID, capID, ok := parsePolicySubjectsPath(r.URL.Path)
	if !ok || capID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "DELETE path must be /policy/subjects/{type}/{id}/capabilities/{cap_id}"})
		return
	}
	subjectID = a.resolveSubjectName(subjectType, subjectID)
	if err := a.deleteCapability(subjectType, subjectID, capID); err != nil {
		writeJSON(w, policyErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	a.notifyToolsChanged()
	w.WriteHeader(http.StatusNoContent)
}

func decodeCapabilitySpec(w http.ResponseWriter, r *http.Request) (CapabilitySpec, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var spec CapabilitySpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return CapabilitySpec{}, err
	}
	return spec, nil
}

func (a *API) notifyToolsChanged() {
	if a.backendMgr != nil {
		a.backendMgr.NotifyToolsChanged()
	}
	// Capability mutations on any subject (agent/group/role) can change
	// the inherited-capability count for some set of agents. The cheapest
	// correct invalidation is to clear all cached summaries — these
	// edits are rare admin actions, not hot-path; recomputing is two SQL
	// reads on the next listing request.
	a.invalidateAllAgentPolicySummaries()
}

// Sentinel errors for HTTP status routing in policyErrorStatus. Handlers wrap
// these via fmt.Errorf("...: %w", ErrXxx) so the message stays informative
// while the status decision stays robust against message changes.
var (
	ErrSubjectNotFound     = errors.New("subject not found")
	ErrCapabilityNotFound  = errors.New("capability not found")
	ErrInvalidSpec         = errors.New("invalid capability spec")
	ErrTemplateImmutable   = errors.New("template version is immutable")
	ErrPolicyConflict      = errors.New("policy conflict")
	ErrPolicyNotConfigured = errors.New("policy storage not configured")
)

func policyErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	switch {
	case errors.Is(err, ErrSubjectNotFound), errors.Is(err, ErrCapabilityNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrInvalidSpec):
		return http.StatusBadRequest
	case errors.Is(err, ErrTemplateImmutable), errors.Is(err, ErrPolicyConflict):
		return http.StatusConflict
	case errors.Is(err, ErrPolicyNotConfigured):
		return http.StatusServiceUnavailable
	}
	// Fallback string heuristics retained for errors bubbling up from the
	// auth/authserver packages we don't own (predicate validation, hash
	// computation, etc.). These error messages are stable contract surface
	// covered by their own packages' tests.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "not configured"), strings.Contains(msg, "not available"):
		return http.StatusServiceUnavailable
	case strings.Contains(msg, "unsupported"),
		strings.Contains(msg, "invalid"),
		strings.Contains(msg, "must"),
		strings.Contains(msg, "required"),
		strings.Contains(msg, "unknown"):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// createCapability compiles the spec to either a scope mutation or a
// template+binding pair, per spec §7.1.
func (a *API) createCapability(subjectType, subjectID string, spec CapabilitySpec) (CapabilityView, error) {
	if err := validateCapabilitySpec(spec); err != nil {
		return CapabilityView{}, err
	}
	enabled := a.enabledBackends()
	if spec.hasConstraints() {
		return a.createGrantCapability(subjectType, subjectID, spec, enabled)
	}
	return a.createScopeCapability(subjectType, subjectID, spec, enabled)
}

// editCapability ALWAYS forks per spec §7.2: it creates the new shape first,
// then deletes the old. No mutation of any existing template — that protects
// other subjects bound to the same hash.
//
// No-op edits (new spec == old spec) short-circuit to avoid deleting the
// binding being "re-saved". Both the grant-shape bindingID and the scope-shape
// capability ID are deterministic over spec content, so an unchanged spec
// would resolve to the same capability ID and the create-then-delete sequence
// would silently destroy the row.
func (a *API) editCapability(subjectType, subjectID, capID string, spec CapabilitySpec) (CapabilityView, error) {
	if err := validateCapabilitySpec(spec); err != nil {
		return CapabilityView{}, err
	}
	// Verify the old capability exists on this subject before creating
	// anything new — saves operators from accidental orphan creation when
	// they edit a stale row.
	existing, err := a.lookupCapability(subjectType, subjectID, capID)
	if err != nil {
		return CapabilityView{}, err
	}
	// Compute the new capability ID before any writes so we can short-circuit
	// no-op edits. If the new spec resolves to the same id as the row being
	// edited, the create-then-delete pair would target the same KV row and
	// the delete would destroy the row we just re-saved.
	newID, err := a.predictCapabilityID(subjectType, subjectID, spec)
	if err == nil && newID == capID {
		return existing, nil
	}
	view, err := a.createCapability(subjectType, subjectID, spec)
	if err != nil {
		return CapabilityView{}, err
	}
	if err := a.deleteCapability(subjectType, subjectID, capID); err != nil {
		// Best-effort rollback: deleting the freshly-created capability so
		// we don't leave the operator with a duplicate. If the rollback
		// fails the original error still wins — operators see the cleanup
		// path in audit.
		_ = a.deleteCapability(subjectType, subjectID, view.ID)
		return CapabilityView{}, err
	}
	return view, nil
}

// predictCapabilityID returns the capability ID that createCapability would
// produce for spec, without performing any KV writes. Used by editCapability
// to short-circuit no-op edits.
//
// Mirrors the create-side branching exactly:
//   - scope shape: encodeScopeCapabilityID over the canonical scope list
//   - grant shape: "bind-" + bindingIDFromTemplate over the derived hash
//
// Roles always take the grant path (see createScopeCapability).
func (a *API) predictCapabilityID(subjectType, subjectID string, spec CapabilitySpec) (string, error) {
	enabled := a.enabledBackends()
	if spec.hasConstraints() || subjectType == subjectTypeRoles {
		gs, err := compileToGrantSpec(spec, enabled)
		if err != nil {
			return "", err
		}
		if err := gs.Validate(); err != nil {
			return "", err
		}
		hash, err := auth.ComputeTemplateHash(gs)
		if err != nil {
			return "", err
		}
		t := auth.GrantTemplate{ID: templateIDFromHash(hash)}
		return "bind-" + bindingIDFromTemplate(t, subjectType, subjectID), nil
	}
	scopes, err := scopeStringsForAction(spec.Action, enabled)
	if err != nil {
		return "", err
	}
	if len(scopes) == 0 {
		return "", errors.New("verb resolved to zero tools for enabled backends")
	}
	return encodeScopeCapabilityID(spec, scopes), nil
}

// deleteCapability removes either a scope string from the subject's policy or
// a binding from the grant store, depending on the ID prefix.
//
// Prefix routing (task-46 added the deny path):
//   - "scope-deny-..." → remove from AgentPolicy.Deny
//   - "scope-..."      → remove from AgentPolicy.Grant / Group.Scopes
//   - "bind-..."       → delete from the grant binding store
func (a *API) deleteCapability(subjectType, subjectID, capID string) error {
	switch {
	case strings.HasPrefix(capID, scopeDenyIDPrefix):
		scope, ok := decodeScopeID(capID)
		if !ok {
			return errors.New("invalid scope capability id")
		}
		return a.removeScopeFromSubject(subjectType, subjectID, scope, capabilityEffectDeny)
	case strings.HasPrefix(capID, "scope-"):
		scope, ok := decodeScopeID(capID)
		if !ok {
			return errors.New("invalid scope capability id")
		}
		return a.removeScopeFromSubject(subjectType, subjectID, scope, capabilityEffectAllow)
	case strings.HasPrefix(capID, "bind-"):
		bindingID := strings.TrimPrefix(capID, "bind-")
		if !isValidID(bindingID) {
			return errors.New("invalid binding capability id")
		}
		if a.grantMgr == nil {
			return errors.New("grant management not configured")
		}
		// Confirm the binding actually belongs to the subject before deleting.
		b, err := a.grantMgr.GetGrantBinding(bindingID)
		if err != nil {
			return fmt.Errorf("binding %s: %w", bindingID, ErrCapabilityNotFound)
		}
		if !bindingTargetsSubject(b, subjectType, subjectID) {
			return fmt.Errorf("binding %s does not target %s/%s: %w", bindingID, subjectType, subjectID, ErrCapabilityNotFound)
		}
		return a.grantMgr.DeleteGrantBinding(bindingID)
	default:
		return errors.New("invalid capability id")
	}
}

// lookupCapability returns the current CapabilityView for an existing ID.
// Used by edit-fork to validate the row before producing the replacement.
func (a *API) lookupCapability(subjectType, subjectID, capID string) (CapabilityView, error) {
	views, err := a.composeCapabilityViews(subjectType, subjectID)
	if err != nil {
		return CapabilityView{}, err
	}
	for _, v := range views {
		if v.ID == capID {
			return v, nil
		}
	}
	return CapabilityView{}, fmt.Errorf("capability %s on %s/%s: %w", capID, subjectType, subjectID, ErrCapabilityNotFound)
}

// validateCapabilitySpec enforces the cross-field invariants the JSON shape
// doesn't capture. Per-field validation (predicate validity, etc.) happens
// inside the auth package when SaveGrantTemplate is called.
func validateCapabilitySpec(spec CapabilitySpec) error {
	switch spec.Action.Mode {
	case "verb":
		if !isValidID(spec.Action.VerbSlug) {
			return fmt.Errorf("action.verb_slug is required: %w", ErrInvalidSpec)
		}
		if _, ok := FindVerb(spec.Action.VerbSlug); !ok {
			return fmt.Errorf("unknown verb %q: %w", spec.Action.VerbSlug, ErrInvalidSpec)
		}
	case "tool":
		if !isValidID(spec.Action.Backend) || !isValidID(spec.Action.Tool) {
			return fmt.Errorf("action.backend and action.tool are required for mode=tool: %w", ErrInvalidSpec)
		}
	case "backend_wildcard":
		if !isValidID(spec.Action.Backend) {
			return fmt.Errorf("action.backend is required for mode=backend_wildcard: %w", ErrInvalidSpec)
		}
	default:
		return fmt.Errorf("action.mode must be one of {verb,tool,backend_wildcard}; got %q: %w", spec.Action.Mode, ErrInvalidSpec)
	}
	if spec.Where != nil && spec.Where.Mode == "path_prefix" && strings.TrimSpace(spec.Where.PathPrefix) == "" {
		return fmt.Errorf("where.path_prefix is required for mode=path_prefix: %w", ErrInvalidSpec)
	}
	return nil
}

// createScopeCapability appends a scope string to the subject's stored policy.
// Roles have no native scope-policy slot so verb/tool/wildcard capabilities
// on role subjects route through the grant path even with no constraints —
// the spec's "Roles surface as first-class subjects" promise (§4.2) requires
// some binding to attach to.
func (a *API) createScopeCapability(subjectType, subjectID string, spec CapabilitySpec, enabled []string) (CapabilityView, error) {
	if subjectType == subjectTypeRoles {
		// No scope shape for roles — fall through to grant path so the
		// capability still attaches via a binding with RoleRequired.
		return a.createGrantCapability(subjectType, subjectID, spec, enabled)
	}
	scopes, err := scopeStringsForAction(spec.Action, enabled)
	if err != nil {
		return CapabilityView{}, err
	}
	if len(scopes) == 0 {
		return CapabilityView{}, errors.New("verb resolved to zero tools for enabled backends; widen the verb mapping or pick a specific tool")
	}
	if err := a.appendScopesToSubject(subjectType, subjectID, scopes); err != nil {
		return CapabilityView{}, err
	}
	// One CapabilityView covers the verb expansion as a single row — the ID
	// identifies the source spec, not the individual scope strings. The
	// deterministic encoder canonicalizes the scope list so the same spec
	// always produces the same capability ID.
	id := encodeScopeCapabilityID(spec, scopes)
	view := CapabilityView{
		ID:             id,
		Source:         capabilitySourceScope,
		Spec:           spec,
		DisplaySummary: renderDisplaySummary(spec),
		Chips:          renderChips(spec),
	}
	return view, nil
}

// scopeStringsForAction expands an Action into one or more "backend:tool"
// scope strings. Specific-tool and backend_wildcard produce one scope each;
// a verb produces N (one per resolved pair, filtered to enabled backends).
func scopeStringsForAction(action ActionSpec, enabled []string) ([]string, error) {
	switch action.Mode {
	case "tool":
		return []string{action.Backend + ":" + action.Tool}, nil
	case "backend_wildcard":
		return []string{action.Backend + ":*"}, nil
	case "verb":
		return ScopeStringsForVerb(action.VerbSlug, enabled)
	}
	return nil, fmt.Errorf("unsupported action.mode %q", action.Mode)
}

// createGrantCapability builds a GrantSpec from the capability spec, computes
// its hash, reuses any matching existing template, and creates a fresh
// binding scoped to the subject.
func (a *API) createGrantCapability(subjectType, subjectID string, spec CapabilitySpec, enabled []string) (CapabilityView, error) {
	if a.grantMgr == nil {
		return CapabilityView{}, errors.New("grant management not configured")
	}
	gs, err := compileToGrantSpec(spec, enabled)
	if err != nil {
		return CapabilityView{}, err
	}
	if err := gs.Validate(); err != nil {
		return CapabilityView{}, err
	}
	hash, err := auth.ComputeTemplateHash(gs)
	if err != nil {
		return CapabilityView{}, err
	}
	existing, _ := a.grantMgr.GetGrantTemplateByHash(hash)
	var template auth.GrantTemplate
	if existing.Hash == hash && existing.ID != "" {
		template = existing
	} else {
		t := auth.GrantTemplate{
			ID:        templateIDFromHash(hash),
			Spec:      gs,
			CreatedAt: time.Now().UTC(),
		}
		saved, err := a.grantMgr.SaveGrantTemplate(t)
		if err != nil {
			return CapabilityView{}, err
		}
		template = saved
	}
	binding := auth.GrantBinding{
		ID:           bindingIDFromTemplate(template, subjectType, subjectID),
		TemplateID:   template.ID,
		TemplateHash: template.Hash,
		Subjects:     subjectSelectorFor(subjectType, subjectID, spec),
	}
	saved, err := a.grantMgr.SetGrantBinding(binding)
	if err != nil {
		return CapabilityView{}, err
	}
	view := CapabilityView{
		ID:             "bind-" + saved.ID,
		Source:         capabilitySourceGrant,
		Spec:           spec,
		DisplaySummary: renderDisplaySummary(spec),
		Chips:          renderChips(spec),
		SharedWith:     a.sharedSubjectsForTemplate(template.Hash, subjectType, subjectID),
	}
	return view, nil
}

// compileToGrantSpec lowers a CapabilitySpec into a GrantSpec the grants
// store can persist. The Tool/Backend/Args triplet is set per the compile
// table in spec §7.1; constraint chips lower into the optional fields.
func compileToGrantSpec(spec CapabilitySpec, enabled []string) (auth.GrantSpec, error) {
	gs := auth.GrantSpec{
		Type: auth.GrantTypeMCPCall,
	}
	if err := applyActionToGrantSpec(&gs, spec.Action, enabled); err != nil {
		return auth.GrantSpec{}, err
	}
	if err := applyWhereToGrantSpec(&gs, spec.Where); err != nil {
		return auth.GrantSpec{}, err
	}
	applyWhenToGrantSpec(&gs, spec.When)
	applyHowSecureToGrantSpec(&gs, spec.HowSecure)
	if spec.Advanced != nil {
		mergeAdvancedIntoGrantSpec(&gs, *spec.Advanced)
	}
	return gs, nil
}

func applyActionToGrantSpec(gs *auth.GrantSpec, action ActionSpec, enabled []string) error {
	switch action.Mode {
	case "tool":
		gs.Backend = action.Backend
		gs.Tool = action.Tool
	case "backend_wildcard":
		gs.Backend = action.Backend
		gs.Tool = "*"
	case "verb":
		entries, err := ScopeStringsForVerb(action.VerbSlug, enabled)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("verb %q resolved to zero tools — no matching backends are enabled: %w", action.VerbSlug, ErrInvalidSpec)
		}
		gs.Backend = "*"
		gs.Tool = "*"
		if gs.Args == nil {
			gs.Args = make(map[string]auth.Predicate)
		}
		gs.Args["_tool"] = auth.Predicate{ToolInSet: entries}
	default:
		return fmt.Errorf("unsupported action.mode %q", action.Mode)
	}
	return nil
}

func applyWhereToGrantSpec(gs *auth.GrantSpec, where *WhereSpec) error {
	if where == nil || where.Mode == "" || where.Mode == "anywhere" {
		return nil
	}
	switch where.Mode {
	case "path_prefix":
		pfx := where.PathPrefix
		if gs.Args == nil {
			gs.Args = make(map[string]auth.Predicate)
		}
		gs.Args["path"] = auth.Predicate{Prefix: &pfx}
	case "agent_home":
		pfx := "/workspace/${agent.prism_id}/"
		if gs.Args == nil {
			gs.Args = make(map[string]auth.Predicate)
		}
		gs.Args["path"] = auth.Predicate{Prefix: &pfx}
	case "shared":
		val := "virtual"
		gs.Workspace.Type = &auth.Predicate{Equals: val}
	case "ephemeral":
		val := "ephemeral"
		gs.Workspace.Type = &auth.Predicate{Equals: val}
	default:
		return fmt.Errorf("unsupported where.mode %q", where.Mode)
	}
	return nil
}

func applyWhenToGrantSpec(gs *auth.GrantSpec, when *WhenSpec) {
	if when == nil || when.Mode == "" || when.Mode == "anytime" {
		return
	}
	switch when.Mode {
	case "business":
		tz := when.Timezone
		if tz == "" {
			tz = "UTC"
		}
		gs.Hours = "weekdays 09:00-18:00 " + tz
	case "window":
		gs.Hours = when.Hours
	}
}

func applyHowSecureToGrantSpec(gs *auth.GrantSpec, h *HowSecureSpec) {
	if h == nil || h.Mode == "" || h.Mode == "token" {
		return
	}
	switch h.Mode {
	case "mfa":
		gs.AcrRequired = "urn:prism:mfa"
		if h.MfaFreshnessSec > 0 {
			gs.AuthFreshnessMax = h.MfaFreshnessSec
		}
	case "mfa_dpop":
		gs.AcrRequired = "urn:prism:mfa"
		if h.MfaFreshnessSec > 0 {
			gs.AuthFreshnessMax = h.MfaFreshnessSec
		}
		gs.CnfRequired = true
	}
	if h.AcrOverride != "" {
		gs.AcrRequired = h.AcrOverride
	}
	if h.RequireDPoP {
		gs.CnfRequired = true
	}
}

func mergeAdvancedIntoGrantSpec(gs *auth.GrantSpec, adv AdvancedSpec) {
	if len(adv.Args) > 0 {
		if gs.Args == nil {
			gs.Args = make(map[string]auth.Predicate, len(adv.Args))
		}
		for k, v := range adv.Args {
			gs.Args[k] = v
		}
	}
	if adv.Workspace != nil {
		if adv.Workspace.ID != nil {
			gs.Workspace.ID = adv.Workspace.ID
		}
		if adv.Workspace.Type != nil {
			gs.Workspace.Type = adv.Workspace.Type
		}
		if adv.Workspace.WriteMode != nil {
			gs.Workspace.WriteMode = adv.Workspace.WriteMode
		}
	}
	if adv.AcrRequired != "" {
		gs.AcrRequired = adv.AcrRequired
	}
}

// subjectSelectorFor builds the SubjectSelector for a freshly-created binding.
// RoleRequired (from advanced) is AND-combined on top of whichever primary
// selector the subject type implies.
func subjectSelectorFor(subjectType, subjectID string, spec CapabilitySpec) auth.SubjectSelector {
	sel := auth.SubjectSelector{}
	switch subjectType {
	case subjectTypeGroups:
		sel.Groups = []string{subjectID}
	case subjectTypeRoles:
		sel.Roles = []string{subjectID}
	case subjectTypeAgents:
		sel.AgentIDs = []string{subjectID}
	}
	if spec.Advanced != nil && spec.Advanced.RoleRequired != "" {
		sel.RoleRequired = spec.Advanced.RoleRequired
	}
	return sel
}

// templateIDFromHash derives a stable, deterministic template ID from the
// canonical hash. Two subjects whose specs produce the same hash will share
// the same template — that's the dedup property from spec §7.2.
//
// Hash format is "sha256-<hex>"; we strip the prefix and take the leading 16
// hex chars (64 bits of entropy) as a compact, KV-key-safe ID. Collisions
// over the lifetime of one prism install are astronomically unlikely.
func templateIDFromHash(hash string) string {
	clean := strings.TrimPrefix(hash, "sha256-")
	if len(clean) >= 16 {
		clean = clean[:16]
	}
	return "tmpl-" + clean
}

// bindingIDFromTemplate derives a binding ID that is unique per (template,
// subject) pair. This keeps subjects sharing a template each with their own
// binding (the v1 promise: edits fork). The fingerprint depends on subject
// type + ID so the same group can't accidentally get two bindings against
// the same template.
func bindingIDFromTemplate(t auth.GrantTemplate, subjectType, subjectID string) string {
	src := t.ID + "|" + subjectType + "|" + subjectID
	sum := sha256.Sum256([]byte(src))
	return "b" + strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(sum[:8]), "="))
}

// encodeScopeCapabilityID derives a stable, decodable ID for a scope-shape
// capability. The decoder needs to recover the list of scope strings so
// DELETE can find and remove them; we base32-encode a compact JSON blob that
// captures both the action mode and the canonical scope list.
func encodeScopeCapabilityID(spec CapabilitySpec, scopes []string) string {
	payload := scopeIDPayload{Mode: spec.Action.Mode, Scopes: scopes}
	if spec.Action.Mode == "verb" {
		payload.VerbSlug = spec.Action.VerbSlug
	}
	raw, _ := json.Marshal(payload)
	return "scope-" + strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="))
}

// encodeScopeDenyCapabilityID is the deny-side counterpart. Same payload
// format as the allow-side ID, just a different prefix so the DELETE handler
// can route deletes to AgentPolicy.Deny instead of AgentPolicy.Grant
// without touching the wire surface.
func encodeScopeDenyCapabilityID(spec CapabilitySpec, scopes []string) string {
	payload := scopeIDPayload{Mode: spec.Action.Mode, Scopes: scopes}
	if spec.Action.Mode == "verb" {
		payload.VerbSlug = spec.Action.VerbSlug
	}
	raw, _ := json.Marshal(payload)
	return scopeDenyIDPrefix + strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="))
}

type scopeIDPayload struct {
	Mode     string   `json:"mode"`
	VerbSlug string   `json:"verb,omitempty"`
	Scopes   []string `json:"scopes"`
}

func decodeScopeID(id string) (scopeIDPayload, bool) {
	// Recognize both the allow-side "scope-" prefix and the deny-side
	// "scope-deny-" prefix. The longer prefix is checked first so the
	// shorter one doesn't swallow it. The caller routes on the original
	// id prefix to decide which storage list to mutate.
	var rest string
	switch {
	case strings.HasPrefix(id, scopeDenyIDPrefix):
		rest = strings.TrimPrefix(id, scopeDenyIDPrefix)
	case strings.HasPrefix(id, "scope-"):
		rest = strings.TrimPrefix(id, "scope-")
	default:
		return scopeIDPayload{}, false
	}
	// base32 strip padding requires re-adding to multiple of 8.
	rest = strings.ToUpper(rest)
	if pad := len(rest) % 8; pad != 0 {
		rest += strings.Repeat("=", 8-pad)
	}
	raw, err := base32.StdEncoding.DecodeString(rest)
	if err != nil {
		return scopeIDPayload{}, false
	}
	var out scopeIDPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		return scopeIDPayload{}, false
	}
	return out, true
}

// removeScopeFromSubject deletes the scope-shape capability's underlying
// scope strings from the subject's stored policy. Missing scopes are
// tolerated (idempotent delete).
//
// task-46: `effect` discriminates between removing from the allow list
// (AgentPolicy.Grant / Group.Scopes) and the deny list (AgentPolicy.Deny).
// The two lists live in the same storage struct so the mutate helper just
// branches inside the SetAgentPolicy call.
func (a *API) removeScopeFromSubject(subjectType, subjectID string, payload scopeIDPayload, effect string) error {
	return a.mutateScopes(subjectType, subjectID, effect, func(current []string) []string {
		drop := make(map[string]struct{}, len(payload.Scopes))
		for _, s := range payload.Scopes {
			drop[s] = struct{}{}
		}
		out := make([]string, 0, len(current))
		for _, s := range current {
			if _, isDrop := drop[s]; isDrop {
				continue
			}
			out = append(out, s)
		}
		return out
	})
}

// appendScopesToSubject is the additive counterpart used by create. Existing
// scope strings are preserved; the new list is appended without duplicates.
// Create paths only target the allow list — the SecOps surface adds deny
// rows via direct AgentPolicy edits, not via the capability builder.
func (a *API) appendScopesToSubject(subjectType, subjectID string, scopes []string) error {
	return a.mutateScopes(subjectType, subjectID, capabilityEffectAllow, func(current []string) []string {
		seen := make(map[string]struct{}, len(current)+len(scopes))
		out := make([]string, 0, len(current)+len(scopes))
		for _, s := range current {
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		for _, s := range scopes {
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		return out
	})
}

func (a *API) mutateScopes(subjectType, subjectID, effect string, mutate func([]string) []string) error {
	switch subjectType {
	case subjectTypeGroups:
		if a.groupMgr == nil {
			return errors.New("group management not configured")
		}
		g := a.groupMgr.GetGroup(subjectID)
		if g == nil {
			return errors.New("group not found")
		}
		// Group subjects have no deny list today — mutating one would have
		// nowhere to land. Reject defensively so a stale "scope-deny-..."
		// id targeting a group can't silently corrupt the allow list.
		if effect == capabilityEffectDeny {
			return errors.New("group subjects have no deny list")
		}
		return a.groupMgr.SetGroup(subjectID, mutate(g.Scopes))
	case subjectTypeAgents:
		if a.agentMgr == nil {
			return errors.New("agent management not configured")
		}
		reader, ok := a.agentMgr.(PolicyAgentReader)
		if !ok {
			return errors.New("agent policy reader not configured")
		}
		policy, err := reader.GetAgentPolicy(subjectID)
		if err != nil {
			return err
		}
		groups, grant, deny := []string(nil), []string(nil), []string(nil)
		if policy != nil {
			groups = policy.Groups
			grant = policy.Grant
			deny = policy.Deny
		}
		if effect == capabilityEffectDeny {
			return a.agentMgr.SetAgentPolicy(subjectID, groups, grant, mutate(deny))
		}
		return a.agentMgr.SetAgentPolicy(subjectID, groups, mutate(grant), deny)
	}
	return fmt.Errorf("scope mutation not supported for subject type %q", subjectType)
}

// enabledBackends returns the list of backend IDs currently registered with
// the gateway. Falls back to nil when the BackendManager doesn't surface a
// listing; the compile-down router then refuses verb expansions and the
// operator gets a clear error.
func (a *API) enabledBackends() []string {
	if a.backendMgr == nil {
		return nil
	}
	type lister interface {
		ListBackendIDs() []string
	}
	if l, ok := a.backendMgr.(lister); ok {
		return l.ListBackendIDs()
	}
	// Fallback: parse the status payload (statusFn returns a struct that
	// production embeds backend ids inside). Tests pass an explicit set via
	// a hook below; production wires a lister.
	if a.statusFn != nil {
		raw, err := json.Marshal(a.statusFn())
		if err == nil {
			var status struct {
				Backends []struct {
					ID string `json:"id"`
				} `json:"backends"`
			}
			if json.Unmarshal(raw, &status) == nil {
				out := make([]string, 0, len(status.Backends))
				for _, b := range status.Backends {
					if b.ID != "" {
						out = append(out, b.ID)
					}
				}
				return out
			}
		}
	}
	return nil
}

// bindingTargetsSubject reports whether b is one of the subject's bindings
// (the row belongs to them). This is the guard on capability-scoped DELETE
// so an operator can't reach across to another subject's binding via its ID.
func bindingTargetsSubject(b auth.GrantBinding, subjectType, subjectID string) bool {
	switch subjectType {
	case subjectTypeGroups:
		return containsString(b.Subjects.Groups, subjectID)
	case subjectTypeRoles:
		return containsString(b.Subjects.Roles, subjectID)
	case subjectTypeAgents:
		return containsString(b.Subjects.AgentIDs, subjectID)
	}
	return false
}

// sharedSubjectsForTemplate returns other subject IDs (formatted as
// "{type}:{id}") bound to the same template hash. Returned to the UI on
// CapabilityView.SharedWith so Power Tools can show "this template covers N
// subjects" without an extra round-trip.
func (a *API) sharedSubjectsForTemplate(hash, subjectType, subjectID string) []string {
	if a.grantMgr == nil {
		return nil
	}
	out := make(map[string]struct{})
	for _, b := range a.grantMgr.ListGrantBindings() {
		if b.TemplateHash != hash {
			continue
		}
		for _, g := range b.Subjects.Groups {
			if subjectType == subjectTypeGroups && g == subjectID {
				continue
			}
			out["groups:"+g] = struct{}{}
		}
		for _, r := range b.Subjects.Roles {
			if subjectType == subjectTypeRoles && r == subjectID {
				continue
			}
			out["roles:"+r] = struct{}{}
		}
		for _, id := range b.Subjects.AgentIDs {
			if subjectType == subjectTypeAgents && id == subjectID {
				continue
			}
			out["agents:"+id] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
