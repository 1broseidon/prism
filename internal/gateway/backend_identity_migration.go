package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
)

// GrantTemplateRewriter is the grant-store surface needed to rewrite backend
// references from legacy display-name keys to stable backend ULIDs.
type GrantTemplateRewriter interface {
	ListGrantTemplates() []auth.GrantTemplate
	SaveGrantTemplate(t auth.GrantTemplate) (auth.GrantTemplate, error)
	ListGrantBindings() []auth.GrantBinding
	SetGrantBinding(b auth.GrantBinding) (auth.GrantBinding, error)
}

type backendIdentityMigrationConfig struct {
	key   string
	oldID string
	pb    persistedBackend
}

type backendIdentityTemplateRewrite struct {
	newHash       string
	newTemplateID string
}

// SetIdentityDispatcher wires the shared identity registry used by backend
// boot-time migrations.
func (g *Gateway) SetIdentityDispatcher(dispatcher identity.Dispatcher) {
	g.identityDispatcher = dispatcher
}

// RunBackendIdentityMigration promotes persisted backend keys from legacy
// operator-chosen names into stable backend ULIDs, then rewrites grant
// templates and bindings that referenced the legacy backend IDs.
func (g *Gateway) RunBackendIdentityMigration(ctx context.Context, logger *slog.Logger, grantMgr GrantTemplateRewriter) error {
	if g == nil || g.kvStore == nil {
		return nil
	}
	if err := backendIdentityMigrationContextErr(ctx); err != nil {
		return err
	}
	if logger == nil {
		logger = g.logger
	}
	if logger == nil {
		logger = slog.Default()
	}
	if g.identityDispatcher == nil {
		return fmt.Errorf("backend identity migration: identity dispatcher not configured")
	}

	dispatcher := g.identityDispatcher
	if len(dispatcher.List(identity.KindBackend)) > 0 {
		return nil
	}

	items, err := g.backendIdentityMigrationLoadBackends()
	if err != nil {
		return err
	}
	if len(items) > 0 && grantMgr == nil {
		return fmt.Errorf("backend identity migration: grant template rewriter not configured")
	}

	logger.Info("backend_identity_migration.start", "backends_total", len(items))

	oldIDtoULID := make(map[string]string, len(items))
	for _, item := range items {
		if err := backendIdentityMigrationContextErr(ctx); err != nil {
			return err
		}
		ent, err := dispatcher.Allocate(identity.KindBackend, item.oldID)
		if err != nil {
			return fmt.Errorf("backend identity migration: allocate backend %q: %w", item.oldID, err)
		}
		pb := item.pb
		pb.DisplayName = item.oldID
		data, err := json.Marshal(&pb)
		if err != nil {
			return fmt.Errorf("backend identity migration: encode backend %q: %w", item.oldID, err)
		}
		if err := g.kvStore.Set(backendKVPrefix+ent.ID, data); err != nil {
			return fmt.Errorf("backend identity migration: write backend id key %q: %w", ent.ID, err)
		}
		if err := g.kvStore.Delete(item.key); err != nil {
			return fmt.Errorf("backend identity migration: delete backend name key %q: %w", item.oldID, err)
		}
		oldIDtoULID[item.oldID] = ent.ID
		logger.Info("backend_identity_migration.backend_minted", "old_id", item.oldID, "new_ulid", ent.ID)
	}

	g.backendIdentityMigrationRewriteInMemory(oldIDtoULID)

	oldHashToNew, templatesRewritten, err := backendIdentityMigrationRewriteTemplates(ctx, logger, grantMgr, oldIDtoULID)
	if err != nil {
		return err
	}
	bindingsRepointed, err := backendIdentityMigrationRepointBindings(ctx, logger, grantMgr, oldHashToNew)
	if err != nil {
		return err
	}

	logger.Info("backend_identity_migration.complete",
		"backends_minted", len(oldIDtoULID),
		"templates_rewritten", templatesRewritten,
		"bindings_repointed", bindingsRepointed,
	)
	return nil
}

