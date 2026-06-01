package filter

import (
	"context"
	"testing"
)

func TestCompile_RejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := Compile(""); err == nil {
		t.Fatal("expected error on empty expression")
	}
}

func TestCompile_RejectsInvalidJq(t *testing.T) {
	t.Parallel()
	if _, err := Compile(".foo |"); err == nil {
		t.Fatal("expected parse error on trailing pipe")
	}
}

func TestMatch_EqualityOnNestedField(t *testing.T) {
	t.Parallel()
	f, err := Compile(`.data[].data.net.peerPort == 53`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{
			name: "DNS hit",
			v: map[string]any{
				"data": []any{map[string]any{
					"data": map[string]any{
						"net": map[string]any{"peerPort": float64(53)},
					},
				}},
			},
			want: true,
		},
		{
			name: "HTTP miss",
			v: map[string]any{
				"data": []any{map[string]any{
					"data": map[string]any{
						"net": map[string]any{"peerPort": float64(80)},
					},
				}},
			},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := f.Match(t.Context(), c.v)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestMatch_ReturnsErrorOnMalformedInput documents the contract: when
// a jq expression assumes a structure the input lacks (e.g. .data[]
// against a value where .data is null), Match returns a non-nil error
// rather than silently treating the envelope as "no match". Callers
// in the service layer choose to skip-and-continue per message; that's
// a service-layer policy, not a filter-layer one.
func TestMatch_ReturnsErrorOnMalformedInput(t *testing.T) {
	t.Parallel()
	f, err := Compile(`.data[].whatever`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = f.Match(t.Context(), map[string]any{})
	if err == nil {
		t.Fatal("expected error when .data is null and we iterate over it")
	}
}

func TestMatch_TruthyOnNonBoolValue(t *testing.T) {
	t.Parallel()
	// .name yields a string; any non-empty, non-false, non-null value
	// is truthy in jq.
	f, err := Compile(`.name`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ok, err := f.Match(context.Background(), map[string]any{"name": "tawon-directive-foo"})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !ok {
		t.Errorf("expected truthy match on non-empty string")
	}
}

func TestMatch_FalseAndNullAreFalsy(t *testing.T) {
	t.Parallel()
	f, err := Compile(`.flag`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, v := range []any{
		map[string]any{"flag": false},
		map[string]any{"flag": nil},
		map[string]any{}, // .flag is null
	} {
		ok, err := f.Match(t.Context(), v)
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		if ok {
			t.Errorf("expected falsy match on %v", v)
		}
	}
}
