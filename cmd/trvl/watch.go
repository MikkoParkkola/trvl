package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/livecheck"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
	"github.com/spf13/cobra"
)

func watchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Track flight and hotel prices, and room availability",
		Long: `Monitor flight and hotel prices over time and get alerts when prices drop.
Also supports room-level availability monitoring with keyword matching.

Examples:
  trvl watch add HEL BCN --depart 2026-07-01 --return 2026-07-08 --below 200
  trvl watch rooms "Beverly Hills Heights, Tenerife" --checkin 2026-07-01 --checkout 2026-07-08 --keywords "2 bedroom,balcony,sea view"
  trvl watch list
  trvl watch check
  trvl watch history <id>
  trvl watch remove <id>`,
	}

	cmd.AddCommand(
		watchAddCmd(),
		watchRoomsCmd(),
		watchListCmd(),
		watchRemoveCmd(),
		watchCheckCmd(),
		watchDaemonCmd(),
		watchHistoryCmd(),
		watchMigrateCmd(),
	)
	return cmd
}

func watchAddCmd() *cobra.Command {
	var (
		departDate     string
		returnDate     string
		departFrom     string
		departTo       string
		belowPrice     float64
		currency       string
		watchType      string
		webhookURL     string
		lastMinute     bool
		lastMinuteDrop float64
		alertDropPct   float64
		alertDropAbs   float64
	)

	cmd := &cobra.Command{
		Use:   "add ORIGIN DESTINATION",
		Short: "Add a price watch",
		Long: `Add a new price watch for a flight or hotel route.

Three modes:
  Specific date:  --depart 2026-07-01        Check one date
  Date range:     --from 2026-07-01 --to 2026-07-31   Cheapest in range
  Route watch:    (no dates)                  Monitor next 60 days for deals

Examples:
  trvl watch add HEL BCN --depart 2026-07-01 --return 2026-07-08 --below 200
  trvl watch add HEL PRG --from 2026-07-01 --to 2026-08-31 --below 100
  trvl watch add HEL NRT --below 500
  trvl watch add --type hotel Prague --depart 2026-07-01 --return 2026-07-08 --below 80`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			origin := args[0]
			destination := ""
			if len(args) >= 2 {
				destination = args[1]
			} else {
				// Single arg: for hotels, it's the destination
				destination = args[0]
				origin = ""
			}

			w := watch.Watch{
				Type:              watchType,
				Origin:            origin,
				Destination:       destination,
				DepartDate:        departDate,
				ReturnDate:        returnDate,
				DepartFrom:        departFrom,
				DepartTo:          departTo,
				BelowPrice:        belowPrice,
				Currency:          currency,
				WebhookURL:        webhookURL,
				LastMinuteMode:    lastMinute,
				LastMinuteDropPct: lastMinuteDrop,
				AlertDropPct:      alertDropPct,
				AlertDropAbs:      alertDropAbs,
			}

			id, created, err := store.Add(w)
			if err != nil {
				return fmt.Errorf("add watch: %w", err)
			}

			mode := ""
			switch {
			case w.IsRouteWatch():
				mode = "route watch (next 60 days)"
			case w.IsDateRange():
				mode = fmt.Sprintf("date range %s to %s", w.DepartFrom, w.DepartTo)
			default:
				mode = fmt.Sprintf("on %s", w.DepartDate)
			}

			verb := "Added"
			if !created {
				verb = "Updated existing"
			}
			fmt.Printf("%s %s watch %s: %s -> %s %s", verb,
				w.Type, id, w.Origin, w.Destination, mode)
			if w.ReturnDate != "" {
				fmt.Printf(" (return %s)", w.ReturnDate)
			}
			if w.BelowPrice > 0 {
				fmt.Printf(" [alert below %.0f %s]", w.BelowPrice, w.Currency)
			}
			if w.LastMinuteMode {
				fmt.Printf(" [last-minute %.0f%% drop]", w.LastMinuteDropPct)
			}
			if w.AlertDropPct > 0 || w.AlertDropAbs > 0 {
				switch {
				case w.AlertDropPct > 0 && w.AlertDropAbs > 0:
					fmt.Printf(" [drop alert %.0f%% or %.0f %s]", w.AlertDropPct, w.AlertDropAbs, w.Currency)
				case w.AlertDropPct > 0:
					fmt.Printf(" [drop alert %.0f%%]", w.AlertDropPct)
				default:
					fmt.Printf(" [drop alert %.0f %s]", w.AlertDropAbs, w.Currency)
				}
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVar(&departDate, "depart", "", "Specific departure date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&returnDate, "return", "", "Return/check-out date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&departFrom, "from", "", "Date range start (YYYY-MM-DD)")
	cmd.Flags().StringVar(&departTo, "to", "", "Date range end (YYYY-MM-DD)")
	cmd.Flags().Float64Var(&belowPrice, "below", 0, "Alert when price drops below this amount")
	cmd.Flags().StringVar(&currency, "currency", "", "Currency for price alerts (e.g. EUR). Empty = API default")
	cmd.Flags().StringVar(&watchType, "type", "flight", "Watch type: flight or hotel")
	cmd.Flags().StringVar(&webhookURL, "webhook", "", "URL to POST JSON payload on price drop")
	cmd.Flags().BoolVar(&lastMinute, "last-minute", false, "Hotel watches: alert when sub-48h availability is materially below last seen price")
	cmd.Flags().Float64Var(&lastMinuteDrop, "last-minute-drop", 25, "Hotel watches: percent drop from last seen price required for last-minute alert")
	cmd.Flags().Float64Var(&alertDropPct, "alert-drop", 0, "Proactively alert when the fare falls this percent below its baseline (default 10% if no threshold set)")
	cmd.Flags().Float64Var(&alertDropAbs, "alert-drop-abs", 0, "Proactively alert when the fare falls this many currency units below its baseline")
	// --depart is optional: route watches monitor next 60 days without specific dates

	return cmd
}

func watchRoomsCmd() *cobra.Command {
	var (
		checkIn    string
		checkOut   string
		keywords   string
		belowPrice float64
		currency   string
	)

	cmd := &cobra.Command{
		Use:   "rooms HOTEL_NAME",
		Short: "Watch for room availability matching criteria",
		Long: `Monitor a specific hotel for when rooms matching your criteria become available.

Keywords are matched case-insensitively against room names and descriptions.
All keywords must match for a room to be considered a hit.

Examples:
  trvl watch rooms "Beverly Hills Heights, Tenerife" --checkin 2026-07-01 --checkout 2026-07-08 --keywords "2 bedroom,balcony,sea view"
  trvl watch rooms "Hotel Lutetia Paris" --checkin 2026-08-01 --checkout 2026-08-05 --keywords "suite,terrace" --below 500`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hotelName := args[0]

			if keywords == "" {
				return fmt.Errorf("--keywords is required (comma-separated, e.g. \"2 bedroom,balcony,sea view\")")
			}

			// Parse keywords: comma-separated, trimmed.
			var kws []string
			for _, k := range strings.Split(keywords, ",") {
				k = strings.TrimSpace(k)
				if k != "" {
					kws = append(kws, k)
				}
			}
			if len(kws) == 0 {
				return fmt.Errorf("at least one non-empty keyword is required")
			}

			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			w := watch.Watch{
				Type:         "room",
				HotelName:    hotelName,
				Destination:  hotelName, // for display in list
				DepartDate:   checkIn,
				ReturnDate:   checkOut,
				RoomKeywords: kws,
				BelowPrice:   belowPrice,
				Currency:     currency,
			}

			id, created, err := store.Add(w)
			if err != nil {
				return fmt.Errorf("add room watch: %w", err)
			}

			verb := "Added"
			if !created {
				verb = "Updated existing"
			}
			fmt.Printf("%s room watch %s: %s (%s to %s)\n", verb, id, hotelName, checkIn, checkOut)
			fmt.Printf("  Keywords: %s\n", strings.Join(kws, ", "))
			if belowPrice > 0 {
				fmt.Printf("  Alert below: %.0f %s\n", belowPrice, currency)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&checkIn, "checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().StringVar(&checkOut, "checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().StringVar(&keywords, "keywords", "", "Comma-separated keywords to match (e.g. \"2 bedroom,balcony,sea view\")")
	cmd.Flags().Float64Var(&belowPrice, "below", 0, "Alert when matching room price is below this amount")
	cmd.Flags().StringVar(&currency, "currency", "USD", "Currency for price alerts")

	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")
	_ = cmd.MarkFlagRequired("keywords")

	return cmd
}

func watchListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show all active watches",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			watches := store.List()
			if len(watches) == 0 {
				fmt.Println("No active watches.")
				return nil
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, watches)
			}

			headers := []string{"ID", "Type", "Route", "Dates", "Goal", "Last", "Lowest", "Trend", "Checked"}
			rows := make([][]string, 0, len(watches))
			for _, w := range watches {
				route := w.Origin + " -> " + w.Destination
				if w.IsRoomWatch() {
					route = w.HotelName
				}

				dates := formatWatchDates(w)

				goal := ""
				if w.BelowPrice > 0 {
					goal = fmt.Sprintf("%.0f %s", w.BelowPrice, w.Currency)
				}
				last := ""
				if w.LastPrice > 0 {
					last = fmt.Sprintf("%.0f %s", w.LastPrice, w.Currency)
				}
				lowest := ""
				if w.LowestPrice > 0 {
					lowest = fmt.Sprintf("%.0f %s", w.LowestPrice, w.Currency)
				}

				// Sparkline + trend arrow from price history.
				history := store.History(w.ID)
				trend := watch.Sparkline(history, 10)
				arrow := watch.TrendArrow(history)
				if arrow != "" {
					trend = trend + " " + arrow
				}

				checked := formatLastCheck(w.LastCheck)

				// A watch that has stopped being checked must say so. Expiry is
				// silent otherwise: the row looks identical to a healthy watch
				// while no price is ever fetched again, so a missed price drop
				// is indistinguishable from "no drop happened".
				if !watch.IsActiveNow(w) {
					checked += " (expired)"
				}

				rows = append(rows, []string{
					w.ID, w.Type, route, dates,
					goal, last, lowest, trend, checked,
				})
			}

			models.FormatTable(os.Stdout, headers, rows)
			return nil
		},
	}
}

