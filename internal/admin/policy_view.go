package admin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
)

// composeCapabilityViews returns one CapabilityView per row on the subject's
// policy page. Scope-shape and grant-shape capabilities are returned in a
// single ordered list (scope first for visual stability since scope rows
// don't change on grant edits).
//
// Construction is read-only: the function joins the subject's stored policy
// (scopes for groups/agents) with the grant bindings whose SubjectSelector
// references the subject. No KV writes happen here.
func (a *API) composeCapabilityViews(subjectType, subjectID string) ([]CapabilityView, error) {
	scopeRows, err := a.scopeCapabilityViews(subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	bindingRows, err := a.bindingCapabilityViews(subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	out := make([]CapabilityView, 0, len(scopeRows)+len(bindingRows))
	out = append(out, scopeRows...)
	out = append(out, bindingRows...)
	return out, nil
}

func (a *API) scopeCapabilityViews(subjectType, subjectID string) ([]CapabilityView, error) {
	allow, deny, err := a.readSubjectScopes(subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	if len(allow) == 0 && len(deny) == 0 {
		return nil, nil
	}
	// Each individual scope string becomes its own CapabilityView. Verb
	// expansion that produced N scopes can't be losslessly reconstructed
	// from storage alone (the table maps verb→tools but tools→verb is
	// ambiguous when one tool appears in multiple verbs); listing scopes
	// individually keeps the view honest and matches what /grants/templates
	// shows in Power Tools.
	out := make([]CapabilityView, 0, len(allow)+len(deny))
	for _, scope := range allow {
		spec := specFromScope(scope)
		view := CapabilityView{
			ID:             encodeScopeCapabilityID(spec, []string{scope}),
			Source:         capabilitySourceScope,
			Effect:         capabilityEffectAllow,
			Spec:           spec,
			DisplaySummary: renderDisplaySummary(spec),
			Chips:          renderChips(spec),
		}
		out = append(out, view)
	}
	for _, scope := range deny {
		spec := specFromScope(scope)
		view := CapabilityView{
			ID:             encodeScopeDenyCapabilityID(spec, []string{scope}),
			Source:         capabilitySourceScope,
			Effect:         capabilityEffectDeny,
			Spec:           spec,
			DisplaySummary: renderDisplaySummary(spec),
			Chips:          renderChips(spec),
		}
		out = append(out, view)
	}
	return out, nil
}

func (a *API) bindingCapabilityViews(subjectType, subjectID string) ([]CapabilityView, error) {
	if a.grantMgr == nil {
		return nil, nil
	}
	if subjectType == subjectTypeAgents {
		return a.composeAgentCapabilityViews(subjectID)
	}
	bindings := a.grantMgr.ListGrantBindings()
	out := make([]CapabilityView, 0)
	for _, b := range bindings {
		if !bindingTargetsSubject(b, subjectType, subjectID) {
			continue
		}
		template, err := a.grantMgr.GetGrantTemplateByHash(b.TemplateHash)
		if err != nil {
			// Skip the row but don't fail the whole listing — operators
			// can delete dangling bindings via Power Tools.
			continue
		}
		spec := specFromGrantTemplate(template, b)
		view := CapabilityView{
			ID:             "bind-" + b.ID,
			Source:         capabilitySourceGrant,
			Effect:         capabilityEffectAllow,
			Spec:           spec,
			DisplaySummary: renderDisplaySummary(spec),
			Chips:          renderChips(spec),
			SharedWith:     a.sharedSubjectsForTemplate(template.Hash, subjectType, subjectID),
		}
		out = append(out, view)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// composeAgentCapabilityViews collects every binding contributing to an
// agent's effective capabilities: direct (AgentIDs match), inherited via group
// membership (Subjects.Groups intersects agent.Policy.Groups), or inherited
// via role membership (Subjects.Roles intersects the agent's roles, or
// Subjects.RoleRequired is one of them).
//
// The operator-facing API previously only returned direct bindings, hiding
// group/role inheritance. The runtime authorizer composes the same join (see
// ResolveScopesByPrismID → resolvePolicy) so a missing operator view was a
// pure display gap, not a security boundary; this resolves spec §12
// acceptance #11.
//
// Each returned CapabilityView carries InheritedFrom entries naming the
// upstream source(s). A capability bound to both a group and a role the agent
// holds appears once with both sources listed.
func (a *API) composeAgentCapabilityViews(prismID string) ([]CapabilityView, error) {
	groups, roles := a.agentInheritanceLookup(prismID)
	groupSet := stringSet(groups)
	roleSet := stringSet(roles)

	bindings := a.grantMgr.ListGrantBindings()
	// Dedup capabilities by ID; aggregate sources per capability.
	byID := make(map[string]*CapabilityView)
	order := make([]string, 0, len(bindings))
	appendSource := func(v *CapabilityView, src InheritanceSource) {
		for _, existing := range v.InheritedFrom {
			if existing == src {
				return
			}
		}
		v.InheritedFrom = append(v.InheritedFrom, src)
	}
	for _, b := range bindings {
		sources := agentInheritanceSources(b, prismID, groupSet, roleSet)
		if len(sources) == 0 {
			continue
		}
		id := "bind-" + b.ID
		view, ok := byID[id]
		if !ok {
			template, err := a.grantMgr.GetGrantTemplateByHash(b.TemplateHash)
			if err != nil {
				continue
			}
			spec := specFromGrantTemplate(template, b)
			view = &CapabilityView{
				ID:             id,
				Source:         capabilitySourceGrant,
				Effect:         capabilityEffectAllow,
				Spec:           spec,
				DisplaySummary: renderDisplaySummary(spec),
				Chips:          renderChips(spec),
				SharedWith:     a.sharedSubjectsForTemplate(template.Hash, subjectTypeAgents, prismID),
			}
			byID[id] = view
			order = append(order, id)
		}
		for _, src := range sources {
			appendSource(view, src)
		}
	}

	out := make([]CapabilityView, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// agentInheritanceLookup returns the agent's groups + roles. Roles are parsed
// from AgentPolicy.Grant entries prefixed with "role:" — the same convention
// authserver.subjectIdentity uses to derive subject roles at authorization
// time.
func (a *API) agentInheritanceLookup(prismID string) (groups, roles []string) {
	if a.agentMgr == nil {
		return nil, nil
	}
	reader, ok := a.agentMgr.(PolicyAgentReader)
	if !ok {
		return nil, nil
	}
	policy, err := reader.GetAgentPolicy(prismID)
	if err != nil || policy == nil {
		return nil, nil
	}
	groups = append(groups, policy.Groups...)
	for _, g := range policy.Grant {
		const rolePfx = "role:"
		if len(g) > len(rolePfx) && g[:len(rolePfx)] == rolePfx {
			roles = append(roles, g[len(rolePfx):])
		}
	}
	return groups, roles
}

// agentInheritanceSources returns the inheritance sources that explain why a
// given binding applies to the agent. Returns an empty slice when the binding
// does not target the agent at all.
func agentInheritanceSources(b auth.GrantBinding, prismID string, groupSet, roleSet map[string]struct{}) []InheritanceSource {
	out := make([]InheritanceSource, 0, 2)
	if containsString(b.Subjects.AgentIDs, prismID) {
		out = append(out, InheritanceSource{Type: "direct"})
	}
	for _, g := range b.Subjects.Groups {
		if _, ok := groupSet[g]; ok {
			out = append(out, InheritanceSource{Type: "group", Name: g})
		}
	}
	for _, r := range b.Subjects.Roles {
		if _, ok := roleSet[r]; ok {
			out = append(out, InheritanceSource{Type: "role", Name: r})
		}
	}
	if b.Subjects.RoleRequired != "" {
		if _, ok := roleSet[b.Subjects.RoleRequired]; ok {
			// AND-style required role still surfaces as a contributing
			// inheritance source — the binding only applies to the agent
			// because they hold this role.
			out = append(out, InheritanceSource{Type: "role", Name: b.Subjects.RoleRequired})
		}
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

// readSubjectScopes returns (allow, deny) scope strings for the subject.
// Allow rows produce Effect="allow" CapabilityViews; deny rows produce
// Effect="deny" rows that delete back through AgentPolicy.Deny.
//
// Group + role subject types currently have no deny shape in the data model,
// so deny is always nil for those. Only agent subjects join in their
// AgentPolicy.Deny list — that's enough for v1 of the SecOps presentation
// pass (task-46); group/role deny shapes can extend this later without
// changing the wire surface.
func (a *API) readSubjectScopes(subjectType, subjectID string) (allow []string, deny []string, err error) {
	switch subjectType {
	case subjectTypeGroups:
		if a.groupMgr == nil {
			return nil, nil, nil
		}
		g := a.groupMgr.GetGroup(subjectID)
		if g == nil {
			return nil, nil, nil
		}
		return g.Scopes, nil, nil
	case subjectTypeAgents:
		if a.agentMgr == nil {
			return nil, nil, nil
		}
		reader, ok := a.agentMgr.(PolicyAgentReader)
		if !ok {
			return nil, nil, nil
		}
		policy, readErr := reader.GetAgentPolicy(subjectID)
		if readErr != nil {
			return nil, nil, readErr
		}
		if policy == nil {
			return nil, nil, nil
		}
		// Filter out "role:<name>" entries — authserver stores agent role
		// memberships inline in the Grant slice (see
		// authserver.subjectIdentity), and surfacing those as capability
		// rows would lie to operators about what the agent can call. The
		// role memberships drive InheritedFrom on the binding-side views.
		grant := make([]string, 0, len(policy.Grant))
		for _, g := range policy.Grant {
			if strings.HasPrefix(g, "role:") {
				continue
			}
			grant = append(grant, g)
		}
		// Deny entries are surfaced as Effect="deny" capability rows.
		// Role-prefixed entries are theoretically not valid here (deny is
		// scope-shaped) but we filter defensively in case operators
		// hand-edited the policy doc.
		denyOut := make([]string, 0, len(policy.Deny))
		for _, d := range policy.Deny {
			if strings.HasPrefix(d, "role:") {
				continue
			}
			denyOut = append(denyOut, d)
		}
		return grant, denyOut, nil
	case subjectTypeRoles:
		// Roles have no scope shape — bindings only.
		return nil, nil, nil
	}
	return nil, nil, fmt.Errorf("unsupported subject type %q", subjectType)
}

// specFromScope inverts a "backend:tool" or "backend:*" scope string into a
// CapabilitySpec. Verb reconstruction is deliberately not attempted here
// (the same tool can belong to multiple verbs); the row shows as a "tool"
// or "backend_wildcard" action.
func specFromScope(scope string) CapabilitySpec {
	parts := strings.SplitN(scope, ":", 2)
	spec := CapabilitySpec{Action: ActionSpec{Mode: "tool"}}
	if len(parts) == 2 {
		spec.Action.Backend = parts[0]
		spec.Action.Tool = parts[1]
		if parts[1] == "*" {
			spec.Action.Mode = "backend_wildcard"
			spec.Action.Tool = ""
		}
	}
	return spec
}

// specFromGrantTemplate decompiles a stored template + binding back into a
// CapabilitySpec for the UI edit form.
func specFromGrantTemplate(t auth.GrantTemplate, b auth.GrantBinding) CapabilitySpec {
	spec := CapabilitySpec{}
	gs := t.Spec
	// Action — verb detection is best-effort: a template carrying _tool
	// tool_in_set indicates a verb compile path.
	if pred, ok := gs.Args["_tool"]; ok && len(pred.ToolInSet) > 0 {
		spec.Action.Mode = "verb"
		if slug := matchVerbFromToolInSet(pred.ToolInSet); slug != "" {
			spec.Action.VerbSlug = slug
		}
	} else if gs.Tool == "*" {
		spec.Action.Mode = "backend_wildcard"
		spec.Action.Backend = gs.Backend
	} else {
		spec.Action.Mode = "tool"
		spec.Action.Backend = gs.Backend
		spec.Action.Tool = gs.Tool
	}
	// Where — path prefix or workspace type.
	if pred, ok := gs.Args["path"]; ok && pred.Prefix != nil {
		spec.Where = &WhereSpec{Mode: "path_prefix", PathPrefix: *pred.Prefix}
		if *pred.Prefix == "/workspace/${agent.prism_id}/" {
			spec.Where.Mode = "agent_home"
			spec.Where.PathPrefix = ""
		}
	} else if gs.Workspace.Type != nil && gs.Workspace.Type.Equals != nil {
		if v, ok := gs.Workspace.Type.Equals.(string); ok {
			switch v {
			case "virtual":
				spec.Where = &WhereSpec{Mode: "shared"}
			case "ephemeral":
				spec.Where = &WhereSpec{Mode: "ephemeral"}
			}
		}
	}
	// When — hours window.
	if gs.Hours != "" {
		if strings.HasPrefix(gs.Hours, "weekdays 09:00-18:00 ") {
			spec.When = &WhenSpec{
				Mode:     "business",
				Timezone: strings.TrimPrefix(gs.Hours, "weekdays 09:00-18:00 "),
			}
		} else {
			spec.When = &WhenSpec{Mode: "window", Hours: gs.Hours}
		}
	}
	// How securely.
	if gs.AcrRequired != "" || gs.CnfRequired {
		spec.HowSecure = &HowSecureSpec{}
		if gs.CnfRequired {
			spec.HowSecure.Mode = "mfa_dpop"
		} else if gs.AcrRequired != "" {
			spec.HowSecure.Mode = "mfa"
		}
		if gs.AuthFreshnessMax > 0 {
			spec.HowSecure.MfaFreshnessSec = gs.AuthFreshnessMax
		}
		if gs.AcrRequired != "" && gs.AcrRequired != "urn:prism:mfa" {
			spec.HowSecure.AcrOverride = gs.AcrRequired
		}
	}
	// Advanced — surface remaining args predicates (everything but _tool/path
	// which the simple fields already cover) plus RoleRequired and workspace
	// extras the simple fields don't capture.
	adv := AdvancedSpec{
		RoleRequired: b.Subjects.RoleRequired,
	}
	extraArgs := make(map[string]auth.Predicate)
	for k, p := range gs.Args {
		if k == "_tool" || k == "path" {
			continue
		}
		extraArgs[k] = p
	}
	if len(extraArgs) > 0 {
		adv.Args = extraArgs
	}
	if (gs.Workspace.ID != nil) || (gs.Workspace.WriteMode != nil) {
		adv.Workspace = &auth.WorkspaceConstraint{
			ID:        gs.Workspace.ID,
			WriteMode: gs.Workspace.WriteMode,
		}
	}
	if adv.RoleRequired != "" || adv.Args != nil || adv.Workspace != nil {
		spec.Advanced = &adv
	}
	return spec
}

// matchVerbFromToolInSet returns the verb slug whose canonical compiled
// entries cover the predicate's set, or "" if no verb matches.
//
// A predicate's entries are a subset of the verb's all-backends expansion
// because the original save filtered the verb by the set of currently-enabled
// backends. Reading the template back later (possibly under a different
// enabled-backends set) should still recognize the verb, so we use subset
// containment rather than equality — preferring exact-match first to keep
// the result stable for the common case.
func matchVerbFromToolInSet(entries []string) string {
	sortedTarget := make([]string, len(entries))
	copy(sortedTarget, entries)
	sort.Strings(sortedTarget)
	// Prefer exact match (most precise) before falling back to subset
	// containment so two verbs that overlap don't both claim the row.
	for _, v := range Verbs {
		if equalStringSets(sortedTarget, expandVerbAllBackends(v)) {
			return v.Slug
		}
	}
	for _, v := range Verbs {
		if stringSetContains(expandVerbAllBackends(v), sortedTarget) {
			return v.Slug
		}
	}
	return ""
}

// stringSetContains reports whether sorted `needle` is a subset of sorted
// `haystack`. Both slices must already be sorted lexicographically.
func stringSetContains(haystack, needle []string) bool {
	if len(needle) == 0 {
		return false
	}
	idx := 0
	for _, want := range needle {
		for idx < len(haystack) && haystack[idx] < want {
			idx++
		}
		if idx >= len(haystack) || haystack[idx] != want {
			return false
		}
	}
	return true
}

// expandVerbAllBackends mirrors ResolveVerb but doesn't filter by enabled
// backends — used only to compare against persisted tool_in_set lists where
// the original "enabled backends" set isn't recoverable. Sorted.
func expandVerbAllBackends(v Verb) []string {
	seen := make(map[string]struct{})
	for _, pat := range v.Patterns {
		backend := pat.Backend
		if backend == "*" {
			// We don't know which concrete backends were enabled at compile
			// time; sentinel "*" yields a single entry per tool with the
			// wildcard backend literal.
			for _, t := range pat.Tools {
				seen["*:"+t] = struct{}{}
			}
			continue
		}
		for _, t := range pat.Tools {
			seen[backend+":"+t] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderDisplaySummary returns the server-rendered English sentence shown on
// the capability row. Templates are intentionally simple strings — spec §13
// defers i18n.
//
// task-46: the "Can " prefix was dropped — the ACL-row presentation already
// implies effect via the section grouping (ALLOWED / DENIED) and the
// effect-color left-border on the row itself. The summary now reads purely
// as the action phrase plus any constraint clauses, e.g.
// "call github.create_issue in acme/ during business hours with MFA".
func renderDisplaySummary(spec CapabilitySpec) string {
	verb := actionPhrase(spec.Action)
	parts := []string{verb}
	if spec.Where != nil {
		switch spec.Where.Mode {
		case "path_prefix":
			parts = append(parts, "in "+spec.Where.PathPrefix)
		case "agent_home":
			parts = append(parts, "in /workspace/${agent}/")
		case "shared":
			parts = append(parts, "on shared (virtual) storage")
		case "ephemeral":
			parts = append(parts, "on ephemeral storage")
		}
	}
	if spec.When != nil {
		switch spec.When.Mode {
		case "business":
			parts = append(parts, "during business hours")
		case "window":
			if spec.When.Hours != "" {
				parts = append(parts, "during "+spec.When.Hours)
			}
		}
	}
	if spec.HowSecure != nil {
		switch spec.HowSecure.Mode {
		case "mfa":
			parts = append(parts, "with MFA")
		case "mfa_dpop":
			parts = append(parts, "with MFA + DPoP")
		}
	}
	return strings.Join(parts, " ")
}

// actionPhrase returns the human verb phrase for the action chip:
// "write files" (from verb label "Write files"), "call fs.write_file"
// (specific tool), "call github.*" (backend wildcard).
func actionPhrase(action ActionSpec) string {
	switch action.Mode {
	case "verb":
		if v, ok := FindVerb(action.VerbSlug); ok {
			// Lowercase the first letter so "Can Write files" doesn't look
			// like a sentence inside a sentence.
			if len(v.Label) > 0 {
				return strings.ToLower(v.Label[:1]) + v.Label[1:]
			}
			return v.Slug
		}
		return action.VerbSlug
	case "tool":
		return "call " + action.Backend + "." + action.Tool
	case "backend_wildcard":
		return "call " + action.Backend + ".*"
	}
	return ""
}

// renderChips returns the pre-tokenized chip array. Order matches spec §5.2:
// where → storage → time → freshness → auth manner.
func renderChips(spec CapabilitySpec) []Chip {
	chips := make([]Chip, 0, 5)
	if spec.Where != nil {
		switch spec.Where.Mode {
		case "path_prefix":
			chips = append(chips, Chip{Kind: "where", Label: "in " + spec.Where.PathPrefix, Value: spec.Where.PathPrefix})
		case "agent_home":
			chips = append(chips, Chip{Kind: "where", Label: "in /workspace/${agent}/", Value: "/workspace/${agent.prism_id}/"})
		case "shared":
			chips = append(chips, Chip{Kind: "storage", Label: "shared storage", Value: "virtual"})
		case "ephemeral":
			chips = append(chips, Chip{Kind: "storage", Label: "ephemeral storage", Value: "ephemeral"})
		}
	}
	if spec.When != nil {
		switch spec.When.Mode {
		case "business":
			chips = append(chips, Chip{Kind: "time", Label: "business hours", Value: spec.When.Timezone})
		case "window":
			if spec.When.Hours != "" {
				chips = append(chips, Chip{Kind: "time", Label: spec.When.Hours, Value: spec.When.Hours})
			}
		}
	}
	if spec.HowSecure != nil {
		switch spec.HowSecure.Mode {
		case "mfa":
			label := "MFA required"
			if spec.HowSecure.MfaFreshnessSec > 0 {
				label = fmt.Sprintf("MFA in last %dm", spec.HowSecure.MfaFreshnessSec/60)
			}
			chips = append(chips, Chip{Kind: "freshness", Label: label})
		case "mfa_dpop":
			label := "MFA + DPoP"
			if spec.HowSecure.MfaFreshnessSec > 0 {
				label = fmt.Sprintf("MFA in last %dm + DPoP", spec.HowSecure.MfaFreshnessSec/60)
			}
			chips = append(chips, Chip{Kind: "auth_manner", Label: label})
		}
	}
	return chips
}
