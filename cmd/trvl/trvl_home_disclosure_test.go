package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// README.md tells the reader exactly what trvl keeps under ~/.trvl, in the
// section "What trvl reads, and what it keeps". That sentence has been wrong
// before, because a new package started writing under the home directory and
// nobody re-read the disclosure. This test makes the disclosure a checked
// claim rather than a remembered one.
//
// Each non-test file that constructs a path under ~/.trvl must be listed here
// against the README category that discloses it. Adding a new writer is fine;
// leaving it undisclosed is not. If this test fails, either the new file
// belongs to an existing category (add it below) or it is something the README
// does not yet admit to keeping (say so in README.md first, then add it).
//
// Its reach is a literal search for the string ".trvl", so it catches the way
// every writer here builds its path today and would catch a new one written the
// same way. A file that reaches the directory through a helper or a constant
// defined elsewhere would slip past. That is a floor under the disclosure, not
// a proof of completeness.
var trvlHomeWriters = map[string]string{
	"cmd/trvl/nudge.go":                    "upgrade and provider self-heal bookkeeping",
	"cmd/trvl/setup.go":                    "cached cookies and provider tokens",
	"cmd/trvl/share.go":                    "search history",
	"internal/dategrid/dategrid.go":        "search history",
	"internal/dealquality/dealquality.go":  "search history",
	"internal/flights/afklm/client.go":     "cached cookies and provider tokens",
	"internal/flights/wizzair_selfheal.go": "upgrade and provider self-heal bookkeeping",
	"internal/hotelarb/hotelarb.go":        "saved trips",
	"internal/preferences/preferences.go":  "preferences and traveller profile",
	"internal/probecache/probecache.go":    "cached cookies and provider tokens",
	"internal/profile/profile.go":          "preferences and traveller profile",
	"internal/providers/cookie_cache.go":   "cached cookies and provider tokens",
	"internal/providers/health_log.go":     "a provider health log",
	"internal/providers/registry.go":       "upgrade and provider self-heal bookkeeping",
	"internal/selfupdate/check.go":         "upgrade and provider self-heal bookkeeping",
	"internal/telemetry/heartbeat.go":      "a random install id",
	"internal/trips/trips.go":              "saved trips",
	"internal/upgrade/upgrade.go":          "upgrade and provider self-heal bookkeeping",
	"internal/watch/store.go":              "price watches",
}

func TestTrvlHomeWritersAreDisclosedInREADME(t *testing.T) {
	root := filepath.Join("..", "..")

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readmeText := string(readme)

	found := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if !strings.Contains(string(body), `".trvl"`) {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			found[filepath.ToSlash(rel)] = true
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", dir, walkErr)
		}
	}

	for rel := range found {
		if _, ok := trvlHomeWriters[rel]; !ok {
			t.Errorf("%s builds a path under ~/.trvl but is not mapped to a README disclosure category.\n"+
				"Add it to trvlHomeWriters, and if it is not already covered by the README section\n"+
				"\"What trvl reads, and what it keeps\", disclose it there first.", rel)
		}
	}
	for rel := range trvlHomeWriters {
		if !found[rel] {
			t.Errorf("%s is listed as a ~/.trvl writer but no longer references \".trvl\"; drop the stale entry", rel)
		}
	}

	// The categories are only worth mapping if the README still states them.
	for rel, category := range trvlHomeWriters {
		if !strings.Contains(readmeText, category) {
			t.Errorf("README.md no longer contains the disclosure category %q claimed for %s;\n"+
				"the ~/.trvl sentence changed without this mapping being updated", category, rel)
		}
	}
}
