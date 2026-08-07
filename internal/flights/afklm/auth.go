package afklm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/safeexec"
	"golang.org/x/sync/singleflight"
)

// Environment variables that configure the AF-KLM credential.
const (
	// EnvKey holds the API key directly. Consulted under every policy.
	EnvKey = "AFKLM_KEY"

	// EnvOpRef holds a 1Password secret reference, e.g.
	// "op://Private/AF-KLM/credential". The 1Password CLI is invoked ONLY when
	// this is set: trvl must not guess where a user keeps their secrets, and a
	// guessed reference costs a subprocess that can block or prompt (#507).
	EnvOpRef = "AFKLM_OP_REF"

	// EnvKeychainService overrides the macOS Keychain service name. Unlike the
	// 1Password reference this has a default, because the two are not the same
	// kind of thing: a service name is a convention any user can create
	// (`security add-generic-password -s afklm-api-key -w <key>`), whereas the
	// old hardcoded op:// reference named one specific item in one specific
	// vault that only its author possessed. The override exists so a user who
	// files the key elsewhere is not forced to adopt trvl's naming.
	EnvKeychainService = "AFKLM_KEYCHAIN_SERVICE"
)

// defaultKeychainService is the documented service name for the AF-KLM key.
const defaultKeychainService = "afklm-api-key"

// externalLookupTimeout bounds each external backend individually. `op` and
// `security` are third-party binaries that may wait on a daemon, a network
// round-trip, or a user gesture; without a bound they wait forever (#507).
//
// A var rather than a const solely so tests can control it. Two seconds is
// generous for a real helper, while tests use a larger budget for fast shell
// fixtures and a smaller one for intentional timeout cases.
var externalLookupTimeout = 2 * time.Second

// negativeCacheTTL suppresses repeated external lookups after a failure. trvl
// runs as a long-lived MCP server, so an uncached miss is re-paid on every
// search and, under a fan-out agent, concurrently. The TTL (rather than a
// permanent sync.Once) keeps a credential added mid-session discoverable.
const negativeCacheTTL = 60 * time.Second

// ErrNoCredential is returned when no AF-KLM API key can be found under the
// requested policy. Callers treat this as "provider not configured".
var ErrNoCredential = errors.New("afklm: no API key found (set AFKLM_KEY, or AFKLM_OP_REF for a 1Password reference)")

// ErrHelperTimedOut is returned when an external credential helper exceeded
// externalLookupTimeout. It is deliberately distinct from ErrNoCredential:
// telling a user to go configure a key when in fact `op` hung sends them to fix
// the wrong thing.
//
// It is still negative-cached, unlike a caller cancellation. A hung helper is
// exactly the condition the cache exists for — #507 was helpers accumulating
// because every search re-paid a lookup that never returned. Not caching this
// would restore that bug at two seconds per search rather than forever.
var ErrHelperTimedOut = errors.New("credential helper timed out")

// Policy selects which credential backends ResolveCredential may consult.
//
// The split exists because trvl includes AF-KLM opportunistically in the
// DEFAULT round-trip merge (roundtrip.go), which runs for every user on every
// round-trip search. Nothing on that path may spawn a credential subprocess:
// `op` and `security` can block indefinitely, can open /dev/tty to prompt even
// when their stdio is redirected, and can leave helper processes behind. That
// combination produced #507 — an interactive 1Password setup prompt appearing
// mid-session in an agent's terminal, plus ~20 stalled `op read` processes.
//
// The rule this encodes: an opportunistic lookup the user did not ask for must
// be free and silent. Only an explicit request may touch an external store.
type Policy int

const (
	// PolicyEnvOnly consults EnvKey and nothing else: no subprocess, no
	// prompt, no blocking, no measurable latency. Required on the default
	// search path.
	PolicyEnvOnly Policy = iota

	// PolicyExternal additionally consults the macOS Keychain and, when
	// EnvOpRef is set, the 1Password CLI. Legal only when the user explicitly
	// asked for AF-KLM (`--provider afklm`), where a credential prompt is
	// expected rather than a surprise. Callers should pass a ctx with a
	// deadline; each backend is separately bounded regardless.
	PolicyExternal
)

// ResolveCredential resolves the AF-KLM API key under the given policy.
//
// EnvKey is consulted first under every policy and on every call, so a
// credential exported mid-session takes effect immediately without waiting for
// any cache to expire.
func ResolveCredential(ctx context.Context, policy Policy) (string, error) {
	if key := strings.TrimSpace(os.Getenv(EnvKey)); key != "" {
		return key, nil
	}
	if policy != PolicyExternal {
		return "", ErrNoCredential
	}
	return resolveExternal(ctx)
}

