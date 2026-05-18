package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/1broseidon/prism/internal/openapi"
)

// openAPIRequestBodyLimit caps inbound bodies on the OpenAPI admin endpoints.
// The largest payload is the raw spec under source.file (base64 of up to 5MB
// of YAML/JSON). 16MB leaves comfortable headroom for base64 overhead plus the
// surrounding JSON envelope without giving operators a footgun for arbitrarily
// large uploads.
const openAPIRequestBodyLimit = 16 * 1024 * 1024

// openAPISpecSource is the polymorphic input accepted on every OpenAPI admin
// endpoint that needs to materialize a spec. Exactly one of File or URL must
// be non-empty.
//
// File is the base64-encoded raw spec bytes (JSON or YAML). The handler
// decodes and feeds the raw bytes into the parser, so the parser's size cap
// and external-$ref guard still apply.
//
// URL is fetched server-side through the SSRF-guarded openapi.Fetcher; the
// fetcher refuses loopback, link-local, RFC1918, and unspecified destinations
// unless explicitly allow-listed (operators wire the allowlist at startup, not
// per-request).
type openAPISpecSource struct {
	File string `json:"file,omitempty"`
	URL  string `json:"url,omitempty"`
}

// openAPIPreviewRequest is the body for POST /backends/preview-openapi.
type openAPIPreviewRequest struct {
	Source openAPISpecSource `json:"source"`
}

// openAPISecuritySchemeView is the public projection of a parsed scheme.
// "type" is the OpenAPI security type ("http" or "apiKey"); for bearer
// schemes we surface "bearer" so the UI can pick the right credential form
// without parsing scheme + type both.
type openAPISecuritySchemeView struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
}

