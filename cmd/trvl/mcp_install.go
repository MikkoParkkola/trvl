package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// mcpInstallCmd adds trvl to Claude Desktop's MCP configuration file.
//
// This is the single most important friction reducer for non-technical users:
// instead of asking them to find and hand-edit a JSON config file, they run
// one command and trvl installs itself as an MCP server for Claude Desktop.
func mcpInstallCmd() *cobra.Command {
	var (
		client string
		force  bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install trvl as an MCP server in your AI client's config",
		Long: `Install trvl into your AI client's MCP configuration automatically.

No JSON editing, no file-path hunting. Just run this command and restart
your client — trvl will appear as an MCP server ready to search flights
and hotels.

By default, installs into Claude Desktop. Use --client to target other
MCP-aware clients:

  trvl mcp install                          # Claude Desktop (default)
  trvl mcp install --client cursor          # Cursor
  trvl mcp install --client claude-code     # Claude Code
  trvl mcp install --client windsurf        # Windsurf
  trvl mcp install --client codex           # OpenAI Codex CLI
  trvl mcp install --client vscode          # VS Code Copilot
  trvl mcp install --client gemini          # Gemini CLI
  trvl mcp install --client amazon-q        # Amazon Q Developer
  trvl mcp install --client zed             # Zed
  trvl mcp install --client lm-studio       # LM Studio
  trvl mcp install --client --list          # show all supported clients
  trvl mcp install --dry-run                # show what would change
`,
		Aliases: []string{"install-claude-desktop"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(client, force, dryRun)
		},
	}

	cmd.Flags().StringVar(&client, "client", "claude-desktop", "MCP client: claude-desktop, cursor, claude-code, windsurf, codex, vscode, gemini, amazon-q, zed, lm-studio")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing trvl entry without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned change without writing the file")

	return cmd
}

// clientConfigPath returns the MCP config file path for the given client.
func clientConfigPath(client string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	switch strings.ToLower(client) {
	case "claude-desktop", "claude":
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
		case "linux":
			return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), nil
		case "windows":
			appdata := os.Getenv("APPDATA")
			if appdata == "" {
				appdata = filepath.Join(home, "AppData", "Roaming")
			}
			return filepath.Join(appdata, "Claude", "claude_desktop_config.json"), nil
		}
	case "cursor":
		return filepath.Join(home, ".cursor", "mcp.json"), nil
	case "claude-code":
		return filepath.Join(home, ".claude.json"), nil
	case "windsurf":
		return filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), nil
	case "codex":
		return filepath.Join(home, ".codex", "config.toml"), nil
	case "vscode", "vs-code", "copilot":
		return filepath.Join(".vscode", "mcp.json"), nil // workspace-relative
	case "gemini":
		return filepath.Join(home, ".gemini", "settings.json"), nil
	case "amazon-q", "q":
		return filepath.Join(home, ".aws", "amazonq", "mcp.json"), nil
	case "zed":
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(home, "Library", "Application Support", "Zed", "settings.json"), nil
		default:
			return filepath.Join(home, ".config", "zed", "settings.json"), nil
		}
	case "lm-studio":
		return filepath.Join(home, ".lm-studio", "mcp.json"), nil
	}
	return "", fmt.Errorf("unknown client %q\n\nSupported clients:\n  claude-desktop  Claude Desktop (default)\n  cursor          Cursor\n  claude-code     Claude Code\n  windsurf        Windsurf\n  codex           OpenAI Codex CLI\n  vscode          VS Code Copilot\n  gemini          Gemini CLI\n  amazon-q        Amazon Q Developer\n  zed             Zed\n  lm-studio       LM Studio", client)
}

// trvlBinaryPath returns the absolute path to the currently-running trvl
// binary, so the config points at the exact tool the user just invoked.
func trvlBinaryPath() (string, error) {
	// Prefer argv[0] resolved, then fall back to $PATH lookup.
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs, nil
		}
	}
	if path, err := exec.LookPath("trvl"); err == nil {
		return filepath.Abs(path)
	}
	return "", fmt.Errorf("cannot locate trvl binary")
}

// mcpConfigKey returns the JSON key name used for MCP server entries in a given client.
// Most clients use "mcpServers", but VS Code Copilot uses "servers", Zed uses "context_servers".
func mcpConfigKey(client string) string {
	switch strings.ToLower(client) {
	case "vscode", "vs-code", "copilot":
		return "servers"
	case "zed":
		return "context_servers"
	default:
		return "mcpServers"
	}
}

