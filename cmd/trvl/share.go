package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/spf13/cobra"
)

// LastSearch is the cached result from the most recent search command.
// Written to ~/.trvl/last_search.json after every successful search.
type LastSearch struct {
	Command   string    `json:"command"`
	Timestamp time.Time `json:"timestamp"`

	// Trip-plan style results (from trip, discover, weekend, etc.).
	Origin      string `json:"origin,omitempty"`
	Destination string `json:"destination,omitempty"`
	DepartDate  string `json:"depart_date,omitempty"`
	ReturnDate  string `json:"return_date,omitempty"`
	Nights      int    `json:"nights,omitempty"`
	Guests      int    `json:"guests,omitempty"`

	// Best options found.
	FlightPrice    float64 `json:"flight_price,omitempty"`
	FlightCurrency string  `json:"flight_currency,omitempty"`
	FlightAirline  string  `json:"flight_airline,omitempty"`
	FlightStops    int     `json:"flight_stops,omitempty"`
	HotelPrice     float64 `json:"hotel_price,omitempty"`
	HotelCurrency  string  `json:"hotel_currency,omitempty"`
	HotelName      string  `json:"hotel_name,omitempty"`
	TotalPrice     float64 `json:"total_price,omitempty"`
	TotalCurrency  string  `json:"total_currency,omitempty"`
}

func shareCmd() *cobra.Command {
	var (
		last      bool
		formatOut string
	)

	cmd := &cobra.Command{
		Use:   "share [trip_id]",
		Short: "Generate a shareable trip summary",
		Long: `Generate a formatted trip summary for sharing on Slack, email, or social media.

Examples:
  trvl share trip_abc123
  trvl share --last
  trvl share --last --format clipboard
  trvl share trip_abc123 --format link`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if last {
				return shareLastSearch(formatOut)
			}
			if len(args) == 0 {
				return fmt.Errorf("provide a trip_id or use --last for the most recent search")
			}
			return shareTrip(args[0], formatOut)
		},
	}

	cmd.Flags().BoolVar(&last, "last", false, "Share the most recent search results")
	cmd.Flags().StringVar(&formatOut, "format", "markdown", "Output format: markdown, clipboard, link")

	return cmd
}

// shareTrip generates a shareable summary from a saved trip.
func shareTrip(tripID, formatOut string) error {
	store, err := loadTripStore()
	if err != nil {
		return err
	}
	t, err := store.Get(tripID)
	if err != nil {
		return err
	}

	md := formatTripMarkdown(t)
	return outputShare(md, formatOut)
}

// shareLastSearch generates a shareable summary from the cached last search.
func shareLastSearch(formatOut string) error {
	ls, err := loadLastSearch()
	if err != nil {
		return err
	}
	md := formatLastSearchMarkdown(ls)
	return outputShare(md, formatOut)
}

// outputShare routes the markdown to stdout, clipboard, or gist.
func outputShare(md, formatOut string) error {
	switch formatOut {
	case "clipboard":
		return copyToClipboard(md)
	case "link":
		return createGist(md)
	default:
		fmt.Print(md)
		return nil
	}
}

// formatTripMarkdown renders a saved trip as clean shareable markdown.
func formatTripMarkdown(t *trips.Trip) string {
	var b strings.Builder

	// Derive origin/destination and dates from legs.
	origin, dest, depart, ret, nights := extractTripRoute(t)

	// Header line.
	if origin != "" && dest != "" {
		b.WriteString(fmt.Sprintf("**%s -> %s**", origin, dest))
		if depart != "" && ret != "" {
			b.WriteString(fmt.Sprintf(" | %s-%s", formatDateCompact(depart), formatDateCompact(ret)))
		}
		if nights > 0 {
			b.WriteString(fmt.Sprintf(" | %d nights", nights))
		}
		b.WriteString("\n\n")
	} else {
		b.WriteString(fmt.Sprintf("**%s**\n\n", t.Name))
	}

	// Price table.
	hasPrice := false
	for _, leg := range t.Legs {
		if leg.Price > 0 {
			hasPrice = true
			break
		}
	}

	if hasPrice {
		b.WriteString("| | Price |\n")
		b.WriteString("|---|---|\n")
		var total float64
		var currency string
		for _, leg := range t.Legs {
			if leg.Price <= 0 {
				continue
			}
			label := capitalizeFirst(leg.Type)
			detail := fmt.Sprintf("%s %.0f", leg.Currency, leg.Price)
			if leg.Provider != "" {
				detail += fmt.Sprintf(" (%s)", leg.Provider)
			}
			b.WriteString(fmt.Sprintf("| %s | %s |\n", label, detail))
			total += leg.Price
			if currency == "" {
				currency = leg.Currency
			}
		}
		if currency != "" {
			b.WriteString(fmt.Sprintf("| **Total** | **%s %.0f** |\n", currency, total))
		}
		b.WriteString("\n")
	}

	b.WriteString(trvlFooter())
	return b.String()
}

