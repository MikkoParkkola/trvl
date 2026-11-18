package hacks

// searchKind is the kind of naive search a savings pass runs against, and so
// the fare a hack's saving must be measured on to apply to it.
type searchKind int

const (
	flightSearch searchKind = iota // zero value: flight search
	groundSearch
	// hotelNight marks a saving that is a hotel night not paid, not a lower
	// fare. No search has this kind, so BestSaving never prices such a hack;
	// lists still show it (#819).
	hotelNight
)

// groundDetectors is the roster a ground search's savings pass runs. A
// detector belongs here only if its Savings is the caller's ground fare minus
// the price of a cheaper way to make the same ground trip. None qualifies
// today: the flight detectors measure against their own flight fare,
// night_transport and ferry_cabin save a hotel night, and the ground
// advisories (throwaway_ground, cross_border_rail, regional_pass,
// eurostar_return) report no saving. A ground search therefore attaches no
// saving rather than a fabricated one.
func groundDetectors() []detectFn {
	return nil
}

// rosterFor returns the detectors a search of kind k runs.
func rosterFor(k searchKind) []detectFn {
	if k == groundSearch {
		return groundDetectors()
	}
	return currentDetectorRoster()()
}

// stampBasis gives each hack the roster's kind unless its detector declared
// its own (hotelNight). Unset is the zero value, flightSearch, so a flight
// roster's hacks are unchanged.
func stampBasis(hs []Hack, k searchKind) {
	for i := range hs {
		if hs[i].basis == flightSearch {
			hs[i].basis = k
		}
	}
}
