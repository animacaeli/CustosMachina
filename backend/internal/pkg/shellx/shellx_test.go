package shellx

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"nginx":                  "'nginx'",
		"":                       "''",
		"x; rm -rf /":            "'x; rm -rf /'",
		"x$(id)y":                "'x$(id)y'",
		"back`tick`":             "'back`tick`'",
		"it's":                   "'it'\\''s'",
		"a\nb":                   "'a\nb'",
		strings.Repeat("a", 100): "'" + strings.Repeat("a", 100) + "'",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q)=%q want %q", in, got, want)
		}
	}
}