// openAPIOperationView is the public projection of a parsed operation. The
// input schema is intentionally omitted from preview — clients showing a
// curation list don't need the full flat schema, and including 500+ schemas
// in one JSON blob would dominate response size.
type openAPIOperationView struct {
	Name        string   `json:"name"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Deprecated  bool     `json:"deprecated,omitempty"`
	Security    []string `json:"security,omitempty"`
	Fingerprint string   `json:"fingerprint"`
}

// openAPISkippedView mirrors openapi.SkippedOperation for JSON.
type openAPISkippedView struct {
	Name   string `json:"name"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// openAPIPreviewResponse is the wire shape returned by preview + reused in
// other OpenAPI responses for consistency.
type openAPIPreviewResponse struct {
	Title           string                      `json:"title"`
	Version         string                      `json:"version"`
	BaseURL         string                      `json:"base_url"`
	SecuritySchemes []openAPISecuritySchemeView `json:"security_schemes"`
	Operations      []openAPIOperationView      `json:"operations"`
	Skipped         []openAPISkippedView        `json:"skipped"`
	SpecWarnings    []string                    `json:"spec_warnings,omitempty"`
}

// openAPISaveRequest is the body for POST /backends/{id} when type=="openapi".
type openAPISaveRequest struct {
	Type            string            `json:"type,omitempty"`
	Source          openAPISpecSource `json:"source"`
	BaseURLOverride string            `json:"base_url_override,omitempty"`
	SecurityScheme  string            `json:"security_scheme,omitempty"`
	Credential      *CredentialConfig `json:"credential,omitempty"`
	DisabledTools   []string          `json:"disabled_tools,omitempty"`
}

// openAPIDiffRequest is the body for POST /backends/{id}/openapi-diff.
type openAPIDiffRequest struct {
	Source openAPISpecSource `json:"source"`
}

// openAPIReimportRequest is the body for POST /backends/{id}/reimport.
type openAPIReimportRequest struct {
	Source                  openAPISpecSource `json:"source"`
	DisabledToolsResolution string            `json:"disabled_tools_resolution,omitempty"`
}

// openAPIDiffEntry identifies an operation by name/method/path.
type openAPIDiffEntry struct {
	Name   string `json:"name"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
}

// openAPIRenameEntry records a fingerprint-matched rename.
type openAPIRenameEntry struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// openAPISignatureChange records a same-name operation whose fingerprint
// changed between revisions.
type openAPISignatureChange struct {
	Name           string `json:"name"`
	OldFingerprint string `json:"old_fingerprint"`
	NewFingerprint string `json:"new_fingerprint"`
}

// openAPIDiffResponse is the wire shape returned by /openapi-diff.
type openAPIDiffResponse struct {
	Added            []openAPIDiffEntry       `json:"added"`
	Removed          []openAPIDiffEntry       `json:"removed"`
	Renamed          []openAPIRenameEntry     `json:"renamed"`
	SignatureChanged []openAPISignatureChange `json:"signature_changed"`
	UnchangedCount   int                      `json:"unchanged_count"`
	NewlySkipped     []openAPISkippedView     `json:"newly_skipped"`
}

// disabledToolsResolution enumerates the supported reimport behaviors.
const (
	disabledToolsResolutionPreserve       = "preserve"
	disabledToolsResolutionDefaultEnabled = "default_enabled"
)

// PersistedOpenAPIBackend is the snapshot the gateway returns when the admin
// API needs to diff or reimport an existing OpenAPI backend. The admin layer
// never persists this structure itself — it is read-only data flowing out of
// the gateway's KV-backed source of truth.
type PersistedOpenAPIBackend struct {
	SpecRaw        []byte
	BaseURL        string
	SecurityScheme string
	SourceURL      string
	DisabledTools  []string
}

// OpenAPISaveParams carries everything the gateway needs to attach a fresh
// OpenAPI backend in one shot. Spec is already parsed by the admin layer —
// the gateway re-uses the parsed result so it doesn't double-parse — but the
// raw bytes are persisted verbatim so a restart re-parses exactly what the
// operator imported.
type OpenAPISaveParams struct {
	Spec            *openapi.Spec
	SpecRaw         []byte
	SourceURL       string
	BaseURLOverride string
	SecurityScheme  string
	Credential      *CredentialConfig
	DisabledTools   []string
}

// OpenAPIReimportParams carries the inputs for an in-place spec swap.
// PreserveDisabled distinguishes the "preserve" resolution (new ops default
// disabled) from "default_enabled" (new ops on). The gateway is responsible
// for translating fingerprint matches into the final disabled-tools list.
type OpenAPIReimportParams struct {
	Spec             *openapi.Spec
	SpecRaw          []byte
	SourceURL        string
	PreserveDisabled bool
}

// OpenAPIBackendManager is the optional interface a BackendManager may
// implement to support OpenAPI-typed backends. The admin layer falls back to
// 503 when the live manager doesn't implement it (e.g. the gateway is built
// without OpenAPI support, or a future split keeps it out of process).
type OpenAPIBackendManager interface {
	// SaveOpenAPIBackend attaches a brand-new OpenAPI backend. Implementations
	// must persist the raw spec bytes, register the credential, register the
	// chosen security scheme, apply the disabled-tools list, and connect the
	// dispatcher in a single transaction.
	SaveOpenAPIBackend(ctx context.Context, id string, params OpenAPISaveParams) error

	// LoadOpenAPIBackend returns the persisted snapshot for an existing
	// OpenAPI-typed backend. Returns an error when the backend doesn't exist
	// or is not OpenAPI-typed.
	LoadOpenAPIBackend(id string) (*PersistedOpenAPIBackend, error)

	// ReimportOpenAPIBackend swaps the persisted spec for an existing OpenAPI
	// backend and re-registers tools without dropping its credential or its
	// per-tool toggles. The credential and security scheme are kept from the
	// previously persisted entry.
	ReimportOpenAPIBackend(ctx context.Context, id string, params OpenAPIReimportParams) error
}

// fetcherFactory lets tests inject a fetcher without going through the real
// SSRF-guarded HTTP client. Production wires the real openapi.NewFetcher.
type fetcherFactory func() *openapi.Fetcher

// defaultFetcherFactory returns the production SSRF-guarded fetcher with no
// host allowlist (operators currently rely on the parser's external-$ref
// guard for tighter control; an explicit allowlist would be exposed via
// network settings in a later task).
func defaultFetcherFactory() *openapi.Fetcher {
	return openapi.NewFetcher(openapi.FetcherConfig{})
}

// resolveOpenAPISource turns the polymorphic source into raw spec bytes plus
// the source URL (empty when File was supplied). Exactly one of File or URL
// must be provided.
func resolveOpenAPISource(ctx context.Context, src openAPISpecSource, fetch fetcherFactory) (raw []byte, sourceURL string, err error) {
	hasFile := strings.TrimSpace(src.File) != ""
	hasURL := strings.TrimSpace(src.URL) != ""
	if hasFile == hasURL {
		if hasFile {
			return nil, "", errors.New("source must specify exactly one of file or url")
		}
		return nil, "", errors.New("source.file or source.url is required")
	}
	if hasFile {
		raw, err = base64.StdEncoding.DecodeString(src.File)
		if err != nil {
			return nil, "", fmt.Errorf("decode base64 file: %w", err)
		}
		return raw, "", nil
	}
	if fetch == nil {
		fetch = defaultFetcherFactory
	}
	f := fetch()
	raw, err = f.Fetch(ctx, src.URL)
	if err != nil {
		return nil, "", err
	}
	return raw, src.URL, nil
}

// parseOpenAPISpecFromSource handles both fetch and parse with consistent
// error wrapping; the returned error is safe to surface verbatim to the
// operator (parser/fetcher errors already include category prefixes like
// "ssrf guard:" and "openapi spec exceeds N byte limit").
func parseOpenAPISpecFromSource(ctx context.Context, src openAPISpecSource, fetch fetcherFactory) (raw []byte, sourceURL string, spec *openapi.Spec, err error) {
	raw, sourceURL, err = resolveOpenAPISource(ctx, src, fetch)
	if err != nil {
		return nil, "", nil, err
	}
	spec, err = openapi.NewParser().Parse(raw)
	if err != nil {
		return nil, "", nil, err
	}
	return raw, sourceURL, spec, nil
}

// projectSpecToPreview converts a parsed Spec into the wire shape returned by
// /preview-openapi. Other endpoints reuse the helpers internally.
func projectSpecToPreview(spec *openapi.Spec) openAPIPreviewResponse {
	resp := openAPIPreviewResponse{
		Title:           spec.Title,
		Version:         spec.Version,
		BaseURL:         spec.BaseURL,
		SecuritySchemes: projectSecuritySchemes(spec.SecuritySchemes),
		Operations:      projectOperations(spec.Operations),
		Skipped:         projectSkipped(spec.Skipped),
		SpecWarnings:    append([]string(nil), spec.Warnings...),
	}
	return resp
}

// projectSecuritySchemes flattens the parser's map into a deterministically
// ordered slice. Sorting by name keeps response bodies stable across runs so
// diffs against UI snapshots stay clean.
func projectSecuritySchemes(in map[string]openapi.SecurityScheme) []openAPISecuritySchemeView {
	if len(in) == 0 {
		return []openAPISecuritySchemeView{}
	}
	out := make([]openAPISecuritySchemeView, 0, len(in))
	for name, scheme := range in {
		view := openAPISecuritySchemeView{Name: name}
		switch {
		case strings.EqualFold(scheme.Type, "http") && strings.EqualFold(scheme.Scheme, "bearer"):
			view.Type = "bearer"
		case strings.EqualFold(scheme.Type, "apiKey"):
			view.Type = "apiKey"
			view.Header = scheme.HeaderName
		default:
			// Unsupported schemes are stripped by the parser, so this only
			// triggers if the parser changes its filter; surface the raw type
			// to make diagnosis obvious instead of silently dropping.
			view.Type = scheme.Type
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func projectOperations(in []openapi.OperationSpec) []openAPIOperationView {
	if len(in) == 0 {
		return []openAPIOperationView{}
	}
	out := make([]openAPIOperationView, 0, len(in))
	for i := range in {
		op := &in[i]
		out = append(out, openAPIOperationView{
			Name:        op.Name,
			Method:      op.Method,
			Path:        op.Path,
			Summary:     op.Summary,
			Tags:        append([]string(nil), op.Tags...),
			Deprecated:  op.Deprecated,
			Security:    append([]string(nil), op.Security...),
			Fingerprint: op.Fingerprint,
		})
	}
	return out
}

func projectSkipped(in []openapi.SkippedOperation) []openAPISkippedView {
	if len(in) == 0 {
		return []openAPISkippedView{}
	}
	out := make([]openAPISkippedView, 0, len(in))
	for _, s := range in {
		out = append(out, openAPISkippedView{
			Name:   s.Name,
			Method: s.Method,
			Path:   s.Path,
			Reason: string(s.Reason),
			Detail: s.Detail,
		})
	}
	return out
}

// handlePreviewOpenAPI implements POST /backends/preview-openapi.
//
// Stateless: never touches KV, never creates a backend, never registers a
// credential. The operator can preview as many specs as they like before
// committing.
func (a *API) handlePreviewOpenAPI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, openAPIRequestBodyLimit)
	var req openAPIPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	_, _, spec, err := parseOpenAPISpecFromSource(r.Context(), req.Source, a.openAPIFetcher)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, projectSpecToPreview(spec))
}

// handleSaveOpenAPIBackend implements POST /backends/{id} when the body
// declares type=="openapi". Mounted by handleBackendPost as a dispatch from
// the existing add-backend route.
//
// the body once and hands ownership to this handler.
//
//nolint:gocritic // req intentionally passed by value; the call site decodes
func (a *API) handleSaveOpenAPIBackend(w http.ResponseWriter, r *http.Request, id string, req openAPISaveRequest) {
	mgr, ok := a.backendMgr.(OpenAPIBackendManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "openapi backends not available"})
		return
	}
	raw, sourceURL, spec, err := parseOpenAPISpecFromSource(r.Context(), req.Source, a.openAPIFetcher)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.SecurityScheme != "" {
		if _, found := spec.SecuritySchemes[req.SecurityScheme]; !found {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("security_scheme %q is not defined in the parsed spec", req.SecurityScheme),
			})
			return
		}
	}
	params := OpenAPISaveParams{
		Spec:            spec,
		SpecRaw:         raw,
		SourceURL:       sourceURL,
		BaseURLOverride: strings.TrimSpace(req.BaseURLOverride),
		SecurityScheme:  req.SecurityScheme,
		Credential:      req.Credential,
		DisabledTools:   append([]string(nil), req.DisabledTools...),
	}
	if err := mgr.SaveOpenAPIBackend(r.Context(), id, params); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":     "ok",
		"id":         id,
		"operations": len(spec.Operations),
		"skipped":    len(spec.Skipped),
	})
}

// handleOpenAPIDiff implements POST /backends/{id}/openapi-diff. Pure read:
// loads the currently persisted spec for {id} and compares against the
// supplied source without writing anything.
func (a *API) handleOpenAPIDiff(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.backendMgr.(OpenAPIBackendManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "openapi backends not available"})
		return
	}
	id, ok := extractBackendIDFromSuffix(r.URL.Path, "/openapi-diff")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backend id"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, openAPIRequestBodyLimit)
	var req openAPIDiffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	current, err := mgr.LoadOpenAPIBackend(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	currentSpec, err := openapi.NewParser().Parse(current.SpecRaw)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "re-parse current spec: " + err.Error(),
		})
		return
	}
	_, _, nextSpec, err := parseOpenAPISpecFromSource(r.Context(), req.Source, a.openAPIFetcher)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, computeOpenAPIDiff(currentSpec, nextSpec))
}

// handleOpenAPIReimport implements POST /backends/{id}/reimport. Applies the
// new spec in place: re-parses, persists, re-registers tools, and resolves
// disabled-tools according to the requested resolution strategy.
func (a *API) handleOpenAPIReimport(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.backendMgr.(OpenAPIBackendManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "openapi backends not available"})
		return
	}
	id, ok := extractBackendIDFromSuffix(r.URL.Path, "/reimport")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backend id"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, openAPIRequestBodyLimit)
	var req openAPIReimportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	resolution := req.DisabledToolsResolution
	if resolution == "" {
		resolution = disabledToolsResolutionPreserve
	}
	if resolution != disabledToolsResolutionPreserve && resolution != disabledToolsResolutionDefaultEnabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "disabled_tools_resolution must be 'preserve' or 'default_enabled'",
		})
		return
	}
	raw, sourceURL, nextSpec, err := parseOpenAPISpecFromSource(r.Context(), req.Source, a.openAPIFetcher)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	params := OpenAPIReimportParams{
		Spec:             nextSpec,
		SpecRaw:          raw,
		SourceURL:        sourceURL,
		PreserveDisabled: resolution == disabledToolsResolutionPreserve,
	}
	if err := mgr.ReimportOpenAPIBackend(r.Context(), id, params); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"id":         id,
		"operations": len(nextSpec.Operations),
		"skipped":    len(nextSpec.Skipped),
	})
}

// extractBackendIDFromSuffix returns the {id} segment when path matches
// "/backends/{id}<suffix>". Returns ("", false) when the path doesn't fit the
// pattern or the id is not a valid identifier.
func extractBackendIDFromSuffix(path, suffix string) (string, bool) {
	const prefix = "/backends/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := path[len(prefix) : len(path)-len(suffix)]
	if !isValidID(id) {
		return "", false
	}
	return id, true
}

// computeOpenAPIDiff is the diff algorithm. Matching strategy:
//  1. Operations whose (name -> fingerprint) is identical in both specs are
//     unchanged.
//  2. Operations whose name is in both specs but fingerprint differs are
//     signature_changed.
//  3. Operations whose name is missing on one side but whose fingerprint
//     matches an unmatched operation on the other side are renamed.
//  4. Remaining new operations are added; remaining old operations are
//     removed.
//
// Skipped operations don't appear in added/removed; newly_skipped is reported
// separately so the UI can warn the operator that an operation they had
// previously curated is now untranslatable (e.g. the upstream switched the
// response to multipart).
//
// helpers obscures the intent more than the branching costs.
//
//nolint:gocyclo // diff is a sequence of independent passes; splitting it into
func computeOpenAPIDiff(prev, next *openapi.Spec) openAPIDiffResponse {
	resp := openAPIDiffResponse{
		Added:            []openAPIDiffEntry{},
		Removed:          []openAPIDiffEntry{},
		Renamed:          []openAPIRenameEntry{},
		SignatureChanged: []openAPISignatureChange{},
		NewlySkipped:     []openAPISkippedView{},
	}

	prevByName := make(map[string]*openapi.OperationSpec, len(prev.Operations))
	for i := range prev.Operations {
		op := &prev.Operations[i]
		prevByName[op.Name] = op
	}
	nextByName := make(map[string]*openapi.OperationSpec, len(next.Operations))
	for i := range next.Operations {
		op := &next.Operations[i]
		nextByName[op.Name] = op
	}

	// Track which operations on each side are still unaccounted for; later
	// passes do fingerprint-based rename matching against these residues.
	unmatchedPrev := make(map[string]*openapi.OperationSpec, len(prev.Operations))
	unmatchedNext := make(map[string]*openapi.OperationSpec, len(next.Operations))

	// Pass 1: same name on both sides.
	for name, op := range nextByName {
		if prevOp, ok := prevByName[name]; ok {
			if prevOp.Fingerprint == op.Fingerprint {
				resp.UnchangedCount++
			} else {
				resp.SignatureChanged = append(resp.SignatureChanged, openAPISignatureChange{
					Name:           name,
					OldFingerprint: prevOp.Fingerprint,
					NewFingerprint: op.Fingerprint,
				})
			}
		} else {
			unmatchedNext[name] = op
		}
	}
	for name, op := range prevByName {
		if _, ok := nextByName[name]; !ok {
			unmatchedPrev[name] = op
		}
	}

	// Pass 2: fingerprint match among unmatched. A fingerprint identifies the
	// (method, path, top-level input keys, response shape) tuple, so an
	// upstream rename — operationId changed but everything else identical —
	// shows up here.
	prevByFingerprint := make(map[string]*openapi.OperationSpec, len(unmatchedPrev))
	for _, op := range unmatchedPrev {
		// If two previously-removed operations share a fingerprint (very rare
		// in practice — would require duplicated paths in the old spec), keep
		// the first; the rest fall through to "removed".
		if _, exists := prevByFingerprint[op.Fingerprint]; !exists {
			prevByFingerprint[op.Fingerprint] = op
		}
	}
	// Stable order for renames so test assertions don't flap.
	nextNames := make([]string, 0, len(unmatchedNext))
	for n := range unmatchedNext {
		nextNames = append(nextNames, n)
	}
	sort.Strings(nextNames)
	for _, name := range nextNames {
		op := unmatchedNext[name]
		if prevOp, ok := prevByFingerprint[op.Fingerprint]; ok {
			resp.Renamed = append(resp.Renamed, openAPIRenameEntry{
				From: prevOp.Name,
				To:   op.Name,
			})
			delete(prevByFingerprint, op.Fingerprint)
			delete(unmatchedPrev, prevOp.Name)
			delete(unmatchedNext, name)
		}
	}

	// Pass 3: anything still unmatched is genuinely added or removed.
	for _, op := range unmatchedNext {
		resp.Added = append(resp.Added, openAPIDiffEntry{
			Name:   op.Name,
			Method: op.Method,
			Path:   op.Path,
		})
	}
	for _, op := range unmatchedPrev {
		resp.Removed = append(resp.Removed, openAPIDiffEntry{
			Name:   op.Name,
			Method: op.Method,
			Path:   op.Path,
		})
	}
	sort.Slice(resp.Added, func(i, j int) bool { return resp.Added[i].Name < resp.Added[j].Name })
	sort.Slice(resp.Removed, func(i, j int) bool { return resp.Removed[i].Name < resp.Removed[j].Name })
	sort.Slice(resp.SignatureChanged, func(i, j int) bool {
		return resp.SignatureChanged[i].Name < resp.SignatureChanged[j].Name
	})
	sort.Slice(resp.Renamed, func(i, j int) bool { return resp.Renamed[i].To < resp.Renamed[j].To })

	// newly_skipped: skipped on the new side that weren't skipped on the old.
	prevSkipped := make(map[string]struct{}, len(prev.Skipped))
	for _, s := range prev.Skipped {
		prevSkipped[s.Name] = struct{}{}
	}
	for _, s := range next.Skipped {
		if _, ok := prevSkipped[s.Name]; ok {
			continue
		}
		resp.NewlySkipped = append(resp.NewlySkipped, openAPISkippedView{
			Name:   s.Name,
			Method: s.Method,
			Path:   s.Path,
			Reason: string(s.Reason),
			Detail: s.Detail,
		})
	}
	sort.Slice(resp.NewlySkipped, func(i, j int) bool { return resp.NewlySkipped[i].Name < resp.NewlySkipped[j].Name })
	return resp
}

// ResolveReimportDisabledTools is the canonical disabled-tools resolver
// shared between the admin layer and the gateway. Both call sites need the
// same logic — admin tests want to assert the outcome without standing up a
// gateway, and the gateway needs the result to persist + apply at reimport
// time.
//
// The algorithm:
//  1. Build the set of new operation names.
//  2. For each operation in prevDisabled, check whether it survived under
//     the same name (still in next operations) — if so, keep it disabled.
//  3. Otherwise check whether it survived under a rename (fingerprint match
//     to a new operation) — if so, disable the new name.
//  4. Drop everything else; a removed operation can't be disabled.
//  5. If preserve is false (default_enabled), the result starts empty
//     regardless of prev — only new ops are evaluated to keep operator
//     intent transparent: explicit opt-out wins per call.
//
// Resolution is independent of admin/gateway state; given prev + next + prev
// disabled list it always produces the same output. That makes it testable in
// isolation.
func ResolveReimportDisabledTools(prev, next *openapi.Spec, prevDisabled []string, preserve bool) []string {
	if !preserve {
		return nil
	}
	if prev == nil || next == nil {
		return nil
	}
	// Index prev by name for fingerprint lookups.
	prevByName := make(map[string]*openapi.OperationSpec, len(prev.Operations))
	for i := range prev.Operations {
		op := &prev.Operations[i]
		prevByName[op.Name] = op
	}
	// Index next ops by name (for survives-by-name) and by fingerprint (for
	// survives-by-rename). Build a "claimed" map so the same new op isn't
	// re-disabled by two different prev names that happen to share its
	// fingerprint.
	nextByName := make(map[string]struct{}, len(next.Operations))
	nextByFingerprint := make(map[string]string, len(next.Operations))
	for i := range next.Operations {
		op := &next.Operations[i]
		nextByName[op.Name] = struct{}{}
		if _, exists := nextByFingerprint[op.Fingerprint]; !exists {
			nextByFingerprint[op.Fingerprint] = op.Name
		}
	}
	out := make([]string, 0, len(prevDisabled))
	seen := make(map[string]struct{}, len(prevDisabled))
	for _, name := range prevDisabled {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		if _, exists := nextByName[name]; exists {
			out = append(out, name)
			seen[name] = struct{}{}
			continue
		}
		prevOp, ok := prevByName[name]
		if !ok {
			continue
		}
		if renamed, ok := nextByFingerprint[prevOp.Fingerprint]; ok {
			if _, dup := seen[renamed]; !dup {
				out = append(out, renamed)
				seen[renamed] = struct{}{}
			}
		}
	}
	sort.Strings(out)
	return out
}
