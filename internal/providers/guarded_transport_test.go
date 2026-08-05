package providers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// trvl#539 TRVL.HARDEN.1 -- GuardedTransport is exported so other packages can
// route through this package's destination policy, and it must actually carry
// it.
//
// Asserted through a real dial rather than by inspecting the returned struct. A
// transport that merely LOOKS configured proves nothing: the policy lives on the
// dialer's Control function, and comparing struct fields would still pass if
// that were later dropped. So the transport is pointed at a live loopback server
// and required to refuse.
//
// The live server is the point. Without the policy the request SUCCEEDS, so
// there is no ambiguity between "refused" and "could not connect" -- the failure
// mode that would make this test meaningless.
func TestGuardedTransportRefusesLoopback(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: GuardedTransport()}

	resp, err := client.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("GuardedTransport connected to a loopback server (status %s); it is exported for "+
			"other packages to inherit this policy, so a transport without it hands them a "+
			"guarantee that does not exist", resp.Status)
	}
	if !errors.Is(err, ErrDestinationRefused) {
		t.Errorf("failed with %v, want an error wrapping ErrDestinationRefused -- the request must "+
			"be refused BY POLICY, not merely fail for an unrelated reason", err)
	}
}

// The control: the opt-in must still work through the exported transport, or
// every consumer inherits a policy they cannot escape for legitimate local use.
func TestGuardedTransportHonoursTheLocalOptIn(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: GuardedTransport()}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("the local opt-in did not reach the exported transport: %v", err)
	}
	_ = resp.Body.Close()
}
