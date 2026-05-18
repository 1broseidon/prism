package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/openapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// openapiResponseLimit is the hard cap on a single OpenAPI response body
// returned to the calling agent. Bodies bigger than this are truncated with a
// trailing footer so the agent can still see (and reason about) the response
// shape without blowing up the MCP transport.
const openapiResponseLimit = 32 * 1024

// openapiDefaultTimeout is the per-call HTTP client timeout. Operators may
// pre-build a *http.Client with a different timeout; the dispatcher honors it.
const openapiDefaultTimeout = 30 * time.Second

// OpenAPICredResolver returns the (header, value) credential to inject on
// each upstream call. Returning ("", "") leaves the request unauthenticated.
// The resolver runs per-call so dynamic credentials (env, command, OAuth) all
// behave consistently. The injected header always takes precedence over any
// header that callers may have set via flat args.
type OpenAPICredResolver func(ctx context.Context) (header string, value string)

// OpenAPIDispatcher routes a parsed *openapi.Spec into HTTP requests against
// the operation's upstream baseURL. It is one of the implementations of
// ToolDispatcher; the gateway's policy/audit stack still runs above it.
type OpenAPIDispatcher struct {
	spec           *openapi.Spec
	baseURL        string
	securityScheme string
	httpClient     *http.Client
	credResolver   OpenAPICredResolver
	logger         *slog.Logger
	tools          []BackendToolInfo

	// byName indexes operations by their MCP tool name (un-namespaced) for
	// O(1) Dispatch lookup. Built once at construction time.
	byName map[string]*openapi.OperationSpec
}

// OpenAPIDispatcherOptions wires non-required dependencies. Zero values pick
// safe defaults: a new http.Client with the 30s timeout, no logger, and no
// credential injection.
type OpenAPIDispatcherOptions struct {
	HTTPClient   *http.Client
	CredResolver OpenAPICredResolver
	Logger       *slog.Logger
}

// NewOpenAPIDispatcher constructs a dispatcher for spec. The baseURL override
// wins over spec.BaseURL when non-empty (operators frequently swap test/prod
// hosts at attach time). securityScheme picks one of spec.SecuritySchemes for
// credential injection; pass "" when the spec has no auth.
func NewOpenAPIDispatcher(spec *openapi.Spec, baseURL, securityScheme string, opts OpenAPIDispatcherOptions) (*OpenAPIDispatcher, error) {
	if spec == nil {
		return nil, errors.New("openapi spec is required")
	}
	effectiveBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if effectiveBase == "" {
		effectiveBase = strings.TrimRight(strings.TrimSpace(spec.BaseURL), "/")
	}
	if effectiveBase == "" {
		return nil, errors.New("openapi spec has no base URL and no override was provided")
	}
	if securityScheme != "" {
		if _, ok := spec.SecuritySchemes[securityScheme]; !ok {
			return nil, fmt.Errorf("openapi security scheme %q is not defined in the spec", securityScheme)
		}
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: openapiDefaultTimeout}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	d := &OpenAPIDispatcher{
		spec:           spec,
		baseURL:        effectiveBase,
		securityScheme: securityScheme,
		httpClient:     client,
		credResolver:   opts.CredResolver,
		logger:         logger,
		byName:         make(map[string]*openapi.OperationSpec, len(spec.Operations)),
	}
	for i := range spec.Operations {
		op := &spec.Operations[i]
		d.byName[op.Name] = op
	}
	return d, nil
}

// SetTools records the namespaced tool metadata exposed via the gateway. The
// gateway calls this after AddTool so Status() can list the operations the
// same way it lists MCP tools.
func (d *OpenAPIDispatcher) SetTools(tools []BackendToolInfo) {
	if d == nil {
		return
	}
	d.tools = tools
}

// Tools satisfies ToolDispatcher.
func (d *OpenAPIDispatcher) Tools() []BackendToolInfo {
	if d == nil {
		return nil
	}
	return d.tools
}

// Spec returns the underlying parsed spec for callers that need to introspect
// operations (e.g. tool registration in ConnectOpenAPIBackend).
func (d *OpenAPIDispatcher) Spec() *openapi.Spec {
	if d == nil {
		return nil
	}
	return d.spec
}

// BaseURL returns the effective base URL used for upstream calls.
func (d *OpenAPIDispatcher) BaseURL() string {
	if d == nil {
		return ""
	}
	return d.baseURL
}

// SecurityScheme returns the security scheme name (if any) used for credential
// injection.
func (d *OpenAPIDispatcher) SecurityScheme() string {
	if d == nil {
		return ""
	}
	return d.securityScheme
}

