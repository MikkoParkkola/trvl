package telemetry

// Daily heartbeat (MIK-6568).
//
// A released build emits at most one heartbeat per install per day to
// https://telemetry.revaluator.ai/v1/heartbeat. A non-empty
// TRVL_TELEMETRY_ENDPOINT replaces that URL. The JSON body never contains an
// IP, hostname, or username. install_date is the UTC day the install id was
// created. machine_id is a random id shared by products on this machine, not
// a name or an account. Cloudflare terminates TLS for
// the default host, so Cloudflare can see the connection address. The receiver
// does not copy that address into the stored record. It does store the city
// name and country code from Cloudflare's geolocation of the connection.
// Coordinates and request headers are not stored. The design mirrors the
// fire-and-forget daily update check (internal/selfupdate).
//
// It is failure-open: any collector timeout, 4xx, or 5xx is swallowed and never
// reaches the product path. It is also opt-out via DO_NOT_TRACK, NO_TELEMETRY,
// and TRVL_NO_TELEMETRY, and auto-suppressed for CI, dev builds, and tests.
// Importing this package without calling HeartbeatInBackground performs no
// network I/O — there are no init() effects.

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
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

const (
	projectID      = "trvl"
	heartbeatEvent = "heartbeat"

	// heartbeatFile is the daily-cap timestamp cache in ~/.trvl, matching the
	// per-user state dir the rest of trvl uses (selfupdate, prefs, providers).
	heartbeatFile = "heartbeat"
	installIDFile = "install-id"

	sendInterval   = 24 * time.Hour
	maxPayloadSize = 2048

	// defaultEndpoint is the heartbeat receiver on telemetry.revaluator.ai.
	defaultEndpoint = "https://telemetry.revaluator.ai/v1/heartbeat"
)

// optOutEnvs are honored independently; any set (non-empty, not "0"/"false")
// suppresses the heartbeat. DO_NOT_TRACK is the cross-tool DNT analog (trvl has
// no browser surface; documented as such), NO_TELEMETRY is the common
// convention, TRVL_NO_TELEMETRY is the project-native switch.
var optOutEnvs = []string{"DO_NOT_TRACK", "NO_TELEMETRY", "TRVL_NO_TELEMETRY"}

var errPayloadTooLarge = errors.New("telemetry: payload exceeds size cap")

// heartbeatPayload is the entire wire contract. No IP, hostname, or username
// is in the body. The connection itself still reveals the caller IP to
// whoever terminates TLS for the URL from endpoint.
type heartbeatPayload struct {
	Project     string `json:"project"`
	Event       string `json:"event"`
	Version     string `json:"version"`
	Runtime     string `json:"runtime"`
	InstallID   string `json:"install_id,omitempty"`
	InstallDate string `json:"install_date,omitempty"`
	MachineID   string `json:"machine_id,omitempty"`
}

// HeartbeatInBackground fires a daily anonymous heartbeat in a detached
// goroutine and returns immediately. It does nothing when the build is
// suppressed: no dial, and no install id or daily-slot file. When it does
// send, it claims the daily slot
// synchronously (writes the timestamp before dispatching) so the
// at-most-one-per-24h cap holds even when the collector is unreachable. Pass
// a non-cancellable context (e.g. context.Background()) so a fast-exiting
// command does not abort the send mid-flight; the bounded client timeout is
// the only deadline that matters.
func HeartbeatInBackground(ctx context.Context, version string) {
	if suppressed(version) {
		return
	}
	dialHeartbeat(ctx, version, endpoint())
}

// dialHeartbeat sends one heartbeat to url. An empty url returns before any
// cache directory, install id, or daily-slot write, and before any dial.
func dialHeartbeat(ctx context.Context, version, url string) {
	if url == "" {
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
	installed := installDate(dir)
	machine := ""
	if home, err := os.UserHomeDir(); err == nil {
		machine = machineID(home)
	}
	go func() { _ = send(ctx, url, buildPayload(version, id, installed, machine)) }()
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

// endpoint returns TRVL_TELEMETRY_ENDPOINT when that value is non-empty.
// os.Getenv cannot tell an unset variable from an empty one, so empty uses
// defaultEndpoint.
func endpoint() string {
	if e := os.Getenv("TRVL_TELEMETRY_ENDPOINT"); e != "" {
		return e
	}
	return defaultEndpoint
}

func buildPayload(version, id, installed, machine string) heartbeatPayload {
	return heartbeatPayload{
		Project:     projectID,
		Event:       heartbeatEvent,
		Version:     version,
		Runtime:     runtime.GOOS + "/" + runtime.GOARCH + "/" + runtime.Version(),
		InstallID:   id,
		InstallDate: installed,
		MachineID:   machine,
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
	// #nosec G304 -- heartbeatFile is constant and dir is trvl's local telemetry
	// state directory.
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
	// #nosec G304 -- installIDFile is constant and dir is trvl's local telemetry
	// state directory.
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

func installDate(dir string) string {
	path := filepath.Join(dir, "install-date")
	if data, err := os.ReadFile(path); err == nil {
		day := strings.TrimSpace(string(data))
		if _, err := time.Parse("2006-01-02", day); err == nil && day == timeMust(day) {
			return day
		}
	}
	day := time.Now().UTC().Format("2006-01-02")
	if info, err := os.Stat(filepath.Join(dir, installIDFile)); err == nil && !info.ModTime().IsZero() {
		// A brand-new id file is this process. Keep today's UTC date from the
		// caller clock only when the file was just written; an older file's
		// modification time is the original install.
		if time.Since(info.ModTime()) > 2*time.Minute {
			day = info.ModTime().UTC().Format("2006-01-02")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(path, []byte(day), 0o600)
	}
	return day
}

func timeMust(day string) string {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	return parsed.Format("2006-01-02")
}

func machineID(home string) string {
	dir := filepath.Join(home, ".revaluator")
	path := filepath.Join(dir, "machine-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if machineIDOK(id) {
			return id
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(dir, 0o700); err == nil {
		if err := os.WriteFile(path, []byte(id), 0o600); err == nil {
			return id
		}
	}
	return ""
}

func machineIDOK(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trvl"), nil
}
