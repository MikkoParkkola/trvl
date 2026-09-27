package scoring_test

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/scoring"
)

func TestFactor_WarsawFilter_BlankEntryDoesNotExcludeEveryone(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ExcludedDestinations = []string{"", "  "}

	in := baseInput()
	in.AirportCode = "KEF"
	in.CityName = "Reykjavik"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorWarsawFilter] != 1.0 {
		t.Fatalf("warsaw_filter = %.2f, a blank exclusion must not match every city", bd[scoring.FactorWarsawFilter])
	}
}

func TestFactor_WarsawFilter_ShortNameDoesNotExcludeADifferentCity(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ExcludedDestinations = []string{"Nice", "Bar"}

	in := baseInput()
	in.CityName = "Venice"
	in.AirportCode = "VCE"
	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorWarsawFilter] != 1.0 {
		t.Fatalf("venice warsaw_filter = %.2f, Nice must not exclude Venice", bd[scoring.FactorWarsawFilter])
	}

	in.CityName = "Barcelona"
	in.AirportCode = "BCN"
	_, bd = scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorWarsawFilter] != 1.0 {
		t.Fatalf("barcelona warsaw_filter = %.2f, Bar must not exclude Barcelona", bd[scoring.FactorWarsawFilter])
	}
}

func TestFactor_BucketListBoost_IcelandAndBalkans(t *testing.T) {
	prefs := defaultPrefs()
	prefs.BucketList = []string{"Iceland", "Balkans"}

	in := baseInput()
	in.CityName = "Reykjavik"
	in.AirportCode = "KEF"
	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorBucketListBoost] < 0.9 {
		t.Errorf("Reykjavik boost = %.2f, want ≥ 0.9 when the bucket list says Iceland", bd[scoring.FactorBucketListBoost])
	}

	in.CityName = "Dubrovnik"
	in.AirportCode = "DBV"
	_, bd = scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorBucketListBoost] < 0.9 {
		t.Errorf("Dubrovnik boost = %.2f, want ≥ 0.9 when the bucket list says Balkans", bd[scoring.FactorBucketListBoost])
	}
}
