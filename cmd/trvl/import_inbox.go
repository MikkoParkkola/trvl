package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/inboxparser"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/spf13/cobra"
)

// importInboxResult is the structured outcome of an import-inbox run. It is the
// return value of the pure collectInbox helper so tests can assert on parsing
// without touching the on-disk trip store.
type importInboxResult struct {
	Trip    trips.Trip                `json:"trip"`
	Summary inboxparser.IngestSummary `json:"summary"`
	Read    []string                  `json:"read"`    // files successfully read
	Skipped []skippedFile             `json:"skipped"` // files that could not be read
}

// skippedFile records a path that was skipped along with the reason, so the run
// is fail-soft per file rather than aborting the whole import.
type skippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// importInboxCmd implements `trvl import-inbox` — the no-manual-entry path that
// turns travel-confirmation emails into a trip with bookings and legs.
//
// It accepts one or more .eml file paths and/or directories of them, parses
// each via internal/inboxparser, and either prints the resulting summary
// (default) or persists it: --save NAME creates a new trip, --trip-id updates
// an existing one. Re-importing the same mail is idempotent on provider+ref.
//
// Examples:
//
//	trvl import-inbox confirmation.eml
//	trvl import-inbox ~/Mail/travel/                 # all .eml in a directory
//	trvl import-inbox klm.eml booking.eml --save "Paris weekend"
//	trvl import-inbox klm.eml --trip-id trip_abc123
//	trvl import-inbox ~/Mail/travel/ --format json
func importInboxCmd() *cobra.Command {
	var (
		save   string
		tripID string
	)

	cmd := &cobra.Command{
		Use:   "import-inbox FILE_OR_DIR [FILE_OR_DIR...]",
		Short: "Import travel-confirmation emails (.eml) into a trip",
		Long: `Parse travel-confirmation emails and build a trip from them — no manual entry.

Each argument is an .eml file or a directory containing .eml files. Recognised
providers (KLM, Booking.com, Airbnb) are turned into trip legs and bookings;
unrecognised or unreadable files are skipped without aborting the run.

By default the parsed trip is summarised to stdout. To persist:
  --save NAME      create a new saved trip from the imported mail
  --trip-id ID     merge the imported mail into an existing saved trip

Re-importing the same confirmation is idempotent (deduplicated on provider +
booking reference).

Examples:
  trvl import-inbox confirmation.eml
  trvl import-inbox ~/Mail/travel/
  trvl import-inbox klm.eml booking.eml --save "Paris weekend"
  trvl import-inbox klm.eml --trip-id trip_abc123
  trvl import-inbox ~/Mail/travel/ --format json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if save != "" && tripID != "" {
				return fmt.Errorf("use only one of --save or --trip-id, not both")
			}

			paths, err := expandEmlPaths(args)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("no .eml files found in: %s", strings.Join(args, ", "))
			}

			base := trips.Trip{Name: save, Status: "planning"}
			if tripID != "" {
				store, err := loadTripStore()
				if err != nil {
					return err
				}
				existing, err := store.Get(tripID)
				if err != nil {
					return err
				}
				base = *existing
			}

			result := collectInbox(base, paths, os.ReadFile)

			switch {
			case tripID != "":
				store, err := loadTripStore()
				if err != nil {
					return err
				}
				if err := store.Update(tripID, func(t *trips.Trip) error {
					*t = result.Trip
					return nil
				}); err != nil {
					return err
				}
				result.Trip.ID = tripID
			case save != "":
				store, err := loadTripStore()
				if err != nil {
					return err
				}
				id, err := store.Add(result.Trip)
				if err != nil {
					return err
				}
				result.Trip.ID = id
			}

			if format == "json" {
				return models.FormatJSON(cmd.OutOrStdout(), result)
			}
			printImportInbox(cmd.OutOrStdout(), result, save != "" || tripID != "")
			return nil
		},
	}

	cmd.Flags().StringVar(&save, "save", "", "Save the import as a new trip with this name")
	cmd.Flags().StringVar(&tripID, "trip-id", "", "Merge the import into an existing saved trip")
	return cmd
}

// collectInbox is the pure core of import-inbox: it reads each path with the
// supplied reader, parses recognised confirmations into the base trip, and
// returns the merged trip plus a summary. It never panics and treats a read
// error or unrecognised mail as a soft per-file skip. The reader is injected so
// tests can exercise the logic without an on-disk store.
func collectInbox(base trips.Trip, paths []string, readFile func(string) ([]byte, error)) importInboxResult {
	res := importInboxResult{}

	raws := make([][]byte, 0, len(paths))
	for _, p := range paths {
		raw, err := readFile(p)
		if err != nil {
			res.Skipped = append(res.Skipped, skippedFile{Path: p, Reason: err.Error()})
			continue
		}
		raws = append(raws, raw)
		res.Read = append(res.Read, p)
	}

	merged, summary := inboxparser.IngestConfirmations(base, raws)
	res.Trip = merged
	res.Summary = summary
	return res
}

// expandEmlPaths turns the CLI arguments (files and/or directories) into a
// sorted, de-duplicated list of .eml file paths. A file argument is taken as-is
// regardless of extension (the user pointed at it explicitly); a directory is
// scanned one level deep for *.eml entries.
func expandEmlPaths(args []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string

	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", arg, err)
		}
		if !info.IsDir() {
			add(arg)
			continue
		}
		entries, err := os.ReadDir(arg)
		if err != nil {
			return nil, fmt.Errorf("read dir %s: %w", arg, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".eml") {
				continue
			}
			add(filepath.Join(arg, e.Name()))
		}
	}

	sort.Strings(out)
	return out, nil
}

// printImportInbox renders a concise human-readable summary of an import run.
func printImportInbox(w io.Writer, res importInboxResult, persisted bool) {
	s := res.Summary
	_, _ = fmt.Fprintf(w, "Imported %d file(s): %d parsed, %d unrecognised, %d skipped\n",
		len(res.Read)+len(res.Skipped), s.Parsed, s.Unrecognised, len(res.Skipped))
	_, _ = fmt.Fprintf(w, "  Legs in trip:    %d\n", len(res.Trip.Legs))
	_, _ = fmt.Fprintf(w, "  Bookings added:  %d (total %d)\n", s.BookingsAdded, len(res.Trip.Bookings))

	for _, b := range res.Trip.Bookings {
		ref := b.Reference
		if ref == "" {
			ref = "(no ref)"
		}
		_, _ = fmt.Fprintf(w, "    • %-10s %s %s\n", b.Provider, ref, b.Type)
	}
	for _, sk := range res.Skipped {
		_, _ = fmt.Fprintf(w, "  skipped %s: %s\n", sk.Path, sk.Reason)
	}
	if persisted && res.Trip.ID != "" {
		_, _ = fmt.Fprintf(w, "Saved trip: %s\n", res.Trip.ID)
	}
}
