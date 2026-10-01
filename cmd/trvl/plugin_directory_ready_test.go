package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPluginDirectoryReady(t *testing.T) {
	repo := filepath.Join("..", "..")
	plugin := filepath.Join(repo, "plugin")

	manifests := findPluginManifests(t, repo)
	if len(manifests) != 1 {
		t.Fatalf("plugin manifests = %v, want exactly one", manifests)
	}
	wantManifest := filepath.Join(plugin, ".claude-plugin", "plugin.json")
	if manifests[0] != wantManifest {
		t.Fatalf("manifest = %s, want %s", manifests[0], wantManifest)
	}

	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		License string `json:"license"`
	}
	decodeJSON(t, wantManifest, &manifest)
	if manifest.Name != "trvl" {
		t.Fatalf("name = %q", manifest.Name)
	}
	if manifest.License != "PolyForm-Noncommercial-1.0.0" {
		t.Fatalf("license = %q", manifest.License)
	}
	latest := latestReleaseTag(t, "MikkoParkkola/trvl")
	if manifest.Version != strings.TrimPrefix(latest, "v") {
		t.Fatalf("plugin version %q, latest release %q", manifest.Version, latest)
	}

	readme := string(readPluginFile(t, plugin, "README.md"))
	if wordsOutsideCode(readme) < 40 {
		t.Fatalf("README has %d words outside code blocks", wordsOutsideCode(readme))
	}
	examples := regexp.MustCompile(`(?m)^### \d+\. `).FindAllString(readme, -1)
	if len(examples) < 3 {
		t.Fatalf("examples = %d, want at least 3", len(examples))
	}

	repoLicence := string(readPluginFile(t, repo, "LICENSE"))
	pluginLicence := string(readPluginFile(t, plugin, "LICENSE"))
	if !strings.Contains(pluginLicence, "PolyForm Noncommercial License 1.0.0") {
		t.Fatal("licence text is not PolyForm Noncommercial 1.0.0")
	}
	if emailPattern.MatchString(pluginLicence) {
		t.Fatal("plugin licence contains an email address")
	}
	for _, line := range strings.Split(pluginLicence, "\n") {
		if !strings.Contains(repoLicence, line) {
			t.Fatalf("plugin licence line is not in the repository licence: %q", line)
		}
	}
	var onlyInRepo []string
	for _, line := range strings.Split(repoLicence, "\n") {
		if strings.TrimSpace(line) == "" || strings.Contains(pluginLicence, line) {
			continue
		}
		onlyInRepo = append(onlyInRepo, line)
	}
	if len(onlyInRepo) != 1 || !emailPattern.MatchString(onlyInRepo[0]) {
		t.Fatalf("repository licence should differ only by its contact email, got %d extra lines", len(onlyInRepo))
	}

	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	decodeJSON(t, filepath.Join(plugin, ".mcp.json"), &mcp)
	server, ok := mcp.MCPServers["trvl"]
	if !ok {
		t.Fatal("mcp server trvl is missing")
	}
	if isShell(server.Command) {
		t.Fatalf("command %q is a shell", server.Command)
	}
	if server.Command != "npx" {
		t.Fatalf("command = %q, want npx", server.Command)
	}
	pin := "trvl-mcp@" + strings.TrimPrefix(latest, "v")
	if !containsString(server.Args, pin) {
		t.Fatalf("args = %v, want exact pin %s", server.Args, pin)
	}
	if containsString(server.Args, "trvl") {
		t.Fatal("launcher must not invoke a trvl binary already on PATH")
	}

	privacy := string(readPluginFile(t, plugin, "PRIVACY.md"))
	for _, needle := range []string{
		"https://telemetry.revaluator.ai/v1/heartbeat",
		"at most one POST",
		"`project`",
		"`event`",
		"the trvl version",
		"Go runtime",
		"`install_id`",
		"city name",
		"country code",
		"Coordinates are not written",
		"not written into the stored record",
		"TRVL_NO_TELEMETRY",
		"NO_TELEMETRY",
		"DO_NOT_TRACK",
		"Mikko Parkkola",
		"https://github.com/MikkoParkkola/trvl/issues",
	} {
		if !strings.Contains(privacy, needle) {
			t.Fatalf("PRIVACY.md missing %q", needle)
		}
	}
	if strings.Contains(privacy, "telemetry.trvl.app") {
		t.Fatal("PRIVACY.md names telemetry.trvl.app")
	}
	if emailInTree(t, plugin) {
		t.Fatal("plugin folder contains an email address")
	}
}

func emailInTree(t *testing.T, root string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if emailPattern.Match(b) {
			t.Errorf("email in %s", path)
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk plugin: %v", err)
	}
	return found
}

func findPluginManifests(t *testing.T, repo string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if d.Name() == "plugin.json" && filepath.Base(filepath.Dir(path)) == ".claude-plugin" {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return found
}

func latestReleaseTag(t *testing.T, repo string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("User-Agent", "trvl-plugin-directory-test")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("latest release: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("latest release status %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	if payload.TagName == "" {
		t.Fatal("latest release has no tag")
	}
	return payload.TagName
}

func wordsOutsideCode(markdown string) int {
	without := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(markdown, " ")
	return len(strings.Fields(without))
}

func isShell(command string) bool {
	switch strings.ToLower(filepath.Base(command)) {
	case "sh", "bash", "zsh", "dash", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh":
		return true
	default:
		return false
	}
}

func decodeJSON(t *testing.T, path string, dest any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, dest); err != nil {
		t.Fatalf("json %s: %v", path, err)
	}
}

var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
