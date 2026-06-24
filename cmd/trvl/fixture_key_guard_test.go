package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// googleAPIKeyPattern matches a Google API key (AIza + 35 url-safe chars).
// Captured HTML fixtures under internal/*/testdata embed third-party providers'
// own Google Maps keys; they must be scrubbed before commit so GitHub
// credential-scanning stays quiet (alerts #1/#2). This guard walks the whole
// repo and fails if any tracked file carries one.
var googleAPIKeyPattern = regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)

func TestRepoHasNoLeakedGoogleKeys(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than fail the walk
		}
		if d.IsDir() {
			// Skip dot-directories (.git, .gitnexus cache, etc.) — they are
			// gitignored and never reach GitHub's scanner, so a key there is
			// not a leak. Only committed source is in scope.
			if name := d.Name(); name != "." && name != ".." && len(name) > 0 && name[0] == '.' {
				return filepath.SkipDir
			}
			if name := d.Name(); name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		// This guard test itself names the pattern; skip it.
		if filepath.Base(path) == "fixture_key_guard_test.go" {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if m := googleAPIKeyPattern.FindString(string(b)); m != "" {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s contains a Google API key (%s…); scrub it before commit", rel, m[:10])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}
