package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^]]*\]\(([^)[:space:]]+)\)`)

func TestReleaseFacingDocsStayAligned(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	groundCount := marketedGroundProviderCount()
	transportCount := marketedTransportProviderCount()
	aliasCount := registeredMCPCompatibilityAliasCount(t)

	required := map[string][]string{
		".goreleaser.yaml": {
			"Current interface:",
			fmt.Sprintf("1 smart MCP tool, %d legacy-compatible capabilities", aliasCount),
			fmt.Sprintf("%d CLI commands", cliCommandCountMarketed),
		},
		"docs/COMPARISON.md": {
			fmt.Sprintf("%d bus, train, and ferry providers", groundCount),
			fmt.Sprintf("%d transport providers plus separate flight and hotel rosters", transportCount),
		},
		"docs/POSITIONING.md": {
			fmt.Sprintf("%d transport providers", transportCount),
			fmt.Sprintf("%d ground/ferry providers", groundCount),
		},
		"docs/index.html": {
			fmt.Sprintf("<b>%d</b> transport providers", transportCount),
			"no personal keys for default search",
		},
		"ROADMAP.md": {
			"v1.21.0** (2026-08-08): current release line",
		},
		"CHANGELOG.md": {
			"## [1.21.0] - 2026-08-08",
		},
	}

	for path, needles := range required {
		text := read(path)
		for _, needle := range needles {
			if !strings.Contains(text, needle) {
				t.Errorf("%s missing current release claim %q", path, needle)
			}
		}
	}

	for _, path := range []string{"README.md", "AGENTS.md"} {
		text := read(path)
		if strings.Contains(text, "/releases/latest/download/trvl_") {
			t.Errorf("%s still uses an unversioned release asset name", path)
		}
		for _, needle := range []string{"TRVL_VERSION=", "trvl_${TRVL_VERSION}_"} {
			if !strings.Contains(text, needle) {
				t.Errorf("%s missing version-aware direct-install marker %q", path, needle)
			}
		}
	}
}

func TestMaintainedDocsLocalLinksResolve(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	docs := []string{
		"README.md",
		"AGENTS.md",
		"ROADMAP.md",
		"CONTRIBUTORS.md",
		"DESIGN.md",
		"docs/CLI.md",
		"docs/COMPARISON.md",
		"docs/DEMO.md",
		"docs/DISTRIBUTION.md",
		"docs/MCP-TOOLS-REFERENCE.md",
		"docs/POSITIONING.md",
		"docs/PROVIDERS.md",
		"docs/index.html",
		"npm/README.md",
		"plugin/README.md",
	}

	for _, doc := range docs {
		doc := doc
		t.Run(doc, func(t *testing.T) {
			t.Parallel()

			fullPath := filepath.Join(root, doc)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("read %s: %v", doc, err)
			}

			for _, match := range markdownLinkPattern.FindAllStringSubmatch(string(data), -1) {
				target := match[1]
				if strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "cursor:") {
					continue
				}
				target = strings.SplitN(target, "#", 2)[0]
				target = strings.SplitN(target, "?", 2)[0]
				decoded, err := url.PathUnescape(target)
				if err != nil {
					t.Errorf("%s has invalid local link %q: %v", doc, target, err)
					continue
				}
				resolved := filepath.Clean(filepath.Join(filepath.Dir(fullPath), decoded))
				if _, err := os.Stat(resolved); err != nil {
					t.Errorf("%s local link %q does not resolve: %v", doc, match[1], err)
				}
			}
		})
	}
}
