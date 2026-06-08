package mcp

import "testing"

// TestAudienceMatches covers the confused-deputy audience-validation logic
// (RFC 7662 section 2.2) across every supported claim shape. This is
// security-critical: a false-negative would reject valid tokens, a
// false-positive would accept tokens minted for another service at the same IdP.
func TestAudienceMatches(t *testing.T) {
	t.Parallel()
	const want = "trvl-mcp"
	cases := []struct {
		name string
		raw  any
		want bool
	}{
		{"string match", "trvl-mcp", true},
		{"string mismatch", "other-api", false},
		{"string empty", "", false},
		{"slice-any contains", []any{"a", "trvl-mcp", "b"}, true},
		{"slice-any missing", []any{"a", "b"}, false},
		{"slice-any non-string elems ignored", []any{1, true, "trvl-mcp"}, true},
		{"slice-any all non-string", []any{1, 2.0, true}, false},
		{"slice-string contains", []string{"x", "trvl-mcp"}, true},
		{"slice-string missing", []string{"x", "y"}, false},
		{"slice-string empty", []string{}, false},
		{"nil", nil, false},
		{"wrong type int", 42, false},
		{"wrong type map", map[string]any{"aud": "trvl-mcp"}, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := audienceMatches(c.raw, want); got != c.want {
				t.Errorf("audienceMatches(%#v,%q) = %v, want %v", c.raw, want, got, c.want)
			}
		})
	}
}