// formatLastSearchMarkdown renders the cached last-search as shareable markdown.
func formatLastSearchMarkdown(ls *LastSearch) string {
	var b strings.Builder

	// Header.
	originName := ls.Origin
	destName := ls.Destination

	if originName != "" && destName != "" {
		b.WriteString(fmt.Sprintf("**%s -> %s**", originName, destName))
		if ls.DepartDate != "" && ls.ReturnDate != "" {
			b.WriteString(fmt.Sprintf(" | %s-%s", formatDateCompact(ls.DepartDate), formatDateCompact(ls.ReturnDate)))
		}
		if ls.Nights > 0 {
			b.WriteString(fmt.Sprintf(" | %d nights", ls.Nights))
		}
		b.WriteString("\n\n")
	} else {
		b.WriteString(fmt.Sprintf("**%s search**\n\n", ls.Command))
	}

	// Price table.
	hasAny := ls.FlightPrice > 0 || ls.HotelPrice > 0
	if hasAny {
		b.WriteString("| | Price |\n")
		b.WriteString("|---|---|\n")
		if ls.FlightPrice > 0 {
			detail := fmt.Sprintf("%s %.0f", ls.FlightCurrency, ls.FlightPrice)
			if ls.FlightAirline != "" {
				stops := "nonstop"
				if ls.FlightStops == 1 {
					stops = "1 stop"
				} else if ls.FlightStops > 1 {
					stops = fmt.Sprintf("%d stops", ls.FlightStops)
				}
				detail += fmt.Sprintf(" (%s, %s)", ls.FlightAirline, stops)
			}
			b.WriteString(fmt.Sprintf("| Flight | %s |\n", detail))
		}
		if ls.HotelPrice > 0 {
			detail := fmt.Sprintf("%s %.0f", ls.HotelCurrency, ls.HotelPrice)
			if ls.HotelName != "" {
				detail += fmt.Sprintf(" (%s)", ls.HotelName)
			}
			b.WriteString(fmt.Sprintf("| Hotel | %s |\n", detail))
		}
		if ls.TotalPrice > 0 {
			b.WriteString(fmt.Sprintf("| **Total** | **%s %.0f** |\n", ls.TotalCurrency, ls.TotalPrice))
		}
		b.WriteString("\n")
	}

	b.WriteString(trvlFooter())
	return b.String()
}

// trvlFooter is emitted at the bottom of every shared trip card.
// Tool-surface copy is enforced by share tests so public trip cards do not
// drift back to stale advertised counts.
func trvlFooter() string {
	return "*Found by [trvl](https://github.com/MikkoParkkola/trvl) — 1 smart MCP tool + 62 compatibility aliases, no API keys*\n"
}

// extractTripRoute derives origin, destination, dates, and nights from trip legs.
func extractTripRoute(t *trips.Trip) (origin, dest, depart, ret string, nights int) {
	if len(t.Legs) == 0 {
		return "", "", "", "", 0
	}

	origin = t.Legs[0].From
	dest = t.Legs[0].To

	depart = t.Legs[0].StartTime
	if len(depart) > 10 {
		depart = depart[:10]
	}

	last := t.Legs[len(t.Legs)-1]
	ret = last.EndTime
	if ret == "" {
		ret = last.StartTime
	}
	if len(ret) > 10 {
		ret = ret[:10]
	}

	if depart != "" && ret != "" {
		if d, err := models.ParseDate(depart); err == nil {
			if r, err2 := models.ParseDate(ret); err2 == nil {
				nights = int(r.Sub(d).Hours()) / 24
			}
		}
	}

	return origin, dest, depart, ret, nights
}

// formatDateCompact renders "2026-06-16" as "Jun 16".
func formatDateCompact(date string) string {
	t, err := models.ParseDate(date)
	if err != nil {
		return date
	}
	return t.Format("Jan 2")
}

// capitalizeFirst uppercases the first rune of s.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- Clipboard ---

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try xclip first, fall back to xsel.
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		}
	default:
		return fmt.Errorf("clipboard not supported on %s — use default markdown output", runtime.GOOS)
	}

	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy to clipboard: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Copied to clipboard.")
	return nil
}

// --- Gist ---

func createGist(md string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		// Fallback: just print markdown.
		fmt.Fprintln(os.Stderr, "gh CLI not found — printing markdown instead. Install gh for gist support.")
		fmt.Print(md)
		return nil
	}

	cmd := exec.Command("gh", "gist", "create", "--public", "-f", "trip.md", "-")
	cmd.Stdin = strings.NewReader(md)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("create gist: %w", err)
	}
	url := strings.TrimSpace(string(out))
	fmt.Println(url)
	return nil
}

// --- Last search cache ---

func lastSearchPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".trvl", "last_search.json")
}

// saveLastSearch marshals the search result and writes to the cache file.
func saveLastSearch(ls *LastSearch) {
	ls.Timestamp = time.Now()

	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return // best-effort
	}

	path := lastSearchPath()
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o700)

	// Write atomically via temp file.
	tmp, err := os.CreateTemp(dir, "last_search.json.tmp-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	_ = tmp.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
	}
}

func loadLastSearch() (*LastSearch, error) {
	data, err := os.ReadFile(lastSearchPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no recent search found — run a search first (e.g. trvl flights, trvl trip, trvl discover)")
		}
		return nil, fmt.Errorf("read last search: %w", err)
	}

	var ls LastSearch
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, fmt.Errorf("parse last search: %w", err)
	}
	return &ls, nil
}
