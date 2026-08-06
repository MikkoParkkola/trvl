package logredact

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TRVL.LOGLEAK.9 -- the guard must be shown to FAIL against planted leaks, in
// the test suite, permanently.
//
// Every previous version of this protection was verified by someone remembering
// to break the code by hand, and each one was later found unable to fail for a
// whole class of input: a shell script that skipped any line mentioning
// logredact; its replacement that could not see multi-line calls; and the first
// version of the key rule here, which assumed the first key sits at argument 1
// and therefore skipped every *Context call silently.
//
// Three guards, three silent blind spots, three manual verifications that all
// passed. A guard whose ability to fail is re-established by hand each time is
// a guard that will eventually stop failing without anyone noticing. This test
// is the standing proof, and it runs the REAL inspectFile rather than a copy of
// the rules that could drift away from it.
func TestGuardActuallyFlagsPlantedLeaks(t *testing.T) {
	const planted = `package fixture

import (
	"context"
	"log/slog"
)

func leaks(ctx context.Context, err error, listingURL, resolvedURL string) {
	// 1. plain call, URL-shaped key, name not in riskyIdent
	slog.Debug("a", "url", listingURL)

	// 2. context variant -- the off-by-one that shipped
	slog.DebugContext(ctx, "b", "url", resolvedURL)

	// 3. slog.Log, attrs start at 3
	slog.Log(ctx, slog.LevelWarn, "c", "endpoint", resolvedURL)

	// 4. multi-line, both fields bad -- the airbnb shape
	slog.Debug("d",
		"url", listingURL,
		"error", err.Error())

	// 5. a raw error under an ordinary key
	slog.Warn("e", "err", err)
}
`
	fset := token.NewFileSet()
	f, parseErr := parser.ParseFile(fset, "fixture.go", planted, 0)
	if parseErr != nil {
		t.Fatalf("fixture does not parse: %v", parseErr)
	}

	found := inspectFile(fset, f, "fixture.go")
	joined := strings.Join(found, "\n")

	// Each entry is a leak the guard MUST report, with the label used when it
	// does not, so a regression names the class it lost rather than a count.
	for _, want := range []struct{ needle, why string }{
		{`slog value listingURL is logged under the URL-shaped key "url"`,
			"plain call with a URL-shaped key and a variable name outside riskyIdent"},
		{`slog value resolvedURL is logged under the URL-shaped key "url"`,
			"DebugContext -- the context variants shift every argument by one, and the first key rule skipped them all"},
		{`slog value resolvedURL is logged under the URL-shaped key "endpoint"`,
			"slog.Log, whose attributes start at index 3"},
		{`slog arg err.Error()`,
			"a raw error beside a redacted URL, on a multi-line call"},
		{`slog arg err `,
			"a bare error identifier"},
	} {
		if !strings.Contains(joined, want.needle) {
			t.Errorf("guard did not flag: %s\n  missing: %s\n  got:\n%s", want.why, want.needle, joined)
		}
	}

	// And the multi-line call must be caught on BOTH fields, not just the first.
	//
	// Counted rather than pinned to a line number: the same URL-key message must
	// appear twice, once for the single-line call and once for the multi-line
	// one. A line number here would break every time the fixture is edited, and a
	// brittle assertion is one that gets deleted.
	if n := strings.Count(joined, `slog value listingURL is logged under the URL-shaped key "url"`); n < 2 {
		t.Errorf("the URL-key rule fired %d time(s), want 2: the second is the multi-line call, "+
			"which is the exact shape the previous shell guard could not see.\ngot:\n%s", n, joined)
	}
}

// The other half: the guard must NOT fire on already-wrapped calls, or it gets
// deleted by whoever it annoys. A guard that cries wolf is removed, and a
// removed guard protects nothing.
func TestGuardIsQuietOnWrappedCalls(t *testing.T) {
	const clean = `package fixture

import (
	"context"
	"log/slog"

	"github.com/MikkoParkkola/trvl/internal/logredact"
)

func fine(ctx context.Context, err error, listingURL string) {
	slog.Debug("a", "url", logredact.URL(listingURL))
	slog.DebugContext(ctx, "b", "url", logredact.URL(listingURL), "err", logredact.Err(err))
	slog.Warn("c", "count", 3, "name", "paris", "elapsed", 12)
}
`
	fset := token.NewFileSet()
	f, parseErr := parser.ParseFile(fset, "clean.go", clean, 0)
	if parseErr != nil {
		t.Fatalf("fixture does not parse: %v", parseErr)
	}

	if found := inspectFile(fset, f, "clean.go"); len(found) != 0 {
		t.Errorf("guard fired on correctly wrapped calls, which is how guards get deleted:\n%s",
			strings.Join(found, "\n"))
	}
}
