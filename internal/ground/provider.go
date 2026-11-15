package ground

import (
	"context"

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
