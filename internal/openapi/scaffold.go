package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// MaxCurlInputBytes is the cap on the raw curl-string input accepted by the
// admin scaffold endpoint. A 32KB ceiling is far more than any sane curl
// invocation, while still preventing abuse of an unauthenticated-shaped JSON
// surface (admin-gated, but still worth bounding).
const MaxCurlInputBytes = 32 * 1024

// CurlHeader is one parsed -H argument. Name preserves the case the operator
// wrote so output stays familiar; canonical comparison happens via
// strings.EqualFold elsewhere.
type CurlHeader struct {
	Name  string
	Value string
}

// CurlCommand is the structured shape of a parsed curl invocation. Body holds
// the raw bytes (BodyKind tells the scaffold step how to interpret them);
// FileBody is true when the operator used `-d @path` and we deliberately did
// not read from the local filesystem.
type CurlCommand struct {
	Method   string
	URL      string
	Headers  []CurlHeader
	Body     []byte
	FileBody bool // -d @filename — we record the reference but skip reading it
}

// ParseCurl turns a curl command string into a CurlCommand. Unrecognized
// flags emit a warning but parsing continues — the goal is "give the operator
// a starting point", not strict conformance with curl's full CLI.
//
// Recognized flags: -X / --request, -H / --header, -d / --data / --data-raw /
// --data-binary / --data-ascii. The positional argument is treated as the URL.
//
// Multi-line continuations (backslash at end of line) are folded before
// tokenizing. Single/double quoting and backslash escapes are honored.
//
// A large switch over curl flag tokens is the clearest way to express the
// parser; splitting it into per-flag helpers would obscure the precedence.
//
//nolint:gocyclo // see comment above
func ParseCurl(input string) (*CurlCommand, []string, error) {
	input = foldContinuations(input)
	tokens, err := tokenizeCurl(input)
	if err != nil {
		return nil, nil, err
	}
	if len(tokens) == 0 {
		return nil, nil, errors.New("curl input is empty")
	}
	// Tolerate a leading "curl" token or "$ curl".
	for len(tokens) > 0 && (tokens[0] == "curl" || tokens[0] == "$" || tokens[0] == "%") {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return nil, nil, errors.New("no command tokens after 'curl'")
	}

	var (
		cmd       CurlCommand
		warnings  []string
		method    string
		urls      []string
		bodyParts [][]byte
	)

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "-X" || tok == "--request":
			if i+1 >= len(tokens) {
				warnings = append(warnings, "flag "+tok+" requires a value")
				continue
			}
			i++
			method = strings.ToUpper(strings.TrimSpace(tokens[i]))
		case strings.HasPrefix(tok, "--request="):
			method = strings.ToUpper(strings.TrimSpace(tok[len("--request="):]))
		case tok == "-H" || tok == "--header":
			if i+1 >= len(tokens) {
				warnings = append(warnings, "flag "+tok+" requires a value")
				continue
			}
			i++
			if h, ok := splitHeader(tokens[i]); ok {
				cmd.Headers = append(cmd.Headers, h)
			} else {
				warnings = append(warnings, "ignoring malformed header: "+tokens[i])
			}
		case strings.HasPrefix(tok, "--header="):
			val := tok[len("--header="):]
			if h, ok := splitHeader(val); ok {
				cmd.Headers = append(cmd.Headers, h)
			} else {
				warnings = append(warnings, "ignoring malformed header: "+val)
			}
		case tok == "-d" || tok == "--data" || tok == "--data-raw" || tok == "--data-binary" || tok == "--data-ascii":
			if i+1 >= len(tokens) {
				warnings = append(warnings, "flag "+tok+" requires a value")
				continue
			}
			i++
			val := tokens[i]
			if strings.HasPrefix(val, "@") {
				cmd.FileBody = true
				warnings = append(warnings, "ignored body file reference "+val+" (replace with literal JSON or edit the generated spec)")
				continue
			}
			bodyParts = append(bodyParts, []byte(val))
		case strings.HasPrefix(tok, "--data="),
			strings.HasPrefix(tok, "--data-raw="),
			strings.HasPrefix(tok, "--data-binary="),
			strings.HasPrefix(tok, "--data-ascii="):
			eq := strings.IndexByte(tok, '=')
			val := tok[eq+1:]
			if strings.HasPrefix(val, "@") {
				cmd.FileBody = true
				warnings = append(warnings, "ignored body file reference "+val)
				continue
			}
			bodyParts = append(bodyParts, []byte(val))
		case tok == "--url":
			if i+1 >= len(tokens) {
				warnings = append(warnings, "flag --url requires a value")
				continue
			}
			i++
			urls = append(urls, tokens[i])
		case strings.HasPrefix(tok, "--url="):
			urls = append(urls, tok[len("--url="):])
		case strings.HasPrefix(tok, "-"):
			// Silent skip for a few common no-arg switches we know are safe.
			if isKnownNoArgFlag(tok) {
				continue
			}
			// Try to swallow the next token as the flag's value when the
			// flag looks like it takes one. We can't always know, so we
			// emit a warning either way.
			if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") && flagLikelyTakesValue(tok) {
				warnings = append(warnings, "unrecognized flag "+tok+" (with value); ignoring")
				i++
			} else {
				warnings = append(warnings, "unrecognized flag "+tok+"; ignoring")
			}
		default:
			urls = append(urls, tok)
		}
	}

	if len(urls) == 0 {
		return nil, warnings, errors.New("no URL found in curl command")
	}
	if len(urls) > 1 {
		warnings = append(warnings, "multiple URLs in curl command; using the first")
	}
	cmd.URL = urls[0]

	if len(bodyParts) > 0 {
		// curl semantics: multiple -d args concatenate with `&` (form-style).
		// For our use case (best-effort scaffold) we preserve whichever the
		// operator typed: if just one chunk, use it verbatim; if multiple, join
		// with newline since concatenating arbitrary JSON with & makes no sense.
		if len(bodyParts) == 1 {
			cmd.Body = bodyParts[0]
		} else {
			cmd.Body = joinBytes(bodyParts, []byte("\n"))
			warnings = append(warnings, "multiple -d arguments joined with newline; review the generated body")
		}
	}

	if method == "" {
		if cmd.Body != nil || cmd.FileBody {
			method = "POST"
		} else {
			method = "GET"
		}
	}
	cmd.Method = method

	return &cmd, warnings, nil
}

