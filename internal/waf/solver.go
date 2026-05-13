package waf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/sobek"
)

// Errors returned by SolveAWSWAF. Callers typically only care about
// ErrNoChallenge (page didn't look like a WAF interstitial; nothing to do)
// and wrap the rest as "try again" / "escalate to Tier 4".
var (
	ErrNoChallenge    = errors.New("waf: page does not reference an AWS WAF challenge")
	ErrNoToken        = errors.New("waf: challenge completed but no aws-waf-token was set")
	ErrChallengeFail  = errors.New("waf: challenge.js rejected or threw")
	ErrChallengeLimit = errors.New("waf: challenge execution exceeded time budget")
)

// Defaults matching what a real Chrome 129 sends; tuned to align with the
// UA our uTLS fingerprint advertises.
const (
	defaultUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	defaultBudget  = 20 * time.Second
	cookieTokenKey = "aws-waf-token"
)

// Options lets callers tweak behaviour without ballooning the positional
// signature. Zero values pick sane defaults.
type Options struct {
	UserAgent string        // UA advertised to challenge.js. Must match the UA the http.Client sends.
	Origin    string        // Absolute page origin (scheme://host). Defaults to the page's Location if present.
	Budget    time.Duration // Hard wall-clock cap on VM execution. Defaults to 20s.
	Logger    func(msg string, args ...any)
}

// SolveAWSWAF runs the Tier-3 challenge solver against pageBody (the HTML of
// the WAF interstitial page) using client for any outbound HTTPS fetches.
//
// Contract:
//   - client MUST be configured with the same TLS fingerprint + cookie jar
//     that the caller will reuse for the real request afterwards. Nothing in
//     here mints its own client.
//   - pageURL is the absolute URL of the interstitial response (used only
//     for cookie scoping and relative-URL resolution).
//   - Returns a ready-to-install *http.Cookie with Name=aws-waf-token and a
//     5-minute expiry. Callers should jar.SetCookies it against pageURL's
//     registrable domain.
func SolveAWSWAF(ctx context.Context, client *http.Client, pageURL, pageBody string, opts *Options) (*http.Cookie, error) {
	if client == nil {
		return nil, errors.New("waf: SolveAWSWAF requires an http.Client")
	}
	if opts == nil {
		opts = &Options{}
	}
	budget := opts.Budget
	if budget <= 0 {
		budget = defaultBudget
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUA
	}

	info, err := parseChallengePage(pageURL, pageBody)
	if err != nil {
		return nil, err
	}

	origin := opts.Origin
	if origin == "" {
		origin = info.origin
	}

	// Fetch the challenge script through the caller's client.
	fetchCtx, cancelFetch := context.WithTimeout(ctx, 10*time.Second)
	defer cancelFetch()
	script, err := fetchChallengeScript(fetchCtx, client, info.scriptURL, origin, ua)
	if err != nil {
		return nil, fmt.Errorf("waf: fetch challenge.js: %w", err)
	}

	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, client, origin, ua)
	host.logger = opts.Logger
	if err := host.install(); err != nil {
		return nil, err
	}

	// Expose gokuProps so challenge.js (or any wrapper) can read it.
	if info.gokuProps != "" {
		script := fmt.Sprintf("window.gokuProps = %s;", info.gokuProps)
		if _, err := vm.RunString(script); err != nil {
			return nil, fmt.Errorf("waf: seed gokuProps: %w", err)
		}
	}

	// Evaluate challenge.js. Track errors; a throw here is terminal.
	if _, err := vm.RunScript(info.scriptURL, script); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChallengeFail, err)
	}

	// Drive getToken() until resolved or we hit the budget.
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	token, err := driveGetToken(runCtx, vm, loop)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrChallengeLimit
		}
		return nil, err
	}

	// Prefer an explicit token returned by getToken(); fall back to scanning
	// the document.cookie sink populated during execution.
	if token == "" {
		token = extractTokenFromJar(host.cookies)
	}
	if token == "" {
		return nil, ErrNoToken
	}

	return buildCookie(pageURL, token), nil
}

// challengeInfo collects the details we pluck from the interstitial HTML.
type challengeInfo struct {
	origin    string
	scriptURL string
	gokuProps string
}

