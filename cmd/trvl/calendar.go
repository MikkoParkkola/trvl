package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/calendar"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/spf13/cobra"
)

// calendarCmd implements `trvl calendar` — exports trips as iCalendar (.ics)
// files for import into Apple Calendar, Google Calendar, Outlook, etc.
//
// Each leg becomes one VEVENT. Hotel legs are emitted as multi-day all-day
// events. Confirmed legs get STATUS:CONFIRMED, planned legs get TENTATIVE.
//
// Examples:
//
//	trvl calendar trip_abc123                    # print to stdout
//	trvl calendar trip_abc123 --output trip.ics  # write to file
//	trvl calendar --last                         # use the most recent search
//	trvl calendar --last --output last.ics
func calendarCmd() *cobra.Command {
	var (
		output  string
		last    bool
		tripID  string
		icsFlag bool
	)

	cmd := &cobra.Command{
		Use:   "calendar [trip_id]",
		Short: "Export a trip as an iCalendar (.ics) file",
		Long: `Export a saved trip as an iCalendar (.ics) file. Compatible with
Apple Calendar, Google Calendar, Outlook, and all RFC 5545 calendar apps.

Each trip leg becomes a calendar event:
  • Flights / trains / buses / ferries — timed events using leg start/end
  • Hotel stays — multi-day all-day events spanning check-in to check-out
  • Confirmed bookings get STATUS:CONFIRMED, planned legs get STATUS:TENTATIVE

Output goes to stdout by default. Use --output FILE to write to disk.
Use --ics with --trip-id to auto-generate a filename from the trip name.

Examples:
  trvl calendar trip_abc123
  trvl calendar trip_abc123 --output ~/Desktop/krakow-trip.ics
  trvl calendar --trip-id trip_abc123 --ics
  trvl calendar --last
  trvl calendar --last --output last-search.ics`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var trip *trips.Trip

			switch {
			case last:
				ls, err := loadLastSearch()
				if err != nil {
					return err
				}
				trip = lastSearchToTrip(ls)

			case tripID != "":
				store, err := loadTripStore()
				if err != nil {
					return err
				}
				t, err := store.Get(tripID)
				if err != nil {
					return err
				}
				trip = t

			case len(args) == 1:
				store, err := loadTripStore()
				if err != nil {
					return err
				}
				t, err := store.Get(args[0])
				if err != nil {
					return err
				}
				trip = t

			default:
				return fmt.Errorf("provide a trip_id argument, --trip-id flag, or --last flag")
			}

			icsContent := calendar.WriteICS(trip)

			// When --ics is set, auto-generate the output filename from the trip name.
			if icsFlag && output == "" {
				output = icsFilename(trip.Name)
			}

			if output == "" || output == "-" {
				fmt.Print(icsContent)
				return nil
			}

			if err := os.WriteFile(output, []byte(icsContent), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", output, err)
			}
			_, _ = fmt.Fprintf(os.Stderr, "Wrote %d events to %s\n", len(trip.Legs), output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output file path (default: stdout)")
	cmd.Flags().BoolVar(&last, "last", false, "Export the most recent search instead of a saved trip")
	cmd.Flags().StringVar(&tripID, "trip-id", "", "Trip ID to export")
	cmd.Flags().BoolVar(&icsFlag, "ics", false, "Write to an auto-named .ics file (trip_name.ics)")
	return cmd
}

// icsFilename generates a safe .ics filename from a trip name.
// "Krakow Weekend" becomes "krakow_weekend.ics"
func icsFilename(name string) string {
	if name == "" {
		return "trip.ics"
	}
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if r == ' ' || r == '-' {
			return '_'
		}
		return -1 // drop
	}, name)
	if safe == "" {
		return "trip.ics"
	}
	return safe + ".ics"
}

// lastSearchToTrip synthesizes a one-leg Trip from a cached LastSearch so the
// calendar exporter can render it without special-casing the LastSearch shape.
func lastSearchToTrip(ls *LastSearch) *trips.Trip {
	t := &trips.Trip{
		ID:        "last_" + time.Now().Format("20060102_150405"),
		Name:      lastSearchName(ls),
		Status:    "planning",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if ls.FlightPrice > 0 {
		t.Legs = append(t.Legs, trips.TripLeg{
			Type:      "flight",
			From:      ls.Origin,
			To:        ls.Destination,
			Provider:  ls.FlightAirline,
			StartTime: ls.DepartDate,
			EndTime:   ls.DepartDate,
			Price:     ls.FlightPrice,
			Currency:  ls.FlightCurrency,
		})
		// Round-trip return leg.
		if ls.ReturnDate != "" {
			t.Legs = append(t.Legs, trips.TripLeg{
				Type:      "flight",
				From:      ls.Destination,
				To:        ls.Origin,
				Provider:  ls.FlightAirline,
				StartTime: ls.ReturnDate,
				EndTime:   ls.ReturnDate,
				Currency:  ls.FlightCurrency,
			})
		}
	}

	if ls.HotelPrice > 0 {
		t.Legs = append(t.Legs, trips.TripLeg{
			Type:      "hotel",
			From:      ls.Destination,
			To:        ls.Destination,
			Provider:  ls.HotelName,
			StartTime: ls.DepartDate,
			EndTime:   ls.ReturnDate,
			Price:     ls.HotelPrice,
			Currency:  ls.HotelCurrency,
		})
	}

	return t
}

// lastSearchName builds a friendly name for the synthesized trip.
func lastSearchName(ls *LastSearch) string {
	parts := []string{}
	if ls.Origin != "" && ls.Destination != "" {
		parts = append(parts, ls.Origin+" → "+ls.Destination)
	}
	if ls.DepartDate != "" {
		parts = append(parts, ls.DepartDate)
	}
	if len(parts) == 0 {
		parts = append(parts, "Last search")
	}
	return strings.Join(parts, " · ")
}