// ScaffoldFromCurl converts a parsed CurlCommand into an OpenAPI 3.1 YAML
// stub. The output is intentionally minimal — one path, one operation — and
// designed to slot into the inline editor as a starting point.
//
// Returns (yamlBytes, warnings, error). The warnings slice may be appended to
// the parser's warnings by the caller.
//
// The function is a sequence of independent yaml.Node builders for each
// top-level OpenAPI section; splitting it costs locality more than it gains.
//
//nolint:gocyclo // see comment above
func ScaffoldFromCurl(cmd *CurlCommand) (yamlBytes []byte, warnings []string, err error) {
	if cmd == nil {
		return nil, nil, errors.New("nil curl command")
	}
	u, err := url.Parse(cmd.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, nil, fmt.Errorf("url %q is missing scheme or host", cmd.URL)
	}

	method := strings.ToLower(cmd.Method)
	if method == "" {
		method = "get"
	}

	path := u.Path
	if path == "" {
		path = "/"
	}

	// servers entry: scheme://host (preserve port only when it's non-default
	// for the scheme).
	serverURL := u.Scheme + "://" + u.Host
	if hostPort := u.Port(); hostPort != "" {
		switch {
		case u.Scheme == "http" && hostPort == "80":
			serverURL = u.Scheme + "://" + stripPort(u.Host)
		case u.Scheme == "https" && hostPort == "443":
			serverURL = u.Scheme + "://" + stripPort(u.Host)
		default:
			// keep host as-is (already includes the port)
		}
	}

	title := defaultTitle(u.Hostname())

	// Build the operation object as a YAML mapping. Using yaml.Node lets us
	// control comment placement and ordering precisely.
	operationNode := &yaml.Node{Kind: yaml.MappingNode}

	// summary
	appendMapping(operationNode, "summary", scalar(fmt.Sprintf("%s %s", strings.ToUpper(cmd.Method), path)))

	// operationId — derive from method + path for stability.
	opID := generateName(cmd.Method, path)
	appendMapping(operationNode, "operationId", scalar(opID))

	// description — placeholder comment so the operator knows where to expand.
	desc := scalar("generated from curl; expand with response shapes before using in production")
	appendMapping(operationNode, "description", desc)

	// parameters: query + custom headers (plus path comment).
	paramNodes := buildParameterNodes(u, cmd.Headers, &warnings)

	// Security detection: builds securityScheme entries (component name +
	// definition) and security requirement entries for this operation.
	schemes, secReq, paramAdditions, securityWarnings := detectSecurity(cmd.Headers)
	warnings = append(warnings, securityWarnings...)
	paramNodes = append(paramNodes, paramAdditions...)

	if len(paramNodes) > 0 {
		params := &yaml.Node{Kind: yaml.SequenceNode}
		params.Content = paramNodes
		appendMapping(operationNode, "parameters", params)
	}

	// requestBody when there's a body and it's a verb that takes one.
	if cmd.Body != nil || cmd.FileBody {
		body := buildRequestBody(cmd, &warnings)
		appendMapping(operationNode, "requestBody", body)
	}

	// security requirement for this operation.
	if len(secReq) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, sr := range secReq {
			m := &yaml.Node{Kind: yaml.MappingNode}
			s := &yaml.Node{Kind: yaml.SequenceNode}
			appendMapping(m, sr, s)
			seq.Content = append(seq.Content, m)
		}
		appendMapping(operationNode, "security", seq)
	}

	// responses: a minimal 200 with application/json and an empty object schema.
	responses := &yaml.Node{Kind: yaml.MappingNode}
	resp200 := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(resp200, "description", scalar("successful response"))
	content := &yaml.Node{Kind: yaml.MappingNode}
	mediaType := &yaml.Node{Kind: yaml.MappingNode}
	mediaSchema := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(mediaSchema, "type", scalar("object"))
	appendMapping(mediaSchema, "additionalProperties", scalarBool(true))
	appendMapping(mediaType, "schema", mediaSchema)
	appendMapping(content, "application/json", mediaType)
	appendMapping(resp200, "content", content)
	statusKey := scalar("200")
	statusKey.Style = yaml.DoubleQuotedStyle
	responses.Content = append(responses.Content, statusKey, resp200)
	appendMapping(operationNode, "responses", responses)

	// Build the document root.
	root := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(root, "openapi", scalar("3.1.0"))

	info := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(info, "title", scalar(title))
	versionNode := scalar("1.0")
	versionNode.Style = yaml.DoubleQuotedStyle
	appendMapping(info, "version", versionNode)
	appendMapping(root, "info", info)

	servers := &yaml.Node{Kind: yaml.SequenceNode}
	serverObj := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(serverObj, "url", scalar(serverURL))
	servers.Content = append(servers.Content, serverObj)
	appendMapping(root, "servers", servers)

	// paths
	paths := &yaml.Node{Kind: yaml.MappingNode}
	pathItem := &yaml.Node{Kind: yaml.MappingNode}
	// Insert head comment on the path so operators see how to template ids.
	pathKey := scalar(path)
	pathKey.HeadComment = "path is taken from the curl URL verbatim. Replace numeric/UUID segments\nwith {id}-style templates manually if you want path parameters."
	pathItem.Kind = yaml.MappingNode
	appendMapping(pathItem, method, operationNode)
	paths.Content = append(paths.Content, pathKey, pathItem)
	appendMapping(root, "paths", paths)

	// components.securitySchemes when we detected any.
	if len(schemes) > 0 {
		components := &yaml.Node{Kind: yaml.MappingNode}
		schemesNode := &yaml.Node{Kind: yaml.MappingNode}
		names := make([]string, 0, len(schemes))
		for n := range schemes {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			appendMapping(schemesNode, n, schemes[n])
		}
		appendMapping(components, "securitySchemes", schemesNode)
		appendMapping(root, "components", components)
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal yaml: %w", err)
	}

	if cmd.FileBody {
		warnings = append(warnings, "the curl referenced a @filename body; the generated requestBody uses a placeholder schema")
	}
	return out, warnings, nil
}

