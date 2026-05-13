package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/visa"
	"github.com/spf13/cobra"
)

func visaCmd() *cobra.Command {
	var (
		passport    string
		destination string
		listAll     bool
	)

	cmd := &cobra.Command{
		Use:   "visa",
		Short: "Check visa and entry requirements for a passport→destination pair",
		Long: `Look up visa requirements between countries using ISO country codes.

Examples:
  trvl visa --passport FI --destination JP
  trvl visa --passport US --destination TH
  trvl visa --passport GB --destination CN --format json
  trvl visa --list                             # list all supported country codes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listAll {
				codes := visa.ListCountries()
				if format == "json" {
					type entry struct {
						Code string `json:"code"`
						Name string `json:"name"`
					}
					var entries []entry
					for _, c := range codes {
						entries = append(entries, entry{Code: c, Name: visa.CountryName(c)})
					}
					return models.FormatJSON(os.Stdout, entries)
				}
				models.Banner(os.Stdout, "🌍", "Supported Countries")
				fmt.Println()
				for _, c := range codes {
					fmt.Printf("  %s  %s\n", c, visa.CountryName(c))
				}
				fmt.Println()
				return nil
			}

			if passport == "" || destination == "" {
				return fmt.Errorf("both --passport and --destination are required (use ISO country codes, e.g. FI, JP, US)")
			}

			result := visa.Lookup(passport, destination)

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			if !result.Success {
				_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", result.Error)
				return nil
			}

			return printVisaResult(result)
		},
	}

	cmd.Flags().StringVar(&passport, "passport", "", "Passport country code (ISO 3166-1 alpha-2, e.g. FI)")
	cmd.Flags().StringVar(&destination, "destination", "", "Destination country code (ISO 3166-1 alpha-2, e.g. JP)")
	cmd.Flags().BoolVar(&listAll, "list", false, "List all supported country codes")
	return cmd
}

func printVisaResult(result visa.Result) error {
	req := result.Requirement
	from := visa.CountryName(req.Passport)
	to := visa.CountryName(req.Destination)

	models.Banner(os.Stdout, "🌍", fmt.Sprintf("Visa · %s (%s) → %s (%s)", from, req.Passport, to, req.Destination))
	fmt.Println()

	emoji := visa.StatusEmoji(req.Status)
	statusLabel := strings.ReplaceAll(req.Status, "-", " ")
	statusLabel = strings.ToUpper(statusLabel[:1]) + statusLabel[1:]

	fmt.Printf("  Status:    %s %s\n", emoji, colorizeVisaStatus(req.Status, statusLabel))

	if req.MaxStay != "" {
		fmt.Printf("  Max stay:  %s\n", req.MaxStay)
	}

	if req.Notes != "" {
		fmt.Println()
		fmt.Printf("  %s\n", models.Dim(req.Notes))
	}

	fmt.Println()
	fmt.Println("  " + models.Dim("Data is advisory only. Always verify with the destination embassy before travel."))
	fmt.Println()
	return nil
}

func colorizeVisaStatus(status, label string) string {
	switch status {
	case "visa-free", "freedom-of-movement":
		return models.Green(label)
	case "visa-on-arrival":
		return models.Yellow(label)
	case "e-visa":
		return models.Yellow(label)
	case "visa-required":
		return models.Red(label)
	default:
		return label
	}
}
