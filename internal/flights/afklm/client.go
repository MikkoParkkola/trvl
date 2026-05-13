package afklm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultBaseURL = "https://api.airfranceklm.com"
	defaultHost    = "KL"
	// quotaHardLimit is the maximum daily calls before we hard-refuse.
	quotaHardLimit = 95
)

// ErrDailyQuotaExhausted is returned when the daily quota has been exhausted.
var ErrDailyQuotaExhausted = errors.New("afklm: daily quota exhausted (>=95/100 calls used)")

// ClientOptions configures the AF-KLM HTTP client.
type ClientOptions struct {
	BaseURL    string           // default "https://api.airfranceklm.com"
	Host       string           // "KL" (default) or "AF"
	Credential string           // if empty, resolved via auth.ResolveCredential
	CacheDir   string           // default ~/.trvl/cache/afklm
	HTTPClient *http.Client     // default: stdlib default
	Now        func() time.Time // injectable for tests
}

// Client is an HTTP client for the AF-KLM Offers API v3.
type Client struct {
	baseURL    string
	host       string
	key        string
	httpClient *http.Client
	limiter    *rate.Limiter
	cache      *Cache
	now        func() time.Time
	mu         sync.Mutex // serialises quota check + increment
}

// NewClient creates a new Client. If opts.Credential is empty it resolves
// credentials via auth.ResolveCredential. Returns ErrNoCredential if no
// credential can be found.
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.BaseURL == "" {
		opts.BaseURL = defaultBaseURL
	}
	if opts.Host == "" {
		opts.Host = defaultHost
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}

	key := opts.Credential
	if key == "" {
		var err error
		key, err = ResolveCredential(context.Background())
		if err != nil {
			return nil, err
		}
	}

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("afklm: home dir: %w", err)
		}
		cacheDir = filepath.Join(home, ".trvl", "cache", "afklm")
	}

	c, err := NewCache(cacheDir, opts.Now)
	if err != nil {
		return nil, fmt.Errorf("afklm: init cache: %w", err)
	}

	// 1 QPS token bucket; burst=1 for strict 1-per-second enforcement.
	lim := rate.NewLimiter(rate.Every(time.Second), 1)

	return &Client{
		baseURL:    opts.BaseURL,
		host:       opts.Host,
		key:        key,
		httpClient: opts.HTTPClient,
		limiter:    lim,
		cache:      c,
		now:        opts.Now,
	}, nil
}

// do performs a POST request to the given API path with the given body.
// It enforces QPS and daily quota, caches the response, and retries 5xx/429
// once after 2s.
//
// The cache key and TTL are derived from the endpoint + body + days until
// departure. Pass daysUntilDep=-1 to use the minimum TTL (2h).
func (c *Client) do(ctx context.Context, path string, body interface{}, daysUntilDep int) ([]byte, bool, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("afklm: marshal request: %w", err)
	}

	key := CacheKey(path, rawBody)

	// Check cache.
	entry, stale, err := c.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("afklm: cache get: %w", err)
	}
	if entry != nil && !stale {
		c.cache.WriteLastRequest(key, "hit")
		return entry.Body, false, nil
	}
	if entry != nil && stale {
		c.cache.WriteLastRequest(key, "stale")
		// Fire async refresh but return stale data immediately.
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _, _ = c.fetch(bgCtx, path, rawBody, key, daysUntilDep)
		}()
		return entry.Body, true, nil
	}

	c.cache.WriteLastRequest(key, "miss")
	respBody, _, err := c.fetch(ctx, path, rawBody, key, daysUntilDep)
	if err != nil {
		return nil, false, err
	}
	return respBody, false, nil
}

// fetch executes the actual HTTP call, enforcing QPS + quota, writing cache.
func (c *Client) fetch(ctx context.Context, path string, rawBody []byte, cacheKey string, daysUntilDep int) ([]byte, bool, error) {
	// Quota check (serialised).
	c.mu.Lock()
	used, err := c.cache.QuotaUsed(c.now())
	if err != nil {
		c.mu.Unlock()
		return nil, false, fmt.Errorf("afklm: quota check: %w", err)
	}
	if used >= quotaHardLimit {
		c.mu.Unlock()
		return nil, false, ErrDailyQuotaExhausted
	}
	c.mu.Unlock()

	// Rate-limit wait.
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, false, fmt.Errorf("afklm: rate limiter: %w", err)
	}

	respBody, err := c.httpPost(ctx, path, rawBody)
	if err != nil {
		// Retry once for 5xx/429.
		if isRetryable(err) {
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			respBody, err = c.httpPost(ctx, path, rawBody)
		}
		if err != nil {
			return nil, false, err
		}
	}

	// Increment quota on successful call.
	c.mu.Lock()
	_ = c.cache.IncQuota(c.now())
	c.mu.Unlock()

	// Write to cache.
	ttl := DepArrTTL(daysUntilDep)
	if daysUntilDep < 0 {
		ttl = 2 * time.Hour
	}
	_ = c.cache.Put(cacheKey, respBody, ttl)

	return respBody, false, nil
}

// APIError is a typed error carrying HTTP status and response body snippet.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("afklm: API error %d: %s", e.Status, e.Message)
}

// retryableError marks errors that should be retried once.
type retryableError struct {
	inner *APIError
}

func (e *retryableError) Error() string { return e.inner.Error() }
func (e *retryableError) Unwrap() error { return e.inner }

func isRetryable(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}

// httpPost sends a single POST request to the AF-KLM API.
// The API key is never included in error messages.
func (c *Client) httpPost(ctx context.Context, path string, body []byte) ([]byte, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("afklm: new request: %w", err)
	}
	req.Header.Set("AFKL-TRAVEL-Host", c.host)
	req.Header.Set("API-Key", c.key)
	req.Header.Set("Accept", "application/hal+json")
	req.Header.Set("Content-Type", "application/hal+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("afklm: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB max
	if err != nil {
		return nil, fmt.Errorf("afklm: read body: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nil
	}

	// Truncate message to avoid leaking anything sensitive.
	snippet := string(respBody)
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	apiErr := &APIError{Status: resp.StatusCode, Message: snippet}

	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, &retryableError{inner: apiErr}
	}
	return nil, apiErr
}

// Cache returns the underlying Cache (used by search.go).
func (c *Client) Cache() *Cache { return c.cache }

// Now returns the current time via the injected clock (used by search.go).
func (c *Client) Now() time.Time { return c.now() }
