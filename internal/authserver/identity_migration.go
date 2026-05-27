package authserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/oklog/ulid/v2"
)

type identityMigrationPolicy struct {
	prismID string
	policy  AgentPolicy
}

type identityMigrationBinding struct {
	key     string
	binding auth.GrantBinding
}

// RunIdentityMigration promotes name-keyed authserver group and role
// references into the shared identity registry, then rewrites authserver KV
// records to hold those stable IDs. It is intentionally gated on an empty
// group+role identity registry so crash restarts never replay over a partial
// best-effort migration.
func (s *Server) RunIdentityMigration(ctx context.Context, logger *slog.Logger) error {
	if s == nil || s.store == nil {
		return nil
	}
	if err := identityMigrationContextErr(ctx); err != nil {
		return err
	}
	if logger == nil {
		logger = s.logger
	}
	if logger == nil {
		logger = slog.Default()
	}
	if s.identityDispatcher == nil {
		return fmt.Errorf("identity migration: identity dispatcher not configured")
	}

	dispatcher := s.identityDispatcher
	// Idempotency: skip only when no agent policy still references a
	// raw name (every Groups entry is a ULID and every role:* marker
	// resolves to a ULID). Checking the dispatcher's List would skip
	// this run if SetGroup/SetRole had already registered names eagerly
	// — e.g. from admin handlers between migrations — leaving stale
	// name-keyed policies behind. The actual rewrite is naturally
	// idempotent (ULID→ULID is a no-op), so a second pass is safe.
	if alreadyMigrated, err := s.identityMigrationPoliciesAllULIDKeyed(); err == nil && alreadyMigrated {
		return nil
	}

	groups := make(map[string]struct{})
	roles := make(map[string]struct{})

	policies, err := s.identityMigrationLoadPolicies(groups, roles)
	if err != nil {
		return err
	}
	groupConfigs, err := s.identityMigrationLoadGroups(groups)
	if err != nil {
		return err
	}
	bindings, err := s.identityMigrationLoadBindings(groups, roles)
	if err != nil {
		return err
	}

	groupNames := identityMigrationSortedNames(groups)
	roleNames := identityMigrationSortedNames(roles)
	logger.Info("identity_migration.start", "groups_total", len(groupNames), "roles_total", len(roleNames))

	groupIDs := make(map[string]string, len(groupNames))
	for _, name := range groupNames {
		if err := identityMigrationContextErr(ctx); err != nil {
			return err
		}
		id, err := identityMigrationResolveOrMint(dispatcher, identity.KindGroup, name)
		if err != nil {
			return fmt.Errorf("identity migration: register group %q: %w", name, err)
		}
		groupIDs[name] = id
		logger.Info("identity_migration.group_minted", "group", name, "id", id)
	}

	roleIDs := make(map[string]string, len(roleNames))
	for _, name := range roleNames {
		if err := identityMigrationContextErr(ctx); err != nil {
			return err
		}
		id, err := identityMigrationResolveOrMint(dispatcher, identity.KindRole, name)
		if err != nil {
			return fmt.Errorf("identity migration: register role %q: %w", name, err)
		}
		roleIDs[name] = id
		logger.Info("identity_migration.role_minted", "role", name, "id", id)
	}

	policiesRewritten, err := s.identityMigrationRewritePolicies(ctx, logger, policies, groupIDs, roleIDs)
	if err != nil {
		return err
	}
	bindingsRewritten, err := s.identityMigrationRewriteBindings(ctx, logger, bindings, groupIDs, roleIDs)
	if err != nil {
		return err
	}
	if err := s.identityMigrationRewriteGroupKeys(ctx, groupConfigs, groupIDs); err != nil {
		return err
	}
	s.identityMigrationRewriteInMemoryGroups(groupConfigs, groupIDs)

	logger.Info("identity_migration.complete",
		"groups_minted", len(groupIDs),
		"roles_minted", len(roleIDs),
		"policies_rewritten", policiesRewritten,
		"bindings_rewritten", bindingsRewritten,
	)
	return nil
}