// isCodexTOML returns true if the client uses TOML config instead of JSON.
func isCodexTOML(client string) bool {
	return strings.ToLower(client) == "codex"
}

// runInstall wires trvl into the target client's MCP config.
func runInstall(client string, force, dryRun bool) error {
	cfgPath, err := clientConfigPath(client)
	if err != nil {
		return err
	}
	binary, err := trvlBinaryPath()
	if err != nil {
		return err
	}

	// Codex uses TOML, not JSON.
	if isCodexTOML(client) {
		return runInstallCodexTOML(cfgPath, binary, force, dryRun)
	}

	cfg, existingData, err := loadJSONConfig(cfgPath, force)
	if err != nil {
		return err
	}

	key := mcpConfigKey(client)

	// Ensure MCP servers section exists.
	servers, _ := cfg[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	// Check for existing trvl entry.
	if existing, ok := servers["trvl"]; ok && !force {
		if dryRun {
			fmt.Printf("trvl is already installed in %s\n  existing: %v\n  would not change (use --force to overwrite)\n", cfgPath, existing)
			return nil
		}
		fmt.Printf("trvl is already installed in %s\nUse --force to overwrite.\n", cfgPath)
		return nil
	}

	// Write the trvl entry.
	servers["trvl"] = map[string]any{
		"command": binary,
		"args":    []string{"mcp"},
	}
	cfg[key] = servers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	if dryRun {
		fmt.Printf("Would write to %s:\n\n%s\n", cfgPath, out)
		return nil
	}

	// Create parent directory if missing.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Backup existing file if present.
	if len(existingData) > 0 {
		backup := cfgPath + ".trvl.bak"
		_ = os.WriteFile(backup, existingData, 0o644)
	}

	if err := os.WriteFile(cfgPath, out, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", cfgPath, err)
	}

	fmt.Printf("Installed trvl as MCP server for %s.\n", client)
	fmt.Printf("  config: %s\n", cfgPath)
	fmt.Printf("  binary: %s\n", binary)
	fmt.Println()
	fmt.Println("Restart your AI client to pick up the change.")
	fmt.Println("Then ask: \"Use trvl to find flights from AMS to BCN next month.\"")
	return nil
}

func loadJSONConfig(cfgPath string, force bool) (map[string]any, []byte, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil, nil
		}
		return nil, nil, fmt.Errorf("read config %s: %w", cfgPath, err)
	}
	if len(data) == 0 {
		return map[string]any{}, data, nil
	}

	cfg := map[string]any{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		if force {
			return map[string]any{}, data, nil
		}
		return nil, data, fmt.Errorf("parse existing config %s: %w (fix the file or use --force to overwrite)", cfgPath, err)
	}
	return cfg, data, nil
}

// runInstallCodexTOML writes trvl config in OpenAI Codex's TOML format.
func runInstallCodexTOML(cfgPath, binary string, force, dryRun bool) error {
	// Codex uses: [mcp_servers.trvl]
	// command = "/usr/local/bin/trvl"
	// args = ["mcp"]
	entry := fmt.Sprintf("\n[mcp_servers.trvl]\ncommand = %q\nargs = [\"mcp\"]\n", binary)

	existing, _ := os.ReadFile(cfgPath)
	content := string(existing)

	if strings.Contains(content, "[mcp_servers.trvl]") && !force {
		if dryRun {
			fmt.Printf("trvl is already in %s (use --force to overwrite)\n", cfgPath)
			return nil
		}
		fmt.Printf("trvl is already installed in %s\nUse --force to overwrite.\n", cfgPath)
		return nil
	}

	if dryRun {
		fmt.Printf("Would append to %s:\n%s\n", cfgPath, entry)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Backup existing.
	if len(existing) > 0 {
		_ = os.WriteFile(cfgPath+".trvl.bak", existing, 0o644)
	}

	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open config %s: %w", cfgPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("write config %s: %w", cfgPath, err)
	}

	fmt.Printf("Installed trvl as MCP server for Codex.\n")
	fmt.Printf("  config: %s\n", cfgPath)
	fmt.Printf("  binary: %s\n", binary)
	return nil
}
