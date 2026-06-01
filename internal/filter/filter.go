// Package filter wraps itchyny/gojq to provide server-side filtering of
// stream records. The query language is jq — adopted as-is. We do not
// invent expression syntax.
//
// Typical use:
//
//	f, err := filter.Compile(`.data[].data.net.peerPort == 53`)
//	if err != nil { ... }
//	ok, err := f.Match(ctx, decodedJSON)
package filter

import (
	"context"
	"errors"
	"fmt"

	"github.com/itchyny/gojq"
)

// Filter is a compiled jq expression that can be evaluated against
// decoded JSON values. Safe for concurrent Match calls.
type Filter struct {
	code *gojq.Code
}

// Compile parses and compiles a jq expression once. Reuse the result
// across many Match calls — recompiling per message is the
// fork-per-call mistake the architecture doc warns about.
func Compile(expr string) (*Filter, error) {
	if expr == "" {
		return nil, errors.New("filter: empty expression")
	}
	q, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("filter: parse jq %q: %w", expr, err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("filter: compile jq %q: %w", expr, err)
	}
	return &Filter{code: code}, nil
}

// Match returns true if the filter evaluates to any truthy value when
// applied to v. v should be the result of json.Unmarshal into a
// generic any (so types are map[string]any, []any, float64, string,
// bool, nil — what gojq expects).
//
// "Truthy" follows jq's semantics: false and null are falsy, everything
// else is truthy. A filter that yields zero values is also considered
// non-matching.
func (f *Filter) Match(ctx context.Context, v any) (bool, error) {
	iter := f.code.RunWithContext(ctx, v)
	for {
		out, ok := iter.Next()
		if !ok {
			return false, nil
		}
		if err, isErr := out.(error); isErr {
			return false, fmt.Errorf("filter: eval: %w", err)
		}
		if isTruthy(out) {
			return true, nil
		}
	}
}

// isTruthy mirrors jq's notion: only false and null are falsy.
func isTruthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	default:
		return true
	}
}
