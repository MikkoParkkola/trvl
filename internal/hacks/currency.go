package hacks

import "github.com/MikkoParkkola/trvl/internal/destinations"

// convertCurrency is the seam hack detectors use to re-denominate fixed EUR
// classification constants into the caller's requested currency. Defaults to
// the live destinations.ConvertCurrency; overridable in tests so the default
// suite stays offline/deterministic (destinations.ConvertCurrency has no
// offline fallback). Mirrors the existing railGroundSearcher seam pattern.
// ponytail: package-level var; currency-injecting tests set/restore it
// sequentially and must NOT call t.Parallel(), same contract as railGroundSearcher.
var convertCurrency = destinations.ConvertCurrency
