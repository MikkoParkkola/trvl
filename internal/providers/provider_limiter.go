// provider_limiter.go provides the concurrency model the aggregator uses to fan
// out across providers safely:
//
//   - DIFFERENT providers run in PARALLEL (bounded by providerConcurrency()).
//   - The SAME provider SERIALIZES its requests, each gated by a per-provider
//     golang.org/x/time/rate limiter.
//
// This mirrors the per-provider rate.Limiter pattern already used by individual
// flight providers (e.g. wizzLimiter in internal/flights/wizzair.go), but lifts
// it into a shared registry so every cookie-gated provider gets the same
// politeness guarantee without each one re-declaring a package-level limiter.
package providers

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Default rate for a freshly-seen provider: one request every 500ms with a
// burst of 1 — the same conservative cadence as wizzLimiter.
const (
	defaultProviderEvery = 500 * time.Millisecond
	defaultProviderBurst = 1
)

// ProviderLimiterRegistry holds a per-provider rate.Limiter keyed by provider
// name. It is safe for concurrent use. Limiters are created lazily on first
// reference using the registry defaults unless an explicit limit was set.
type ProviderLimiterRegistry struct {
	mu           sync.Mutex
	limiters     map[string]*rate.Limiter
	defaultEvery time.Duration
	defaultBurst int
}

// NewProviderLimiterRegistry creates a registry. A non-positive every/burst
// falls back to the package defaults (500ms / burst 1).
func NewProviderLimiterRegistry(every time.Duration, burst int) *ProviderLimiterRegistry {
	if every <= 0 {
		every = defaultProviderEvery
	}
	if burst <= 0 {
		burst = defaultProviderBurst
	}
	return &ProviderLimiterRegistry{
		limiters:     make(map[string]*rate.Limiter),
		defaultEvery: every,
		defaultBurst: burst,
	}
}

// DefaultProviderLimiters is the process-wide registry the aggregator uses by
// default. Providers needing a custom cadence call SetLimit at init time.
var DefaultProviderLimiters = NewProviderLimiterRegistry(defaultProviderEvery, defaultProviderBurst)

// Limiter returns the rate.Limiter for provider, creating it with the registry
// defaults on first use.
func (r *ProviderLimiterRegistry) Limiter(provider string) *rate.Limiter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.limiters[provider]; ok {
		return l
	}
	l := rate.NewLimiter(rate.Every(r.defaultEvery), r.defaultBurst)
	r.limiters[provider] = l
	return l
}

// SetLimit installs (or replaces) the limiter for provider with the given
// cadence. A non-positive every/burst is ignored for that field, falling back
// to the registry default.
func (r *ProviderLimiterRegistry) SetLimit(provider string, every time.Duration, burst int) {
	if every <= 0 {
		every = r.defaultEvery
	}
	if burst <= 0 {
		burst = r.defaultBurst
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limiters[provider] = rate.NewLimiter(rate.Every(every), burst)
}

// Wait blocks until the provider's limiter allows an event or ctx is done.
func (r *ProviderLimiterRegistry) Wait(ctx context.Context, provider string) error {
	return r.Limiter(provider).Wait(ctx)
}

// ProviderTask is a single unit of provider work. Provider is the rate-limiting
// key; tasks sharing a Provider run sequentially (serialized by that provider's
// limiter), tasks with distinct Providers run in parallel.
type ProviderTask struct {
	Provider string
	Fn       func(ctx context.Context) error
}

// RunParallelAcrossProviders runs tasks with this concurrency model:
//
//   - tasks are grouped by Provider;
//   - each group runs in its own goroutine, so distinct providers proceed in
//     parallel (capped at providerConcurrency() concurrent providers);
//   - within a group, tasks run one at a time, each preceded by reg.Wait so the
//     same provider never issues overlapping or too-rapid requests.
//
// It returns a []error aligned to the input task order: result[i] is the error
// (or nil) from tasks[i].Fn. A nil registry uses DefaultProviderLimiters.
func RunParallelAcrossProviders(ctx context.Context, reg *ProviderLimiterRegistry, tasks []ProviderTask) []error {
	if reg == nil {
		reg = DefaultProviderLimiters
	}
	results := make([]error, len(tasks))
	if len(tasks) == 0 {
		return results
	}

	// Group task indices by provider, preserving order within each group.
	groups := make(map[string][]int)
	var order []string
	for i, t := range tasks {
		if _, ok := groups[t.Provider]; !ok {
			order = append(order, t.Provider)
		}
		groups[t.Provider] = append(groups[t.Provider], i)
	}

	// Bound how many providers run at once.
	sem := make(chan struct{}, providerConcurrency())
	var wg sync.WaitGroup

	for _, provider := range order {
		provider := provider
		idxs := groups[provider]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				for _, i := range idxs {
					results[i] = ctx.Err()
				}
				return
			}
			defer func() { <-sem }()

			for _, i := range idxs {
				if err := ctx.Err(); err != nil {
					results[i] = err
					continue
				}
				if err := reg.Wait(ctx, provider); err != nil {
					results[i] = err
					continue
				}
				results[i] = tasks[i].Fn(ctx)
			}
		}()
	}

	wg.Wait()
	return results
}