// buildParameterNodes turns query params + non-special headers into OpenAPI
// parameter nodes. Special headers (Authorization, Content-Type, Accept,
// known API-key names) are handled separately.
func buildParameterNodes(u *url.URL, headers []CurlHeader, warnings *[]string) []*yaml.Node {
	var out []*yaml.Node
	// Query params: preserve curl's order, but de-dupe by name (curl sends
	// repeated params; OpenAPI parameters keyed by (name, in) — we collapse).
	queryKeys := u.Query()
	keys := make([]string, 0, len(queryKeys))
	for k := range queryKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := queryKeys.Get(k)
		out = append(out, parameterNode("query", k, v, false))
	}
	// Headers: skip the ones we deliberately don't surface as parameters.
	for _, h := range headers {
		if isSpecialHeader(h.Name) {
			continue
		}
		if looksLikeAPIKey(h.Name) {
			// Surfaced as a security scheme instead.
			continue
		}
		out = append(out, parameterNode("header", h.Name, h.Value, true))
	}
	_ = warnings // reserved for future per-param warnings
	return out
}

// parameterNode emits a single Parameter object node. example takes the value
// the operator typed so the YAML is immediately runnable.
func parameterNode(in, name, example string, required bool) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(m, "name", scalar(name))
	appendMapping(m, "in", scalar(in))
	appendMapping(m, "required", scalarBool(required))
	appendMapping(m, "schema", inferScalarSchema(example))
	if example != "" {
		appendMapping(m, "example", scalar(example))
	}
	return m
}

