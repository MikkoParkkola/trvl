package flights

import (
	"context"
	"errors"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// SearchAFKLM queries the Air France-KLM Offers API for the given route and
// date, returning canonical FlightResults tagged provider "afklm". When a
// ReturnDate is set the provider returns genuine both-bound round-trip tickets
// (FareType=FareRoundTrip, legs Direction-tagged outbound/inbound).
//
// AF-KLM is opt-in: it requires a credential (AFKLM_KEY env, macOS Keychain, or
// a 1Password reference in AFKLM_OP_REF). Unlike the silently-skipped
// composition sources, an explicit
// `--provider afklm` request surfaces a clear, actionable error when no
// credential is configured rather than returning an empty result.
func SearchAFKLM(ctx context.Context, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	// PolicyExternal: the user asked for AF-KLM by name, so consulting the
	// Keychain (and 1Password, when AFKLM_OP_REF names the item) is expected
	// rather than a surprise. Each backend is individually bounded and detached
	// from the terminal; see afklm.Policy.
	p, err := afklm.NewProvider(ctx, afklm.PolicyExternal)
	if errors.Is(err, afklm.ErrNoCredential) {
		return nil, fmt.Errorf("afklm: no API key configured — set %s, add it to the macOS Keychain, or set %s to a 1Password secret reference (e.g. op://Private/AF-KLM/credential)", afklm.EnvKey, afklm.EnvOpRef)
	}
	if err != nil {
		return nil, fmt.Errorf("afklm: %w", err)
	}

	return p.SearchFlights(ctx, origin, destination, date, models.FlightSearchOptions{
		ReturnDate: opts.ReturnDate,
		CabinClass: opts.CabinClass,
		MaxStops:   opts.MaxStops,
		SortBy:     opts.SortBy,
		Airlines:   opts.Airlines,
		Adults:     opts.Adults,
		Currency:   opts.Currency,
	})
}
