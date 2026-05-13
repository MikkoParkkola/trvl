package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"
)

// nudgeState tracks star-nudge state across sessions.
type nudgeState struct {
	SearchCount int       `json:"search_count"`
	Shown       bool      `json:"shown"`
	ShownAt     time.Time `json:"shown_at,omitempty"`
}

// nudgeThreshold is the number of successful searches before showing the nudge.
const nudgeThreshold = 3

// searchCommands lists command names that count as "search" for nudge purposes.
var searchCommands = map[string]bool{
	"flights":    true,
	"hotels":     true,
	"dates":      true,
	"prices":     true,
	"explore":    true,
	"grid":       true,
	"weekend":    true,
	"multi-city": true,
	"deals":      true,
	"rooms":      true,
	"discover":   true,
}

// nudgePath returns the path to the nudge state file.
func nudgePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trvl", "nudge.json"), nil
}

// loadNudgeState reads the nudge state from disk. Returns zero state if missing.
func loadNudgeState(path string) nudgeState {
	data, err := os.ReadFile(path)
	if err != nil {
		return nudgeState{}
	}
	var s nudgeState
	if json.Unmarshal(data, &s) != nil {
		return nudgeState{}
	}
	return s
}

// saveNudgeState writes the nudge state to disk atomically.
func saveNudgeState(path string, s nudgeState) {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o700)

	data, err := json.Marshal(s)
	if err != nil {
		return
	}

	tmp, err := os.CreateTemp(dir, "nudge-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_, _ = tmp.Write(data)
	_ = tmp.Close()
	_ = os.Rename(tmpName, path)
}

// shouldShowNudge decides whether to display the star nudge.
// It checks all suppression conditions and returns true only when the nudge
// should be printed.
func shouldShowNudge(commandName string, formatFlag string, getenv func(string) string, stderrFd uintptr, isTerminal func(int) bool) bool {
	// Not a search command.
	if !searchCommands[commandName] {
		return false
	}

	// Suppressed by env var.
	if getenv("TRVL_NO_NUDGE") == "1" {
		return false
	}

	// MCP mode -- never nudge.
	if commandName == "mcp" {
		return false
	}

	// JSON output -- never nudge.
	if formatFlag == "json" {
		return false
	}

	// Piped output -- stderr is not a terminal.
	if !isTerminal(int(stderrFd)) {
		return false
	}

	return true
}

// maybeShowStarNudge is called after a successful search command.
// It increments the search counter and shows the nudge once after the
// threshold is reached.
func maybeShowStarNudge(commandName string, formatFlag string) {
	if !shouldShowNudge(commandName, formatFlag, os.Getenv, os.Stderr.Fd(), term.IsTerminal) {
		return
	}

	path, err := nudgePath()
	if err != nil {
		return
	}

	s := loadNudgeState(path)

	// Already shown -- never again.
	if s.Shown {
		return
	}

	s.SearchCount++

	if s.SearchCount >= nudgeThreshold {
		_, _ = fmt.Fprintln(os.Stderr, "")
		_, _ = fmt.Fprintln(os.Stderr, "  \U0001f4a1 trvl saved you a search? Star us: github.com/MikkoParkkola/trvl")
		s.Shown = true
		s.ShownAt = time.Now()
	}

	saveNudgeState(path, s)
}
