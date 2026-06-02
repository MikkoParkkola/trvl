package main

import (
	"fmt"
	"os"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/spf13/cobra"
)

func rateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rate-status",
		Short: "Show rate limit status for all providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			rm := hotels.HotelRateManager

			fmt.Fprintf(os.Stdout, "=== Rate Limit Status ===\n\n")

			for _, provider := range []string{"google", "booking", "trivago"} {
				reqs, recent429s, throttled := rm.Stats(provider)
				backoff := rm.Backoff(provider)
				status := "✅ OK"
				if throttled {
					status = "❌ THROTTLED"
				} else if recent429s > 0 {
					status = "⚠️  WARNING"
				}
				fmt.Fprintf(os.Stdout, "%-10s %s\n", provider+":", status)
				fmt.Fprintf(os.Stdout, "  Requests:    %d\n", reqs)
				fmt.Fprintf(os.Stdout, "  Recent 429s: %d\n", recent429s)
				fmt.Fprintf(os.Stdout, "  Backoff:     %v\n", backoff)
				fmt.Fprintf(os.Stdout, "\n")
			}

			fmt.Fprintf(os.Stdout, "Tips:\n")
			fmt.Fprintf(os.Stdout, "  • Wait 10s between consecutive searches\n")
			fmt.Fprintf(os.Stdout, "  • Use broad date ranges (e.g. search a whole month)\n")
			fmt.Fprintf(os.Stdout, "  • After a 429 error, wait 60s before retrying\n")
			fmt.Fprintf(os.Stdout, "  • Run 'trvl rate-status' to check current status\n")

			return nil
		},
	}
}