// detectSecurity inspects headers for Authorization: Bearer and API-key style
// headers. Returns:
//   - schemes: map of componentName -> securityScheme node
//   - securityRequirements: list of component names to attach to the operation
//   - extraParams: regular header parameters when an Authorization scheme is
//     unsupported (Basic, custom)
//   - warnings
func detectSecurity(headers []CurlHeader) (schemes map[string]*yaml.Node, requirements []string, extras []*yaml.Node, warnings []string) {
	schemes = map[string]*yaml.Node{}

	for _, h := range headers {
		canonical := strings.ToLower(strings.TrimSpace(h.Name))
		val := strings.TrimSpace(h.Value)
		switch canonical {
		case "authorization":
			if strings.HasPrefix(strings.ToLower(val), "bearer ") || strings.EqualFold(val, "bearer") {
				node := &yaml.Node{Kind: yaml.MappingNode}
				appendMapping(node, "type", scalar("http"))
				appendMapping(node, "scheme", scalar("bearer"))
				schemes["bearerAuth"] = node
				requirements = append(requirements, "bearerAuth")
				continue
			}
			// Other Authorization values (Basic, custom): unsupported by Prism.
			// Emit a header parameter with a comment and note in warnings.
			p := parameterNode("header", h.Name, h.Value, true)
			// Add a comment via head comment on the name node (first child key).
			if len(p.Content) >= 2 {
				p.Content[0].HeadComment = "unsupported auth scheme; v1 of Prism accepts bearer or apiKey-in-header.\nReplace this parameter with a proper securityScheme to integrate."
			}
			extras = append(extras, p)
			warnings = append(warnings, "Authorization header value "+truncateForWarning(val)+" is not a recognized scheme; emitted as a plain header parameter")
		default:
			if looksLikeAPIKey(h.Name) {
				node := &yaml.Node{Kind: yaml.MappingNode}
				appendMapping(node, "type", scalar("apiKey"))
				appendMapping(node, "in", scalar("header"))
				appendMapping(node, "name", scalar(h.Name))
				schemes["apiKeyAuth"] = node
				requirements = append(requirements, "apiKeyAuth")
			}
		}
	}

	// De-dup requirements while keeping order.
	requirements = dedupeStrings(requirements)
	return schemes, requirements, extras, warnings
}

