package main

// MIK-3088: trvl expenses — trip expense reconciler.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/expenses"
	"github.com/spf13/cobra"
)

func expensesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "expenses <payer:amount[:traveller1,traveller2]>...",
		Short: "Reconcile shared trip expenses — who owes whom",
		Long: `expenses computes the minimum set of transfers that settles shared trip
costs across a group of travellers. All amounts must use a single currency.

Each positional argument encodes one booking:
  payer:amount                   payer covers the full amount alone
  payer:amount:t1,t2,t3          payer fronted; split equally among t1,t2,t3
  payer:amount:t1=w1,t2=w2       weighted split (weights are relative, not %)

Use --file with a JSON array of Booking objects for richer input.

Examples:
  trvl expenses alice:300:alice,bob,carol bob:150:bob,carol carol:90:carol
  trvl expenses alice:500 bob:200:alice,bob
  trvl expenses --file bookings.json --format json`,
		Args: cobra.ArbitraryArgs,
		RunE: runExpenses,
	}
	cmd.Flags().String("file", "", "Path to JSON file with bookings array")
	return cmd
}

func runExpenses(cmd *cobra.Command, args []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	format, _ := cmd.Flags().GetString("format")
	var bookings []expenses.Booking
	var err error
	if filePath != "" {
		bookings, err = loadBookingsFromFile(filePath)
	} else {
		if len(args) == 0 {
			return fmt.Errorf("expenses: provide booking args or --file path")
		}
		bookings, err = parseBookingArgs(args)
	}
	if err != nil {
		return fmt.Errorf("expenses: %w", err)
	}
	settlement := expenses.Reconcile(bookings)
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(settlement)
	}
	fmt.Fprint(os.Stdout, expenses.Render(settlement))
	return nil
}

func loadBookingsFromFile(path string) ([]expenses.Booking, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()
	var b []expenses.Booking
	if err := json.NewDecoder(f).Decode(&b); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return b, nil
}

func parseBookingArgs(args []string) ([]expenses.Booking, error) {
	out := make([]expenses.Booking, 0, len(args))
	for i, arg := range args {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid booking %q: expected payer:amount[:travellers]", arg)
		}
		payer := strings.TrimSpace(parts[0])
		if payer == "" {
			return nil, fmt.Errorf("invalid booking %q: payer must not be empty", arg)
		}
		amount, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid booking %q: amount must be a number: %w", arg, err)
		}
		var split []expenses.ShareEntry
		if len(parts) == 3 {
			split, err = parseSplit(parts[2])
			if err != nil {
				return nil, fmt.Errorf("invalid booking %q split: %w", arg, err)
			}
		} else {
			split = []expenses.ShareEntry{{Traveller: payer, Weight: 1}}
		}
		out = append(out, expenses.Booking{ID: fmt.Sprintf("booking-%d", i+1), Payer: payer, Amount: amount, Split: split})
	}
	return out, nil
}

func parseSplit(raw string) ([]expenses.ShareEntry, error) {
	tokens := strings.Split(raw, ",")
	out := make([]expenses.ShareEntry, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if idx := strings.Index(tok, "="); idx >= 0 {
			name := strings.TrimSpace(tok[:idx])
			w, err := strconv.ParseFloat(strings.TrimSpace(tok[idx+1:]), 64)
			if err != nil {
				return nil, fmt.Errorf("weight for %q must be a number", name)
			}
			out = append(out, expenses.ShareEntry{Traveller: name, Weight: w})
		} else {
			out = append(out, expenses.ShareEntry{Traveller: tok, Weight: 1})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("split must contain at least one traveller")
	}
	return out, nil
}