// formatWatchDates returns a compact date summary depending on watch mode.
func formatWatchDates(w watch.Watch) string {
	switch {
	case w.IsRoomWatch():
		s := w.DepartDate + " / " + w.ReturnDate
		if w.MatchedRoom != "" {
			s += " [" + w.MatchedRoom + "]"
		}
		return s
	case w.IsRouteWatch():
		if w.CheapestDate != "" {
			return "any (best: " + w.CheapestDate + ")"
		}
		return "any (next 60d)"
	case w.IsDateRange():
		s := w.DepartFrom + " .. " + w.DepartTo
		if w.CheapestDate != "" {
			s += " (best: " + w.CheapestDate + ")"
		}
		return s
	default:
		s := w.DepartDate
		if w.ReturnDate != "" {
			s += " / " + w.ReturnDate
		}
		return s
	}
}

// formatLastCheck returns a human-readable relative time for the last check.
func formatLastCheck(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func watchRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove ID",
		Short: "Remove a watch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			found, err := store.Remove(args[0])
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("watch %s not found", args[0])
			}

			fmt.Printf("Removed watch %s\n", args[0])
			return nil
		},
	}
}

func watchCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check all watches for price and room availability changes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			notifier := &watch.Notifier{
				Out:      os.Stdout,
				UseColor: models.UseColor,
				Desktop:  true,
			}

			count, err := runWatchCheckCycleWithRooms(cmd.Context(), &liveChecker{}, &liveRoomChecker{}, notifier)
			if err != nil {
				return err
			}
			if count == 0 {
				fmt.Println("No active watches to check.")
				return nil
			}
			return nil
		},
	}
}

func watchMigrateCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Collapse duplicate watches and compact oversized price history",
		Long: `Bring an existing watch store up to current invariants.

Watches only became de-duplicated on creation recently, so a store built before
that keeps every copy ever added — one real store held 468 route watches
covering 4 destinations, plus 380 copies of a single room watch. Price history
retention likewise applies only to new observations, so an existing oversized
history file stays exactly as large as it was and keeps costing memory in every
running trvl process.

This command fixes both, once, explicitly. It is idempotent and takes a
timestamped backup of the store before writing anything.

Duplicates are merged rather than dropped: the surviving watch is the one
carrying real price observations, then the most recently checked, keeping the
earliest creation date of the group so its history is not shortened.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			if dryRun {
				preview, err := store.MigrateDryRun()
				if err != nil {
					return err
				}
				fmt.Println(preview.Summary())
				if preview.Changed() {
					fmt.Println("\nRe-run without --dry-run to apply.")
				}
				return nil
			}

			report, err := store.Migrate()
			if err != nil {
				return err
			}
			fmt.Println(report.Summary())
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would change without writing")
	return cmd
}

func watchHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history ID",
		Short: "Show price history for a watch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			w, ok := store.Get(args[0])
			if !ok {
				return fmt.Errorf("watch %s not found", args[0])
			}

			history := store.History(args[0])
			if len(history) == 0 {
				fmt.Printf("No price history for watch %s (%s -> %s).\n",
					w.ID, w.Origin, w.Destination)
				return nil
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, history)
			}

			fmt.Printf("Price history for %s -> %s (watch %s):\n\n",
				w.Origin, w.Destination, w.ID)

			headers := []string{"Date", "Price", "Currency"}
			rows := make([][]string, 0, len(history))
			for _, p := range history {
				rows = append(rows, []string{
					p.Timestamp.Format("2006-01-02 15:04"),
					fmt.Sprintf("%.0f", p.Price),
					p.Currency,
				})
			}

			models.FormatTable(os.Stdout, headers, rows)
			return nil
		},
	}
}

// liveChecker implements watch.PriceChecker using the real flight/hotel search APIs.
type liveChecker struct{}

func (c *liveChecker) CheckPrice(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	return livecheck.Checker{}.CheckPrice(ctx, w)
}

// liveRoomChecker implements watch.RoomChecker using the real hotel rooms API.
type liveRoomChecker struct{}

func (c *liveRoomChecker) CheckRooms(ctx context.Context, w watch.Watch) ([]watch.RoomMatch, error) {
	currency := w.Currency
	if currency == "" {
		currency = "USD"
	}

	result, err := resolveRoomAvailability(ctx, w.HotelName, w.DepartDate, w.ReturnDate, currency, "")
	if err != nil {
		return nil, err
	}

	var matches []watch.RoomMatch
	for _, room := range result.Rooms {
		if watch.MatchRoomKeywords(w.RoomKeywords, room.Name, room.Description) {
			matches = append(matches, watch.RoomMatch{
				Name:        room.Name,
				Description: room.Description,
				Price:       room.Price,
				Currency:    room.Currency,
				Provider:    room.Provider,
			})
		}
	}
	return matches, nil
}
