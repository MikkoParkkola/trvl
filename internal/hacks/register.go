package hacks

import (
	"context"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// init wires the travel-hacks savings engine into flight and ground search so
// savings auto-compose into every naive search (innovation #1). The dependency
// is registered here — in the hacks package, which already imports flights and
// ground — to invert what would otherwise be an import cycle: flight/ground
// search must not import hacks (the detectors import them).
func init() {
	flights.RegisterHackComposer(func(ctx context.Context, req flights.HackComposeRequest) *models.HackSaving {
		return BestSaving(ctx, DetectorInput{
			Origin:      req.Origin,
			Destination: req.Destination,
			Date:        req.Date,
			ReturnDate:  req.ReturnDate,
			Currency:    req.Currency,
			CarryOnOnly: req.CarryOnOnly,
			NaivePrice:  req.NaivePrice,
			Passengers:  req.Passengers,
		}, nil)
	})

	ground.RegisterHackComposer(groundHackComposer(nil))
}

// groundHackComposer returns the savings composer ground search calls after a
// naive search. detect is BestSaving's detector sweep; nil runs every
// detector, and tests pass a capturing one.
func groundHackComposer(detect DetectFunc) func(context.Context, ground.HackComposeRequest) *models.HackSaving {
	return func(ctx context.Context, req ground.HackComposeRequest) *models.HackSaving {
		return BestSaving(ctx, detectorInputFromGround(req), detect)
	}
}

// detectorInputFromGround maps a ground compose request onto the detector
// input, carrying the caller's ground search scope.
func detectorInputFromGround(req ground.HackComposeRequest) DetectorInput {
	return DetectorInput{
		Origin:                 req.Origin,
		Destination:            req.Destination,
		Date:                   req.Date,
		Currency:               req.Currency,
		NaivePrice:             req.NaivePrice,
		GroundProviders:        req.Providers,
		GroundExcludeProviders: req.ExcludeProviders,
		NoCache:                req.NoCache,
	}
}
