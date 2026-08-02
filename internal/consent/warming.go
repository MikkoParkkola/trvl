package consent

import "fmt"

// A WarmingClaim is one statement trvl is entitled to make to a user about what
// it did, or did not do, to warm browser cookies for a provider.
//
// The type exists because the message has shipped two false statements: that a
// browser had opened when trvl had only asked for one, and that future searches
// would reuse cookies that may not exist (#528). Both were introduced by editing
// a format string, which is a place where nothing asks what establishes the new
// sentence. Here a sentence cannot reach a user without a registry entry, and a
// registry entry sits directly beneath the condition that justifies it.
type WarmingClaim string

const (
	// ClaimLaunchAsked is justified when the launcher returned without error
	// inside the window it is watched for.
	//
	// That establishes exactly one thing: the request was accepted without
	// failing immediately. It does NOT establish that a window appeared -- the
	// watch window is brief, and a launcher that dies after it looks identical
	// to one that succeeded -- so the sentence says "asked", names the URL, and
	// hands the user the fallback rather than promising an outcome.
	ClaimLaunchAsked WarmingClaim = "launch_asked"

	// ClaimLaunchFailed is justified when the launcher returned an error inside
	// that same window. It is the one case where something is definitely known,
	// which is why the error is shown rather than swallowed.
	ClaimLaunchFailed WarmingClaim = "launch_failed"

	// ClaimDeclined is justified when CookiesDeclined() is true at the moment
	// the warming decision is taken. Silence is not an option here: the provider
	// is enabled and no browser appears, so without this the user is left
	// guessing whether setup half-failed. It names the variable so the answer is
	// actionable by whoever set it.
	ClaimDeclined WarmingClaim = "declined"
)

// WarmingFacts is everything the renderer is allowed to know. A claim that
// cannot be derived from these fields is a claim trvl has not checked.
type WarmingFacts struct {
	ProviderName string
	PreflightURL string
	// LaunchErr is the launcher's return inside its watch window. Nil means it
	// did not fail there; it does not mean a browser opened.
	LaunchErr error
	// Declined mirrors CookiesDeclined() at the decision point, passed in rather
	// than read here so the caller's branch and this message cannot disagree.
	Declined bool
}

// warmingClaimText renders one claim. Every string a user can see lives in this
// map and nowhere else, which is what makes "no free-form sentence at the call
// site" a property of the type rather than a review convention.
var warmingClaimText = map[WarmingClaim]func(WarmingFacts) string{
	ClaimLaunchAsked: func(f WarmingFacts) string {
		return fmt.Sprintf("\n\nAsked your browser to open %s so cookies for %s can be reused. If no window appeared, open that URL yourself.",
			f.PreflightURL, f.ProviderName)
	},
	ClaimLaunchFailed: func(f WarmingFacts) string {
		return fmt.Sprintf("\n\nCould not start a browser for %s (%v). Open %s yourself so those cookies can be reused.",
			f.ProviderName, f.LaunchErr, f.PreflightURL)
	},
	ClaimDeclined: func(WarmingFacts) string {
		return "\n\nDid not open a browser to warm cookies: " + CookiesEnv +
			" declines access to your browser. The provider is enabled and will start without warm cookies."
	},
}

// WarmingClaims decides which claims the facts justify. It returns identifiers
// and no text, so a test can assert over the decision without asserting over
// prose, and a new sentence cannot be smuggled in through this function.
func WarmingClaims(f WarmingFacts) []WarmingClaim {
	switch {
	case f.Declined:
		return []WarmingClaim{ClaimDeclined}
	case f.LaunchErr != nil:
		return []WarmingClaim{ClaimLaunchFailed}
	default:
		return []WarmingClaim{ClaimLaunchAsked}
	}
}

// WarmingNote renders the claims WarmingClaims selected.
//
// A claim with no registry entry renders as nothing rather than as text. That is
// the fail-safe direction: an unjustified claim reaching a user is the defect,
// and a missing sentence is not.
func WarmingNote(f WarmingFacts) string {
	note := ""
	for _, c := range WarmingClaims(f) {
		render, ok := warmingClaimText[c]
		if !ok {
			continue
		}
		note += render(f)
	}
	return note
}
