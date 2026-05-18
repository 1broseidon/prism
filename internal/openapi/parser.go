// Package openapi turns an OpenAPI 3.0 / 3.1 document into a normalized,
// Prism-independent representation. The package has no dependencies on any
// other Prism internal packages so it can be reused (e.g. by the gateway,
// the admin API, and tests) without coupling.
package openapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// MaxSpecBytes is the hard cap on raw spec body size. Bigger inputs are
// rejected by Parse without partial decoding.
const MaxSpecBytes = 5 * 1024 * 1024

// SoftOperationLimit is the threshold above which Parse adds a warning to
// Spec.Warnings. Parsing still continues — operators curate via per-tool
// toggles.
const SoftOperationLimit = 500

// allowedContentType is the only request/response media type Prism v1
// translates to MCP. Operations that lack a JSON variant are skipped.
const allowedContentType = "application/json"

// SkipReason enumerates the reasons an operation may be skipped.
type SkipReason string

// Skip reasons. Stable strings safe to surface in the admin UI.
const (
	SkipReasonUnsupportedRequestContentType  SkipReason = "unsupported_request_content_type"
	SkipReasonUnsupportedResponseContentType SkipReason = "unsupported_response_content_type"
	SkipReasonNoSupportedSecurityScheme      SkipReason = "no_supported_security_scheme"
	SkipReasonParameterNameCollision         SkipReason = "parameter_name_collision"
	SkipReasonMissingResponses               SkipReason = "missing_responses"
)

// SecurityScheme is the normalized form of a supported scheme. Only bearer
// (HTTP) and apiKey-in-header are kept; everything else is dropped at parse
// time per locked epic-1 decisions.
type SecurityScheme struct {
	Name        string // component name (key in components.securitySchemes)
	Type        string // "http" (bearer) or "apiKey"
	Scheme      string // "bearer" when Type == "http"
	In          string // "header" when Type == "apiKey"
	HeaderName  string // populated for apiKey-in-header schemes
	Description string
}

// ParameterLocation enumerates where a parameter is carried on the wire.
// The dispatcher uses it to split a flat MCP argument map back into path,
// query, header, and body slots when issuing the upstream HTTP request.
type ParameterLocation string

// Parameter locations recognized by the dispatcher. "body" covers both the
// synthetic "body" key (non-object request bodies) and each top-level body
// property when the request body is an object.
const (
	ParameterLocationPath   ParameterLocation = "path"
	ParameterLocationQuery  ParameterLocation = "query"
	ParameterLocationHeader ParameterLocation = "header"
	ParameterLocationBody   ParameterLocation = "body"
)

// OperationSpec is one accepted operation. InputSchema is a flat JSON Schema
// covering all params + body fields. Fingerprint is deterministic for the
// same upstream operation shape.
type OperationSpec struct {
	Name        string
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	Deprecated  bool
	InputSchema json.RawMessage
	Security    []string
	Annotations Annotations
	Fingerprint string
	// ParameterLocations maps each top-level input-schema property name to its
	// wire location (path/query/header/body). The flat InputSchema deliberately
	// hides location from MCP clients; the dispatcher needs it back to build
	// the upstream HTTP request.
	ParameterLocations map[string]ParameterLocation
	// BodyIsObject is true when the request body was declared as an object and
	// its top-level properties were merged into the flat schema. When false
	// (and a body exists), the body lives under the synthetic "body" key.
	BodyIsObject bool
	// HasRequestBody is true when the operation declares a request body. The
	// dispatcher uses this to decide whether to send a JSON body at all.
	HasRequestBody bool
}

// Annotations carries MCP-flavored hints derived from the HTTP method. The
// gateway is free to project these into MCP `*Hint` fields or into
// `x-mcp-annotations` on the schema — picker's call.
type Annotations struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

// SkippedOperation records an operation we will not expose, along with a
// stable reason and optional human-readable detail.
type SkippedOperation struct {
	Name   string
	Method string
	Path   string
	Reason SkipReason
	Detail string
}

// Spec is the normalized output of Parse. It is safe to JSON-encode and
// persist alongside the raw bytes.
type Spec struct {
	Title           string
	Version         string
	BaseURL         string
	Operations      []OperationSpec
	Skipped         []SkippedOperation
	Warnings        []string
	SecuritySchemes map[string]SecurityScheme
}

// Parser is the entry point. It is stateless; instances may be reused.
type Parser struct{}

// NewParser constructs a Parser.
func NewParser() *Parser { return &Parser{} }

