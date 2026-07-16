// Package points provides valuations for frequent flyer and hotel loyalty
// programs, enabling points-vs-cash calculations.
//
// Valuations are sourced from publicly available data (The Points Guy, NerdWallet,
// Doctor of Credit). All values are cents-per-point (cpp) estimates.
package points

// Program represents a loyalty program with its slug and display name.
type Program struct {
	// Slug is the lowercase hyphenated identifier used on the CLI (e.g. "finnair-plus").
	Slug string
	// Name is the human-readable display name.
	Name string
	// FloorCPP is the conservative (redemption floor) value in cents per point.
	FloorCPP float64
	// CeilingCPP is the aspirational (sweet-spot redemption) value in cents per point.
	CeilingCPP float64
	// Category distinguishes airline, hotel, and transferable programs.
	Category string
}

// Programs is the canonical list of supported loyalty programs.
// Values are in cents per point (cpp).
var Programs = []Program{
	// --- Airline programs ---
	{
		Slug: "finnair-plus", Name: "Finnair Plus",
		FloorCPP: 1.0, CeilingCPP: 2.2, Category: "airline",
	},
	// SK (SAS) joined SkyTeam in 2024; SAS SkyTeam segments earn into Flying Blue
	// (see the "flying-blue" entry below). The retired "SAS EuroBonus" catalog
	// row was removed — SAS is no longer modelled as a Star Alliance/EuroBonus
	// program (issue #466).
	{
		Slug: "british-airways-avios", Name: "British Airways Avios",
		FloorCPP: 1.2, CeilingCPP: 2.0, Category: "airline",
	},
	{
		Slug: "united-mileageplus", Name: "United MileagePlus",
		FloorCPP: 1.0, CeilingCPP: 2.1, Category: "airline",
	},
	{
		Slug: "delta-skymiles", Name: "Delta SkyMiles",
		FloorCPP: 1.0, CeilingCPP: 1.8, Category: "airline",
	},
	{
		Slug: "american-aadvantage", Name: "American AAdvantage",
		FloorCPP: 1.1, CeilingCPP: 2.0, Category: "airline",
	},
	{
		Slug: "flying-blue", Name: "Air France/KLM Flying Blue",
		FloorCPP: 1.2, CeilingCPP: 2.7, Category: "airline",
	},
	{
		Slug: "lufthansa-miles-more", Name: "Lufthansa Miles & More",
		FloorCPP: 1.0, CeilingCPP: 2.5, Category: "airline",
	},
	{
		Slug: "emirates-skywards", Name: "Emirates Skywards",
		FloorCPP: 1.0, CeilingCPP: 2.3, Category: "airline",
	},
	{
		Slug: "qatar-privilege-club", Name: "Qatar Privilege Club",
		FloorCPP: 1.0, CeilingCPP: 2.5, Category: "airline",
	},
	{
		Slug: "singapore-krisflyer", Name: "Singapore KrisFlyer",
		FloorCPP: 1.1, CeilingCPP: 2.2, Category: "airline",
	},
	{
		Slug: "cathay-asia-miles", Name: "Cathay Asia Miles",
		FloorCPP: 1.0, CeilingCPP: 2.5, Category: "airline",
	},
	{
		Slug: "ana-mileage-club", Name: "ANA Mileage Club",
		FloorCPP: 1.3, CeilingCPP: 3.0, Category: "airline",
	},
	{
		Slug: "jal-mileage-bank", Name: "JAL Mileage Bank",
		FloorCPP: 1.3, CeilingCPP: 2.8, Category: "airline",
	},
	{
		Slug: "alaska-mileage-plan", Name: "Alaska Mileage Plan",
		FloorCPP: 1.3, CeilingCPP: 2.8, Category: "airline",
	},
	{
		Slug: "southwest-rapid-rewards", Name: "Southwest Rapid Rewards",
		FloorCPP: 1.3, CeilingCPP: 1.7, Category: "airline",
	},

	// --- Hotel programs ---
	{
		Slug: "marriott-bonvoy", Name: "Marriott Bonvoy",
		FloorCPP: 0.5, CeilingCPP: 1.0, Category: "hotel",
	},
	{
		Slug: "hilton-honors", Name: "Hilton Honors",
		FloorCPP: 0.4, CeilingCPP: 0.8, Category: "hotel",
	},
	{
		Slug: "ihg-rewards", Name: "IHG Rewards",
		FloorCPP: 0.4, CeilingCPP: 0.8, Category: "hotel",
	},
	{
		Slug: "world-of-hyatt", Name: "Hyatt World of Hyatt",
		FloorCPP: 1.5, CeilingCPP: 2.5, Category: "hotel",
	},
	{
		Slug: "wyndham-rewards", Name: "Wyndham Rewards",
		FloorCPP: 0.8, CeilingCPP: 1.3, Category: "hotel",
	},

	// --- Transferable currencies ---
	{
		Slug: "amex-mr", Name: "Amex Membership Rewards",
		FloorCPP: 1.0, CeilingCPP: 2.0, Category: "transferable",
	},
	{
		Slug: "chase-ur", Name: "Chase Ultimate Rewards",
		FloorCPP: 1.0, CeilingCPP: 2.0, Category: "transferable",
	},
	{
		Slug: "citi-thankyou", Name: "Citi ThankYou Points",
		FloorCPP: 1.0, CeilingCPP: 1.7, Category: "transferable",
	},
	{
		Slug: "capital-one-miles", Name: "Capital One Miles",
		FloorCPP: 1.0, CeilingCPP: 1.85, Category: "transferable",
	},
}