// Dispatch satisfies ToolDispatcher. It executes one upstream HTTP call,
// translating the result into an MCP CallToolResult. Network failures are
// always returned as IsError CallToolResults (never as Go errors) so the
// gateway's circuit breaker and audit pipeline see a consistent shape.
func (d *OpenAPIDispatcher) Dispatch(ctx context.Context, toolName string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	if d == nil {
		return nil, errors.New("openapi dispatcher is not initialized")
	}
	op, ok := d.byName[toolName]
	if !ok {
		return errorResult(fmt.Sprintf("openapi dispatcher: operation %q not found", toolName)), nil
	}

	args, err := decodeArgs(arguments)
	if err != nil {
		return errorResult(fmt.Sprintf("openapi dispatcher: parse arguments: %v", err)), nil
	}

	req, err := d.buildRequest(ctx, op, args)
	if err != nil {
		return errorResult(fmt.Sprintf("openapi dispatcher: build request: %v", err)), nil
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		reason := classifyHTTPError(err)
		return errorResult(fmt.Sprintf("openapi dispatcher: %s: %v", reason, err)), nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, truncated, totalSize, readErr := readWithCap(resp.Body, openapiResponseLimit)
	if readErr != nil {
		return errorResult(fmt.Sprintf("openapi dispatcher: read response: %v", readErr)), nil
	}

	contentType := resp.Header.Get("Content-Type")
	header := fmt.Sprintf("HTTP %d %s · %s · %d bytes", resp.StatusCode, http.StatusText(resp.StatusCode), contentType, totalSize)

	var sb strings.Builder
	sb.Grow(len(header) + 2 + len(body) + 64)
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.Write(body)
	if truncated {
		fmt.Fprintf(&sb, "\n\n...response truncated (showed %d of %d bytes)", len(body), totalSize)
	}

	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}
	if resp.StatusCode >= 400 {
		result.IsError = true
	}
	return result, nil
}

// decodeArgs unmarshals raw MCP arguments into a generic map. An empty or
// missing payload is treated as an empty argument set so dispatchers don't
// reject simple "no-arg" calls.
func decodeArgs(arguments json.RawMessage) (map[string]any, error) {
	if len(arguments) == 0 {
		return map[string]any{}, nil
	}
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// buildRequest splits flat args by parameter location, substitutes path vars,
// marshals the JSON body, and injects credentials. Path/query/header
// substitution uses the parser's side map; body fields are everything tagged
// "body" (or the synthetic "body" key for non-object bodies).
func (d *OpenAPIDispatcher) buildRequest(ctx context.Context, op *openapi.OperationSpec, args map[string]any) (*http.Request, error) {
	pathArgs, queryArgs, headerArgs, bodyArgs, err := splitArgsByLocation(op, args)
	if err != nil {
		return nil, err
	}

	endpoint, err := d.buildEndpoint(op, pathArgs, queryArgs)
	if err != nil {
		return nil, err
	}

	bodyReader, hasBody, err := buildBodyReader(op, bodyArgs)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(op.Method), endpoint, bodyReader)
	if err != nil {
		return nil, err
	}
	d.applyRequestHeaders(ctx, req, headerArgs, hasBody)
	return req, nil
}