// buildRequestBody constructs a requestBody node from cmd.Body. When the body
// JSON-parses we infer a schema; otherwise we emit a placeholder.
func buildRequestBody(cmd *CurlCommand, warnings *[]string) *yaml.Node {
	body := &yaml.Node{Kind: yaml.MappingNode}
	appendMapping(body, "required", scalarBool(true))

	content := &yaml.Node{Kind: yaml.MappingNode}
	mediaType := &yaml.Node{Kind: yaml.MappingNode}

	if cmd.FileBody || len(cmd.Body) == 0 {
		schema := &yaml.Node{Kind: yaml.MappingNode}
		appendMapping(schema, "type", scalar("object"))
		appendMapping(schema, "additionalProperties", scalarBool(true))
		// Inline doc.
		schema.HeadComment = "placeholder schema; the curl referenced a file or empty body"
		appendMapping(mediaType, "schema", schema)
		appendMapping(content, "application/json", mediaType)
		appendMapping(body, "content", content)
		return body
	}

	trimmed := strings.TrimSpace(string(cmd.Body))
	if trimmed == "" {
		schema := &yaml.Node{Kind: yaml.MappingNode}
		appendMapping(schema, "type", scalar("object"))
		appendMapping(schema, "additionalProperties", scalarBool(true))
		appendMapping(mediaType, "schema", schema)
		appendMapping(content, "application/json", mediaType)
		appendMapping(body, "content", content)
		return body
	}

	var parsed any
	if err := json.Unmarshal(cmd.Body, &parsed); err != nil {
		schema := &yaml.Node{Kind: yaml.MappingNode}
		appendMapping(schema, "type", scalar("object"))
		appendMapping(schema, "additionalProperties", scalarBool(true))
		schema.HeadComment = "could not parse curl body as JSON; replace with the real schema"
		appendMapping(mediaType, "schema", schema)
		example := scalar(trimmed)
		example.Style = yaml.DoubleQuotedStyle
		appendMapping(mediaType, "example", example)
		appendMapping(content, "application/json", mediaType)
		appendMapping(body, "content", content)
		*warnings = append(*warnings, "curl body was not valid JSON; emitted a placeholder schema")
		return body
	}

	schema := inferSchemaFromValue(parsed)
	appendMapping(mediaType, "schema", schema)
	exampleNode, err := jsonValueToYAMLNode(parsed)
	if err == nil {
		appendMapping(mediaType, "example", exampleNode)
	}
	appendMapping(content, "application/json", mediaType)
	appendMapping(body, "content", content)
	return body
}

