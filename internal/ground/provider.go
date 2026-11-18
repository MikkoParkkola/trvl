package ground

import (
	"context"
	"fmt"
	"net/http"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// DefaultProvider wraps the package-level SearchByName function, implementing
// models.GroundSearcher. It uses the shared HTTP client and rate limiters.
type DefaultProvider struct{}

// SearchGround delegates to the package-level SearchByName, converting
// models.GroundSearchOptions to the package's SearchOptions.
func (p *DefaultProvider) SearchGround(ctx context.Context, from, to, date string, opts models.GroundSearchOptions) (*models.GroundSearchResult, error) {
	return SearchByName(ctx, from, to, date, SearchOptions{
		Currency:  opts.Currency,
		Providers: opts.Providers,
		MaxPrice:  opts.MaxPrice,
		Type:      opts.Type,
	})
}

// livePrice labels a fare parsed for this departure from the upstream answer:
// live when priced. An unpriced route stays unlabelled and the merge pass
// classifies it unavailable.
func livePrice(price float64) models.PriceProvenance {
	if price > 0 {
		return models.PriceLive
	}
	return ""
}

// httpStatusError types a non-200 provider answer: 429, 403 (a bot wall) and
// 5xx are refusals that should be retried later (ErrRateLimited); any other
// status means the endpoint moved or changed (ErrParseFailed).
func httpStatusError(provider, stage string, code int) error {
	if code == http.StatusTooManyRequests || code == http.StatusForbidden || code >= 500 {
		return fmt.Errorf("%s %s: HTTP %d: %w", provider, stage, code, models.ErrRateLimited)
	}
	return fmt.Errorf("%s %s: HTTP %d: %w", provider, stage, code, models.ErrParseFailed)
}