var (
	// Match challenge.js from either *.awswaf.com or custom host paths like
	// w.booking.com/__challenge_*/challenge.js. The common discriminator is
	// the filename "challenge.js" referenced from a <script> tag.
	scriptURLRe = regexp.MustCompile(`https?://[a-zA-Z0-9.-]+/[^"'<>\s]*challenge\.js`)
	gokuPropsRe = regexp.MustCompile(`(?s)window\.gokuProps\s*=\s*(\{.*?\})\s*;`)
)

func parseChallengePage(pageURL, body string) (*challengeInfo, error) {
	out := &challengeInfo{}
	if pageURL != "" {
		if u, err := url.Parse(pageURL); err == nil && u.Scheme != "" && u.Host != "" {
			out.origin = u.Scheme + "://" + u.Host
		}
	}
	if m := scriptURLRe.FindString(body); m != "" {
		out.scriptURL = m
	} else {
		return nil, ErrNoChallenge
	}
	if m := gokuPropsRe.FindStringSubmatch(body); len(m) == 2 {
		out.gokuProps = m[1]
	}
	return out, nil
}

func fetchChallengeScript(ctx context.Context, client *http.Client, scriptURL, origin, ua string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scriptURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	if origin != "" {
		req.Header.Set("Referer", origin+"/")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("challenge.js returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultMaxBody))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// driveGetToken resolves window.AwsWafIntegration.getToken() by running the
// event loop until the returned Promise settles. If the integration object
// never appeared (challenge silently failed to install it) we return empty
// string and let the caller fall back to the cookie jar.
func driveGetToken(ctx context.Context, vm *sobek.Runtime, loop *eventLoop) (string, error) {
	integ := vm.Get("window").ToObject(vm).Get("AwsWafIntegration")
	if integ == nil || sobek.IsUndefined(integ) || sobek.IsNull(integ) {
		// No integration surface — still drive the loop briefly so any
		// side-effecting cookie writes happen before we give up.
		runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		_ = loop.run(runCtx, nil)
		return "", nil
	}
	getTokenVal := integ.ToObject(vm).Get("getToken")
	getToken, ok := sobek.AssertFunction(getTokenVal)
	if !ok {
		runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		_ = loop.run(runCtx, nil)
		return "", nil
	}

	result, err := getToken(integ)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrChallengeFail, err)
	}

	// The result may be a Promise or a plain string. Handle both.
	if p, ok := result.Export().(*sobek.Promise); ok {
		done := func() bool { return p.State() != sobek.PromiseStatePending }
		if err := loop.run(ctx, done); err != nil {
			return "", err
		}
		switch p.State() {
		case sobek.PromiseStateFulfilled:
			return coerceString(p.Result()), nil
		case sobek.PromiseStateRejected:
			return "", fmt.Errorf("%w: %s", ErrChallengeFail, coerceString(p.Result()))
		default:
			return "", ErrChallengeLimit
		}
	}
	return coerceString(result), nil
}

// coerceString handles the token being passed as either a raw string or as
// a {token: "..."} object (some challenge builds wrap it).
func coerceString(v sobek.Value) string {
	if v == nil || sobek.IsUndefined(v) || sobek.IsNull(v) {
		return ""
	}
	if s, ok := v.Export().(string); ok {
		return s
	}
	if m, ok := v.Export().(map[string]interface{}); ok {
		if t, ok := m["token"].(string); ok {
			return t
		}
	}
	s := v.String()
	if s == "undefined" || s == "null" || strings.HasPrefix(s, "[object ") {
		return ""
	}
	return s
}

// extractTokenFromJar scans document.cookie writes captured during execution
// for an aws-waf-token assignment.
func extractTokenFromJar(entries []cookieEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if strings.EqualFold(entries[i].Name, cookieTokenKey) {
			return entries[i].Value
		}
	}
	return ""
}

// buildCookie packages the token into an http.Cookie scoped to the
// registrable domain of pageURL, with a 5-minute TTL (AWS rotates tokens
// frequently enough that caching longer is pointless).
func buildCookie(pageURL, token string) *http.Cookie {
	c := &http.Cookie{
		Name:     cookieTokenKey,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(5 * time.Minute),
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}
	if u, err := url.Parse(pageURL); err == nil && u.Host != "" {
		// Strip leading "www." for broader matching — matches what real AWS
		// WAF does when it sets the cookie via Set-Cookie: Domain=.example.com.
		host := u.Hostname()
		c.Domain = "." + strings.TrimPrefix(host, "www.")
	}
	return c
}
