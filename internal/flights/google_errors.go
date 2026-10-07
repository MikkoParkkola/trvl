package flights

import (
	"errors"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Google Flights error wording lives here so every path names a refusal the
// same way, and the provider reason reads rate_limited for a quota refusal or
// cooldown rather than other or blocked (MIK-8036).

// googleRequestError describes a Google Flights request that was not answered:
// skipped during a cooldown, or failed in transport.
func googleRequestError(err error) error {
	if errors.Is(err, batchexec.ErrRefused) {
		return fmt.Errorf("google flights cooling down: %w", models.ErrRateLimited)
	}
	return fmt.Errorf("request failed: %w", err)
}

// googleNonFlightError describes an HTTP 200 whose payload is not flights: a
// wrb.fr status row, a rate-limit error object, or a challenge page.
func googleNonFlightError(body []byte) error {
	if code, ok := batchexec.WrbStatus(body); ok && code > 0 {
		return googleWrbError(code)
	}
	if batchexec.QuotaRefusal(200, body) {
		return fmt.Errorf("google flights quota refusal (rate-limit error object): %w", models.ErrRateLimited)
	}
	return fmt.Errorf("google flights returned a non-flight error/challenge payload: %w", models.ErrRateLimited)
}

// googleWrbError describes a Google Flights rejection carried as a wrb.fr
// status. Status 13 is the quota refusal and is named as one, so the provider
// reason reads rate_limited; any other code is a plain rejection.
func googleWrbError(code int) error {
	if code == 13 {
		return fmt.Errorf("google flights quota refusal (code %d): %w", code, models.ErrRateLimited)
	}
	return fmt.Errorf("google flights declined the request (code %d): %w", code, models.ErrRateLimited)
}
