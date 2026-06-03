package hotels

import (
	"sync"
	"time"
)

const (
	baseBackoff   = 1 * time.Second
	maxBackoff    = 8 * time.Second
	throttleAfter = 3
)

type providerStats struct {
	requests    int
	recent429s  int
	last429     time.Time
	backoff     time.Duration
	isThrottled bool
}

type RateManager struct {
	mu    sync.Mutex
	stats map[string]*providerStats
}

func NewRateManager() *RateManager {
	return &RateManager{
		stats: make(map[string]*providerStats),
	}
}

func (rm *RateManager) RecordRequest(provider string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	s := rm.getStats(provider)
	s.requests++
}

func (rm *RateManager) Record429(provider string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	s := rm.getStats(provider)
	s.recent429s++
	s.last429 = time.Now()
	switch {
	case s.recent429s >= throttleAfter:
		s.backoff = maxBackoff
		s.isThrottled = true
	case s.recent429s >= 2:
		s.backoff = 4 * time.Second
	default:
		s.backoff = 2 * time.Second
	}
}

func (rm *RateManager) Backoff(provider string) time.Duration {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	s := rm.getStats(provider)
	// Decay backoff if enough time has passed since the last 429.
	if s.recent429s > 0 && time.Since(s.last429) > 60*time.Second {
		s.recent429s = 0
		s.backoff = baseBackoff
	}
	if s.backoff < baseBackoff {
		s.backoff = baseBackoff
	}
	return s.backoff
}

func (rm *RateManager) IsThrottled(provider string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	s := rm.getStats(provider)
	if !s.isThrottled {
		return false
	}
	if time.Since(s.last429) > 60*time.Second {
		s.isThrottled = false
		s.backoff = baseBackoff
		s.recent429s = 0
		return false
	}
	return true
}

func (rm *RateManager) Reset(provider string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	s := rm.getStats(provider)
	s.recent429s = 0
	s.backoff = baseBackoff
	s.isThrottled = false
}

func (rm *RateManager) Stats(provider string) (requests, recent429s int, throttled bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	s := rm.getStats(provider)
	return s.requests, s.recent429s, s.isThrottled
}

func (rm *RateManager) getStats(provider string) *providerStats {
	if _, ok := rm.stats[provider]; !ok {
		rm.stats[provider] = &providerStats{
			backoff: baseBackoff,
		}
	}
	return rm.stats[provider]
}