// ParseWithSource is Parse with a hint about where the spec was fetched from.
// Per OpenAPI 3 §4.7.5, servers[].url may be relative — petstore3's
// "/api/v3" is the canonical example. When sourceURL is non-empty and the
// spec's base URL is relative, we resolve against the source so callers get
// an absolute URL without an operator override.
//
// When sourceURL is empty (file-uploaded specs) and the base URL is relative,
// the spec keeps its raw value and the operator must supply base_url_override.
func (p *Parser) ParseWithSource(data []byte, sourceURL string) (*Spec, error) {
	spec, err := p.Parse(data)
	if err != nil {
		return nil, err
	}
	if sourceURL != "" {
		spec.BaseURL = resolveAgainstSource(spec.BaseURL, sourceURL)
	}
	return spec, nil
}

func resolveAgainstSource(base, source string) string {
	if base == "" {
		// No server entries; fall back to the source URL's origin so the
		// dispatcher has *something* to talk to. Strips any path so we don't
		// concat /openapi.json onto every request.
		src, err := url.Parse(source)
		if err != nil || src.Scheme == "" || src.Host == "" {
			return base
		}
		return src.Scheme + "://" + src.Host
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.IsAbs() {
		return base
	}
	src, err := url.Parse(source)
	if err != nil {
		return base
	}
	return src.ResolveReference(parsed).String()
}

// Parse turns raw spec bytes (JSON or YAML, content-sniffed by kin-openapi)
// into a normalized Spec. Spec-level violations (size, version, external
// refs) return an error. Per-operation issues populate Spec.Skipped.
func (p *Parser) Parse(data []byte) (*Spec, error) {
	if len(data) > MaxSpecBytes {
		return nil, fmt.Errorf("openapi spec exceeds %d byte limit (got %d bytes)", MaxSpecBytes, len(data))
	}
	if len(data) == 0 {
		return nil, errors.New("openapi spec is empty")
	}

	if err := rejectSwagger2(data); err != nil {
		return nil, err
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	// Detach the loader's context — we never make network calls during parsing.
	loader.Context = context.Background()

	doc, err := loader.LoadFromData(data)
	if err != nil {
		if isExternalRefError(err) {
			return nil, fmt.Errorf("external $refs are not allowed: %w", err)
		}
		return nil, fmt.Errorf("parse openapi document: %w", err)
	}

	if err := assertSupportedVersion(doc); err != nil {
		return nil, err
	}

	spec := &Spec{
		SecuritySchemes: map[string]SecurityScheme{},
	}
	if doc.Info != nil {
		spec.Title = doc.Info.Title
		spec.Version = doc.Info.Version
	}
	spec.BaseURL = firstServerURL(doc.Servers)

	spec.SecuritySchemes = collectSecuritySchemes(doc)

	collectOperations(doc, spec)

	if total := len(spec.Operations) + len(spec.Skipped); total > SoftOperationLimit {
		spec.Warnings = append(spec.Warnings,
			fmt.Sprintf("spec defines %d operations (soft limit %d); consider per-tool curation", total, SoftOperationLimit),
		)
	}

	return spec, nil
}

// rejectSwagger2 sniffs for top-level "swagger" key indicating the older
// Swagger 2.0 format, which kin-openapi can parse but we do not want to
// translate.
func rejectSwagger2(data []byte) error {
	// Cheap scan: kin-openapi can also load 2.0 docs, so a positive ID is
	// enough. We avoid full unmarshal for size; only the first few KB.
	probe := data
	if len(probe) > 4096 {
		probe = probe[:4096]
	}
	if bytesContainsToken(probe, []byte("\"swagger\"")) || bytesContainsToken(probe, []byte("swagger:")) {
		return errors.New("swagger 2.0 documents are not supported; OpenAPI 3.0 or 3.1 required")
	}
	return nil
}

// bytesContainsToken reports whether needle appears in haystack. Kept inline
// to avoid pulling in strings.Contains casts.
func bytesContainsToken(haystack, needle []byte) bool {
	return strings.Contains(string(haystack), string(needle))
}

// assertSupportedVersion enforces the OpenAPI 3.0 or 3.1 contract from
// epic-1's locked decisions.
func assertSupportedVersion(doc *openapi3.T) error {
	v := strings.TrimSpace(doc.OpenAPI)
	if v == "" {
		return errors.New("missing openapi version field")
	}
	if !strings.HasPrefix(v, "3.0") && !strings.HasPrefix(v, "3.1") {
		return fmt.Errorf("unsupported openapi version %q (only 3.0 and 3.1 are supported)", v)
	}
	return nil
}

// isExternalRefError detects kin-openapi's external-ref refusal. The library
// returns "found unresolved ref" — we wrap with a clearer message.
func isExternalRefError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unresolved ref") ||
		strings.Contains(msg, "external reference") ||
		strings.Contains(msg, "not allowed")
}

// firstServerURL returns the URL of the first servers[] entry, or "" if none.
func firstServerURL(servers openapi3.Servers) string {
	for _, s := range servers {
		if s == nil {
			continue
		}
		return s.URL
	}
	return ""
}

// collectSecuritySchemes filters Components.SecuritySchemes to the supported
// subset: HTTP bearer and apiKey-in-header. Everything else is dropped here;
// operations that depend exclusively on dropped schemes are skipped during
// operation collection.
func collectSecuritySchemes(doc *openapi3.T) map[string]SecurityScheme {
	out := map[string]SecurityScheme{}
	if doc.Components == nil {
		return out
	}
	for name, ref := range doc.Components.SecuritySchemes {
		if ref == nil || ref.Value == nil {
			continue
		}
		v := ref.Value
		switch {
		case strings.EqualFold(v.Type, "http") && strings.EqualFold(v.Scheme, "bearer"):
			out[name] = SecurityScheme{
				Name:        name,
				Type:        "http",
				Scheme:      "bearer",
				Description: v.Description,
			}
		case strings.EqualFold(v.Type, "apiKey") && strings.EqualFold(v.In, "header"):
			out[name] = SecurityScheme{
				Name:        name,
				Type:        "apiKey",
				In:          "header",
				HeaderName:  v.Name,
				Description: v.Description,
			}
		}
	}
	return out
}

// collectOperations walks every path/method, producing OperationSpec or
// SkippedOperation entries. The output is ordered deterministically by
// (path, method).
func collectOperations(doc *openapi3.T, spec *Spec) {
	type pathOp struct {
		path string
		item *openapi3.PathItem
	}
	if doc.Paths == nil {
		return
	}

	// Sort path keys for determinism.
	pathsMap := doc.Paths.Map()
	paths := make([]pathOp, 0, len(pathsMap))
	for p, item := range pathsMap {
		paths = append(paths, pathOp{p, item})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].path < paths[j].path })

	for _, pi := range paths {
		ops := pi.item.Operations()
		methods := make([]string, 0, len(ops))
		for m := range ops {
			methods = append(methods, m)
		}
		sort.Strings(methods)

		for _, method := range methods {
			op := ops[method]
			processOperation(doc, spec, pi.path, method, pi.item, op)
		}
	}
}

