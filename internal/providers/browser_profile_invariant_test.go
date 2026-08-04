package providers

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBrowserLaunchAttachesAUserProfile pins the single claim the consent
// split rests on: that every browser trvl starts itself is BLANK.
//
// TRVL_NO_BROWSER_COOKIES is allowed to leave the CDP paths running precisely
// because they attach no profile — no logins, no history, none of the user's
// cookies. The moment any launch site passes a user-data directory, that
// reasoning collapses and the cookie opt-out becomes a lie in the docs.
//
// It is asserted at the source level, in the style of
// internal/consent/env_invariant_test.go, because chromedp's allocator options
// are opaque closures with no exported way to read back what they set. A source
// scan is a weaker instrument than a behavioural assertion and is not pretended
// otherwise; it is here because the alternative is no assertion at all on a
// claim that a reviewer already caught us getting wrong once.
func TestNoBrowserLaunchAttachesAUserProfile(t *testing.T) {
	// The flags that would hand a real profile to a launched browser. UserDataDir
	// is the chromedp option; the other two are the raw Chrome switches it can be
	// spelled as via chromedp.Flag.
	banned := []string{"UserDataDir", "user-data-dir", "profile-directory"}

	root := moduleRoot(t)
	// Every directory that launches a browser via chromedp.
	dirs := []string{
		filepath.Join(root, "internal", "providers"),
		filepath.Join(root, "internal", "ground"),
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			// Comments are stripped before the scan: the surrounding doc comments
			// discuss user-data-dir precisely because it is banned, and a scan
			// that trips over its own explanation is a scan nobody keeps.
			src := codeWithoutComments(t, path)
			for _, bad := range banned {
				if strings.Contains(src, bad) {
					t.Errorf("%s references %q: a launched browser must never be given the user's profile, "+
						"or %s stops meaning what the docs say it means", path, bad, "TRVL_NO_BROWSER_COOKIES")
				}
			}
		}
	}
}

// codeWithoutComments renders path's Go source with every comment removed, so
// the ban above applies to what the program does rather than to what its
// authors wrote about it.
func codeWithoutComments(t *testing.T, path string) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0) // 0 = drop comments
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		t.Fatalf("print %s: %v", path, err)
	}
	return buf.String()
}

// moduleRoot walks up from the working directory to the go.mod that defines
// this module, so the scan above is anchored to the repo rather than to
// wherever `go test` happened to be invoked.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}
