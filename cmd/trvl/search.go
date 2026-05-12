package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/nlsearch"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/spf13/cobra"
)

// searchCmd implements `trvl search "free-form query"`. It parses the query
// with the same heuristic the MCP smart `travel` tool uses, then dispatches
// to the appropriate concrete CLI command (flights / hotels / route) so the
// CLI surface has parity with the AI surface.
//
// When the parsed parameters are insufficient (no IATA codes, no dates),
// the command prints what it understood and a hint pointing at the matching
// concrete command, rather than guessing wrong.
func searchCmd() *cobra.Command {
	var (
		dryRun     bool
		jsonFormat bool
	)

	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Natural-language travel search (CLI parity with the travel MCP tool)",
		Long: `Parse a free-form travel query and dispatch to the right concrete search.

The parser is intentionally minimal and rules-based — it understands:
  • intent keywords    (flight / hotel / deals / route)
  • IATA codes         (HEL, NRT, BCN, …)
  • "from X to Y"      (case-insensitive, X and Y must be IATA codes)
  • ISO dates          (2026-06-15)
  • "next weekend" / "this weekend"

For free-form sentences with city names instead of IATA codes, use the travel
MCP tool from an AI assistant — it can route to the compatibility aliases that
resolve cities to airports via sampling.

Examples:
  trvl search "fly HEL NRT 2026-06-15"
  trvl search "hotel BCN 2026-06-15 to 2026-06-19"
  trvl search "from HEL to TLL next weekend"
  trvl search "HEL BCN 2026-06-15" --dry-run`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.TrimSpace(strings.Join(args, " "))
			if query == "" {
				return fmt.Errorf("query is required")
			}

			today := time.Now().Format("2006-01-02")
			params := nlsearch.Heuristic(query, today)

			// If the user didn't put an origin and they have a home airport
			// in preferences, use that for flight/route intent.
			if params.Origin == "" && (params.Intent == "flight" || params.Intent == "route") {
				if prefs, err := preferences.Load(); err == nil && prefs != nil && prefs.HomeAirport() != "" {
					params.Origin = prefs.HomeAirport()
				}
			}

			if jsonFormat || format == "json" {
				return models.FormatJSON(os.Stdout, params)
			}

			printSearchInterpretation(query, params)

			if dryRun {
				return nil
			}

			return dispatchSearch(cmd, params)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print parsed intent and exit without searching")
	cmd.Flags().BoolVar(&jsonFormat, "json", false, "Print parsed parameters as JSON (alias for --format json)")
	return cmd
}

// printSearchInterpretation prints a 1-line summary of how the query was parsed
// to stderr so it does not pollute machine-readable stdout output.
func printSearchInterpretation(query string, p nlsearch.Params) {
	parts := []string{fmt.Sprintf("intent=%s", p.Intent)}
	if p.Origin != "" {
		parts = append(parts, fmt.Sprintf("from=%s", p.Origin))
	}
	if p.Destination != "" {
		parts = append(parts, fmt.Sprintf("to=%s", p.Destination))
	}
	if p.Date != "" {
		parts = append(parts, fmt.Sprintf("date=%s", p.Date))
	}
	if p.ReturnDate != "" {
		parts = append(parts, fmt.Sprintf("return=%s", p.ReturnDate))
	}
	fmt.Fprintf(os.Stderr, "🔎 %s → %s\n", query, strings.Join(parts, " "))
}

// dispatchSearch routes to the right concrete command. It builds a fresh
// cobra subcommand and runs it via SetArgs+Execute so we get the existing
// printing, banner, and JSON-format behavior for free.
func dispatchSearch(parent *cobra.Command, p nlsearch.Params) error {
	switch p.Intent {
	case "flight":
		if p.Origin == "" || p.Destination == "" || p.Date == "" {
			return missingFieldsHint(p, "flight",
				"trvl flights ORIGIN DESTINATION YYYY-MM-DD")
		}
		sub := flightsCmd()
		sub.SetContext(parent.Context())
		sub.SetArgs([]string{p.Origin, p.Destination, p.Date})
		return sub.Execute()

	case "hotel":
		if p.Location == "" || p.CheckIn == "" || p.CheckOut == "" {
			return missingFieldsHint(p, "hotel",
				`trvl hotels "CITY" --checkin YYYY-MM-DD --checkout YYYY-MM-DD`)
		}
		sub := hotelsCmd()
		sub.SetContext(parent.Context())
		sub.SetArgs([]string{p.Location, "--checkin", p.CheckIn, "--checkout", p.CheckOut})
		return sub.Execute()

	case "deals":
		sub := dealsCmd()
		sub.SetContext(parent.Context())
		sub.SetArgs(nil)
		return sub.Execute()

	default: // "route"
		if p.Origin == "" || p.Destination == "" {
			return missingFieldsHint(p, "route",
				"trvl route ORIGIN DESTINATION [YYYY-MM-DD]")
		}
		sub := routeCmd()
		sub.SetContext(parent.Context())
		date := p.Date
		if date == "" {
			date = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		}
		sub.SetArgs([]string{p.Origin, p.Destination, date})
		return sub.Execute()
	}
}

// missingFieldsHint prints a friendly message telling the user which fields
// the parser could not extract and the equivalent concrete command they can
// run by hand. It returns nil so the CLI exits zero — the help text on
// stderr is the value, not an error.
func missingFieldsHint(p nlsearch.Params, intent, template string) error {
	var missing []string
	if p.Origin == "" && intent != "hotel" && intent != "deals" {
		missing = append(missing, "origin")
	}
	if p.Destination == "" && intent != "deals" {
		missing = append(missing, "destination")
	}
	if intent == "hotel" {
		if p.CheckIn == "" {
			missing = append(missing, "check-in date")
		}
		if p.CheckOut == "" {
			missing = append(missing, "check-out date")
		}
	} else if intent != "deals" && p.Date == "" {
		missing = append(missing, "date")
	}

	fmt.Fprintf(os.Stderr, "Missing: %s\n", strings.Join(missing, ", "))
	fmt.Fprintf(os.Stderr, "Try: %s\n", template)
	fmt.Fprintln(os.Stderr, "Or use the travel MCP tool from an AI assistant for free-form parsing.")
	return nil
}