// processOperation evaluates a single operation, deciding accept vs skip,
// and appending the result to spec.
func processOperation(
	doc *openapi3.T,
	spec *Spec,
	path, method string,
	pathItem *openapi3.PathItem,
	op *openapi3.Operation,
) {
	name := operationName(op, method, path)

	// Request content type filter: if there is a request body, it must
	// declare application/json.
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if !hasJSONContent(op.RequestBody.Value.Content) {
			spec.Skipped = append(spec.Skipped, SkippedOperation{
				Name:   name,
				Method: method,
				Path:   path,
				Reason: SkipReasonUnsupportedRequestContentType,
				Detail: contentTypesDetail(op.RequestBody.Value.Content),
			})
			return
		}
	}

	// Response content type filter: at least one 2xx response (or default)
	// with application/json. Responses are required by the spec.
	if op.Responses == nil {
		spec.Skipped = append(spec.Skipped, SkippedOperation{
			Name:   name,
			Method: method,
			Path:   path,
			Reason: SkipReasonMissingResponses,
		})
		return
	}
	if !hasAcceptableResponse(op.Responses) {
		spec.Skipped = append(spec.Skipped, SkippedOperation{
			Name:   name,
			Method: method,
			Path:   path,
			Reason: SkipReasonUnsupportedResponseContentType,
		})
		return
	}

	// Security scheme filter: if the operation specifies a security block,
	// every requirement must be satisfiable by at least one supported
	// scheme. Effective security is op-level if set, else doc-level.
	var effectiveSec openapi3.SecurityRequirements
	switch {
	case op.Security != nil:
		effectiveSec = *op.Security
	case len(doc.Security) > 0:
		effectiveSec = doc.Security
	}
	accepted, fullySkipped := filterSecurity(effectiveSec, spec.SecuritySchemes)
	if fullySkipped {
		spec.Skipped = append(spec.Skipped, SkippedOperation{
			Name:   name,
			Method: method,
			Path:   path,
			Reason: SkipReasonNoSupportedSecurityScheme,
		})
		return
	}

	// Merge path-level + op-level parameters, op-level wins on (name,in).
	params := mergeParameters(pathItem.Parameters, op.Parameters)

	// Build flattened input schema and check for cross-location name
	// collisions.
	built, collision, err := buildInputSchema(params, op.RequestBody)
	if err != nil {
		// Treat schema-build errors as "collision-like": skip with detail.
		spec.Skipped = append(spec.Skipped, SkippedOperation{
			Name:   name,
			Method: method,
			Path:   path,
			Reason: SkipReasonParameterNameCollision,
			Detail: err.Error(),
		})
		return
	}
	if collision != "" {
		spec.Skipped = append(spec.Skipped, SkippedOperation{
			Name:   name,
			Method: method,
			Path:   path,
			Reason: SkipReasonParameterNameCollision,
			Detail: fmt.Sprintf("parameter name %q used in multiple locations", collision),
		})
		return
	}

	annotations := annotationsFor(method)
	schemaWithAnnotations := injectAnnotations(built.Schema, annotations)

	fp := fingerprint(method, path, schemaWithAnnotations, op.Responses)

	spec.Operations = append(spec.Operations, OperationSpec{
		Name:               name,
		Method:             method,
		Path:               path,
		Summary:            op.Summary,
		Description:        op.Description,
		Tags:               append([]string(nil), op.Tags...),
		Deprecated:         op.Deprecated,
		InputSchema:        schemaWithAnnotations,
		Security:           accepted,
		Annotations:        annotations,
		Fingerprint:        fp,
		ParameterLocations: built.Locations,
		BodyIsObject:       built.BodyIsObject,
		HasRequestBody:     built.HasRequestBody,
	})
}

