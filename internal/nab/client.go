package nab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// declineEnv is TRVL_NO_BROWSER_COOKIES, the opt-out from #521.
//
// The parsing rule is duplicated from cookies.Disabled rather than shared,
// because internal/cookies already imports this package and reusing it would
// close an import cycle. internal/cookies owns the definition;
// TestNabAgreesWithCookiesOnTheDecline (in that package, which can see both)
// fails if these two ever disagree.
const declineEnv = "TRVL_NO_BROWSER_COOKIES"

// BrowserCookiesDeclined reports whether the user has declined browser cookie
// reads. Every Fetch passes --cookies to nab, which reads the user's browser
// cookie stores, so a decline has to stop the call rather than adjust it.
func BrowserCookiesDeclined() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(declineEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

var ErrNotAvailable = errors.New("nab not available")

var (
	lookPath       = exec.LookPath
	commandContext = exec.CommandContext
)

type Client struct {
	path string
}

type FetchOptions struct {
	Browser string
	Method  string
	Body    string
	Headers []string
}

type fetchResponse struct {
	Status   int    `json:"status"`
	Markdown string `json:"markdown"`
}

func LookupPath() (string, error) {
	path, err := lookPath("nab")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotAvailable, err)
	}
	return path, nil
}

func New() (*Client, error) {
	path, err := LookupPath()
	if err != nil {
		return nil, err
	}
	return &Client{path: path}, nil
}

func (c *Client) Available() bool {
	return c != nil && c.path != ""
}

// FetchHTML returns the HTML payload from `nab fetch --format json --raw-html`.
// nab emits that payload in the "markdown" field even when the content is raw HTML.
func (c *Client) FetchHTML(ctx context.Context, rawURL, browser string) ([]byte, error) {
	return c.Fetch(ctx, rawURL, FetchOptions{Browser: browser})
}

func (c *Client) Fetch(ctx context.Context, rawURL string, opts FetchOptions) ([]byte, error) {
	if !c.Available() {
		return nil, ErrNotAvailable
	}

	// Every Fetch below passes --cookies, so this whole path reads the user's
	// browser cookie stores. A user who declined that must not have nab started
	// for them -- the rail providers reach here as a fallback AFTER the gated
	// in-process extractor already refused, which is how the opt-out was still
	// leaking cookie reads after #521's first two rounds.
	//
	// ErrNotAvailable rather than a new sentinel: every caller already treats it
	// as "nab is not an option here" and falls through quietly, which is exactly
	// the desired behaviour.
	if BrowserCookiesDeclined() {
		return nil, ErrNotAvailable
	}

	if opts.Browser == "" {
		opts.Browser = "auto"
	}

	args := []string{
		"fetch",
		rawURL,
		"--format",
		"json",
		"--raw-html",
		"--cookies",
		opts.Browser,
		"--no-save",
	}
	if opts.Method != "" && !strings.EqualFold(opts.Method, "GET") {
		args = append(args, "-X", opts.Method)
	}
	if opts.Body != "" {
		args = append(args, "-d", opts.Body)
	}
	for _, header := range opts.Headers {
		args = append(args, "--add-header", header)
	}

	cmd := commandContext(ctx, c.path, args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return nil, fmt.Errorf("nab fetch: %s", stderr)
			}
		}
		return nil, fmt.Errorf("nab fetch: %w", err)
	}

	var resp fetchResponse
	if err := json.Unmarshal(bytes.TrimSpace(out), &resp); err != nil {
		return nil, fmt.Errorf("nab fetch json: %w", err)
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("nab fetch status %d", resp.Status)
	}
	return []byte(resp.Markdown), nil
}
