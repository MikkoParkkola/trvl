package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/browserutils/kooky"
)

// Browser-cookie read outcomes and their reporting (trvl#529).
//
// Split out of cookies.go, which the 800-line ceiling caught at 901. The seam
// is a real one rather than a line-count convenience: everything here is about
// naming WHY a read produced what it did and saying so at a level a user sees.
// The reading itself stays next to the cookie machinery.

// browserCookieOutcome names why a browser-cookie read produced what it did.
//
// The reader used to answer every one of these with a bare nil. Three of them
// are indistinguishable to a caller and to a user, and that ambiguity has a
// measured cost: a whole session was spent believing the in-process reader
// could not decrypt this machine's cookie stores, when it had actually
// short-circuited on the test-binary guard and never opened one (trvl#529). The
// issue was filed on that misreading and its central claim had to be withdrawn.
//
// "A fallback that cannot work must say so rather than return nil."
type browserCookieOutcome int

const (
	// outcomeUnknown is the zero value, and it exists ONLY so that the zero
	// value is not a claim.
	//
	// This enum previously began at outcomeFound, which meant a bare `return
	// nil` from any future branch reported "cookies were found" while returning
	// none. That is the identical shape as the bug this type was introduced to
	// kill (#529: one silent nil standing in for five distinct outcomes), just
	// relocated into the fix. An unset outcome must read as "nobody said", never
	// as the happy path.
	outcomeUnknown browserCookieOutcome = iota
	// outcomeFound: cookies matched and are being returned.
	outcomeFound
	// outcomeNoMatch: the stores were read and hold nothing for this domain.
	// The ordinary, uninteresting case -- and the ONLY one that means "you are
	// not logged in to this site".
	outcomeNoMatch
	// outcomeDeclined: the user set TRVL_NO_BROWSER_COOKIES. Not a failure.
	outcomeDeclined
	// outcomeSuppressedInTest: the test-binary guard skipped the read to avoid
	// repeated macOS Keychain prompts. Expected under `go test`, and the exact
	// state that was misread as a decryption failure.
	outcomeSuppressedInTest
	// outcomeBadURL: the target URL could not be parsed or had no host. A
	// programming error at the call site, not a property of the machine.
	outcomeBadURL
	// outcomeTimedOut: the read did not finish inside browserCookieLookupTimeout.
	// Split out of outcomeReadFailed because the two have opposite advice: a
	// timeout says nothing about whether the store is readable, and telling a
	// user to grant Keychain access because their disk was slow is a confident
	// wrong answer -- which is how #529 came to be filed in the first place.
	outcomeTimedOut
	// outcomeReadFailed: a cookie store existed but could not be read --
	// Keychain access denied, or a value this build cannot decrypt. This is the
	// one that must reach a user: the fallback is not merely empty, it is
	// broken, and no amount of logging into the site in their browser will fix
	// it.
	outcomeReadFailed
)

func (o browserCookieOutcome) String() string {
	switch o {
	case outcomeFound:
		return "found"
	case outcomeNoMatch:
		return "no_match"
	case outcomeDeclined:
		return "declined"
	case outcomeSuppressedInTest:
		return "suppressed_in_test"
	case outcomeBadURL:
		return "bad_url"
	case outcomeTimedOut:
		return "timed_out"
	case outcomeReadFailed:
		return "read_failed"
	default:
		return "unknown"
	}
}

// browserCookiesForURL reads cookies from the user's browsers matching the
// given URL's domain. Iterates all registered browser cookie stores and
// returns every cookie whose domain matches the URL host (or is a parent
// domain of it).
//
// Returns nil for any of the outcomes above. Callers that need to tell them
// apart use browserCookiesForURLWithOutcome; this wrapper reports them instead,
// with outcomeReadFailed at warn level because a fallback that cannot work is
// something the user has to be able to see (trvl#529, TRVL.KOOKY.2).
//
// This is used as a fallback when standard HTTP preflight gets blocked by
// JavaScript bot-detection challenges (HTTP 202/403). The user's actual
// browser has already solved any JS challenges and has valid session
// cookies, which we can read directly from their disk-backed cookie jars.
func browserCookiesForURL(targetURL string) []*http.Cookie {
	out, outcome, readErr := browserCookiesForURLWithOutcome(targetURL)
	reportOutcome(targetURL, outcome, readErr)
	return out
}

