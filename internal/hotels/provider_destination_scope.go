package hotels

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

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
	if !strings.EqualFold(requested.Host, effective.Host) {
		return fmt.Errorf("destination scope: response host %q differs from requested host %q", effective.Host, requested.Host)
	}
	requestedPath := path.Clean(requested.Path)
	effectivePath := path.Clean(effective.Path)
	if requestedPath != effectivePath {
		return fmt.Errorf("destination scope: response path %q differs from requested path %q", effectivePath, requestedPath)
	}
	return nil
}
