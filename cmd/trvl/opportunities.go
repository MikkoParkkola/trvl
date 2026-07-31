package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
	"github.com/spf13/cobra"
)

func opportunitiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "opportunities",
		Short: "Manage opportunity watches (rolling window, favourite destinations)",
		Long: `Monitor a rolling time window for deals to your favourite destinations.

Examples:
  trvl opportunities
  trvl opportunities create --favourites "PRG,KRK,ZRH" --window-from next_30d --window-to next_90d
  trvl opportunities create --min-score 90 --min-nights 3 --max-nights 10`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListOpportunities(cmd)
		},
	}
	cmd.AddCommand(opportunitiesCreateCmd())
	return cmd
}

func runListOpportunities(_ *cobra.Command) error {
	store, err := watch.DefaultStore()
	if err != nil {
		return err
	}
	if err := store.Load(); err != nil {
		return err
	}

	var opps []watch.Watch
	for _, w := range store.List() {
		if w.IsOpportunityWatch() {
			opps = append(opps, w)
		}
	}

	if len(opps) == 0 {
		fmt.Println("No opportunity watches. Use 'trvl opportunities create' to add one.")
		return nil
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, opps)
	}

	headers := []string{"ID", "Favourites", "Window From", "Window To", "Min Score", "Nights", "Created"}
	rows := make([][]string, 0, len(opps))
	for _, w := range opps {
		favStr := strings.Join(w.Favourites, ",")
		if favStr == "" {
			favStr = "(profile)"
		}
		minScore := w.MinScore
		if minScore == 0 {
			minScore = 85
		}
		minNights := w.MinNights
		if minNights == 0 {
			minNights = 3
		}
		maxNights := w.MaxNights
		if maxNights == 0 {
			maxNights = 14
		}
		rows = append(rows, []string{
			w.ID,
			favStr,
			w.WindowFrom,
			w.WindowTo,
			fmt.Sprintf("%d", minScore),
			fmt.Sprintf("%d-%d", minNights, maxNights),
			w.CreatedAt.Format("2006-01-02"),
		})
	}
	models.FormatTable(os.Stdout, headers, rows)
	return nil
}

func opportunitiesCreateCmd() *cobra.Command {
	var (
		favouritesStr string
		windowFrom    string
		windowTo      string
		minScore      int
		minNights     int
		maxNights     int
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new opportunity watch",
		Long: `Create a new opportunity watch that tracks deals to favourite destinations
within a rolling time window.

Examples:
  trvl opportunities create --favourites "PRG,KRK,ZRH"
  trvl opportunities create --window-from next_30d --window-to next_90d --min-score 90`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var favourites []string
			if favouritesStr != "" {
				for _, code := range strings.Split(favouritesStr, ",") {
					code = strings.ToUpper(strings.TrimSpace(code))
					if code != "" {
						favourites = append(favourites, code)
					}
				}
			}

			if windowFrom == "" {
				windowFrom = "next_30d"
			}
			if windowTo == "" {
				windowTo = "next_90d"
			}
			if minScore == 0 {
				minScore = 85
			}
			if minNights == 0 {
				minNights = 3
			}
			if maxNights == 0 {
				maxNights = 14
			}

			store, err := watch.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Load(); err != nil {
				return err
			}

			w := watch.Watch{
				Type:       "opportunity",
				Favourites: favourites,
				WindowFrom: windowFrom,
				WindowTo:   windowTo,
				MinScore:   minScore,
				MinNights:  minNights,
				MaxNights:  maxNights,
			}

			id, created, err := store.Add(w)
			if err != nil {
				return fmt.Errorf("create opportunity watch: %w", err)
			}

			favStr := strings.Join(favourites, ", ")
			if favStr == "" {
				favStr = "(from profile)"
			}
			verb := "Created"
			if !created {
				verb = "Updated existing"
			}
			fmt.Printf("%s opportunity watch %s\n", verb, id)
			fmt.Printf("  Favourites: %s\n", favStr)
			fmt.Printf("  Window: %s → %s\n", windowFrom, windowTo)
			fmt.Printf("  Min score: %d | Nights: %d-%d\n", minScore, minNights, maxNights)
			return nil
		},
	}

	cmd.Flags().StringVar(&favouritesStr, "favourites", "", "Comma-separated destination IATA codes (e.g. PRG,KRK,ZRH)")
	cmd.Flags().StringVar(&windowFrom, "window-from", "next_30d", "Window start (YYYY-MM-DD or next_Nd)")
	cmd.Flags().StringVar(&windowTo, "window-to", "next_90d", "Window end (YYYY-MM-DD or next_Nd)")
	cmd.Flags().IntVar(&minScore, "min-score", 85, "Minimum composite score to alert (0-100)")
	cmd.Flags().IntVar(&minNights, "min-nights", 3, "Minimum trip length in nights")
	cmd.Flags().IntVar(&maxNights, "max-nights", 14, "Maximum trip length in nights")

	return cmd
}
