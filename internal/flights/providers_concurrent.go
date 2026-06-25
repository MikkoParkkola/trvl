package flights

import (
	"context"
	"log/slog"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// This file decomposes each secondary flight provider (everything except Google,
// which determines the session currency and therefore runs first) into a
// self-contained function returning a providerOutcome. Extracting them lets the
// search fan them out concurrently (see runProviderTasks) while preserving the
// exact eligibility gates, status messages, and logging the sequential version
// used. Each function is pure with respect to caller state — it returns its
// outcome rather than appending to a shared slice — which is what makes the
// concurrent execution race-free.

func runKiwiProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !kiwiSearchEligible(client, opts) {
		return providerOutcome{status: models.ProviderStatus{
			ID:      "kiwi",
			Name:    "Kiwi",
			Status:  "skipped",
			Error:   "options not supported by Kiwi (e.g. round-trip, alliance/airline filters, baggage requirements)",
			FixHint: "drop unsupported options or call Kiwi directly via provider=kiwi (when supported)",
		}}
	}
	flights, err := SearchKiwiFlights(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("kiwi flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "kiwi",
			Name:   "Kiwi",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "kiwi",
		Name:    "Kiwi",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runSkiplaggedProvider(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) providerOutcome {
	// The round-trip composer suppresses the one-way Skiplagged leg searches
	// because it queries Skiplagged once as a native round-trip instead (see
	// disableSkiplaggedOneWay). Report a transparent skip rather than a silent
	// no-op so the provider-status list explains the absence.
	if skiplaggedOneWayDisabled(ctx) {
		return providerOutcome{status: models.ProviderStatus{
			ID:      "skiplagged",
			Name:    "Skiplagged",
			Status:  "skipped",
			Error:   "one-way Skiplagged skipped; queried once as a native round-trip instead",
			FixHint: "search one-way for the per-direction Skiplagged list",
		}}
	}
	if !skiplaggedSearchEligible(client, opts) {
		return providerOutcome{status: models.ProviderStatus{
			ID:      "skiplagged",
			Name:    "Skiplagged",
			Status:  "skipped",
			Error:   "options not supported by Skiplagged (alliance/airline filters or baggage requirements set)",
			FixHint: "drop unsupported options or call Skiplagged directly via provider=skiplagged",
		}}
	}
	result, err := SearchSkiplagged(ctx, origin, destination, date, opts)
	if err != nil {
		slog.Warn("skiplagged flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "skiplagged",
			Name:   "Skiplagged",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	var flights []models.FlightResult
	if result != nil {
		flights = result.Flights
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "skiplagged",
		Name:    "Skiplagged",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runRyanairProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !ryanairSearchEligible(client, opts) {
		return providerOutcome{status: models.ProviderStatus{
			ID:      "ryanair",
			Name:    "Ryanair",
			Status:  "skipped",
			Error:   "options not supported by Ryanair direct (round-trip, non-economy cabin, alliance filter, or a non-FR airline filter)",
			FixHint: "search one-way economy, or drop the alliance/airline filter",
		}}
	}
	flights, err := SearchRyanair(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("ryanair flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "ryanair",
			Name:   "Ryanair",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "ryanair",
		Name:    "Ryanair",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runWizzairProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !wizzairSearchEligible(client, opts) {
		return providerOutcome{status: models.ProviderStatus{
			ID:      "wizzair",
			Name:    "Wizz Air",
			Status:  "skipped",
			Error:   "options not supported by Wizz Air direct (round-trip / non-economy cabin / alliance filter / non-W6 airline filter)",
			FixHint: "search one-way economy; drop the alliance/airline filter",
		}}
	}
	flights, err := SearchWizzair(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("wizzair flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		// wizzairFailureStatus renders a typed, actionable status; a 404
		// version-rotation gets a WIZZ_VERSION_ROTATED fix hint.
		return providerOutcome{err: err, status: wizzairFailureStatus(err)}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "wizzair",
		Name:    "Wizz Air",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runTransaviaProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !transaviaSearchEligible(client, opts) {
		fixHint := "search one-way; drop the alliance/airline filter"
		reason := "options not supported by Transavia direct (round-trip / alliance filter / non-HV/TO airline filter)"
		if !transaviaConfigured() {
			reason = "Transavia is opt-in and requires a free developer API key"
			fixHint = "set TRANSAVIA_API_KEY (free key from developer.transavia.com)"
		}
		return providerOutcome{status: models.ProviderStatus{
			ID:      "transavia",
			Name:    "Transavia",
			Status:  "skipped",
			Error:   reason,
			FixHint: fixHint,
		}}
	}
	flights, err := SearchTransavia(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("transavia flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "transavia",
			Name:   "Transavia",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "transavia",
		Name:    "Transavia",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runEasyjetProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !easyjetSearchEligible(client, opts) {
		fixHint := "search one-way economy; drop the alliance/airline filter"
		reason := "options not supported by easyJet direct (round-trip / non-economy cabin / alliance filter / non-U2 airline filter)"
		fixHintCode := ""
		if !easyjetConfigured() {
			reason = "easyJet's public availability API is Akamai bot-defended (HTTP 403); it is opt-in and requires a reachable endpoint"
			fixHint = "set EASYJET_API_BASE to an authorised partner endpoint or a self-hosted proxy that returns the JSON availability API"
			fixHintCode = "AKAMAI_BLOCK"
		}
		return providerOutcome{status: models.ProviderStatus{
			ID:          "easyjet",
			Name:        "easyJet",
			Status:      "skipped",
			Error:       reason,
			FixHint:     fixHint,
			FixHintCode: fixHintCode,
		}}
	}
	flights, err := SearchEasyjet(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("easyjet flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "easyjet",
			Name:   "easyJet",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "easyjet",
		Name:    "easyJet",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runVuelingProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !vuelingSearchEligible(client, opts) {
		fixHint := "search one-way economy; drop the alliance/airline filter"
		reason := "options not supported by Vueling direct (round-trip / non-economy cabin / alliance filter / non-VY airline filter)"
		fixHintCode := ""
		if !vuelingConfigured() {
			reason = "Vueling's public booking/availability engine is Akamai Bot Manager-defended; it is opt-in and requires a reachable endpoint"
			fixHint = "set VUELING_API_BASE to an authorised partner endpoint or a self-hosted proxy that returns the JSON availability API"
			fixHintCode = "AKAMAI_BLOCK"
		}
		return providerOutcome{status: models.ProviderStatus{
			ID:          "vueling",
			Name:        "Vueling",
			Status:      "skipped",
			Error:       reason,
			FixHint:     fixHint,
			FixHintCode: fixHintCode,
		}}
	}
	flights, err := SearchVueling(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("vueling flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "vueling",
			Name:   "Vueling",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "vueling",
		Name:    "Vueling",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}

func runNorwegianProvider(ctx context.Context, client *batchexec.Client, origin, destination, date, currency string, opts SearchOptions) providerOutcome {
	if !norwegianSearchEligible(client, opts) {
		fixHint := "search one-way economy; drop the alliance/airline filter"
		reason := "options not supported by Norwegian direct (round-trip / non-economy cabin / alliance filter / non-DY airline filter)"
		fixHintCode := ""
		if !norwegianConfigured() {
			reason = "Norwegian's public site/booking funnel is Cloudflare Bot Management-defended (HTTP 403 cf-mitigated:challenge); it is opt-in and requires a reachable endpoint"
			fixHint = "set NORWEGIAN_API_BASE to an authorised partner endpoint or a self-hosted proxy that returns the JSON availability API"
			fixHintCode = "CLOUDFLARE_BLOCK"
		}
		return providerOutcome{status: models.ProviderStatus{
			ID:          "norwegian",
			Name:        "Norwegian",
			Status:      "skipped",
			Error:       reason,
			FixHint:     fixHint,
			FixHintCode: fixHintCode,
		}}
	}
	flights, err := SearchNorwegian(ctx, origin, destination, date, currency, opts)
	if err != nil {
		slog.Warn("norwegian flight search failed", "origin", origin, "destination", destination, "date", date, "error", err)
		return providerOutcome{err: err, status: models.ProviderStatus{
			ID:     "norwegian",
			Name:   "Norwegian",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	return providerOutcome{flights: flights, succeeded: true, status: models.ProviderStatus{
		ID:      "norwegian",
		Name:    "Norwegian",
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}}
}
