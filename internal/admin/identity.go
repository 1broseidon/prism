package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/1broseidon/prism/internal/identity"
)

// identityRequestMaxBytes caps incoming Allocate/Rename payloads. The
// wire shape is two short string fields; 8 KiB leaves comfortable
// headroom while keeping the body small enough to read in memory
// without trouble.
const identityRequestMaxBytes = 8 * 1024

// SetIdentity wires the central identity dispatcher for the admin
// API. Required for the identity endpoints to function — without
// this, GET/POST/PUT/DELETE on /identity routes return 503.
//
// Call site: cmd/prism/main.go after NewAPI, mirroring the
// SetGrantManager / SetAnalytics pattern. The wiring-gap pattern
// (`SetX(d)` separate from `NewAPI`) lets tests substitute a real
// dispatcher without dragging unrelated dependencies into the
// constructor signature.
func (a *API) SetIdentity(d identity.Dispatcher) {
	a.identity = d
}

// handleIdentityRoot dispatches GET /identity (list with ?kind=) and
// POST /identity (allocate). Subroute /identity/{id}[...] is handled
// by handleIdentitySub.
func (a *API) handleIdentityRoot(w http.ResponseWriter, r *http.Request) {
	if a.identity == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "identity dispatcher not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.handleIdentityList(w, r)
	case http.MethodPost:
		a.handleIdentityAllocate(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleIdentitySub dispatches /identity/{id} (GET, DELETE) and
// /identity/{id}/display-name (PUT). The single suffix carved out
// today is display-name; future per-id subroutes (e.g. /refs) plug in
// here.
func (a *API) handleIdentitySub(w http.ResponseWriter, r *http.Request) {
	if a.identity == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "identity dispatcher not available"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/identity/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(rest, "/display-name") {
		id := strings.TrimSuffix(rest, "/display-name")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /identity/{id}/display-name"})
			return
		}
		if r.Method != http.MethodPut {
			w.Header().Set("Allow", "PUT")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		a.handleIdentityRename(w, r, id)
		return
	}
	// Plain /identity/{id}
	if strings.Contains(rest, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown identity subroute"})
		return
	}
	id := rest
	switch r.Method {
	case http.MethodGet:
		a.handleIdentityGet(w, r, id)
	case http.MethodDelete:
		a.handleIdentityDelete(w, r, id)
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// identityListResponse is the JSON shape returned by GET /identity.
// `items` always present (empty array when no entities) so callers
// don't have to nil-check.
type identityListResponse struct {
	Items []identity.Entity `json:"items"`
}

func (a *API) handleIdentityList(w http.ResponseWriter, r *http.Request) {
	kind := identity.Kind(r.URL.Query().Get("kind"))
	if kind == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "?kind= is required (one of agent, group, role, backend)"})
		return
	}
	if !identity.ValidKind(kind) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown kind " + string(kind)})
		return
	}
	items := a.identity.List(kind)
	if items == nil {
		items = []identity.Entity{}
	}
	writeJSON(w, http.StatusOK, identityListResponse{Items: items})
}

func (a *API) handleIdentityGet(w http.ResponseWriter, _ *http.Request, id string) {
	ent, err := a.identity.Resolve(id)
	if err != nil {
		writeJSON(w, identityErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ent)
}

func (a *API) handleIdentityAllocate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, identityRequestMaxBytes)
	var body struct {
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	kind := identity.Kind(body.Kind)
	if !identity.ValidKind(kind) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown kind " + body.Kind})
		return
	}
	ent, err := a.identity.Allocate(kind, body.DisplayName)
	if err != nil {
		writeJSON(w, identityErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, ent)
}

func (a *API) handleIdentityRename(w http.ResponseWriter, r *http.Request, id string) {
	r.Body = http.MaxBytesReader(w, r.Body, identityRequestMaxBytes)
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	ent, err := a.identity.Rename(id, body.DisplayName)
	if err != nil {
		writeJSON(w, identityErrorStatus(err), identityErrorBody(err))
		return
	}
	writeJSON(w, http.StatusOK, ent)
}

func (a *API) handleIdentityDelete(w http.ResponseWriter, _ *http.Request, id string) {
	if err := a.identity.Delete(id); err != nil {
		writeJSON(w, identityErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// identityErrorStatus maps the identity package's sentinel errors to
// HTTP status codes via [errors.Is]. The mapping is:
//
//	ErrNotFound           → 404
//	ErrDisplayNameInUse   → 409
//	ErrInvalidDisplayName → 400
//	ErrInvalidID          → 400
//	ErrKindMismatch       → 400
//	(other)               → 500
func identityErrorStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, identity.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, identity.ErrDisplayNameInUse):
		return http.StatusConflict
	case errors.Is(err, identity.ErrInvalidDisplayName),
		errors.Is(err, identity.ErrInvalidID),
		errors.Is(err, identity.ErrKindMismatch):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func identityErrorBody(err error) map[string]string {
	if errors.Is(err, identity.ErrDisplayNameInUse) {
		return map[string]string{"error": "display_name_in_use"}
	}
	return map[string]string{"error": err.Error()}
}
