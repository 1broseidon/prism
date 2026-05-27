package admin

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/1broseidon/prism/internal/identity"
)

type agentsRoleInfo struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	MemberCount int    `json:"member_count"`
}

func (a *API) resolveListIdentity(kind identity.Kind, key string) (identity.Entity, bool) {
	if a.identity == nil || key == "" {
		return identity.Entity{}, false
	}
	var (
		ent identity.Entity
		err error
	)
	if identity.IsULID(key) {
		ent, err = a.identity.Resolve(key)
	} else {
		ent, err = a.identity.ResolveByName(kind, key)
	}
	if err != nil || ent.Kind != kind {
		return identity.Entity{}, false
	}
	return ent, true
}

func (a *API) withBackendDisplayNames(raw any) any {
	if a.identity == nil || raw == nil {
		return raw
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return raw
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		return raw
	}
	for _, row := range rows {
		id, _ := row["id"].(string)
		if ent, ok := a.resolveListIdentity(identity.KindBackend, id); ok {
			row["display_name"] = ent.DisplayName
		}
	}
	return rows
}

func (a *API) handleAgentsRoles(w http.ResponseWriter, _ *http.Request) {
	rows := a.listAgentsRoles()
	writeJSON(w, http.StatusOK, rows)
}

func (a *API) listAgentsRoles() []agentsRoleInfo {
	counts := a.roleMemberCounts()
	rows := make(map[string]agentsRoleInfo)
	addName := func(name string) {
		if name == "" {
			return
		}
		if _, ok := rows[name]; ok {
			return
		}
		rows[name] = agentsRoleInfo{Name: name, MemberCount: counts[name]}
	}
	for name := range counts {
		addName(name)
	}
	if a.grantMgr != nil {
		for _, b := range a.grantMgr.ListGrantBindings() {
			for _, role := range b.Subjects.Roles {
				addName(role)
			}
			addName(b.Subjects.RoleRequired)
		}
	}
	if a.identity != nil {
		for _, ent := range a.identity.List(identity.KindRole) {
			memberCount := counts[ent.ID]
			if ent.DisplayName != ent.ID {
				memberCount += counts[ent.DisplayName]
			}
			delete(rows, ent.ID)
			delete(rows, ent.DisplayName)
			rows[ent.ID] = agentsRoleInfo{
				ID:          ent.ID,
				Name:        ent.DisplayName,
				DisplayName: ent.DisplayName,
				MemberCount: memberCount,
			}
		}
		for key, row := range rows {
			if row.ID != "" {
				continue
			}
			ent, ok := a.resolveListIdentity(identity.KindRole, row.Name)
			if !ok {
				continue
			}
			row.ID = ent.ID
			row.Name = ent.DisplayName
			row.DisplayName = ent.DisplayName
			if counts[ent.ID] != 0 {
				row.MemberCount += counts[ent.ID]
			}
			delete(rows, key)
			rows[ent.ID] = row
		}
	}
	out := make([]agentsRoleInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		li := strings.ToLower(out[i].DisplayName)
		if li == "" {
			li = strings.ToLower(out[i].Name)
		}
		lj := strings.ToLower(out[j].DisplayName)
		if lj == "" {
			lj = strings.ToLower(out[j].Name)
		}
		if li != lj {
			return li < lj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (a *API) roleMemberCounts() map[string]int {
	counts := map[string]int{}
	if a.agentMgr == nil {
		return counts
	}
	for _, raw := range a.agentMgr.ListAgents() {
		_, grants := agentScopeGrantsFor(raw)
		seen := map[string]struct{}{}
		for _, grant := range grants {
			role, ok := strings.CutPrefix(grant, "role:")
			if !ok || role == "" {
				continue
			}
			seen[role] = struct{}{}
		}
		for role := range seen {
			counts[role]++
		}
	}
	return counts
}
