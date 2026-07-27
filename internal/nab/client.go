package nab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// BrowserCookiesDeclined reports whether the user has declined browser cookie
// reads. Every Fetch passes --cookies to nab, which reads the user's browser
// cookie stores, so a decline has to stop the call rather than adjust it.
//
// The rule lives in internal/consent. It used to be written out again here,
// because internal/cookies imports this package and reusing its copy would have
// closed an import cycle; the two copies were held together by a cross-package
// test that failed if they ever disagreed. There is now one copy and nothing to
// disagree with.
func BrowserCookiesDeclined() bool { return consent.CookiesDeclined() }

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
	// the desired behaviour. The cost is that a declined read and a broken nab
	// install look identical to everything downstream, so the one place that can
	// still tell them apart says so here.
	if BrowserCookiesDeclined() {
		slog.Debug("nab fetch declined by the user", "env", consent.CookiesEnv, "url", rawURL)
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
