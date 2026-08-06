package logredact

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
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

// The boundary, asserted rather than remembered: slog.LogAttrs is listed in
// slogFuncs but is, in practice, NOT GUARDED AT ALL.
//
// Its trailing arguments are slog.Attr values built by constructors, so each
// argument's source text is `slog.String("url", raw)` or `slog.Any("err", err)`.
// The key rule skips it because those are not a flat key/value sequence. The
// expression and identifier rules skip it too, because they match the WHOLE
// argument -- and the whole argument is a constructor call, not a bare `err` or
// a `.Error()`.
//
// This test exists because the first draft of it asserted the opposite. The
// commit that added the key rule said the identifier rules "still cover LogAttrs
// arguments"; that was written from reading the slogFuncs table rather than
// running anything, and this fixture disproved it within a minute. It is the
// fourth false coverage claim in this file's history, and the first one caught
// by a test instead of by a reviewer.
//
// Safe today because the codebase has no LogAttrs call. Recorded, rather than
// left as a comment, so it fails loudly the moment that stops being true is not
// something this test can do -- see the note below.
//
// If LogAttrs starts being used, the fix is to walk Attr constructor calls and
// apply the key rule to their first argument. A rule with no subject is a rule
// nobody maintains, which is why it is not written yet.
func TestLogAttrsIsAKnownUnguardedGap(t *testing.T) {
	const fixture = `package fixture

import (
	"context"
	"log/slog"
)

func attrs(ctx context.Context, listingURL string, err error) {
	slog.LogAttrs(ctx, slog.LevelWarn, "a", slog.String("url", listingURL))
	slog.LogAttrs(ctx, slog.LevelWarn, "b", slog.Any("err", err))
}
`
	fset := token.NewFileSet()
	f, parseErr := parser.ParseFile(fset, "attrs.go", fixture, 0)
	if parseErr != nil {
		t.Fatalf("fixture does not parse: %v", parseErr)
	}
	found := inspectFile(fset, f, "attrs.go")

	if len(found) != 0 {
		t.Errorf("LogAttrs is now guarded. That is an improvement, not a failure -- but this test "+
			"and the LogAttrs carve-out in attrStart's comment both say it is not, so update them "+
			"together.\ngot:\n%s", strings.Join(found, "\n"))
	}
}

// And the gap must stay theoretical: no production file may call LogAttrs while
// it is unguarded.
//
// This is the half that makes the gap safe. Documenting an unguarded surface
// protects nothing on its own; what protects it is that nothing uses it, and
// this fails the moment something does -- pointing at the fix rather than
// leaving a leak to be found later.
func TestNothingUsesTheUnguardedLogAttrs(t *testing.T) {
	root := repoRoot(t)
	var users []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || !isEnforced(rel) {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(src), "LogAttrs(") {
			users = append(users, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, u := range users {
		t.Errorf("%s calls slog.LogAttrs, which this guard does not check: its Attr-wrapped "+
			"arguments match none of the rules, so a journey URL passed through slog.String(\"url\", raw) "+
			"reaches the log unreported. Either wrap the value with logredact, or teach inspectFile to "+
			"walk Attr constructors and delete this test.", u)
	}
}
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
