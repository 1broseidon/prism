package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Predicate is one v1 argument/workspace constraint. Exactly one field must be
// set after JSON decoding.
type Predicate struct {
	Equals    any             `json:"equals,omitempty"`
	Prefix    *string         `json:"prefix,omitempty"`
	OneOf     []any           `json:"oneOf,omitempty"`
	Pattern   *string         `json:"pattern,omitempty"`
	SizeMax   *int64          `json:"size_max,omitempty"`
	Range     *RangePredicate `json:"range,omitempty"`
	ToolInSet []string        `json:"tool_in_set,omitempty"`
}

// RangePredicate constrains numeric values.
type RangePredicate struct {
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// ParsePredicate parses and validates a v1 predicate object.
func ParsePredicate(raw json.RawMessage) (Predicate, error) {
	var p Predicate
	if err := json.Unmarshal(raw, &p); err != nil {
		return Predicate{}, err
	}
	return p, nil
}

// Match evaluates one decoded JSON value against one predicate.
func Match(value any, p Predicate) (bool, string) {
	if p.Match(value) {
		return true, ""
	}
	return false, "predicate_failed"
}

// UnmarshalJSON rejects reserved predicate keys and enforces the one-shape rule.
func (p *Predicate) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k := range raw {
		switch k {
		case "equals", "prefix", "oneOf", "pattern", "size_max", "range", "tool_in_set":
		case "expression", "not", "allOf", "anyOf", "$ref":
			return fmt.Errorf("reserved predicate key %q", k)
		default:
			return fmt.Errorf("unknown predicate key %q", k)
		}
	}
	type alias Predicate
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*p = Predicate(out)
	return p.Validate()
}

// Validate checks that a predicate is well-formed.
func (p Predicate) Validate() error {
	count := 0
	if p.Equals != nil {
		count++
	}
	if p.Prefix != nil {
		count++
	}
	if p.OneOf != nil {
		count++
		if len(p.OneOf) == 0 {
			return errors.New("oneOf must not be empty")
		}
	}
	if p.Pattern != nil {
		count++
		if len(*p.Pattern) > 256 {
			return errors.New("pattern must be <= 256 chars")
		}
		if _, err := regexp.Compile(*p.Pattern); err != nil {
			return fmt.Errorf("invalid pattern: %w", err)
		}
		if hasNestedQuantifier(*p.Pattern) {
			return errors.New("pattern contains nested quantifiers")
		}
	}
	if p.SizeMax != nil {
		count++
		if *p.SizeMax < 0 {
			return errors.New("size_max must be >= 0")
		}
	}
	if p.Range != nil {
		count++
		if p.Range.Min == nil && p.Range.Max == nil {
			return errors.New("range requires min or max")
		}
		if p.Range.Min != nil && p.Range.Max != nil && *p.Range.Min > *p.Range.Max {
			return errors.New("range min must be <= max")
		}
	}
	if p.ToolInSet != nil {
		count++
		if err := ValidateToolInSet(p.ToolInSet); err != nil {
			return err
		}
	}
	if count != 1 {
		return fmt.Errorf("predicate must contain exactly one operator, got %d", count)
	}
	return nil
}

// Match reports whether v satisfies the predicate.
func (p Predicate) Match(v any) bool {
	if p.Equals != nil {
		return jsonValueEqual(v, p.Equals)
	}
	if p.Prefix != nil {
		s, ok := v.(string)
		return ok && strings.HasPrefix(s, *p.Prefix)
	}
	if p.OneOf != nil {
		for _, candidate := range p.OneOf {
			if jsonValueEqual(v, candidate) {
				return true
			}
		}
		return false
	}
	if p.Pattern != nil {
		s, ok := v.(string)
		return ok && regexp.MustCompile(*p.Pattern).MatchString(s)
	}
	if p.SizeMax != nil {
		return valueSize(v) <= *p.SizeMax
	}
	if p.Range != nil {
		n, ok := jsonNumber(v)
		if !ok {
			return false
		}
		if p.Range.Min != nil && n < *p.Range.Min {
			return false
		}
		if p.Range.Max != nil && n > *p.Range.Max {
			return false
		}
		return true
	}
	if p.ToolInSet != nil {
		return matchToolInSet(v, p.ToolInSet)
	}
	return false
}

// grantArgsMatchWithCall is the call-aware sibling of grantArgsMatch. It
// synthesizes the "_tool" arg from call.Backend + ":" + call.Tool so the
// tool_in_set predicate (used by verb-shape templates that fan out across
// several concrete tools) can be evaluated without forcing callers to inject
// the tuple into the user-visible argument payload.
//
// The synthetic key is intentionally unset when the predicate map does not
// reference it — callers that care about real argument fields are unaffected.
func grantArgsMatchWithCall(preds map[string]Predicate, raw json.RawMessage, call GrantCall) (bool, string) {
	if len(preds) == 0 {
		return true, ""
	}
	var args map[string]any
	if len(raw) == 0 {
		args = map[string]any{}
	} else if err := json.Unmarshal(raw, &args); err != nil {
		return false, "arguments_invalid"
	}
	if _, ok := preds["_tool"]; ok {
		if _, present := args["_tool"]; !present {
			args["_tool"] = call.Backend + ":" + call.Tool
		}
	}
	for field, pred := range preds {
		value, ok := args[field]
		if !ok {
			return false, "argument_missing:" + field
		}
		if !pred.Match(value) {
			return false, "predicate_failed:" + field
		}
	}
	return true, ""
}

func substituteStrings(v any, vars map[string]string) any {
	switch x := v.(type) {
	case string:
		out := x
		for key, val := range vars {
			out = strings.ReplaceAll(out, "${"+key+"}", val)
		}
		return out
	case []any:
		for i := range x {
			x[i] = substituteStrings(x[i], vars)
		}
		return x
	case map[string]any:
		for k, val := range x {
			x[k] = substituteStrings(val, vars)
		}
		return x
	default:
		return v
	}
}

func valueSize(v any) int64 {
	switch x := v.(type) {
	case string:
		return int64(len(x))
	case []any:
		return int64(len(x))
	case []byte:
		return int64(len(x))
	case map[string]any:
		data, _ := json.Marshal(x)
		return int64(len(data))
	case nil:
		return 0
	default:
		data, _ := json.Marshal(x)
		return int64(len(data))
	}
}

func hasNestedQuantifier(pattern string) bool {
	nested := []string{"+)+", "*)+", "?)+", "}+", "+)*", "*)*", "?)*", "}*", "+)?", "*)?", "?)?", "}?"}
	for _, s := range nested {
		if strings.Contains(pattern, s) {
			return true
		}
	}
	return false
}
