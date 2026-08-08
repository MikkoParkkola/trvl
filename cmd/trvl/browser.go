package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser opens the given URL in the user's default browser.
// It supports macOS (open), Linux (xdg-open), and Windows (start).
func openBrowser(url string) error {
	if url == "" {
		return fmt.Errorf("no URL to open")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// #nosec G204 -- fixed executable and shell-free URL argument.
		cmd = exec.Command("open", url)
	case "linux":
		// #nosec G204 -- fixed executable and shell-free URL argument.
		cmd = exec.Command("xdg-open", url)
	case "windows":
		// Avoid cmd.exe so URL metacharacters cannot become command syntax.
		// #nosec G204 -- fixed executable and shell-free URL argument.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform %s — open %s manually", runtime.GOOS, url)
	}

	return cmd.Start()
}
