package cookies

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// BrowserReadPage opens a URL in the user's default browser, waits for the page
// to load, then reads the rendered page text via macOS osascript (AppleScript).
//
// IMPORTANT: This function is a LAST RESORT for one-time CAPTCHA solving only.
// It must NOT be used for normal automated searches — it opens a browser window
// which is disruptive and defeats the purpose of a CLI tool.
//
// For normal provider searches, use the silent cookie-based approach instead:
//   - cookies.BrowserCookies(domain) — silently extracts cookies via nab (no browser opened)
//   - Use extracted cookies with an HTTP client (batchexec.ChromeHTTPClient())
//   - If no cookies available, fall back to reference schedule without opening browser
//
// SkipBrowserRead defaults to false but should be set to true in all automated
// contexts (tests, CI, background searches). Only set false when explicitly
// prompting the user to complete a CAPTCHA challenge.
var SkipBrowserRead bool

func BrowserReadPage(ctx context.Context, url string, waitSeconds int) (string, error) {
	if SkipBrowserRead {
		return "", fmt.Errorf("browser reading disabled")
	}
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("browser page reading requires macOS")
	}
	if waitSeconds <= 0 {
		waitSeconds = 4
	}

	// Try Chrome first, then Safari.
	for _, browser := range []string{"Google Chrome", "Safari"} {
		text, err := browserReadPageWith(ctx, browser, url, waitSeconds)
		if err != nil {
			slog.Debug("browser read failed", "browser", browser, "err", err)
			continue
		}
		if len(text) > 100 {
			return text, nil
		}
	}
	return "", fmt.Errorf("could not read page from any browser")
}

// sanitizeURL removes characters that could enable AppleScript injection.
func sanitizeURL(url string) string {
	s := strings.ReplaceAll(url, `"`, "")
	return strings.ReplaceAll(s, `\`, "")
}

// browserReadPageWith opens a URL in a specific browser and reads the rendered text.
func browserReadPageWith(ctx context.Context, browser, url string, waitSeconds int) (string, error) {
	safeURL := sanitizeURL(url)

	// Open the URL in the browser.
	openScript := fmt.Sprintf(`tell application "%s"
	activate
	if (count of windows) = 0 then
		make new window
	end if
	set URL of active tab of front window to "%s"
end tell`, browser, safeURL)

	if browser == "Safari" {
		openScript = fmt.Sprintf(`tell application "Safari"
	activate
	if (count of documents) = 0 then
		make new document
	end if
	set URL of front document to "%s"
end tell`, safeURL)
	}

	cmd := exec.CommandContext(ctx, "osascript", "-e", openScript)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("open %s: %w", browser, err)
	}

	// Wait for page to render.
	if err := waitForBrowserRender(ctx, time.Duration(waitSeconds)*time.Second); err != nil {
		return "", err
	}

	// Read the rendered page text.
	readScript := fmt.Sprintf(`tell application "%s"
	execute front window's active tab javascript "document.body.innerText"
end tell`, browser)

	if browser == "Safari" {
		readScript = `tell application "Safari"
	do JavaScript "document.body.innerText" in front document
end tell`
	}

	readCmd := exec.CommandContext(ctx, "osascript", "-e", readScript)
	out, err := readCmd.Output()
	if err != nil {
		return "", fmt.Errorf("read %s page: %w", browser, err)
	}

	return strings.TrimSpace(string(out)), nil
}

func waitForBrowserRender(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// BrowserReadPageCached reads a page via the browser, caching the result for the given TTL.
// Subsequent calls with the same URL return the cached text without opening the browser.
var browserPageCache = struct {
	sync.RWMutex
	entries map[string]browserCacheEntry
}{entries: make(map[string]browserCacheEntry)}

type browserCacheEntry struct {
	text    string
	expires time.Time
}

func BrowserReadPageCached(ctx context.Context, url string, waitSeconds int, ttl time.Duration) (string, error) {
	browserPageCache.RLock()
	entry, ok := browserPageCache.entries[url]
	browserPageCache.RUnlock()
	if ok && time.Now().Before(entry.expires) {
		slog.Debug("browser page cache hit", "url", url)
		return entry.text, nil
	}

	text, err := BrowserReadPage(ctx, url, waitSeconds)
	if err != nil {
		return "", err
	}

	browserPageCache.Lock()
	browserPageCache.entries[url] = browserCacheEntry{text: text, expires: time.Now().Add(ttl)}
	browserPageCache.Unlock()
	return text, nil
}