// reportOutcome says, at a level matching how much the user can do about it,
// why a browser-cookie read produced what it did.
//
// Separated from the read so a test can drive every branch directly. The
// alternative -- provoking a real permissions denial or a real timeout on a CI
// runner -- is the "generate the condition and hope" testing that leaves
// branches unverified, which is how this package shipped a warning nobody had
// ever seen fire.
func reportOutcome(targetURL string, outcome browserCookieOutcome, readErr error) {
	switch outcome {
	case outcomeReadFailed:
		// The only outcome a user can act on, and the only one where staying
		// silent is a lie: the site fallback is unavailable, not empty.
		//
		// The underlying error is logged rather than summarised. The hint names
		// the causes we know of, but this branch fires on ANY read error, and a
		// message that asserts a cause it has not established is how #529 was
		// filed on a misdiagnosis. The hint suggests; the error says.
		slog.Warn("browser cookie fallback unavailable: a cookie store was found but could not be read",
			"host", hostForLog(targetURL),
			"err", logredact.Err(readErr),
			"hint", "often a permissions prompt that was denied, or a browser storing values this build cannot decrypt; the error above is authoritative")
	case outcomeTimedOut:
		// Deliberately does NOT mention permissions or decryption. A timeout
		// says nothing about whether the store is readable, and advice about
		// permissions would send the user to fix something that is not broken.
		slog.Warn("browser cookie lookup timed out",
			"host", hostForLog(targetURL),
			"timeout", browserCookieLookupTimeout,
			"err", logredact.Err(readErr),
			"hint", "the cookie stores may be large or the disk busy; this does not indicate a permissions problem")
	case outcomeBadURL:
		// The URL is reduced to a fingerprint like every other logged URL: a
		// target URL carries the user's journey (origin, destination, dates,
		// passengers) in its query string, and being unparseable does not make
		// it less sensitive. The parse error is what actually diagnoses this.
		slog.Warn("browser cookie lookup skipped: unusable target URL",
			"url", logredact.URL(targetURL), "err", logredact.Err(readErr))
	case outcomeSuppressedInTest:
		slog.Debug("browser cookie lookup skipped: test binary",
			"host", hostForLog(targetURL), "override", "TRVL_ALLOW_BROWSER_COOKIES=1")
	case outcomeDeclined:
		slog.Debug("browser cookie lookup skipped: the user has declined browser access",
			"host", hostForLog(targetURL), "env", cookies.DisableEnv)
	case outcomeNoMatch:
		slog.Debug("no browser cookies for host", "host", hostForLog(targetURL))
	case outcomeFound:
	case outcomeUnknown:
		// Unreachable today: every return path names an outcome. Logged rather
		// than ignored because the zero value reaching here means a new branch
		// forgot to say what happened, which is the defect #529 was filed on.
		slog.Debug("browser cookie lookup returned no outcome", "host", hostForLog(targetURL))
	}
}

// hostForLog returns just the host of a URL for logging. Full URLs can carry
// query parameters, and a cookie-related log line is the wrong place to risk
// echoing one.
//
// KEEPING A HOSTNAME AT ALL IS A DECISION, and #531 set the bar for it.
// TRVL.LOGLEAK.4 records that #530's logHost helper was DELETED, because rounds
// 5-7 of that review established that no character-level reduction of a URL is
// provably non-sensitive, and it warns against resurrecting that helper
// unexamined. This is that helper by another name, so here is the examination
// TRVL.LOGLEAK.6 asks for.
//
// WHY A HOSTNAME IS ACCEPTABLE AT THESE SITES. Every caller is a cookie-lookup
// outcome for a provider the user configured. The host names the travel site;
// it does not name the trip. Origin, destination, dates and party size live in
// the query string, which is exactly what this function drops. "The cookie
// lookup for thetrainline.com found nothing" tells an operator which
// integration to look at and tells a reader nothing about where anyone is
// going. Without it the line says only that some lookup failed, and the first
// question anyone asks is which one.
//
// AND WHY THE ZONE IDENTIFIER GOES. url.Hostname() strips the port and the
// brackets and KEEPS the IPv6 zone: "[fe80::1%25eth0]:443" yields
// "fe80::1%eth0". A zone identifier is free-form text carried inside an
// address, so on a provider URL -- which a user-defined provider supplies, and
// #538 tracks how far that trust extends -- it is an attacker-influenceable
// string riding into a log line under a field named "host". Round 6 of #530
// found this on url.URL.Host and it is no less true here. Cutting at the first
// "%" leaves the address and drops the free text; a hostname cannot legally
// contain one.
func hostForLog(targetURL string) string {
	u, err := url.Parse(targetURL)
	// Hostname(), not Host, for the same reason as the reader at the URL guard
	// below: "https://:443/" has a non-empty Host (":443") and an empty
	// Hostname. Gating on Host would let it through and print an empty string.
	if err != nil || u.Hostname() == "" {
		return "?"
	}
	host := u.Hostname()
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		// The whole host was zone text: "%25evil" cuts to nothing. A blank field
		// under the key "host" reads as a hostname that is somehow empty, which
		// is a worse answer than admitting we have none.
		return "?"
	}
	return host
}