// programsBySlug is a fast lookup map built at init time.
var programsBySlug map[string]*Program

func init() {
	programsBySlug = make(map[string]*Program, len(Programs))
	for i := range Programs {
		programsBySlug[Programs[i].Slug] = &Programs[i]
	}
}

// LookupProgram returns the program for the given slug, or nil if not found.
func LookupProgram(slug string) *Program {
	return programsBySlug[slug]
}

// TransferPartner describes a points-transfer relationship between two programs.
type TransferPartner struct {
	From  string  // source program slug
	To    string  // destination program slug
	Ratio float64 // transfer ratio: 1 From point becomes Ratio To points
}

// TransferPartners lists common transfer partnerships.
// Ratios are 1:1 unless otherwise noted.
var TransferPartners = []TransferPartner{
	// Amex MR transfers
	{From: "amex-mr", To: "flying-blue", Ratio: 1.0},
	{From: "amex-mr", To: "british-airways-avios", Ratio: 1.0},
	{From: "amex-mr", To: "singapore-krisflyer", Ratio: 1.0},
	{From: "amex-mr", To: "ana-mileage-club", Ratio: 1.0},
	{From: "amex-mr", To: "emirates-skywards", Ratio: 1.0},
	{From: "amex-mr", To: "finnair-plus", Ratio: 1.0},
	{From: "amex-mr", To: "cathay-asia-miles", Ratio: 1.0},
	{From: "amex-mr", To: "marriott-bonvoy", Ratio: 1.0},
	// Chase UR transfers
	{From: "chase-ur", To: "united-mileageplus", Ratio: 1.0},
	{From: "chase-ur", To: "british-airways-avios", Ratio: 1.0},
	{From: "chase-ur", To: "flying-blue", Ratio: 1.0},
	{From: "chase-ur", To: "singapore-krisflyer", Ratio: 1.0},
	{From: "chase-ur", To: "world-of-hyatt", Ratio: 1.0},
	{From: "chase-ur", To: "ihg-rewards", Ratio: 1.0},
	{From: "chase-ur", To: "marriott-bonvoy", Ratio: 1.0},
	// Citi ThankYou transfers
	{From: "citi-thankyou", To: "flying-blue", Ratio: 1.0},
	{From: "citi-thankyou", To: "singapore-krisflyer", Ratio: 1.0},
	{From: "citi-thankyou", To: "british-airways-avios", Ratio: 1.0},
	{From: "citi-thankyou", To: "ana-mileage-club", Ratio: 1.0},
	{From: "citi-thankyou", To: "cathay-asia-miles", Ratio: 1.0},
	{From: "citi-thankyou", To: "wyndham-rewards", Ratio: 1.0},
	// Capital One transfers
	{From: "capital-one-miles", To: "flying-blue", Ratio: 1.0},
	{From: "capital-one-miles", To: "british-airways-avios", Ratio: 1.0},
	{From: "capital-one-miles", To: "finnair-plus", Ratio: 1.0},
	{From: "capital-one-miles", To: "singapore-krisflyer", Ratio: 1.0},
	{From: "capital-one-miles", To: "emirates-skywards", Ratio: 1.0},
}