func (g *Gateway) backendIdentityMigrationLoadBackends() ([]backendIdentityMigrationConfig, error) {
	keys, err := g.kvStore.List(backendKVPrefix)
	if err != nil {
		return nil, fmt.Errorf("backend identity migration: list backends: %w", err)
	}
	sort.Strings(keys)

	items := make([]backendIdentityMigrationConfig, 0, len(keys))
	for _, key := range keys {
		oldID := strings.TrimSpace(strings.TrimPrefix(key, backendKVPrefix))
		if oldID == "" {
			continue
		}
		data, err := g.kvStore.Get(key)
		if err != nil {
			return nil, fmt.Errorf("backend identity migration: read backend %q: %w", key, err)
		}
		var pb persistedBackend
		if err := json.Unmarshal(data, &pb); err != nil {
			return nil, fmt.Errorf("backend identity migration: decode backend %q: %w", key, err)
		}
		items = append(items, backendIdentityMigrationConfig{
			key:   key,
			oldID: oldID,
			pb:    pb,
		})
	}
	return items, nil
}

func (g *Gateway) backendIdentityMigrationRewriteInMemory(oldIDtoULID map[string]string) {
	if len(oldIDtoULID) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, backend := range g.backends {
		if backend == nil || backend.Config == nil {
			continue
		}
		oldID := backend.Config.ID
		newID, ok := oldIDtoULID[oldID]
		if !ok {
			continue
		}
		backend.Config.ID = newID
		backend.Config.DisplayName = oldID
		backend.DisplayName = oldID
		delete(g.backends, key)
		g.backends[newID] = backend
	}
}

func backendIdentityMigrationRewriteTemplates(ctx context.Context, logger *slog.Logger, grantMgr GrantTemplateRewriter, oldIDtoULID map[string]string) (map[string]backendIdentityTemplateRewrite, int, error) {
	oldHashToNew := make(map[string]backendIdentityTemplateRewrite)
	if len(oldIDtoULID) == 0 || grantMgr == nil {
		return oldHashToNew, 0, nil
	}

	rewritten := 0
	templates := grantMgr.ListGrantTemplates()
	for _, tmpl := range templates {
		if err := backendIdentityMigrationContextErr(ctx); err != nil {
			return oldHashToNew, rewritten, err
		}
		newBackend, ok := oldIDtoULID[tmpl.Spec.Backend]
		if !ok {
			continue
		}
		next := tmpl
		next.Spec.Backend = newBackend
		saved, err := grantMgr.SaveGrantTemplate(next)
		if err != nil {
			return oldHashToNew, rewritten, fmt.Errorf("backend identity migration: rewrite grant template %q: %w", tmpl.ID, err)
		}
		oldHashToNew[tmpl.Hash] = backendIdentityTemplateRewrite{
			newHash:       saved.Hash,
			newTemplateID: saved.ID,
		}
		rewritten++
		logger.Info("backend_identity_migration.template_rewritten",
			"template_id", tmpl.ID,
			"old_hash", tmpl.Hash,
			"new_hash", saved.Hash,
			"old_backend", tmpl.Spec.Backend,
			"new_backend", saved.Spec.Backend,
		)
	}
	return oldHashToNew, rewritten, nil
}

func backendIdentityMigrationRepointBindings(ctx context.Context, logger *slog.Logger, grantMgr GrantTemplateRewriter, oldHashToNew map[string]backendIdentityTemplateRewrite) (int, error) {
	if len(oldHashToNew) == 0 || grantMgr == nil {
		return 0, nil
	}

	repointed := 0
	for _, binding := range grantMgr.ListGrantBindings() {
		if err := backendIdentityMigrationContextErr(ctx); err != nil {
			return repointed, err
		}
		rewrite, ok := oldHashToNew[binding.TemplateHash]
		if !ok {
			continue
		}
		oldHash := binding.TemplateHash
		binding.TemplateHash = rewrite.newHash
		if rewrite.newTemplateID != "" {
			binding.TemplateID = rewrite.newTemplateID
		}
		if _, err := grantMgr.SetGrantBinding(binding); err != nil {
			return repointed, fmt.Errorf("backend identity migration: repoint grant binding %q: %w", binding.ID, err)
		}
		repointed++
		logger.Info("backend_identity_migration.binding_repointed",
			"binding_id", binding.ID,
			"old_hash", oldHash,
			"new_hash", binding.TemplateHash,
			"template_id", binding.TemplateID,
		)
	}
	return repointed, nil
}

func backendIdentityMigrationContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
