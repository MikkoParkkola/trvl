package main

import (
	"encoding/json"
	"fmt"
	"math"
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

Each leg is ORIGIN:DESTINATION:COST:CURRENCY[:MODE].
The first leg is the flight. The second defaults to train. Its mode may be train, bus, or ferry.
Both currencies are required and have to match.

Examples:
  trvl open-jaw HEL:VIE:180:EUR VIE:BUD:40:EUR
  trvl open-jaw HEL:VIE:180:EUR VIE:BUD:40:EUR:ferry
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
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Open-jaw total: %.2f%s\n", bundle.Total, currency); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s -> %s, then %s %s -> %s\n",
		bundle.Legs[0].Mode, bundle.Legs[0].Origin, bundle.Legs[0].Destination,
		bundle.Legs[1].Mode, bundle.Legs[1].Origin, bundle.Legs[1].Destination)
	return err
}

func parseOpenJawLeg(raw, defaultMode string) (openjaw.Leg, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 4 || len(parts) > 5 {
		return openjaw.Leg{}, fmt.Errorf("leg %q must be ORIGIN:DESTINATION:COST:CURRENCY[:MODE]", raw)
	}
	cost, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return openjaw.Leg{}, fmt.Errorf("cost in %q must be a finite zero or positive number", raw)
	}
	mode := defaultMode
	if len(parts) == 5 {
		if defaultMode == "flight" {
			return openjaw.Leg{}, fmt.Errorf("flight leg %q does not take a mode", raw)
		}
		mode = strings.ToLower(strings.TrimSpace(parts[4]))
		switch mode {
		case "train", "bus", "ferry":
		default:
			return openjaw.Leg{}, fmt.Errorf("ground mode %q must be train, bus, or ferry", parts[4])
		}
	}
	return openjaw.Leg{
		Mode:        mode,
		Origin:      strings.TrimSpace(parts[0]),
		Destination: strings.TrimSpace(parts[1]),
		Cost:        cost,
		Currency:    strings.TrimSpace(parts[3]),
	}, nil
}
