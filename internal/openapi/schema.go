package openapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// schemaBuilder accumulates flattened property entries while tracking each
// property's source location for collision detection.
type schemaBuilder struct {
	props     map[string]json.RawMessage
	required  map[string]bool
	owner     map[string]string
	locations map[string]ParameterLocation
}

func newSchemaBuilder() *schemaBuilder {
	return &schemaBuilder{
		props:     map[string]json.RawMessage{},
		required:  map[string]bool{},
		owner:     map[string]string{},
		locations: map[string]ParameterLocation{},
	}
}

// add returns the colliding property name (empty when none).
func (b *schemaBuilder) add(name, source string, schema json.RawMessage, isRequired bool) string {
	if existing, dup := b.owner[name]; dup && existing != source {
		return name
	}
	b.owner[name] = source
	b.props[name] = schema
	if isRequired {
		b.required[name] = true
	}
	b.locations[name] = locationForSource(source)
	return ""
}

// locationForSource normalizes the schema-builder "source" tag into the
// dispatcher-facing ParameterLocation enum.
func locationForSource(source string) ParameterLocation {
	switch source {
	case "path":
		return ParameterLocationPath
	case "query":
		return ParameterLocationQuery
	case "header":
		return ParameterLocationHeader
	case "body":
		return ParameterLocationBody
	default:
		return ParameterLocation(source)
	}
}

// inputSchemaBuild is the structured result of flattening an operation's
// parameters and body into a single JSON Schema. The dispatcher reads
// Locations to split flat MCP arguments back into path/query/header/body
// slots; BodyIsObject tells it whether to expect a synthetic "body" key.
type inputSchemaBuild struct {
	Schema         json.RawMessage
	Locations      map[string]ParameterLocation
	BodyIsObject   bool
	HasRequestBody bool
}

// buildInputSchema flattens an operation's parameters and body schema into a
// single top-level JSON Schema object. Parameter sources (path/query/header)
// share the top-level namespace; the body schema's top-level properties (if
// the body is an object) are merged in alongside them. If the body is not
// an object, it is included under the synthetic key "body".
//
// The function returns the build result, an optional collision key (empty
// when none), and an error for malformed inputs. A non-empty collision key
// tells the caller the operation must be skipped with
// SkipReasonParameterNameCollision.
func buildInputSchema(params []*openapi3.Parameter, bodyRef *openapi3.RequestBodyRef) (inputSchemaBuild, string, error) {
	b := newSchemaBuilder()

	if collision, err := addParams(b, params); err != nil || collision != "" {
		return inputSchemaBuild{}, collision, err
	}
	bodyIsObject, hasBody, collision, err := addBody(b, bodyRef)
	if err != nil || collision != "" {
		return inputSchemaBuild{}, collision, err
	}

	raw, err := b.marshal()
	if err != nil {
		return inputSchemaBuild{}, "", err
	}
	return inputSchemaBuild{
		Schema:         raw,
		Locations:      b.locations,
		BodyIsObject:   bodyIsObject,
		HasRequestBody: hasBody,
	}, "", nil
}

func addParams(b *schemaBuilder, params []*openapi3.Parameter) (string, error) {
	for _, p := range params {
		if p == nil || p.Name == "" {
			continue
		}
		// "cookie" params are not exposed; skipping them silently is the
		// least-surprise default (epic-1: bearer + apiKey-in-header only).
		if strings.EqualFold(p.In, "cookie") {
			continue
		}
		schema, err := paramToSchema(p)
		if err != nil {
			return "", err
		}
		if c := b.add(p.Name, p.In, schema, p.Required); c != "" {
			return c, nil
		}
	}
	return "", nil
}

