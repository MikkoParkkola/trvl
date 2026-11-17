package hacks

// searchKind is the kind of naive search a savings pass runs against.
type searchKind int

const (
	flightSearch searchKind = iota // zero value: flight search
	groundSearch
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
