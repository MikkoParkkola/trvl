package destinations

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// TRVL.HARDEN.1 -- the destinations HTTP clients must route through the same
// guarded transport as providers, so the dial policy that refuses loopback,
// private, link-local, unspecified and multicast addresses applies here too.
//
// This asserts BEHAVIOUR, not wiring. Comparing the Transport field against
// providers.GuardedTransport() would compare two distinct pointers and prove
// nothing about what happens on a dial; and a wiring assertion would still pass
// if the policy inside the transport were later removed. So each client is
// pointed at a real loopback server and required to refuse the connection.
//
// The test server is the point: if the guard is gone, the request SUCCEEDS,
// because the server is right there and answering. There is no ambiguity
// between "refused" and "could not connect".
//
// Why this matters even though nothing is exploitable today: the destinations
// package composes its URLs from a constant base and numeric parameters, so no
// caller string reaches the host. That made URL construction the guard rather
// than the transport, and a future endpoint taking a caller-supplied value would
// remove it silently (trvl#539).
func TestDestinationsClientsRefuseLoopback(t *testing.T) {
	// Clear the package-wide opt-in from TestMain. Without this the test runs
	// under TRVL_ALLOW_LOCAL_PROVIDERS=1 and passes for the wrong reason -- or
	// rather fails to fail, which is worse: it would report the guard working
	// while the guard was switched off. Adversarial review flagged the same
	// hazard for a developer with the variable set in their shell.
	t.Setenv(providers.AllowLocalEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for name, client := range map[string]*http.Client{
		"destinationsClient":     destinationsClient,
		"destinationsSlowClient": destinationsSlowClient,
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := client.Get(srv.URL)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatalf("%s connected to a loopback test server (status %s); it is not routing through "+
					"the guarded transport, so the destination policy does not apply to this package",
					name, resp.Status)
			}
			if !errors.Is(err, providers.ErrDestinationRefused) {
				t.Errorf("%s failed with %v, want an error wrapping ErrDestinationRefused -- the request "+
					"must be refused BY POLICY, not merely fail for an unrelated reason", name, err)
			}
		})
	}
}
