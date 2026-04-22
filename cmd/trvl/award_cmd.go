package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// runAwardScan implements the --award flag for `trvl flights`.
// date is either a month ("2026-06") or a day ("2026-06-15").
// Cookies can be passed directly or via AFKL_KLM_COOKIES env var.
func runAwardScan(ctx context.Context, origin, destination, date, cookies, format string) error {
	scanner, err := afklm.NewAwardScanner(afklm.AwardScannerOptions{
		Cookies: cookies,
	})
	if err == afklm.ErrNoAwardCookies {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Award search requires Flying Blue session cookies.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "To get your cookies:")
		fmt.Fprintln(os.Stderr, "  1. Log in to klm.com in Brave/Chrome")
		fmt.Fprintln(os.Stderr, "  2. Open DevTools → Network → search for a flight with miles")
		fmt.Fprintln(os.Stderr, "  3. Copy the Cookie header from any klm.com request")
		fmt.Fprintln(os.Stderr, "  4. Export: export AFKL_KLM_COOKIES='<paste cookies here>'")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Then re-run: trvl flights "+origin+" "+destination+" "+date+" --award")
		return nil
	}
	if err != nil {
		return fmt.Errorf("award scanner: %w", err)
	}

	// Determine date range.
	var dates []string
	if isMonthString(date) {
		dates = afklm.MonthDateRange(date)
		if len(dates) == 0 {
			return fmt.Errorf("award: invalid month %q (expected format: 2026-06)", date)
		}
	} else {
		dates = afklm.DateRange(date, date)
		if len(dates) == 0 {
			return fmt.Errorf("award: invalid date %q (expected 2026-06-15 or 2026-06)", date)
		}
	}

	fmt.Fprintf(os.Stderr, "Scanning %d dates for %s→%s award availability (1 req/sec)...\n",
		len(dates), strings.ToUpper(origin), strings.ToUpper(destination))

	result, err := scanner.ScanDateRange(ctx, origin, destination, dates)
	if err != nil && len(result.Offers) == 0 {
		return fmt.Errorf("award scan: %w", err)
	}

	// Report per-date errors (non-fatal).
	if len(result.Errors) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: %d date(s) had errors (shown below).\n", len(result.Errors))
	}

	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	return printAwardTable(result)
}

// printAwardTable renders the award scan result as an ASCII table.
func printAwardTable(result *afklm.AwardScanResult) error {
	if len(result.Offers) == 0 {
		if len(result.Errors) > 0 {
			fmt.Println("No award offers found. Errors:")
			for date, errMsg := range result.Errors {
				fmt.Printf("  %s: %s\n", date, errMsg)
			}
		} else {
			fmt.Println("No award flights found for this route and date range.")
		}
		return nil
	}

	// Sort offers: by miles asc, then date, then departure time.
	sort.Slice(result.Offers, func(i, j int) bool {
		a, b := result.Offers[i], result.Offers[j]
		if a.Miles != b.Miles {
			return a.Miles < b.Miles
		}
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.DepartureTime < b.DepartureTime
	})

	// Build table rows.
	headers := []string{"Date", "Miles", "Tax EUR", "Suitable", "Flight", "Departs", "Arrives", "Stops", "Cabin"}
	var rows [][]string

	suitableCount := 0
	idealCount := 0

	for _, o := range result.Offers {
		suitable := "-"
		if o.Suitable() {
			suitable = "ok"
			suitableCount++
		}
		if o.Ideal() {
			suitable = "ideal"
			idealCount++
		}

		stops := "Direct"
		if o.Stops > 0 {
			stops = fmt.Sprintf("%d stop", o.Stops)
			if o.Stops > 1 {
				stops += "s"
			}
		}

		rows = append(rows, []string{
			o.Date,
			formatMiles(o.Miles),
			fmt.Sprintf("%.2f", o.TaxEUR),
			suitable,
			o.FlightNumber,
			o.DepartureTime,
			o.ArrivalTime,
			stops,
			strings.ToLower(o.Cabin),
		})
	}

	route := fmt.Sprintf("%s → %s", strings.ToUpper(result.Origin), strings.ToUpper(result.Destination))
	models.Banner(os.Stdout, "", "Flying Blue Award Scan", route, fmt.Sprintf("%d offers found", len(result.Offers)))
	fmt.Println()

	models.FormatTable(os.Stdout, headers, rows)

	// Summary.
	fmt.Println()
	fmt.Printf("  Balance ceiling (15,000 mi): %d offer(s) suitable\n", suitableCount)
	fmt.Printf("  Ideal target (10,000 mi):   %d offer(s) ideal\n", idealCount)
	if len(result.Errors) > 0 {
		fmt.Printf("  Errors on %d date(s):\n", len(result.Errors))
		for date, errMsg := range result.Errors {
			fmt.Printf("    %s: %s\n", date, errMsg)
		}
	}
	fmt.Println()
	fmt.Println("  Book at: klm.com → Book with miles")

	return nil
}

// isMonthString returns true if s looks like "2026-06" (7 chars, no day part).
func isMonthString(s string) bool {
	return len(s) == 7 && s[4] == '-'
}