// browserCookiesForURL reads cookies from the user's browsers matching the
// given URL's domain. Iterates all registered browser cookie stores and
// returns every cookie whose domain matches the URL host (or is a parent
// domain of it). Returns nil if the URL cannot be parsed, no cookies are
// found, or cookie access fails (e.g. user denied Keychain access on macOS).
//
// This is used as a fallback when standard HTTP preflight gets blocked by
// JavaScript bot-detection challenges (HTTP 202/403). The user's actual
// browser has already solved any JS challenges and has valid session
// cookies, which we can read directly from their disk-backed cookie jars.
// readCookies is the seam through which the cookie stores are read.
//
// It exists so a test can drive the consent RACE, which is otherwise untestable
// from outside: permittedAfterRead re-asks for consent after the read, and
// nothing in the public API can revoke consent while a read is in flight. A test
// that sets the opt-out before calling exercises the entry check instead, and
// passes whether or not the post-read gate works at all.
//
// That is a production variable serving a test, which is a real cost. It is
// taken because the alternative is a correctness property with no test that can
// fail -- and this branch exists to remove exactly that. The default is the real
// reader, and TestReadCookiesDefaultsToTheRealReader pins it.
var readCookies = kooky.ReadCookies

func browserCookiesForURLWithOutcome(targetURL string) (out []*http.Cookie, outcome browserCookieOutcome, readErr error) {
	// The outcome must agree with what is actually returned.
	//
	// permittedAfterRead re-asks for consent AFTER the seconds-long read, so it
	// can empty the list at the last moment -- the user revoked access while the
	// read was in flight. Applying it to `out` alone left the outcome saying
	// "found" beside no cookies, so a caller acting on the outcome would report
	// a successful read that returned nothing. That is the same ambiguity #529
	// exists to remove, reintroduced by the consent gate rather than by the
	// reader.
	//
	// Named outcomeDeclined rather than outcomeNoMatch: the stores may well have
	// held cookies: the user simply withdrew permission to use them. Reporting
	// "you are not logged in" would be a wrong answer to a question nobody asked.
	//
	// Flagged independently by both second-opinion reviewers, 2026-08-06.
	defer func() {
		before := len(out)
		out = permittedAfterRead(out)
		if before > 0 && len(out) == 0 {
			outcome = outcomeDeclined
			readErr = nil
		}
	}()

	// The opt-out sits on the low-level reader, not on the exported wrapper,
	// because a user setting TRVL_NO_BROWSER_COOKIES means their cookie stores
	// rather than one entry point into them. In-package recovery code reaches
	// this function directly (currentCookieSource, the warm-cache path), so a
	// gate on the wrapper alone would ship a control whose name promises more
	// than it delivers — the same defect class as #507.
	//
	// Whether this reader returns anything on a given machine is a separate
	// question, and currently a doubtful one: see #529.
	//
	// This entry check only saves the work. The GUARANTEE is the deferred
	// permittedAfterRead above, which re-asks after the seconds-long read has
	// finished; deleting this one costs time, deleting that one ships the bug.
	if cookies.Disabled() {
		return nil, outcomeDeclined, nil
	}

	// Check warm cache first — returns instantly if pre-warmed.
	if cached := warmBrowserCookiesResult(targetURL, "", browserCookieLookupTimeout); cached != nil {
		// len, not nil. readBrowserCookiesDirect builds its result with
		// make([]*http.Cookie, 0, n), so a CLEAN read that matched no cookies for
		// this domain returns a slice that is empty but not nil -- and the warm
		// cache stores it verbatim. Labelling that "found" reintroduces exactly
		// the "found beside zero cookies" ambiguity this whole change exists to
		// remove, on the pre-warmed path, for a user who is simply not logged in.
		//
		// A read that FAILED returns bare nil, so it never reaches this branch:
		// it falls through to a real read and gets classified there. That is what
		// makes no_match the honest answer here rather than a guess -- reaching
		// this line means a read completed and found nothing, which is the one
		// case where "you are not logged in to this site" is a true statement.
		//
		// Found by both second-opinion reviewers independently, on two different
		// revisions of this branch.
		if len(cached) == 0 {
			return nil, outcomeNoMatch, nil
		}
		return cached, outcomeFound, nil
	}

	// Skip browser cookie lookups during `go test` to avoid macOS Keychain
	// prompts. Every recompiled test binary gets a new code signature, so
	// "Always Allow" doesn't persist and the user gets prompted repeatedly.
	// Live probe tests that genuinely need browser cookies set
	// TRVL_ALLOW_BROWSER_COOKIES=1 explicitly.
	if os.Getenv("TRVL_ALLOW_BROWSER_COOKIES") == "" && isTestBinary() {
		return nil, outcomeSuppressedInTest, nil
	}

	u, err := url.Parse(targetURL)
	if err != nil || u.Hostname() == "" {
		// Hostname(), not Host. "https://:443/" has a NON-empty Host (":443") and
		// an empty Hostname, so the old check let it through and the read ran
		// against an empty domain suffix -- matching whatever that happens to
		// match rather than refusing a URL with no host in it. Raised by
		// adversarial second-opinion review, 2026-08-06.
		//
		// url.Parse returns a nil error for a URL that merely has no host
		// ("/path", "mailto:x"), so the error is synthesised rather than passed
		// through. Returning nil here would leave the caller logging an empty
		// "err" field and asserting nothing -- the shape this whole change is
		// about.
		if err == nil {
			err = fmt.Errorf("no host in target URL")
		}
		return nil, outcomeBadURL, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), browserCookieLookupTimeout)
	defer cancel()

	host := u.Hostname()
	cookies, err := readCookies(ctx, kooky.Valid, kooky.DomainHasSuffix(registrableSuffix(host)))
	if err != nil && len(cookies) == 0 {
		// A store was found and could not be read. Distinct from "this machine
		// has no cookies for the site", and the distinction is the whole point:
		// one is fixed by logging in, the other never is.
		//
		// A timeout is split out because it is not evidence of either. This
		// branch used to answer every failure with "grant Keychain access, or
		// app-bound encryption", so a slow disk produced confident advice about
		// permissions -- the same species of wrong answer that got #529 filed.
		// ctx.Err() is consulted rather than the returned error because a
		// cancelled read may report the cancellation in any wrapping the
		// library chooses.
		if ctx.Err() != nil {
			return nil, outcomeTimedOut, fmt.Errorf("cookie store read did not finish in %s: %w", browserCookieLookupTimeout, err)
		}
		return nil, outcomeReadFailed, err
	}

	result := make([]*http.Cookie, 0, len(cookies))
	// Track best cookie per dedup key (name+domain+path). When kooky returns
	// cookies from multiple browsers (Chrome, Brave, etc.), stale sessions in
	// one browser can shadow fresh sessions in another. Prefer the cookie with
	// the longest value, since fresh session cookies carry more data than
	// stale/expired ones (e.g. bkng_sso_ses: 96 bytes fresh vs 3 bytes stale).
	type entry struct {
		cookie http.Cookie
		idx    int // position in result slice, -1 if not yet appended
	}
	seen := make(map[string]*entry, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		if !cookieDomainMatchesHost(c.Domain, host) {
			continue
		}
		key := c.Name + "\x00" + c.Domain + "\x00" + c.Path
		if prev, dup := seen[key]; dup {
			// Replace if this cookie has a longer (fresher) value.
			if len(c.Value) > len(prev.cookie.Value) {
				prev.cookie = c.Cookie
				if prev.idx >= 0 {
					result[prev.idx] = &prev.cookie
				}
			}
			continue
		}
		cp := c.Cookie // copy
		idx := len(result)
		result = append(result, &cp)
		seen[key] = &entry{cookie: cp, idx: idx}
	}
	if len(result) == 0 {
		// A PARTIAL failure must not be reported as "no cookies".
		//
		// kooky reads several stores and can return cookies from one while
		// reporting an error from another, so the error branch above -- which
		// requires len(cookies)==0 -- does not fire. If domain filtering then
		// leaves nothing, this used to answer outcomeNoMatch: "you are not
		// logged in to this site". That is a confident claim about the user's
		// account made from a read that partly failed, and it is exactly the
		// misdiagnosis #529 was filed on.
		//
		// Raised by adversarial second-opinion review, 2026-08-06.
		if err != nil {
			if ctx.Err() != nil {
				return nil, outcomeTimedOut, fmt.Errorf("cookie store read did not finish in %s: %w", browserCookieLookupTimeout, err)
			}
			return nil, outcomeReadFailed, err
		}
		// The stores were read successfully and hold nothing for this domain.
		// This is the one outcome that genuinely means "you are not logged in
		// to this site", which is why it must not share a return value with
		// "the store could not be read".
		return nil, outcomeNoMatch, nil
	}
	return result, outcomeFound, nil
}
