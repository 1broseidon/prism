package admin

import "github.com/1broseidon/prism/internal/auth"

// GrantManager is the surface the admin API uses for KV-backed grant
// template + binding CRUD. Implemented in production by
// authserver.Server; tests substitute a thin shim around the same
// authserver to keep optional methods (e.g. ActiveGrantTokenCount)
// behind a feature probe — see the per-call type assertions in
// analytics.go.
//
// The interface deliberately mirrors what the admin handlers call so
// the wiring gap (SetGrantManager separate from NewAPI) stays explicit
// and tests don't need to drag the full authserver into their setup.
type GrantManager interface {
	ListGrantTemplates() []auth.GrantTemplate
	GetGrantTemplate(id string, version int) (auth.GrantTemplate, error)
	GetGrantTemplateByHash(hash string) (auth.GrantTemplate, error)
	SaveGrantTemplate(t auth.GrantTemplate) (auth.GrantTemplate, error)
	DeleteGrantTemplate(id string, version int) error
	ListGrantBindings() []auth.GrantBinding
	GetGrantBinding(id string) (auth.GrantBinding, error)
	SetGrantBinding(b auth.GrantBinding) (auth.GrantBinding, error)
	DeleteGrantBinding(id string) error
}

// SetGrantManager wires the KV-backed grant CRUD surface for the
// admin /grant-templates and /grant-bindings handlers. Without this
// the endpoints return 503; the wiring-gap pattern lets tests
// substitute a shim without dragging the full authserver into their
// setup.
func (a *API) SetGrantManager(m GrantManager) {
	a.grantMgr = m
}