// operationName returns op.OperationID if non-empty, otherwise a generated
// name of the form {method}_{path-with-braces-stripped-and-snake-cased}.
func operationName(op *openapi3.Operation, method, path string) string {
	if op != nil && strings.TrimSpace(op.OperationID) != "" {
		return op.OperationID
	}
	return generateName(method, path)
}

// generateName turns "GET /v1/users/{id}" into "get_v1_users_id".
func generateName(method, path string) string {
	cleaned := strings.ReplaceAll(path, "{", "")
	cleaned = strings.ReplaceAll(cleaned, "}", "")
	segments := strings.Split(cleaned, "/")
	parts := make([]string, 0, len(segments)+1)
	parts = append(parts, strings.ToLower(method))
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		parts = append(parts, snakeCase(seg))
	}
	return strings.Join(parts, "_")
}

// snakeCase lower-cases and converts non-alphanumeric runs to a single
// underscore. The result is ASCII-only; non-ASCII characters become "_".
func snakeCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
			prevUnderscore = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "x"
	}
	return out
}

// hasJSONContent returns true when content contains application/json (with
// or without parameters such as charset).
func hasJSONContent(c openapi3.Content) bool {
	for k := range c {
		if mediaTypeMatches(k, allowedContentType) {
			return true
		}
	}
	return false
}

// mediaTypeMatches checks whether actual is the same media type as want,
// ignoring parameters (e.g. "application/json; charset=utf-8" matches
// "application/json").
func mediaTypeMatches(actual, want string) bool {
	if i := strings.IndexByte(actual, ';'); i >= 0 {
		actual = actual[:i]
	}
	return strings.EqualFold(strings.TrimSpace(actual), want)
}

// contentTypesDetail returns a stable comma-joined list of declared types
// for diagnostic output.
func contentTypesDetail(c openapi3.Content) string {
	if len(c) == 0 {
		return "no content types declared"
	}
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "declared: " + strings.Join(keys, ", ")
}

// hasAcceptableResponse returns true if at least one 2xx response (or the
// default) declares application/json. Operations without any response body
// (e.g. 204 No Content) also pass — they have nothing to translate.
func hasAcceptableResponse(responses *openapi3.Responses) bool {
	// Check 2xx and default. Iterate via the responses map for stability.
	for status, ref := range responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		if !isSuccessOrDefault(status) {
			continue
		}
		// Empty content (e.g. 204) is acceptable.
		if len(ref.Value.Content) == 0 {
			return true
		}
		if hasJSONContent(ref.Value.Content) {
			return true
		}
	}
	return false
}

