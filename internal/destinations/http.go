package destinations

import (
	"net/http"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// Shared HTTP clients for the destinations package.
// Reusing clients enables TCP connection pooling and avoids per-request TLS handshakes.
//
// Both route through providers.GuardedTransport, so the destination policy
// applies to direct traffic and independently validates and pins both hops when
// HTTP_PROXY or HTTPS_PROXY is configured (trvl#539, trvl#586).
//
// Before that, these were plain http.Clients. Nothing was exploitable: this
// package composes its URLs from a constant base and numeric parameters
// (attractions.go:18-20, :50, :72-73), so no caller-supplied string reached the
// host. But that made the URL construction the guard rather than the transport
// -- a safety property resting on nobody adding an endpoint that takes a
// caller-supplied value. That is the class of defect that arrives silently and
// passes review, so the transport now carries the guarantee itself.
//
// The policy runs at DIAL time rather than on the URL, so it still holds when a
// redirect or a DNS answer moves the connection somewhere the URL never named.
var (
	// destinationsClient is the default client for most destination APIs
	// (restcountries, wikivoyage, weather, holidays, etc.).
	destinationsClient = &http.Client{
		Timeout:   15 * time.Second,
		Transport: providers.GuardedTransport(),
	}

	// destinationsSlowClient is for APIs that can be slow under load,
	// such as the Overpass/OSM API.
	destinationsSlowClient = &http.Client{
		Timeout:   30 * time.Second,
		Transport: providers.GuardedTransport(),
	}
)
