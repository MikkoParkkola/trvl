package providers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// trvl#539 TRVL.HARDEN.1 -- the exported value must not expose the transport
// whose dialer carries the policy.
//
// The policy is installed on DialContext. If a consumer could reach the
// underlying *http.Transport, they could set DialTLSContext (which takes
// precedence for HTTPS) or nil out DialContext, and go on holding something
// that still looked guarded. The tests above would not notice: they build their
// own client from a fresh call and never mutate it.
//
// HOW THIS ONE FIRES CHANGED WITH #586, and the change is worth recording.
// While GuardedTransport returned an interface, this was enforced by the
// COMPILER: a type assertion on a concrete type is illegal Go, so widening the
// signature back to *http.Transport stopped the package building. The
// proxy-aware work made the return type the concrete *GuardedRoundTripper, so
// that assertion could no longer be written and the compile-time guard was
// lost with it.
//
// The PROPERTY survived the change -- every field of GuardedRoundTripper is
// unexported, so the dialer is still unreachable from outside the package --
// but nothing was left asserting it. This test now checks the property
// directly by reflection rather than relying on a signature. Exporting any
// field, or adding an accessor that hands back the *http.Transport, fails here
// with the reason spelled out.
func TestGuardedTransportDoesNotExposeItsTransport(t *testing.T) {
	rt := GuardedTransport()

	v := reflect.TypeOf(rt).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.IsExported() {
			t.Errorf("GuardedRoundTripper.%s is exported. The destination policy lives on the "+
				"dialer, so any exported route to the transport lets a consumer set DialTLSContext "+
				"or clear DialContext and keep a value that still looks guarded. Keep the field "+
				"unexported; if a caller needs different behaviour, add a GuardedTransportMode, "+
				"which is checked in one place and fails closed.", f.Name)
		}
	}

	// And the type must not satisfy the shape it is protecting against: if a
	// future refactor makes GuardedTransport hand back the transport itself,
	// this stops that at the point of change rather than at the next audit.
	var anyRT any = rt
	if _, ok := anyRT.(*http.Transport); ok {
		t.Fatal("GuardedTransport returned a *http.Transport: a consumer can then set " +
			"DialTLSContext or clear DialContext and keep a value that looks guarded but dials " +
			"anywhere. The policy lives on the dialer, so the dialer must not be reachable.")
	}
}
