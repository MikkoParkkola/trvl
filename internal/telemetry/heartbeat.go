package telemetry

// Privacy-preserving active-user heartbeat (MIK-6568).
//
// trvl emits at most one anonymous heartbeat per install per day to the shared
// MIK-6565 collector so the project has a directional active-user and (server-
// side, aggregate, k-anonymity >= 5) geography signal. The client never sends
// an IP, hostname, username, or any host identity. The design deliberately
// mirrors the fire-and-forget daily update check (internal/selfupdate) so the
// footprint and risk are minimal.
//
// It is failure-open: any collector timeout, 4xx, or 5xx is swallowed and never
// reaches the product path. It is also opt-out via several standard env vars
// (DO_NOT_TRACK, NO_TELEMETRY, TRVL_NO_TELEMETRY) and auto-suppressed for CI,
// dev builds, and tests. Importing this package without calling
// HeartbeatInBackground performs no network I/O — there are no init() effects.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

const (
	projectID      = "trvl"
	heartbeatEvent = "heartbeat"

	// defaultEndpoint is the shared MIK-6565 collector. Overridable via
	// TRVL_TELEMETRY_ENDPOINT. The send is failure-open, so the client can
	// ship before the collector is live (sends simply no-op until then).
	// ponytail: confirm/replace with the real MIK-6565 URL before AC.7 deploy.
	defaultEndpoint = "https://telemetry.trvl.app/v1/heartbeat"

	// heartbeatFile is the daily-cap timestamp cache in ~/.trvl, matching the
	// per-user state dir the rest of trvl uses (selfupdate, prefs, providers).
	heartbeatFile = "heartbeat"
	installIDFile = "install-id"

	sendInterval   = 24 * time.Hour
	maxPayloadSize = 2048
)

// optOutEnvs are honored independently; any set (non-empty, not "0"/"false")
// suppresses the heartbeat. DO_NOT_TRACK is the cross-tool DNT analog (trvl has
// no browser surface; documented as such), NO_TELEMETRY is the common
// convention, TRVL_NO_TELEMETRY is the project-native switch.
var optOutEnvs = []string{"DO_NOT_TRACK", "NO_TELEMETRY", "TRVL_NO_TELEMETRY"}

var errPayloadTooLarge = errors.New("telemetry: payload exceeds size cap")

// heartbeatPayload is the entire wire contract. No IP, host, or identity field
// exists by construction — geography is derived server-side in aggregate.
type heartbeatPayload struct {
	Project   string `json:"project"`
	Event     string `json:"event"`
	Version   string `json:"version"`
	Runtime   string `json:"runtime"`
	InstallID string `json:"install_id,omitempty"`
}

// HeartbeatInBackground fires a daily anonymous heartbeat in a detached
// goroutine and returns immediately (before the network call completes).
//
// It claims the daily slot synchronously (writes the timestamp before
// dispatching) so the at-most-one-per-24h cap holds even when the collector is
// unreachable. Pass a non-cancellable context (e.g. context.Background()) so a
// fast-exiting command does not abort the send mid-flight; the bounded client
// timeout is the only deadline that matters.
func HeartbeatInBackground(ctx context.Context, version string) {
	if suppressed(version) {
		return
	}
	dir, err := cacheDir()
	if err != nil {
		return
	}
	if !dueForSend(dir, time.Now()) {
		return
	}
	// Claim the slot up-front: failure-open daily cap (one attempt / 24h max).
	_ = markSent(dir, time.Now())
	id := installID(dir)
	go func() { _ = send(ctx, endpoint(), buildPayload(version, id)) }()
}

// suppressed reports whether the heartbeat must not fire. Under `go test` it is
// always true so a test run never beacons; the env/CI/dev paths are tested via
// suppressedExceptTest.
func suppressed(version string) bool {
	if testing.Testing() {
		return true
	}
	return suppressedExceptTest(version)
}

func suppressedExceptTest(version string) bool {
	if version == "" || version == "dev" {
		return true
	}
	if selfupdate.IsCIEnv() {
		return true
	}
	for _, k := range optOutEnvs {
		if v := os.Getenv(k); v != "" && v != "0" && v != "false" {
			return true
		}
	}
	return false
}

func endpoint() string {
	if e := os.Getenv("TRVL_TELEMETRY_ENDPOINT"); e != "" {
		return e
	}
	return defaultEndpoint
}

func buildPayload(version, id string) heartbeatPayload {
	return heartbeatPayload{
		Project:   projectID,
		Event:     heartbeatEvent,
		Version:   version,
		Runtime:   runtime.GOOS + "/" + runtime.GOARCH + "/" + runtime.Version(),
		InstallID: id,
	}
}

// send POSTs the payload with a bounded 3s client timeout. Returns an error
// only for the caller's own bookkeeping; callers swallow it (failure-open).
func send(ctx context.Context, url string, p heartbeatPayload) error {
	return sendWithClient(ctx, url, &http.Client{Timeout: 3 * time.Second}, p)
}

// sendWithClient is the testable core: tests inject a short-timeout client so
// the timeout path does not block a real 3s. A 5xx is swallowed (returns nil)
// because the collector being unhealthy must never reach the product path.
func sendWithClient(ctx context.Context, url string, client *http.Client, p heartbeatPayload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(body) > maxPayloadSize {
		return errPayloadTooLarge
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// dueForSend reports whether 24h have elapsed since the last recorded send (or
// there is no record yet). Read errors are treated as "due" — best-effort.
func dueForSend(dir string, now time.Time) bool {
	data, err := os.ReadFile(filepath.Join(dir, heartbeatFile))
	if err != nil {
		return true
	}
	last, err := time.Parse(time.RFC3339, string(bytes.TrimSpace(data)))
	if err != nil {
		return true
	}
	return now.Sub(last) >= sendInterval
}

// markSent records now as the last-send timestamp (0600, atomic rename).
func markSent(dir string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, heartbeatFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(now.UTC().Format(time.RFC3339)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// installID returns a stable random per-install id, generating and persisting
// one on first use. Best-effort: any failure returns "" (the field is omitted).
func installID(dir string) string {
	path := filepath.Join(dir, installIDFile)
	if data, err := os.ReadFile(path); err == nil {
		if id := string(bytes.TrimSpace(data)); id != "" {
			return id
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(path, []byte(id), 0o600)
	}
	return id
}

func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trvl"), nil
}
