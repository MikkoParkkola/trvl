package ground

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/cookies"
)

// Both rail providers open the user's real, visible browser when a challenge
// needs a human. That seam must be the consent-gated one, not a raw launcher.
//
// This test pins the WIRING: it calls the package var the provider code calls,
// with the decline set, and requires a refusal. If someone rebinds either var
// to an ungated opener, this fails. That rebinding is precisely the defect an
// independent review caught before 1.21.0 shipped.
func TestRailBrowserOpenersRefuseWhenCookiesDeclined(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "1")

	for _, tc := range []struct {
		provider string
		open     func(string) error
		url      string
	}{
		{"trainline", trainlineOpenBrowser, trainlineHomeURL},
		{"sncf", sncfOpenBrowser, sncfHomeURL},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			err := tc.open(tc.url)
			if !errors.Is(err, cookies.ErrBrowserAuthDeclined) {
				t.Fatalf("%s: opening the user's browser after a cookie decline must be refused; got %v", tc.provider, err)
			}
		})
	}
}