// isSuccessOrDefault reports whether status is the literal "default" or a
// 2xx HTTP status code.
func isSuccessOrDefault(status string) bool {
	if status == "default" {
		return true
	}
	if len(status) != 3 {
		return false
	}
	return status[0] == '2'
}

// filterSecurity walks the operation's effective security requirements and
// returns (acceptedSchemeNames, fullySkipped). For each OR alternative
// (SecurityRequirement), every named scheme must be present in supported.
// If at least one OR alternative is fully supported, the op is accepted;
// otherwise it is skipped.
//
// When effectiveSec is empty, the op requires no auth and is accepted with
// an empty Security slice.
func filterSecurity(effectiveSec openapi3.SecurityRequirements, supported map[string]SecurityScheme) (accepted []string, fullySkipped bool) {
	if len(effectiveSec) == 0 {
		return nil, false
	}

	seen := map[string]struct{}{}
	for _, req := range effectiveSec {
		// Empty requirement {} means "no auth" — accept.
		if len(req) == 0 {
			return nil, false
		}
		ok := true
		for schemeName := range req {
			if _, found := supported[schemeName]; !found {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for schemeName := range req {
			if _, dup := seen[schemeName]; dup {
				continue
			}
			seen[schemeName] = struct{}{}
			accepted = append(accepted, schemeName)
		}
	}

	if len(accepted) == 0 {
		return nil, true
	}
	sort.Strings(accepted)
	return accepted, false
}

// mergeParameters merges path-level and operation-level parameters. Op-level
// (name, in) replaces path-level. The returned slice is in deterministic
// order (sorted by in then name) so callers see stable output.
func mergeParameters(pathParams, opParams openapi3.Parameters) []*openapi3.Parameter {
	type key struct{ in, name string }
	merged := map[key]*openapi3.Parameter{}

	add := func(ps openapi3.Parameters) {
		for _, ref := range ps {
			if ref == nil || ref.Value == nil {
				continue
			}
			p := ref.Value
			merged[key{p.In, p.Name}] = p
		}
	}
	add(pathParams)
	add(opParams) // overrides

	out := make([]*openapi3.Parameter, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].In != out[j].In {
			return out[i].In < out[j].In
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// annotationsFor derives the MCP-style hints from the HTTP method.
func annotationsFor(method string) Annotations {
	m := strings.ToUpper(method)
	ann := Annotations{OpenWorld: true}
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		ann.ReadOnly = true
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		ann.Destructive = true
	}
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		ann.Idempotent = true
	}
	return ann
}

// fingerprint hashes the operation's stable identity: method + path + sorted
// top-level input keys + response shape digest. Deterministic per locked
// decisions.
func fingerprint(method, path string, inputSchema json.RawMessage, responses *openapi3.Responses) string {
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{'|'})
	h.Write([]byte(path))
	h.Write([]byte{'|'})

	keys := topLevelInputKeys(inputSchema)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{','})
	}
	h.Write([]byte{'|'})

	digest := responseShapeDigest(responses)
	h.Write([]byte(digest))

	return hex.EncodeToString(h.Sum(nil))
}

// topLevelInputKeys returns the sorted property names of the flat input
// schema. Used to keep the fingerprint stable across map iteration order.
func topLevelInputKeys(schema json.RawMessage) []string {
	if len(schema) == 0 {
		return nil
	}
	var probe struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &probe); err != nil {
		return nil
	}
	keys := make([]string, 0, len(probe.Properties))
	for k := range probe.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// responseShapeDigest produces a stable digest of the response shapes:
// sorted (status, json-schema-keys) tuples. We do not hash schema bodies,
// only the top-level structure — full structural diffing is the dispatcher's
// job.
func responseShapeDigest(responses *openapi3.Responses) string {
	if responses == nil {
		return "no-responses"
	}
	type entry struct {
		Status string
		Keys   []string
	}
	entries := make([]entry, 0)
	for status, ref := range responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		var keys []string
		if mt := ref.Value.Content.Get(allowedContentType); mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
			for k := range mt.Schema.Value.Properties {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		entries = append(entries, entry{Status: status, Keys: keys})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Status < entries[j].Status })

	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.Status))
		h.Write([]byte{':'})
		for _, k := range e.Keys {
			h.Write([]byte(k))
			h.Write([]byte{','})
		}
		h.Write([]byte{'|'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
