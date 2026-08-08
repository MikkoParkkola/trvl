package providers

import "github.com/MikkoParkkola/trvl/internal/logredact"

// redactedError keeps an error's unwrap chain while preventing net/http's
// *url.Error text from returning a credential-bearing request URL to callers.
// Callers that need errors.Is/errors.As still see the original cause; callers
// that render Error() see only the scrubbed form.
type redactedError struct {
	err error
}

func (e redactedError) Error() string { return logredact.Err(e.err) }

func (e redactedError) Unwrap() error { return e.err }

func redactError(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{err: err}
}
