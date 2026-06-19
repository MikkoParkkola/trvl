package hacks

import "context"

// DetectCurrencyArbitrage is the exported entry point for the currency
// arbitrage detector. It delegates to the unexported detectCurrencyArbitrage
// implementation so external callers (e.g. internal/arbreport) can reuse the
// detector in isolation without running the full DetectAll fan-out. The
// detector logic itself lives in currency_arbitrage.go and is not modified
// here — this is a thin re-export only.
func DetectCurrencyArbitrage(ctx context.Context, in DetectorInput) []Hack {
	return detectCurrencyArbitrage(ctx, in)
}
