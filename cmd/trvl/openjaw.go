package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/openjaw"
	"github.com/spf13/cobra"
)

func openJawCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open-jaw <flight-leg> <ground-leg>",
		Short: "Price a flight into a city and a ground leg out of it as one total",
		Long: `open-jaw adds one flight and one ground leg that leaves the flight's arrival city.

Each leg is ORIGIN:DESTINATION:COST[:CURRENCY].
The first leg is the flight. The second is the train, bus, or ferry.
When both legs name a currency, the currencies have to match.

Examples:
  trvl open-jaw HEL:VIE:180:EUR VIE:BUD:40:EUR
  trvl open-jaw HEL:VIE:180:EUR VIE:BUD:40:EUR --format json`,
		Args: cobra.ExactArgs(2),
		RunE: runOpenJaw,
	}
	return cmd
}

func runOpenJaw(cmd *cobra.Command, args []string) error {
	format, _ := cmd.Flags().GetString("format")
	flight, err := parseOpenJawLeg(args[0], "flight")
	if err != nil {
		return fmt.Errorf("open-jaw: %w", err)
	}
	ground, err := parseOpenJawLeg(args[1], "train")
	if err != nil {
		return fmt.Errorf("open-jaw: %w", err)
	}
	bundle, err := openjaw.Compose(flight, ground)
	if err != nil {
		return fmt.Errorf("open-jaw: %w", err)
	}
	if format == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(bundle)
	}
	currency := bundle.Currency
	if currency != "" {
		currency = " " + currency
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Open-jaw total: %.2f%s\n", bundle.Total, currency)
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s -> %s, then %s %s -> %s\n",
		bundle.Legs[0].Mode, bundle.Legs[0].Origin, bundle.Legs[0].Destination,
		bundle.Legs[1].Mode, bundle.Legs[1].Origin, bundle.Legs[1].Destination)
	return nil
}

func parseOpenJawLeg(raw, mode string) (openjaw.Leg, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return openjaw.Leg{}, fmt.Errorf("leg %q must be ORIGIN:DESTINATION:COST[:CURRENCY]", raw)
	}
	cost, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return openjaw.Leg{}, fmt.Errorf("cost in %q: %w", raw, err)
	}
	leg := openjaw.Leg{
		Mode:        mode,
		Origin:      strings.TrimSpace(parts[0]),
		Destination: strings.TrimSpace(parts[1]),
		Cost:        cost,
	}
	if len(parts) == 4 {
		leg.Currency = strings.TrimSpace(parts[3])
	}
	return leg, nil
}