// identityMigrationPoliciesAllULIDKeyed returns true when every persisted
// agent policy already references groups + role markers by ULID — the
// signal that the rewrite step has nothing left to do for the policy
// surface. Roughly equivalent to the dispatcher-empty check used before
// SetGroup auto-registered names eagerly, but checks the actual policy
// shape so post-migration writes don't trick it into skipping.
func (s *Server) identityMigrationPoliciesAllULIDKeyed() (bool, error) {
	if s.store == nil {
		return true, nil
	}
	keys, err := s.store.List(policyKeyPrefix)
	if err != nil {
		return false, err
	}
	if len(keys) == 0 {
		return true, nil
	}
	for _, key := range keys {
		data, err := s.store.Get(key)
		if err != nil {
			return false, err
		}
		var policy AgentPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			return false, err
		}
		for _, group := range policy.Groups {
			if !identity.IsULID(group) {
				return false, nil
			}
		}
		for _, grant := range policy.Grant {
			if role, ok := strings.CutPrefix(grant, "role:"); ok {
				if !identity.IsULID(role) {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

func (s *Server) identityMigrationLoadPolicies(groups, roles map[string]struct{}) ([]identityMigrationPolicy, error) {
	keys, err := s.store.List(policyKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("identity migration: list agent policies: %w", err)
	}
	sort.Strings(keys)
	policies := make([]identityMigrationPolicy, 0, len(keys))
	for _, key := range keys {
		data, err := s.store.Get(key)
		if err != nil {
			return nil, fmt.Errorf("identity migration: read agent policy %q: %w", key, err)
		}
		var policy AgentPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			return nil, fmt.Errorf("identity migration: decode agent policy %q: %w", key, err)
		}
		for _, group := range policy.Groups {
			identityMigrationAddName(groups, group)
		}
		for _, grant := range policy.Grant {
			if role, ok := strings.CutPrefix(grant, "role:"); ok {
				identityMigrationAddName(roles, role)
			}
		}
		policies = append(policies, identityMigrationPolicy{
			prismID: strings.TrimPrefix(key, policyKeyPrefix),
			policy:  policy,
		})
	}
	return policies, nil
}

func (s *Server) identityMigrationLoadGroups(groups map[string]struct{}) (map[string]GroupConfig, error) {
	keys, err := s.store.List(groupKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("identity migration: list groups: %w", err)
	}
	sort.Strings(keys)
	groupConfigs := make(map[string]GroupConfig, len(keys))
	for _, key := range keys {
		suffix := strings.TrimSpace(strings.TrimPrefix(key, groupKeyPrefix))
		if suffix == "" {
			continue
		}
		// Storage is ULID-keyed post-migration. A ULID suffix means this
		// group has already been moved into its canonical key; nothing
		// for the migration to rewrite. Only legacy name-keyed records
		// drive the mint+rewrite path.
		if identity.IsULID(suffix) {
			continue
		}
		data, err := s.store.Get(key)
		if err != nil {
			return nil, fmt.Errorf("identity migration: read group %q: %w", key, err)
		}
		var cfg GroupConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("identity migration: decode group %q: %w", key, err)
		}
		groups[suffix] = struct{}{}
		groupConfigs[suffix] = cfg
	}
	return groupConfigs, nil
}

func (s *Server) identityMigrationLoadBindings(groups, roles map[string]struct{}) ([]identityMigrationBinding, error) {
	keys, err := s.store.List(grantBindingKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("identity migration: list grant bindings: %w", err)
	}
	sort.Strings(keys)
	bindings := make([]identityMigrationBinding, 0, len(keys))
	for _, key := range keys {
		data, err := s.store.Get(key)
		if err != nil {
			return nil, fmt.Errorf("identity migration: read grant binding %q: %w", key, err)
		}
		var binding auth.GrantBinding
		if err := json.Unmarshal(data, &binding); err != nil {
			return nil, fmt.Errorf("identity migration: decode grant binding %q: %w", key, err)
		}
		for _, group := range binding.Subjects.Groups {
			identityMigrationAddName(groups, group)
		}
		for _, role := range binding.Subjects.Roles {
			identityMigrationAddName(roles, role)
		}
		identityMigrationAddName(roles, binding.Subjects.RoleRequired)
		bindings = append(bindings, identityMigrationBinding{key: key, binding: binding})
	}
	return bindings, nil
}

func (s *Server) identityMigrationRewritePolicies(ctx context.Context, logger *slog.Logger, policies []identityMigrationPolicy, groupIDs, roleIDs map[string]string) (int, error) {
	rewritten := 0
	for _, item := range policies {
		if err := identityMigrationContextErr(ctx); err != nil {
			return rewritten, err
		}
		policy := item.policy
		changed := false
		if next, ok := identityMigrationRewriteNames(policy.Groups, groupIDs); ok {
			policy.Groups = next
			changed = true
		}
		if next, ok := identityMigrationRewriteRoleMarkers(policy.Grant, roleIDs); ok {
			policy.Grant = next
			changed = true
		}
		if !changed {
			continue
		}
		if err := s.SetAgentPolicy(item.prismID, &policy); err != nil {
			return rewritten, fmt.Errorf("identity migration: write agent policy %q: %w", item.prismID, err)
		}
		rewritten++
		logger.Info("identity_migration.policy_rewritten", "prism_id", item.prismID)
	}
	return rewritten, nil
}

func (s *Server) identityMigrationRewriteBindings(ctx context.Context, logger *slog.Logger, bindings []identityMigrationBinding, groupIDs, roleIDs map[string]string) (int, error) {
	rewritten := 0
	for _, item := range bindings {
		if err := identityMigrationContextErr(ctx); err != nil {
			return rewritten, err
		}
		binding := item.binding
		changed := false
		if next, ok := identityMigrationRewriteNames(binding.Subjects.Groups, groupIDs); ok {
			binding.Subjects.Groups = next
			changed = true
		}
		if next, ok := identityMigrationRewriteNames(binding.Subjects.Roles, roleIDs); ok {
			binding.Subjects.Roles = next
			changed = true
		}
		if id, ok := roleIDs[strings.TrimSpace(binding.Subjects.RoleRequired)]; ok {
			binding.Subjects.RoleRequired = id
			changed = true
		}
		if !changed {
			continue
		}
		data, err := json.Marshal(binding)
		if err != nil {
			return rewritten, fmt.Errorf("identity migration: encode grant binding %q: %w", binding.ID, err)
		}
		if err := s.store.Set(item.key, data); err != nil {
			return rewritten, fmt.Errorf("identity migration: write grant binding %q: %w", binding.ID, err)
		}
		rewritten++
		logger.Info("identity_migration.binding_rewritten", "binding_id", binding.ID)
	}
	return rewritten, nil
}

func (s *Server) identityMigrationRewriteGroupKeys(ctx context.Context, groupConfigs map[string]GroupConfig, groupIDs map[string]string) error {
	names := identityMigrationSortedGroupConfigNames(groupConfigs)
	for _, name := range names {
		if err := identityMigrationContextErr(ctx); err != nil {
			return err
		}
		id, ok := groupIDs[name]
		if !ok || id == "" || id == name {
			continue
		}
		data, err := json.Marshal(groupConfigs[name])
		if err != nil {
			return fmt.Errorf("identity migration: encode group %q: %w", name, err)
		}
		if err := s.store.Set(groupKeyPrefix+id, data); err != nil {
			return fmt.Errorf("identity migration: write group id key %q: %w", id, err)
		}
		if err := s.store.Delete(groupKeyPrefix + name); err != nil {
			return fmt.Errorf("identity migration: delete group name key %q: %w", name, err)
		}
	}
	return nil
}

func (s *Server) identityMigrationRewriteInMemoryGroups(groupConfigs map[string]GroupConfig, groupIDs map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, id := range groupIDs {
		cfg, ok := groupConfigs[name]
		if !ok {
			cfg, ok = s.groups[name]
		}
		if ok {
			if s.groups == nil {
				s.groups = make(map[string]GroupConfig)
			}
			if _, exists := s.groups[id]; !exists {
				s.groups[id] = cfg
			}
		}
		delete(s.groups, name)
	}
}

func identityMigrationRewriteNames(values []string, ids map[string]string) ([]string, bool) {
	if len(values) == 0 {
		return values, false
	}
	next := make([]string, len(values))
	changed := false
	for i, value := range values {
		if id, ok := ids[strings.TrimSpace(value)]; ok {
			next[i] = id
			if value != id {
				changed = true
			}
			continue
		}
		next[i] = value
	}
	return next, changed
}

func identityMigrationRewriteRoleMarkers(values []string, roleIDs map[string]string) ([]string, bool) {
	if len(values) == 0 {
		return values, false
	}
	next := make([]string, len(values))
	changed := false
	for i, value := range values {
		role, ok := strings.CutPrefix(value, "role:")
		if !ok {
			next[i] = value
			continue
		}
		if id, ok := roleIDs[strings.TrimSpace(role)]; ok {
			next[i] = "role:" + id
			if value != next[i] {
				changed = true
			}
			continue
		}
		next[i] = value
	}
	return next, changed
}

func identityMigrationAddName(set map[string]struct{}, value string) {
	name := strings.TrimSpace(value)
	if name == "" {
		return
	}
	set[name] = struct{}{}
}

func identityMigrationSortedNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func identityMigrationSortedGroupConfigNames(groupConfigs map[string]GroupConfig) []string {
	names := make([]string, 0, len(groupConfigs))
	for name := range groupConfigs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func identityMigrationNewULID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now().UTC()), rand.Reader)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// identityMigrationResolveOrMint returns the dispatcher-assigned ID for
// (kind, name), minting a new ULID via AllocateWithID when no entry exists.
// Safe to invoke when SetGroup or similar has already eagerly registered
// the name — the existing ID is returned instead of allocating again.
func identityMigrationResolveOrMint(dispatcher identity.Dispatcher, kind identity.Kind, name string) (string, error) {
	if ent, err := dispatcher.ResolveByName(kind, name); err == nil {
		return ent.ID, nil
	}
	id, err := identityMigrationNewULID()
	if err != nil {
		return "", err
	}
	ent, err := dispatcher.AllocateWithID(kind, id, name)
	if err != nil {
		return "", err
	}
	return ent.ID, nil
}

func identityMigrationContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