// DefaultPathSkipHint explains why AF-KLM was left out of a default search,
// when the reason is worth saying out loud.
//
// It returns empty for the ordinary case. Most users never enabled AF-KLM, and
// a line about a provider they have never heard of is noise on every search.
//
// It returns a hint when the user demonstrably did configure the provider:
// AFKLM_OP_REF names where their key lives, but the default path will not read
// an external store (#507), so AF-KLM is absent from results the user has every
// reason to expect it in. Staying silent there would look like the provider had
// simply stopped working — and trvl's own rule is never to dress an omission up
// as "nothing found".
//
// Costs one environment read. It must stay that cheap: this runs on every
// round-trip search.
func DefaultPathSkipHint() string {
	if strings.TrimSpace(os.Getenv(EnvKey)) != "" {
		return "" // configured and used; nothing to explain
	}
	if strings.TrimSpace(os.Getenv(EnvOpRef)) == "" {
		return "" // never configured; not this user's concern
	}
	return "AF-KLM is configured via " + EnvOpRef + ", which a default search does not read: " +
		"reading a password manager on every search could block it or raise a prompt. " +
		"Export " + EnvKey + " to include AF-KLM automatically, or run --provider afklm for this search."
}

// External-lookup memoisation. The singleflight group collapses concurrent
// lookups (an agent fanning out parallel searches would otherwise spawn one
// subprocess each); negCache suppresses retries for a while after a failure.
//
// Both are keyed on the configuration that produced the result. A user who
// corrects a bad AFKLM_OP_REF should see the correction take effect at once,
// not sit behind a suppression earned by the value they just replaced.
//
// The cache stores the failure itself, not merely its expiry. Storing only a
// deadline would answer every suppressed call with ErrNoCredential, so a wedged
// helper would be reported correctly once and then misreported as "not
// configured" for the rest of the minute — losing exactly the distinction
// ErrHelperTimedOut exists to make.
type negEntry struct {
	until time.Time
	err   error
}

var (
	extGroup   singleflight.Group
	extCacheMu sync.Mutex
	negCache   = map[string]negEntry{}
)

// externalConfig is one snapshot of everything that decides what an external
// lookup does.
//
// It is read once per resolution and threaded through, rather than each backend
// consulting the environment when it happens to run. Re-reading meant a
// concurrent change could execute configuration B while the result was filed
// against configuration A's cache key — the opposite of the guarantee the keyed
// cache is supposed to give.
type externalConfig struct {
	opRef           string
	keychainService string
}

// snapshotExternalConfig captures the current configuration.
func snapshotExternalConfig() externalConfig {
	svc := strings.TrimSpace(os.Getenv(EnvKeychainService))
	if svc == "" {
		svc = defaultKeychainService
	}
	return externalConfig{
		opRef:           strings.TrimSpace(os.Getenv(EnvOpRef)),
		keychainService: svc,
	}
}

// cacheKey identifies a lookup by the configuration it will use. Every input
// that changes what the lookup does belongs here, or a user who corrects one of
// them sits behind a suppression earned by the value they just replaced. On
// Darwin that includes the Keychain service, not just the 1Password reference.
func (c externalConfig) cacheKey() string {
	if runtime.GOOS != "darwin" {
		return c.opRef
	}
	return c.opRef + "\x00" + c.keychainService
}

// resetExternalCache clears the negative cache and drops any in-flight lookup
// from the group. Test-only.
//
// It must not reassign extGroup: a detached lookup (see resolveExternal) can
// still be running after the caller that started it has gone, and replacing the
// group underneath it is a data race. Forget drops keys through the group's own
// lock instead.
func resetExternalCache() {
	extCacheMu.Lock()
	keys := make([]string, 0, len(negCache))
	for k := range negCache {
		keys = append(keys, k)
	}
	negCache = map[string]negEntry{}
	extCacheMu.Unlock()

	keys = append(keys, snapshotExternalConfig().cacheKey())
	for _, k := range keys {
		extGroup.Forget(k)
	}
}

// suppressed reports the cached failure for key, if one is still in force.
func suppressed(key string) (error, bool) {
	extCacheMu.Lock()
	defer extCacheMu.Unlock()
	e, ok := negCache[key]
	if !ok || !time.Now().Before(e.until) {
		return nil, false
	}
	return e.err, true
}