// inferSchemaFromValue produces a minimal OpenAPI schema for a decoded JSON
// value. Recurses into objects/arrays; for primitives it picks the closest
// scalar type. Per the contract we don't synthesize enum/oneOf from a single
// example — the simplest representation is the goal.
func inferSchemaFromValue(v any) *yaml.Node {
	schema := &yaml.Node{Kind: yaml.MappingNode}
	switch t := v.(type) {
	case map[string]any:
		appendMapping(schema, "type", scalar("object"))
		props := &yaml.Node{Kind: yaml.MappingNode}
		names := make([]string, 0, len(t))
		for n := range t {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			appendMapping(props, n, inferSchemaFromValue(t[n]))
		}
		if len(t) > 0 {
			appendMapping(schema, "properties", props)
		}
	case []any:
		appendMapping(schema, "type", scalar("array"))
		if len(t) > 0 {
			appendMapping(schema, "items", inferSchemaFromValue(t[0]))
		} else {
			items := &yaml.Node{Kind: yaml.MappingNode}
			appendMapping(items, "type", scalar("object"))
			appendMapping(items, "additionalProperties", scalarBool(true))
			appendMapping(schema, "items", items)
		}
	case string:
		appendMapping(schema, "type", scalar("string"))
	case bool:
		appendMapping(schema, "type", scalar("boolean"))
	case float64:
		// json.Unmarshal turns numbers into float64. If the value is an
		// integer, advertise integer; otherwise number.
		if t == float64(int64(t)) {
			appendMapping(schema, "type", scalar("integer"))
		} else {
			appendMapping(schema, "type", scalar("number"))
		}
	case json.Number:
		if _, err := t.Int64(); err == nil {
			appendMapping(schema, "type", scalar("integer"))
		} else {
			appendMapping(schema, "type", scalar("number"))
		}
	case nil:
		// JSON nulls don't pin a type — fall back to object placeholder.
		appendMapping(schema, "type", scalar("object"))
		appendMapping(schema, "nullable", scalarBool(true))
	default:
		appendMapping(schema, "type", scalar("object"))
		appendMapping(schema, "additionalProperties", scalarBool(true))
	}
	return schema
}

// inferScalarSchema produces a minimal schema for a single primitive example
// taken from a header or query value.
func inferScalarSchema(example string) *yaml.Node {
	schema := &yaml.Node{Kind: yaml.MappingNode}
	if example == "" {
		appendMapping(schema, "type", scalar("string"))
		return schema
	}
	if _, err := strconv.ParseInt(example, 10, 64); err == nil {
		appendMapping(schema, "type", scalar("integer"))
		return schema
	}
	if _, err := strconv.ParseFloat(example, 64); err == nil {
		appendMapping(schema, "type", scalar("number"))
		return schema
	}
	if strings.EqualFold(example, "true") || strings.EqualFold(example, "false") {
		appendMapping(schema, "type", scalar("boolean"))
		return schema
	}
	appendMapping(schema, "type", scalar("string"))
	return schema
}

// jsonValueToYAMLNode converts a decoded JSON value into a yaml.Node that
// preserves the original structure as an example.
func jsonValueToYAMLNode(v any) (*yaml.Node, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return node.Content[0], nil
	}
	return &node, nil
}

// --- helpers --------------------------------------------------------------

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v}
}

func scalarBool(v bool) *yaml.Node {
	val := "false"
	if v {
		val = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!bool"}
}

func appendMapping(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content, scalar(key), value)
}

func splitHeader(s string) (CurlHeader, bool) {
	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return CurlHeader{}, false
	}
	name := strings.TrimSpace(s[:idx])
	val := strings.TrimSpace(s[idx+1:])
	if name == "" {
		return CurlHeader{}, false
	}
	return CurlHeader{Name: name, Value: val}, true
}