// buildEndpoint substitutes path variables and merges query args onto the
// dispatcher's base URL. Pulled out of buildRequest to keep cyclomatic
// complexity in check.
func (d *OpenAPIDispatcher) buildEndpoint(op *openapi.OperationSpec, pathArgs, queryArgs map[string]any) (string, error) {
	urlPath, err := substitutePathVars(op.Path, pathArgs)
	if err != nil {
		return "", err
	}
	endpoint := d.baseURL + urlPath
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	if len(queryArgs) == 0 {
		return parsed.String(), nil
	}
	q := parsed.Query()
	for name, val := range queryArgs {
		for _, encoded := range queryValuesFor(val) {
			q.Add(name, encoded)
		}
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// buildBodyReader marshals the JSON body payload (when the spec declares
// one) and reports whether a body is actually present. Empty bodies return a
// nil reader so the upstream request omits the body and Content-Type.
func buildBodyReader(op *openapi.OperationSpec, bodyArgs map[string]any) (io.Reader, bool, error) {
	if !op.HasRequestBody {
		return nil, false, nil
	}
	payload, err := encodeBodyPayload(op, bodyArgs)
	if err != nil {
		return nil, false, err
	}
	if payload == nil {
		return nil, false, nil
	}
	return bytes.NewReader(payload), true, nil
}

// applyRequestHeaders sets the static Accept/Content-Type pair, copies any
// header parameters from the flat args, and finally lets the credential
// resolver overwrite a header with the resolved auth value.
func (d *OpenAPIDispatcher) applyRequestHeaders(ctx context.Context, req *http.Request, headerArgs map[string]any, hasBody bool) {
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, val := range headerArgs {
		req.Header.Set(name, fmt.Sprint(val))
	}
	if d.credResolver == nil {
		return
	}
	credHeader, credValue := d.credResolver(ctx)
	if credHeader != "" && credValue != "" {
		req.Header.Set(credHeader, credValue)
	}
}

// splitArgsByLocation partitions a flat argument map into the four wire
// locations using the parser's side map. Arguments that lack a location entry
// (e.g. dispatched by an older spec snapshot) are silently dropped so an
// upgrade can never inject unexpected fields upstream.
func splitArgsByLocation(op *openapi.OperationSpec, args map[string]any) (path, query, header, body map[string]any, err error) {
	path = map[string]any{}
	query = map[string]any{}
	header = map[string]any{}
	body = map[string]any{}

	for name, val := range args {
		loc, ok := op.ParameterLocations[name]
		if !ok {
			// Unknown key: ignore. Schema validation upstream of dispatch
			// owns "is this a real parameter" — silently drop here so an
			// older spec snapshot can never proxy arbitrary fields.
			continue
		}
		switch loc {
		case openapi.ParameterLocationPath:
			path[name] = val
		case openapi.ParameterLocationQuery:
			query[name] = val
		case openapi.ParameterLocationHeader:
			header[name] = val
		case openapi.ParameterLocationBody:
			body[name] = val
		default:
			return nil, nil, nil, nil, fmt.Errorf("openapi dispatcher: unknown parameter location %q for %q", loc, name)
		}
	}
	return path, query, header, body, nil
}

// substitutePathVars replaces "{name}" segments with the corresponding
// argument value, URL-path-escaped. Missing required vars surface as an
// explicit error so the caller sees a clear message instead of a malformed
// upstream URL.
func substitutePathVars(template string, args map[string]any) (string, error) {
	if !strings.Contains(template, "{") {
		return template, nil
	}
	var b strings.Builder
	b.Grow(len(template))
	i := 0
	for i < len(template) {
		ch := template[i]
		if ch != '{' {
			b.WriteByte(ch)
			i++
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("openapi dispatcher: unclosed path variable in %q", template)
		}
		name := template[i+1 : i+end]
		val, ok := args[name]
		if !ok {
			return "", fmt.Errorf("openapi dispatcher: missing required path variable %q", name)
		}
		b.WriteString(url.PathEscape(formatScalar(val)))
		i += end + 1
	}
	return b.String(), nil
}

// queryValuesFor renders a query argument as one or more string values.
// Arrays expand to repeated key=value pairs.
func queryValuesFor(val any) []string {
	switch v := val.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, formatScalar(item))
		}
		return out
	default:
		return []string{formatScalar(val)}
	}
}

// formatScalar renders an MCP argument value as a wire-format string. The
// catch: JSON-decoded numbers arrive as float64 even when the schema says
// integer, and fmt.Sprint(float64(99999999)) emits "9.9999999e+07" — which
// any upstream that expects a plain integer rejects with 400. Integer-shaped
// floats get formatted as integers; everything else falls back to the obvious
// representation.
func formatScalar(val any) string {
	switch v := val.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) && v >= math.MinInt64 && v <= math.MaxInt64 {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		f := float64(v)
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', -1, 32)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v)
	default:
		return fmt.Sprint(v)
	}
}

// encodeBodyPayload marshals the body arguments according to whether the spec
// declared an object body (flat top-level merge) or a non-object body
// (synthetic "body" key carrying the value verbatim). Returns nil when there
// is nothing to send so the caller can omit Content-Type.
func encodeBodyPayload(op *openapi.OperationSpec, body map[string]any) ([]byte, error) {
	if op.BodyIsObject {
		if len(body) == 0 {
			return nil, nil
		}
		return json.Marshal(body)
	}
	raw, ok := body["body"]
	if !ok {
		return nil, nil
	}
	return json.Marshal(raw)
}

// readWithCap reads up to limit+1 bytes from r so we can distinguish "fits"
// vs "truncated". Returns the body (clamped to limit), a truncated flag, and
// the total bytes read so the response header can report it accurately.
func readWithCap(r io.Reader, limit int) (body []byte, truncated bool, total int, err error) {
	limited := io.LimitReader(r, int64(limit)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, 0, err
	}
	if len(data) <= limit {
		return data, false, len(data), nil
	}
	// Drain the rest so we can report the true byte count. We cap drainage at
	// a generous extra window — agents rarely need exact gigabyte totals.
	const drainCap = 64 * 1024 * 1024
	drained, err := io.Copy(io.Discard, io.LimitReader(r, int64(drainCap)))
	if err != nil {
		// Treat as truncated with the conservative count we already have.
		return data[:limit], true, len(data), nil //nolint:nilerr // read error is non-fatal: we still surface the truncated body
	}
	total = len(data) + int(drained)
	return data[:limit], true, total, nil
}

// classifyHTTPError turns an *http.Client error into a short human-readable
// reason for the CallToolResult text content.
func classifyHTTPError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "request canceled"
	}
	msg := err.Error()
	if strings.Contains(strings.ToLower(msg), "timeout") {
		return "request timeout"
	}
	return "network error"
}

// errorResult is a tiny helper that builds the IsError CallToolResult shape
// the gateway expects.
func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
