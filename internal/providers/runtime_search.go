package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preflightttl"
)

// SearchHotels queries all hotel-category providers and returns combined results
// along with per-provider status entries so the caller can surface failures to
// the LLM for autonomous diagnosis. The optional filters parameter passes
// search filters (price, property type, stars, etc.) through to provider URL
// templates. A nil filters value is safe and means no filter vars are set.
func (rt *Runtime) SearchHotels(ctx context.Context, location string, lat, lon float64, checkin, checkout, currency string, guests int, filters *HotelFilterParams) ([]models.HotelResult, []models.ProviderStatus, error) {
	providers := rt.registry.ListByCategory("hotels")
	if len(providers) == 0 {
		return nil, nil, nil
	}

	type result struct {
		hotels    []models.HotelResult
		err       error
		id        string
		name      string
		latencyMs int64
	}

	// Filter out circuit-broken providers up-front so the worker pool
	// only enqueues work that will actually run. Skipped providers do
	// not appear in statuses (existing behavior preserved).
	//
	// Cooldown semantics: once a provider has crossed the failure
	// threshold, it stays tripped only while it is *still inside the
	// cooldown window since the last failure*. After cooldown elapses
	// the provider is allowed through as a half-open probe — a
	// successful response triggers MarkSuccess (resets ErrorCount), a
	// failed response refreshes LastErrorAt and re-arms cooldown.
	//
	// Pre-fix bug: the check used `now - LastSuccess > cooldown` so
	// providers whose last success was simply long ago stayed
	// permanently tripped (Booking/Airbnb/Hostelworld locked out for
	// 12+ days even though the upstream had recovered).
	live := make([]*ProviderConfig, 0, len(providers))
	recovering := make(map[string]int) // provider ID → prior error count for recovery slog
	// trippedStatuses surfaces circuit-broken providers to the caller so
	// the agent knows WHICH providers were skipped and WHEN they will
	// retry. Pre-fix the runtime silently dropped them, leaving callers
	// unable to explain a small result set.
	var trippedStatuses []models.ProviderStatus
	now := time.Now()
	for _, cfg := range providers {
		// Snapshot the circuit-breaker fields under the registry lock. These
		// fields (ErrorCount/LastError/LastErrorAt/LastSuccess) are mutated by
		// MarkSuccess/MarkError on the shared *ProviderConfig while a
		// concurrent search — reached via singleflight — iterates the same
		// pointers here. Reading cfg.* directly is a data race; the snapshot
		// is a consistent, lock-protected copy used for the trip decision.
		bs, _ := rt.registry.BreakerSnapshot(cfg.ID)
		if bs.ErrorCount >= circuitBreakerThreshold {
			// Determine when the cooldown window started. Prefer the
			// explicit failure timestamp; fall back to LastSuccess when
			// LastErrorAt is missing on legacy configs from before the
			// field existed.
			tripAt := bs.LastErrorAt
			if tripAt.IsZero() {
				tripAt = bs.LastSuccess
			}
			// When neither timestamp is available, we have no way to
			// know when the trip happened. Treat the provider as
			// freshly-tripped and skip — better to wait one full
			// cooldown than to flood a permanently-bad upstream with
			// probes. This also preserves the "never-successful
			// provider stays skipped" contract from earlier behaviour.
			if tripAt.IsZero() {
				slog.Warn("circuit breaker: provider tripped",
					"provider", cfg.ID,
					"failure_count", bs.ErrorCount,
					"last_error_at", "never",
					"reason", "no_timestamp_freshly_tripped")
				trippedStatuses = append(trippedStatuses, models.ProviderStatus{
					ID:      cfg.ID,
					Name:    cfg.Name,
					Status:  models.StatusCircuitBroken,
					Error:   fmt.Sprintf("circuit breaker tripped after %d consecutive failures (never succeeded; awaiting cooldown)", bs.ErrorCount),
					FixHint: "fix the upstream credential / cookie / endpoint, then run `trvl provider reset <id>` to clear the breaker",
				})
				LogHealth(HealthEntry{
					Provider:   cfg.ID,
					Operation:  "search",
					Status:     "circuit_broken",
					Error:      fmt.Sprintf("circuit breaker tripped after %d consecutive failures (never succeeded; awaiting cooldown)", bs.ErrorCount),
					ErrorClass: "CIRCUIT_BROKEN",
					HintCode:   "CIRCUIT_BROKEN",
				})
				continue
			}
			if now.Sub(tripAt) < circuitBreakerCooldown {
				recoveryAt := tripAt.Add(circuitBreakerCooldown)
				args := []any{
					"provider", cfg.ID,
					"failure_count", bs.ErrorCount,
					"last_error_at", tripAt.Format(time.RFC3339),
					"recovery_at", recoveryAt.Format(time.RFC3339),
				}
				slog.Warn("circuit breaker: provider tripped", args...)
				trippedStatuses = append(trippedStatuses, models.ProviderStatus{
					ID:     cfg.ID,
					Name:   cfg.Name,
					Status: models.StatusCircuitBroken,
					Error: fmt.Sprintf("circuit breaker tripped after %d consecutive failures; last error: %s; recovery probe at %s",
						bs.ErrorCount,
						bs.LastError,
						recoveryAt.Format(time.RFC3339)),
					FixHint: "wait for cooldown to elapse, or run `trvl provider reset <id>` to retry immediately",
				})
				LogHealth(HealthEntry{
					Provider:   cfg.ID,
					Operation:  "search",
					Status:     "circuit_broken",
					Error:      fmt.Sprintf("circuit breaker tripped after %d consecutive failures; last error: %s; recovery probe at %s", bs.ErrorCount, bs.LastError, recoveryAt.Format(time.RFC3339)),
					ErrorClass: "CIRCUIT_BROKEN",
					HintCode:   "CIRCUIT_BROKEN",
				})
				continue
			}
			// Cooldown elapsed — let the provider through as a half-open
			// probe. We log it explicitly so operators can see the
			// retry attempt in the journal.
			slog.Info("circuit breaker: half-open probe",
				"provider", cfg.ID,
				"failure_count", bs.ErrorCount,
				"cooldown_elapsed", true)
		}
		if bs.ErrorCount > 0 {
			recovering[cfg.ID] = bs.ErrorCount
		}
		live = append(live, cfg)
	}

	results := make(chan result, len(live))

	// MIK-3072: worker-pool dispatch. Bounds peak goroutines to
	// min(providerConcurrency, len(live)) instead of fanning out one
	// goroutine per provider. Workers consume from `work` until ctx
	// cancellation or channel close. The inflight gauge tracks how many
	// workers are currently inside searchProvider (excludes blocked-on-
	// channel-recv idle time).
	workers := providerConcurrency()
	if workers > len(live) {
		workers = len(live)
	}
	work := make(chan *ProviderConfig)
	var wg sync.WaitGroup

	// Dispatcher: feeds work; respects ctx cancellation.
	go func() {
		defer close(work)
		for _, cfg := range live {
			select {
			case work <- cfg:
			case <-ctx.Done():
				return
			}
		}
	}()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cfg := range work {
				cur := rt.inflight.Add(1)
				slog.Debug("provider_concurrent_inflight",
					"count", cur,
					"cap", workers,
					"provider", cfg.ID)
				// Per-provider timeout: prevent any single provider from holding
				// up the entire search. Covers the full preflight → auth → search
				// → parse cascade including browser cookie reads and WAF solving.
				provCtx, provCancel := context.WithTimeout(ctx, perProviderTimeout)
				t0 := time.Now()
				hotels, err := rt.searchProvider(provCtx, cfg, location, lat, lon, checkin, checkout, currency, guests, filters)
				provCancel()
				rt.inflight.Add(-1)
				results <- result{hotels: hotels, err: err, id: cfg.ID, name: cfg.Name, latencyMs: time.Since(t0).Milliseconds()}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var statuses []models.ProviderStatus
	var combined []models.HotelResult
	var firstErr error
	for r := range results {
		if r.err != nil {
			slog.Warn("provider error", "provider", r.id, "error", logredact.Err(r.err))
			rt.registry.MarkError(r.id, r.err.Error())
			rt.mu.RLock()
			if pc := rt.clients[r.id]; pc != nil {
				pc.authMu.Lock()
				pc.ttlState = preflightttl.Update(pc.ttlState, preflightttl.OutcomeFailure, time.Now())
				pc.authMu.Unlock()
			}
			rt.mu.RUnlock()
			errMsg := r.err.Error()
			status := "error"
			if isTimeoutError(r.err) {
				status = "timeout"
			}
			hintCode, hint := classifyProviderError(r.err)
			LogHealth(HealthEntry{
				Provider:  r.id,
				Operation: "search",
				Status:    status,
				LatencyMs: r.latencyMs,
				Error:     errMsg,
				HintCode:  string(hintCode),
			})
			statuses = append(statuses, models.ProviderStatus{
				ID:          r.id,
				Name:        r.name,
				Status:      status,
				Error:       errMsg,
				FixHint:     hint,
				FixHintCode: string(hintCode),
			})
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		rt.registry.MarkSuccess(r.id)
		rt.mu.RLock()
		if pc := rt.clients[r.id]; pc != nil {
			pc.authMu.Lock()
			pc.ttlState = preflightttl.Update(pc.ttlState, preflightttl.OutcomeSuccess, time.Now())
			pc.authMu.Unlock()
		}
		rt.mu.RUnlock()
		if prior, ok := recovering[r.id]; ok {
			slog.Info("circuit breaker: provider recovered",
				"provider", r.id,
				"was_failure_count", prior)
		}
		LogHealth(HealthEntry{
			Provider:  r.id,
			Operation: "search",
			Status:    "ok",
			LatencyMs: r.latencyMs,
			Results:   len(r.hotels),
		})
		statuses = append(statuses, models.ProviderStatus{
			ID:      r.id,
			Name:    r.name,
			Status:  "ok",
			Results: len(r.hotels),
		})
		combined = append(combined, r.hotels...)
	}

	// Surface circuit-broken providers in the response so the agent can
	// see which providers were silently dropped before the fan-out.
	// Pre-fix these were swallowed and the caller had no diagnostic
	// signal at all — only an unexplained small result set.
	statuses = append(statuses, trippedStatuses...)

	if len(combined) == 0 && firstErr != nil {
		return nil, statuses, firstErr
	}
	return combined, statuses, nil
}

// isTimeoutError returns true when err is a context deadline or timeout.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline") ||
		strings.Contains(msg, "timeout")
}

// providerFixHint returns the human-readable hint for a provider error.
// It delegates to classifyProviderError and is kept for backward compatibility
// with any callers that only need the hint string.
func providerFixHint(err error) string {
	_, hint := classifyProviderError(err)
	return hint
}

// applyFilterVars translates a HotelFilterParams into ${...} template
// variables on the shared vars map, resolving provider-specific lookups
// (property type, sort, amenities) from cfg. A nil filters value is a no-op.
func applyFilterVars(filters *HotelFilterParams, cfg *ProviderConfig, currency string, vars map[string]string) {
	if filters != nil {
		if filters.MinPrice > 0 {
			vars["${min_price}"] = strconv.FormatFloat(filters.MinPrice, 'f', -1, 64)
		}
		if filters.MaxPrice > 0 {
			vars["${max_price}"] = strconv.FormatFloat(filters.MaxPrice, 'f', -1, 64)
		}
		if filters.PropertyType != "" {
			// Resolve to provider-specific ID if a lookup table exists.
			if resolved := resolvePropertyType(cfg.PropertyTypeLookup, filters.PropertyType); resolved != "" {
				vars["${property_type}"] = resolved
			} else {
				vars["${property_type}"] = filters.PropertyType
			}
		}
		if filters.Sort != "" {
			if resolved, ok := cfg.SortLookup[strings.ToLower(filters.Sort)]; ok && resolved != "" {
				// Provider has a mapping for this sort value — use it.
				vars["${sort}"] = resolved
			} else if len(cfg.SortLookup) == 0 {
				// No lookup table — provider accepts raw sort values.
				vars["${sort}"] = filters.Sort
			}
			// When a SortLookup exists but has no mapping for this value,
			// skip setting ${sort} entirely. Sending an unmapped value
			// (e.g. "cheapest" to Hostelworld) causes HTTP 400.
		}
		if filters.Stars > 0 {
			vars["${stars}"] = strconv.Itoa(filters.Stars)
		}
		if filters.MinRating > 0 {
			vars["${min_rating}"] = strconv.FormatFloat(filters.MinRating, 'f', 1, 64)
		}
		if len(filters.Amenities) > 0 {
			vars["${amenities}"] = strings.Join(filters.Amenities, ",")
			// Resolve amenity names to provider-specific IDs.
			if len(cfg.AmenityLookup) > 0 {
				var resolved []string
				for _, a := range filters.Amenities {
					if id, ok := cfg.AmenityLookup[strings.ToLower(a)]; ok && id != "" {
						resolved = append(resolved, id)
					}
				}
				if len(resolved) > 0 {
					vars["${amenity_ids}"] = strings.Join(resolved, ",")
				}
			}
		}
		if filters.FreeCancellation {
			vars["${free_cancellation}"] = "1"
			vars["${flexible_cancellation}"] = "true"
		}
		if filters.Refundable {
			vars["${refundable}"] = "1"
		}
		if len(filters.ChildrenAges) > 0 {
			vars["${children}"] = strconv.Itoa(len(filters.ChildrenAges))
			vars["${children_ages}"] = joinIntValues(filters.ChildrenAges, ",")
		}
		if filters.Rooms > 0 {
			vars["${rooms}"] = strconv.Itoa(filters.Rooms)
		}
		// Build composite price_range var for providers like Booking that
		// encode price filters as "currency-min-max-1" (e.g. "EUR-50-200-1").
		if filters.MinPrice > 0 || filters.MaxPrice > 0 {
			minS := "0"
			maxS := "9999"
			if filters.MinPrice > 0 {
				minS = strconv.FormatFloat(filters.MinPrice, 'f', 0, 64)
			}
			if filters.MaxPrice > 0 {
				maxS = strconv.FormatFloat(filters.MaxPrice, 'f', 0, 64)
			}
			vars["${price_range}"] = currency + "-" + minS + "-" + maxS + "-1"
		}

		// Extended filter vars.
		if filters.MinBedrooms > 0 {
			vars["${min_bedrooms}"] = strconv.Itoa(filters.MinBedrooms)
		}
		if filters.MinBathrooms > 0 {
			vars["${min_bathrooms}"] = strconv.Itoa(filters.MinBathrooms)
		}
		if filters.MinBeds > 0 {
			vars["${min_beds}"] = strconv.Itoa(filters.MinBeds)
		}
		if filters.RoomType != "" {
			// Map canonical names to Airbnb room_types[] values.
			switch strings.ToLower(filters.RoomType) {
			case "entire_home", "entire home", "entire":
				vars["${room_type}"] = "Entire home/apt"
			case "private_room", "private room", "private":
				vars["${room_type}"] = "Private room"
			case "shared_room", "shared room", "shared":
				vars["${room_type}"] = "Shared room"
			case "hotel_room", "hotel room", "hotel":
				vars["${room_type}"] = "Hotel room"
			default:
				vars["${room_type}"] = filters.RoomType
			}
		}
		if filters.Superhost {
			vars["${superhost}"] = "true"
		}
		if filters.InstantBook {
			vars["${instant_book}"] = "true"
		}
		if filters.MaxDistanceM > 0 {
			vars["${max_distance_m}"] = strconv.Itoa(filters.MaxDistanceM)
		}
		if filters.Sustainable {
			vars["${sustainable}"] = "1"
		}
		if filters.MealPlan {
			vars["${meal_plan}"] = "1"
		}
		if filters.IncludeSoldOut {
			vars["${include_sold_out}"] = "1"
		}
		if filters.MustHaveKitchen {
			vars["${must_have_kitchen}"] = "1"
		}
		if filters.MustHaveWifi {
			vars["${must_have_wifi}"] = "1"
		}
		if filters.MustHaveWorkspace {
			vars["${must_have_workspace}"] = "1"
		}
	}
}

func tagProviderRoomTypes(rooms []models.Room, cfg *ProviderConfig, source models.PriceSource, bookingURL, currency string) []models.Room {
	if len(rooms) == 0 {
		return rooms
	}
	out := make([]models.Room, len(rooms))
	for i, room := range rooms {
		if room.Provider == "" {
			room.Provider = source.Provider
			if room.Provider == "" && cfg != nil {
				room.Provider = cfg.ID
			}
		}
		if room.ProviderURL == "" {
			room.ProviderURL = firstNonEmptyString(source.BookingURL, bookingURL)
		}
		if room.Currency == "" {
			room.Currency = firstNonEmptyString(source.Currency, currency)
		}
		if room.MatchConfidence == "" {
			room.MatchConfidence = models.RoomInventoryMatchExact
		}
		if room.PriceBasis == "" {
			if room.TotalPrice > 0 {
				room.PriceBasis = models.PriceBasisRoomTotal
			} else {
				room.PriceBasis = models.PriceBasisRoomNightly
			}
		}
		if room.PriceConfidence == "" {
			room.PriceConfidence = models.PriceConfidenceRoomLevel
		}
		if room.TotalPrice == 0 && room.Price > 0 && room.PriceBasis == models.PriceBasisRoomTotal {
			room.TotalPrice = room.Price
		}
		out[i] = room
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func joinIntValues(values []int, separator string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, separator)
}
