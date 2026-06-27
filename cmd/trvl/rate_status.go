package main

import (
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/spf13/cobra"
)

func rateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rate-status",
		Short: "Show rate limit status for all providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			rm := hotels.HotelRateManager
			write := func(format string, args ...any) error {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), format, args...)
				return err
			}

			if err := write("=== Rate Limit Status ===\n\n"); err != nil {
				return err
			}

			for _, provider := range []string{"google", "booking", "trivago"} {
				reqs, recent429s, throttled := rm.Stats(provider)
				backoff := rm.Backoff(provider)
				status := "✅ OK"
				if throttled {
					status = "❌ THROTTLED"
				} else if recent429s > 0 {
					status = "⚠️  WARNING"
				}
				if err := write("%-10s %s\n", provider+":", status); err != nil {
					return err
				}
				if err := write("  Requests:    %d\n", reqs); err != nil {
					return err
				}
				if err := write("  Recent 429s: %d\n", recent429s); err != nil {
					return err
				}
				if err := write("  Backoff:     %v\n", backoff); err != nil {
					return err
				}
				if err := write("\n"); err != nil {
					return err
				}
			}

			if err := write("Tips:\n"); err != nil {
				return err
			}
			if err := write("  • Wait 10s between consecutive searches\n"); err != nil {
				return err
			}
			if err := write("  • Use broad date ranges (e.g. search a whole month)\n"); err != nil {
				return err
			}
			if err := write("  • After a 429 error, wait 60s before retrying\n"); err != nil {
				return err
			}
			if err := write("  • Run 'trvl rate-status' to check current status\n"); err != nil {
				return err
			}

			return nil
		},
	}
}