func resolveExternal(ctx context.Context) (string, error) {
	// Snapshot the reference once and use that same value for the cache key,
	// the singleflight key, and the lookup itself. Reading the environment
	// again inside the lookup would let a concurrent change execute reference B
	// under key A, and file the result against the wrong configuration.
	cfg := snapshotExternalConfig()
	cacheKey := cfg.cacheKey()

	if err, ok := suppressed(cacheKey); ok {
		return "", err
	}

	// A caller that is already gone gets nothing started on its behalf.
	// Without this, a client that cancels and retries could launch a detached
	// helper per attempt, since successful results are not cached.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// The shared lookup must not inherit one arbitrary caller's cancellation.
	// With a plain singleflight.Do, whichever caller happens to lead owns the
	// context: a leader that goes away would fail the lookup for everyone queued
	// behind it and — worse — publish that failure to the negative cache,
	// suppressing a perfectly good credential for the whole TTL. Detaching the
	// lookup's lifetime from any single request removes that coupling. Each
	// backend still carries its own deadline (see credentialCommand), so an
	// uncancellable context cannot mean an unbounded one.
	lookupCtx := context.WithoutCancel(ctx)

	ch := extGroup.DoChan(cacheKey, func() (any, error) {
		// Re-check under the lock at callback entry. The outer check happens
		// before joining the group, so a flight that finished in between could
		// otherwise be re-run: caller A reads "not suppressed", flight B
		// publishes a failure, then A enters the group and spawns a second
		// helper. Re-checking here closes that window.
		if err, ok := suppressed(cacheKey); ok {
			return "", err
		}

		val, err := resolveExternalUncached(lookupCtx, cfg)
		if err != nil && !isContextError(err) {
			// A user staring at "no API key found" needs some way to tell a
			// wedged helper from an absent one. The classified error is safe to
			// log; the helper's own output is not, since it echoes the secret
			// reference and sometimes more of the item.
			slog.Debug("afklm: external credential lookup failed", "err", logredact.Err(err), "ref_configured", cfg.opRef != "")

			// Published inside the flight, before the result becomes visible, so
			// a caller arriving after this call leaves the group cannot pass the
			// cache check and spawn a second helper.
			extCacheMu.Lock()
			negCache[cacheKey] = negEntry{until: time.Now().Add(negativeCacheTTL), err: err}
			extCacheMu.Unlock()
		}
		return val, err
	})

	select {
	case <-ctx.Done():
		// Followers give up independently; the flight continues for whoever is
		// still waiting, and its result still populates the cache.
		return "", ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		key, ok := res.Val.(string)
		if !ok || key == "" {
			return "", ErrNoCredential
		}
		return key, nil
	}
}

// isContextError reports whether err came from the CALLER going away rather
// than from the lookup itself. Only these are exempt from the negative cache:
// a request that was cancelled says nothing about whether a credential exists.
//
// A helper deadline is not in this category. It surfaces as ErrHelperTimedOut
// and is cached, because a hung helper is the condition the cache exists to
// stop re-paying.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func resolveExternalUncached(ctx context.Context, cfg externalConfig) (string, error) {
	if runtime.GOOS == "darwin" {
		key, err := keychainLookup(ctx, cfg.keychainService)
		if err == nil {
			return key, nil
		}
		// A Keychain that timed out is not a Keychain that said "no". Falling
		// through would spend a second backend deadline on 1Password and then
		// report "not configured", so the user would be told to set a variable
		// while the real problem was a wedged helper — and the caller would wait
		// twice as long to hear it.
		if errors.Is(err, ErrHelperTimedOut) || isContextError(err) {
			return "", err
		}
	}

	if cfg.opRef == "" {
		return "", ErrNoCredential
	}
	if _, err := exec.LookPath("op"); err != nil {
		return "", ErrNoCredential
	}
	return opLookup(ctx, cfg.opRef)
}

// keychainLookup reads from the macOS Keychain using the security CLI. Bounded
// and detached: `security` can raise a GUI unlock dialog on a locked keychain,
// which is acceptable only because this runs under PolicyExternal.
func keychainLookup(ctx context.Context, service string) (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	cmd, cmdCtx, cancel := credentialCommand(ctx, "security",
		"find-generic-password",
		"-a", u.Username,
		"-s", service,
		"-w",
	)
	defer cancel()

	out, err := safeexec.Output(cmd)
	if err != nil {
		return "", classifyHelperFailure(ctx, cmdCtx, err)
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", ErrNoCredential
	}
	return key, nil
}

// opLookup reads the given secret reference via the 1Password CLI.
func opLookup(ctx context.Context, ref string) (string, error) {
	cmd, cmdCtx, cancel := credentialCommand(ctx, "op", "read", ref)
	defer cancel()

	out, err := safeexec.Output(cmd)
	if err != nil {
		// safeexec.Output discards the helper's stderr rather than returning it:
		// `op` echoes the secret reference there and, on some failures, more of
		// the item than belongs in a log line.
		return "", classifyHelperFailure(ctx, cmdCtx, err)
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", ErrNoCredential
	}
	return key, nil
}

// credentialCommand builds a bounded, terminal-detached command for reading a
// secret from an external helper. It returns the command's own context so the
// caller can tell a helper deadline apart from a caller cancellation, plus a
// cancel func the caller must invoke.
func credentialCommand(ctx context.Context, name string, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	return safeexec.Command(ctx, externalLookupTimeout, name, args...)
}

// classifyHelperFailure maps a failed helper invocation onto the right error.
// The three cases are genuinely different and callers act on them differently:
// a caller that went away, a helper that hung, and a helper that answered "no".
func classifyHelperFailure(parent, cmdCtx context.Context, err error) error {
	if cerr := parent.Err(); cerr != nil {
		return cerr // the caller went away; not our failure and not cacheable
	}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		return ErrHelperTimedOut
	}
	// A helper that never ran is not a helper that said "no credential here".
	// Permission denied, a corrupt binary or exhausted process resources are
	// operational faults, and reporting them as "not configured" sends the user
	// to edit an environment variable that was never the problem.
	var execErr *exec.Error
	var pathErr *fs.PathError
	if errors.As(err, &execErr) || errors.As(err, &pathErr) {
		return fmt.Errorf("afklm: credential helper could not run: %w", err)
	}
	return ErrNoCredential
}