func foldContinuations(s string) string {
	// Replace `\<newline>` (with optional carriage return) with a single space.
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == '\n' {
				b.WriteByte(' ')
				i += 2
				continue
			}
			if s[i+1] == '\r' && i+2 < len(s) && s[i+2] == '\n' {
				b.WriteByte(' ')
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// tokenizeCurl walks the folded command, honoring single and double quotes.
// Inside double quotes \" and \\ are escaped. Inside single quotes nothing is
// escaped (POSIX single-quote semantics).
//
// linearly; splitting it into helpers would obscure the state machine.
//
//nolint:gocyclo // a single-pass tokenizer with quote-state branches reads
func tokenizeCurl(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	hasContent := false
	flush := func() {
		if !hasContent && cur.Len() == 0 {
			return
		}
		tokens = append(tokens, cur.String())
		cur.Reset()
		hasContent = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
				continue
			}
			cur.WriteByte(c)
			hasContent = true
		case inDouble:
			if c == '\\' && i+1 < len(s) {
				next := s[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					cur.WriteByte(next)
					i++
					hasContent = true
					continue
				}
				cur.WriteByte(c)
				hasContent = true
				continue
			}
			if c == '"' {
				inDouble = false
				continue
			}
			cur.WriteByte(c)
			hasContent = true
		default:
			switch {
			case c == '\'':
				inSingle = true
				hasContent = true
			case c == '"':
				inDouble = true
				hasContent = true
			case c == '\\' && i+1 < len(s):
				// Backslash outside quotes escapes the next char (drop the slash).
				cur.WriteByte(s[i+1])
				i++
				hasContent = true
			case unicode.IsSpace(rune(c)):
				flush()
			default:
				cur.WriteByte(c)
				hasContent = true
			}
		}
	}
	if inSingle || inDouble {
		return nil, errors.New("unterminated quote in curl input")
	}
	flush()
	return tokens, nil
}

// isKnownNoArgFlag returns true for curl switches we can drop without warning.
func isKnownNoArgFlag(tok string) bool {
	switch tok {
	case "-k", "--insecure",
		"-L", "--location",
		"-s", "--silent",
		"-S", "--show-error",
		"-v", "--verbose",
		"-i", "--include",
		"-I", "--head",
		"-f", "--fail",
		"--compressed":
		return true
	}
	return false
}

// flagLikelyTakesValue returns true for a small set of common single-arg
// curl flags so the parser swallows the value rather than treating it as a
// URL. Conservative — when uncertain we leave the next token alone so
// positional URLs aren't accidentally consumed.
func flagLikelyTakesValue(tok string) bool {
	switch tok {
	case "-A", "--user-agent",
		"-b", "--cookie",
		"-c", "--cookie-jar",
		"-e", "--referer",
		"-o", "--output",
		"-u", "--user",
		"--connect-timeout",
		"--max-time",
		"--resolve",
		"-F", "--form",
		"--cacert":
		return true
	}
	return false
}

// isSpecialHeader returns true for headers we don't surface as parameters
// because OpenAPI handles them via other constructs (content-type via
// requestBody.content map; accept via responses.content map).
func isSpecialHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "content-type", "accept", "host", "content-length":
		return true
	}
	return false
}

// looksLikeAPIKey heuristically detects headers that name an API key by
// convention. Conservative: only obvious patterns trigger.
func looksLikeAPIKey(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	if low == "" {
		return false
	}
	candidates := []string{
		"x-api-key",
		"api-key",
		"x-apikey",
		"apikey",
		"x-auth-token",
		"x-access-token",
	}
	for _, c := range candidates {
		if low == c {
			return true
		}
	}
	// Anything starting with "x-" and containing "key" is plausible.
	if strings.HasPrefix(low, "x-") && strings.Contains(low, "key") {
		return true
	}
	return false
}

func defaultTitle(host string) string {
	if host == "" {
		return "Generated API"
	}
	return host + " API"
}

func stripPort(host string) string {
	// IPv6 hosts come bracketed: [::1]:80 — preserve the brackets.
	if strings.HasPrefix(host, "[") {
		if idx := strings.Index(host, "]"); idx >= 0 {
			return host[:idx+1]
		}
	}
	if idx := strings.LastIndexByte(host, ':'); idx >= 0 {
		return host[:idx]
	}
	return host
}

func truncateForWarning(s string) string {
	const limit = 32
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func joinBytes(parts [][]byte, sep []byte) []byte {
	if len(parts) == 0 {
		return nil
	}
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	total += len(sep) * (len(parts) - 1)
	out := make([]byte, 0, total)
	for i, p := range parts {
		if i > 0 {
			out = append(out, sep...)
		}
		out = append(out, p...)
	}
	return out
}
