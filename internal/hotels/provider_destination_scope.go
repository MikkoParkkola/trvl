package hotels

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func effectiveResponseURL(resp *http.Response) *url.URL {
	if resp == nil || resp.Request == nil {
		return nil
	}
	return resp.Request.URL
}

// validateDestinationResponseURL fails closed when a destination-scoped HTTP
// request is redirected to a generic page, another destination, or another
// host. Query changes, scheme upgrades, and trailing slashes do not remove the
// destination scope.
func validateDestinationResponseURL(requested, effective *url.URL) error {
	if requested == nil {
		return fmt.Errorf("destination scope: requested URL is missing")
	}
	if effective == nil {
		return fmt.Errorf("destination scope: effective URL is missing")
	}
	sameScheme := strings.EqualFold(requested.Scheme, effective.Scheme)
	safeUpgrade := strings.EqualFold(requested.Scheme, "http") && strings.EqualFold(effective.Scheme, "https")
	if !sameScheme && !safeUpgrade {
		return fmt.Errorf("destination scope: response scheme %q is not a safe continuation of requested scheme %q", effective.Scheme, requested.Scheme)
	}
	if !strings.EqualFold(requested.Hostname(), effective.Hostname()) {
		return fmt.Errorf("destination scope: response hostname %q differs from requested hostname %q", effective.Hostname(), requested.Hostname())
	}
	normalizedPort := func(u *url.URL) string {
		port := u.Port()
		switch {
		case strings.EqualFold(u.Scheme, "http") && port == "80":
			return ""
		case strings.EqualFold(u.Scheme, "https") && port == "443":
			return ""
		default:
			return port
		}
	}
	requestedPort := normalizedPort(requested)
	effectivePort := normalizedPort(effective)
	if requestedPort != effectivePort {
		return fmt.Errorf("destination scope: response port %q differs from requested port %q", effectivePort, requestedPort)
	}
	requestedPath := path.Clean(requested.Path)
	effectivePath := path.Clean(effective.Path)
	if requestedPath != effectivePath {
		return fmt.Errorf("destination scope: response path %q differs from requested path %q", effectivePath, requestedPath)
	}
	return nil
}
