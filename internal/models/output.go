package models

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// UseColor controls whether ANSI color codes are emitted.
// Set to false when output is piped (not a terminal).
var UseColor = true

// Green wraps s in ANSI green (for positive values like savings).
func Green(s string) string {
	if !UseColor {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

// Red wraps s in ANSI red (for warnings or high prices).
func Red(s string) string {
	if !UseColor {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

// Yellow wraps s in ANSI yellow (for cautions or moderate values).
func Yellow(s string) string {
	if !UseColor {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

// Bold wraps s in ANSI bold.
func Bold(s string) string {
	if !UseColor {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

// Dim wraps s in ANSI dim/faint.
func Dim(s string) string {
	if !UseColor {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

// Cyan wraps s in ANSI cyan.
func Cyan(s string) string {
	if !UseColor {
		return s
	}
	return "\033[36m" + s + "\033[0m"
}

// Banner prints a styled box header to w.
// Accepts one or more subtitle lines. Extra lines appear between
// the first subtitle and the bottom border.
//
//	╭── Flights · round_trip ──────────────────────────────╮
//	│   Found 73 flights                                   │
//	│   🔥 Deal: Fly4Free flash sale on this route!        │
//	╰──────────────────────────────────────────────────────╯
//
// Uses displayWidth for correct alignment with emojis and Unicode.
func Banner(w io.Writer, icon, title string, subtitles ...string) {
	// Build content strings without box chars for width calculation.
	titleContent := fmt.Sprintf(" %s %s ", icon, title)
	titleDisplayW := displayWidth(titleContent)

	// Compute inner width: must fit title + all subtitles.
	minInner := 56
	innerNeeded := titleDisplayW + 1 // +1 for the ─ after ╭
	for _, sub := range subtitles {
		subW := cellDisplayWidth(sub) + 3 // "  " prefix + " " suffix
		if subW > innerNeeded {
			innerNeeded = subW
		}
	}
	if innerNeeded < minInner {
		innerNeeded = minInner
	}

	// Top line: ╭─<titleContent><pad>╮
	topPad := innerNeeded - titleDisplayW - 1
	if topPad < 1 {
		topPad = 1
	}
	_, _ = fmt.Fprintf(w, "╭─%s%s╮\n", titleContent, strings.Repeat("─", topPad))

	// Subtitle lines: │  subtitle<pad> │
	for _, sub := range subtitles {
		if sub == "" {
			continue
		}
		subPad := innerNeeded - cellDisplayWidth(sub) - 3
		if subPad < 0 {
			subPad = 0
		}
		_, _ = fmt.Fprintf(w, "│  %s%s │\n", sub, strings.Repeat(" ", subPad))
	}

	// Bottom line: ╰─────────╯
	_, _ = fmt.Fprintf(w, "╰%s╯\n", strings.Repeat("─", innerNeeded))
}

// displayWidth estimates the terminal display width of a string.
// Handles ASCII (1 cell), common emojis (2 cells), and other Unicode.
func displayWidth(s string) int {
	w := 0
	i := 0
	runes := []rune(s)
	for i < len(runes) {
		r := runes[i]
		switch {
		case r < 128:
			// ASCII: 1 cell
			w++
		case r == 0xFE0F:
			// Variation selector: 0 cells (follows emoji)
		case r >= 0x1F300 && r <= 0x1FAFF:
			// Common emoji block: 2 cells
			w += 2
		case r >= 0x2600 && r <= 0x27BF:
			// Misc symbols + dingbats: 2 cells
			w += 2
		case r >= 0x2300 && r <= 0x23FF:
			// Misc technical: 2 cells
			w += 2
		case r >= 0xFF00 && r <= 0xFFEF:
			// Fullwidth forms: 2 cells
			w += 2
		case r >= 0x4E00 && r <= 0x9FFF:
			// CJK: 2 cells
			w += 2
		case r >= 0x2190 && r <= 0x21FF:
			// Arrows: 1 cell
			w++
		default:
			// Other Unicode: assume 1 cell
			w++
		}
		i++
	}
	return w
}

// Summary prints a dimmed summary line after a table.
func Summary(w io.Writer, text string) {
	_, _ = fmt.Fprintf(w, "\n  %s\n", Dim(text))
}

// BookingHint prints a hint about getting booking URLs.
func BookingHint(w io.Writer) {
	_, _ = fmt.Fprintf(w, "  %s\n", Dim("Tip: --format json | jq '.flights[0].booking_url' for direct booking links"))
}

// FormatJSON writes v as pretty-printed JSON to w.
func FormatJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// FormatTable writes a formatted ASCII table to w with aligned columns.
// Each column width is determined by the widest display value in that column,
// with one space of padding on each side. Handles ANSI color codes and
// multi-byte Unicode (emojis) correctly.
func FormatTable(w io.Writer, headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	// Compute column widths using display width (handles ANSI + emoji).
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = cellDisplayWidth(h)
	}
	for _, row := range rows {
		for i := range min(len(row), len(widths)) {
			dw := cellDisplayWidth(row[i])
			if dw > widths[i] {
				widths[i] = dw
			}
		}
	}

	// Print header row.
	printRow(w, headers, widths)

	// Print separator.
	parts := make([]string, len(widths))
	for i, width := range widths {
		parts[i] = strings.Repeat("-", width+2) // +2 for padding
	}
	_, _ = fmt.Fprintf(w, "+%s+\n", strings.Join(parts, "+"))

	// Print data rows.
	for _, row := range rows {
		printRow(w, row, widths)
	}
}

// printRow writes a single pipe-delimited row with padded columns.
// Uses display-width-aware padding for correct alignment with ANSI colors and emojis.
func printRow(w io.Writer, cells []string, widths []int) {
	parts := make([]string, len(widths))
	for i, width := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		// Pad based on display width difference, not byte length.
		dw := cellDisplayWidth(cell)
		pad := width - dw
		if pad < 0 {
			pad = 0
		}
		parts[i] = " " + cell + strings.Repeat(" ", pad) + " "
	}
	_, _ = fmt.Fprintf(w, "|%s|\n", strings.Join(parts, "|"))
}

// cellDisplayWidth computes the display width of a table cell string,
// stripping ANSI escape codes before counting.
func cellDisplayWidth(s string) int {
	return displayWidth(stripANSI(s))
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			// Skip to the end of the ANSI sequence (terminates at a letter).
			j := i + 2
			for j < len(s) && ((s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z')) {
				j++
			}
			if j < len(s) {
				j++ // skip the terminator letter
			}
			i = j
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}
