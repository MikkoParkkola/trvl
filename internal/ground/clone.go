package ground

import (
	"slices"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// cloneGroundSearchResult returns a caller-private deep copy of a shared
// result: every slice and pointer it reaches is copied, so a caller that edits
// its copy cannot change what other callers, the savings-pass memo or the
// cache hold.
//
// singleflight.Do shares the winner's *GroundSearchResult pointer across
// concurrent callers, and MCP handlers post-filter Routes and rewrite Count.
// TestCloneGroundSearchResult_SharesNoReferences fills every reference field
// by reflection, so a field added to the models later fails it until it is
// copied here.
func cloneGroundSearchResult(shared *models.GroundSearchResult) *models.GroundSearchResult {
	if shared == nil {
		return nil
	}
	cp := *shared
	if shared.Routes != nil {
		cp.Routes = make([]models.GroundRoute, len(shared.Routes))
		for i, route := range shared.Routes {
			route.Amenities = slices.Clone(route.Amenities)
			if route.Legs != nil {
				legs := make([]models.GroundLeg, len(route.Legs))
				for j, leg := range route.Legs {
					leg.Amenities = slices.Clone(leg.Amenities)
					legs[j] = leg
				}
				route.Legs = legs
			}
			route.SeatsLeft = clonePtr(route.SeatsLeft)
			route.Sources = slices.Clone(route.Sources)
			route.Confidence = cloneConfidence(route.Confidence)
			route.SeaState = clonePtr(route.SeaState)
			cp.Routes[i] = route
		}
	}
	cp.ProviderStatuses = slices.Clone(shared.ProviderStatuses)
	cp.Completeness.Missing = slices.Clone(shared.Completeness.Missing)
	cp.HackSaving = cloneHackSaving(shared.HackSaving)
	cp.Destination = cloneDestination(shared.Destination)
	return &cp
}

// clonePtr returns a pointer to a copy of *p; nil stays nil. For pointees
// with no slices or pointers of their own.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneConfidence(c *models.Confidence) *models.Confidence {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Signals = slices.Clone(c.Signals)
	return &cp
}

func cloneHackSaving(s *models.HackSaving) *models.HackSaving {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Risks = slices.Clone(s.Risks)
	cp.Steps = slices.Clone(s.Steps)
	cp.Citations = slices.Clone(s.Citations)
	if s.Candidates != nil {
		cp.Candidates = make([]models.FlightResult, len(s.Candidates))
		for i, f := range s.Candidates {
			f.Warnings = slices.Clone(f.Warnings)
			f.Legs = slices.Clone(f.Legs)
			f.Sources = slices.Clone(f.Sources)
			f.SegmentSources = slices.Clone(f.SegmentSources)
			f.CarryOnIncluded = clonePtr(f.CarryOnIncluded)
			f.CheckedBagsIncluded = clonePtr(f.CheckedBagsIncluded)
			f.Confidence = cloneConfidence(f.Confidence)
			cp.Candidates[i] = f
		}
	}
	return &cp
}

func cloneDestination(d *models.DestinationInfo) *models.DestinationInfo {
	if d == nil {
		return nil
	}
	cp := *d
	cp.Country.Languages = slices.Clone(d.Country.Languages)
	cp.Country.Currencies = slices.Clone(d.Country.Currencies)
	cp.Weather.Forecast = slices.Clone(d.Weather.Forecast)
	cp.Holidays = slices.Clone(d.Holidays)
	return &cp
}
