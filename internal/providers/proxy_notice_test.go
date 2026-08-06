package providers

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// TRVL.HARDEN.4 -- a user behind a proxy must be told their proxy is ignored.
//
// internal/destinations previously used http.DefaultTransport, which honours
// HTTP_PROXY and HTTPS_PROXY. Routing it through GuardedTransport (#539) dropped
// that, because the guarded transport sets no Proxy: through a proxy the dialer
// sees the PROXY's address, so the destination policy would be validating the
// wrong host while still reporting requests as guarded.
//
// Direct-only is the right call for the policy. Silent direct-only is not: a
// user on a mandatory-proxy network would see connection errors with no way to
// connect them to a security decision they cannot see. Degrading is sometimes
// necessary; degrading silently is the defect this whole issue is about.
//
// Found by adversarial second-opinion review, 2026-08-06, which correctly
// identified it as a regression rather than a pre-existing gap.
func TestProxyUsersAreToldTheirProxyIsIgnored(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://corp-proxy.invalid:3128")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	// The notice is once-per-process, so reset the latch for this test. Without
	// this the test passes or fails depending on which other test built a
	// transport first -- an order dependency, which is its own kind of test that
	// cannot fail.
	proxyWarnOnce = sync.Once{}
	defer func() { proxyWarnOnce = sync.Once{} }()

	_ = GuardedTransport()

	got := buf.String()
	if !strings.Contains(got, "proxy") {
		t.Fatalf("a user with HTTPS_PROXY set was told nothing. Their destination lookups will fail "+
			"with connection errors they cannot explain.\ngot: %s", got)
	}
	if !strings.Contains(got, "HTTPS_PROXY") {
		t.Errorf("the warning does not name the variable in effect, so the user cannot tell which "+
			"setting is being ignored:\n%s", got)
	}
}

// The control: no proxy configured, no notice. A warning that fires for everyone
// is noise, and noise gets filtered out along with the signal.
func TestNoProxyNoWarning(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(name, "")
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	proxyWarnOnce = sync.Once{}
	defer func() { proxyWarnOnce = sync.Once{} }()

	_ = GuardedTransport()

	if strings.Contains(buf.String(), "proxy") {
		t.Errorf("warned about proxies with none configured:\n%s", buf.String())
	}
}
