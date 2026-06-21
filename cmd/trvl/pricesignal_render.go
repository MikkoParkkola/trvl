package main

import (
	"fmt"
	"io"

	"github.com/MikkoParkkola/trvl/internal/pricesignal"
)

// printPricePosition renders the MIK-6229 price-position summary line.
//
// Honesty contract (TRVL.PH.3): below the confidence floor it states that there
// is not enough history and asserts NO trend or verdict. It never fabricates a
// buy/wait recommendation from sparse data.
func printPricePosition(w io.Writer, pos *pricesignal.Position) {
	if pos == nil {
		return
	}
	if !pos.Confident {
		fmt.Fprintf(w, "\nPrice position: not enough history yet (%d observation(s)) — current price shown, no trend.\n", pos.Observations)
		return
	}
	fmt.Fprintf(w, "\nPrice position: %s — %s of this route (low %.0f / median %.0f / high %.0f over %d obs, %s vs median).\n",
		verdictText(pos.Verdict),
		bandText(pos.Band),
		pos.Low, pos.Median, pos.High, pos.Observations,
		signedPct(pos.VsMedianPct),
	)
}

func verdictText(v pricesignal.Verdict) string {
	switch v {
	case pricesignal.VerdictBuy:
		return "BUY now"
	case pricesignal.VerdictWait:
		return "WAIT — historically high"
	case pricesignal.VerdictNeutral:
		return "typical price"
	default:
		return "no verdict"
	}
}

func bandText(b pricesignal.Band) string {
	switch b {
	case pricesignal.BandLow:
		return "in the cheap third"
	case pricesignal.BandHigh:
		return "in the expensive third"
	case pricesignal.BandTypical:
		return "in the middle third"
	default:
		return "position unknown"
	}
}

func signedPct(p float64) string {
	if p >= 0 {
		return fmt.Sprintf("+%.0f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}