// addBody merges the request body into the flat input schema.
// bodyIsObject is true when the body is a JSON object whose properties were
// hoisted up alongside path/query/header. hasBody is true whenever the
// operation declares a request body — even one without an application/json
// variant — so the dispatcher knows to expect a body payload from callers.
// collisionKey is the first property name that collides with an existing
// param (empty when none); err surfaces malformed input.
func addBody(b *schemaBuilder, bodyRef *openapi3.RequestBodyRef) (bodyIsObject, hasBody bool, collisionKey string, err error) {
	if bodyRef == nil || bodyRef.Value == nil {
		return false, false, "", nil
	}
	mt := bodyRef.Value.Content.Get(allowedContentType)
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		return false, true, "", nil
	}
	s := mt.Schema.Value
	if !isObjectSchema(s) {
		bodySchema, err := schemaRefToJSON(mt.Schema)
		if err != nil {
			return false, true, "", err
		}
		return false, true, b.add("body", "body", bodySchema, bodyRef.Value.Required), nil
	}
	reqSet := map[string]bool{}
	for _, r := range s.Required {
		reqSet[r] = true
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sub := s.Properties[n]
		subSchema, err := schemaRefToJSON(sub)
		if err != nil {
			return true, true, "", err
		}
		if c := b.add(n, "body", subSchema, reqSet[n]); c != "" {
			return true, true, c, nil
		}
	}
	return true, true, "", nil
}

func (b *schemaBuilder) marshal() (json.RawMessage, error) {
	out := struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required,omitempty"`
	}{
		Type:       "object",
		Properties: b.props,
	}
	if len(b.required) > 0 {
		req := make([]string, 0, len(b.required))
		for k := range b.required {
			req = append(req, k)
		}
		sort.Strings(req)
		out.Required = req
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal input schema: %w", err)
	}
	return raw, nil
}

// paramToSchema returns the JSON Schema for a single parameter, falling back
// to a string type when none is declared.
func paramToSchema(p *openapi3.Parameter) (json.RawMessage, error) {
	if p.Schema != nil {
		return schemaRefToJSON(p.Schema)
	}
	if mt := p.Content.Get(allowedContentType); mt != nil && mt.Schema != nil {
		return schemaRefToJSON(mt.Schema)
	}
	// Fallback: untyped string parameter.
	return json.Marshal(map[string]string{"type": "string"})
}

// schemaRefToJSON marshals a kin-openapi SchemaRef to JSON. After parsing
// with refs resolved, ref.Value is the authoritative form — we serialize
// that directly so consumers get plain JSON Schema without $ref pointers.
func schemaRefToJSON(ref *openapi3.SchemaRef) (json.RawMessage, error) {
	if ref == nil {
		return json.RawMessage(`{}`), nil
	}
	if ref.Value == nil {
		// Defensive: kin-openapi should have resolved internal refs by now.
		return nil, fmt.Errorf("unresolved schema ref %q", ref.Ref)
	}
	data, err := ref.Value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return data, nil
}

// isObjectSchema reports whether the schema is unambiguously an object.
// OpenAPI 3.0 uses `type: "object"`; 3.1 may use a list. We treat schemas
// that declare any non-object type as non-objects.
func isObjectSchema(s *openapi3.Schema) bool {
	if s == nil {
		return false
	}
	if s.Type == nil || len(*s.Type) == 0 {
		// No type declared; if properties are present treat as object.
		return len(s.Properties) > 0
	}
	for _, t := range *s.Type {
		if t == openapi3.TypeObject {
			return true
		}
	}
	return false
}

// injectAnnotations adds an "x-mcp-annotations" object to the top-level
// schema. Callers can pluck it back out at registration time. Emitting
// inside the schema keeps the OperationSpec.InputSchema self-contained.
func injectAnnotations(schema json.RawMessage, ann Annotations) json.RawMessage {
	if len(schema) == 0 {
		return schema
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(schema, &m); err != nil {
		return schema
	}
	annJSON, err := json.Marshal(map[string]bool{
		"readOnly":    ann.ReadOnly,
		"destructive": ann.Destructive,
		"idempotent":  ann.Idempotent,
		"openWorld":   ann.OpenWorld,
	})
	if err != nil {
		return schema
	}
	m["x-mcp-annotations"] = annJSON

	// Re-marshal with sorted keys for stability.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// json.Marshal on a map already sorts keys, so just marshal the map.
	out, err := json.Marshal(m)
	if err != nil {
		return schema
	}
	return out
}
