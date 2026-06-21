package main

import (
	"fmt"
	"io"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
)

// printSavings renders call-free (MIK-6234 Tier 0) counterfactual savings. The
// heading makes the zero-cost guarantee explicit so the output never implies a
// fresh fan-out happened.
func printSavings(w io.Writer, savings []counterfactual.Saving) {
	if len(savings) == 0 {
		return
	}
	fmt.Fprintln(w, "\nSavings you could capture (no extra searches):")
	for _, s := range savings {
		fmt.Fprintf(w, "  • %s\n", s.Description)
	}
}
