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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

const (
	projectID      = "trvl"
	heartbeatEvent = "heartbeat"

	// heartbeatFile is the daily-cap timestamp cache in ~/.trvl, matching the
	// per-user state dir the rest of trvl uses (selfupdate, prefs, providers).
	heartbeatFile   = "heartbeat"
	installIDFile   = "install-id"
	installDateFile = "install-date"

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
	id, installed := installIdentity(dir, time.Now())
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
	stamp, ok := readState(filepath.Join(dir, heartbeatFile))
	if !ok {
		return true
	}
	last, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return true
	}
	return now.Sub(last) >= sendInterval
}

// markSent records now as the last-send timestamp (0600, atomic rename that
// replaces a link at the path instead of writing through it).
func markSent(dir string, now time.Time) error {
	return atomicjson.WriteBytes(filepath.Join(dir, heartbeatFile), []byte(now.UTC().Format(time.RFC3339)))
}

// installIdentity returns the install id and the day it was created. A new id
// (first run, or an id file deleted to reset the install) starts a new install,
// so a day stored for the id it replaced is dropped first.
func installIdentity(dir string, now time.Time) (id, day string) {
	id, created := installID(dir)
	if created {
		_ = os.Remove(filepath.Join(dir, installDateFile))
	}
	return id, installDate(dir, now)
}

// maxStateFile bounds a telemetry state read; every value written is far smaller.
const maxStateFile = 256

// readState returns the trimmed contents of a small regular state file. A
// missing file, a symbolic link, a directory or device, or anything larger than
// maxStateFile reads as absent. The opened file must be the very file the
// Lstat saw, so a swap to a link between the two checks is caught, and the
// read itself is capped. A hard link is a regular file and passes; a caller
// that must refuse one uses readStateLinks.
// ponytail: a path swapped to a FIFO in that window can still block the open;
// O_NONBLOCK|O_NOFOLLOW would close it but has no portable spelling here.
func readState(path string) (string, bool) {
	value, _, ok := readStateLinks(path)
	return value, ok
}

// testHookAfterOpen, when non-nil, runs after readStateLinks has opened a file
// and counted its links, so a test can remove another name of that open file
// at exactly that point.
// Nil in production.
var testHookAfterOpen func(path string)

// readStateLinks is readState plus the link count of the very file whose
// contents it returns: the count is bound to that file (see linkCount), so no
// swap of the path can pair one file's contents with another file's count.
func readStateLinks(path string) (string, uint64, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxStateFile {
		return "", 0, false
	}
	// #nosec G304 -- callers pass a fixed state-file name under trvl's or the
	// user's own state directory, checked above to be a small regular file.
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer func() { _ = f.Close() }()
	// Count links as soon as the file is open: removing another name later
	// lowers the live count, and an open handle does not stop that on Windows
	// any more than on unix. A name removed in the instant between the open
	// and this count is not seen.
	links, ok := linkCount(f, info)
	if !ok {
		return "", 0, false
	}
	if hook := testHookAfterOpen; hook != nil {
		hook(path)
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", 0, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStateFile+1))
	if err != nil || len(data) > maxStateFile {
		return "", 0, false
	}
	return strings.TrimSpace(string(data)), links, true
}

// installID returns a stable random per-install id, generating and persisting
// one on first use. Best-effort: any failure returns "" (the field is omitted).
// created reports that this call generated the id and saved it; an id that
// could not be saved is still returned, with created false.
func installID(dir string) (id string, created bool) {
	path := filepath.Join(dir, installIDFile)
	// Only a generated id that is the sole name of its file is accepted: a hard
	// link is a regular file and may even hold 32 hex characters (an API key),
	// while trvl itself only ever replaces install-id and never links it.
	if id, links, ok := readStateLinks(path); ok && links == 1 && machineIDOK(id) {
		return id, false
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", false
	}
	id = hex.EncodeToString(buf[:])
	// created means a new install now exists on disk; an unsaved id is reported
	// but leaves the stored install day alone.
	return id, atomicjson.WriteBytes(path, []byte(id)) == nil
}

func installDate(dir string, now time.Time) string {
	path := filepath.Join(dir, installDateFile)
	if day, ok := readState(path); ok && validDay(day) {
		return day
	}
	// The id file's modification time is the install day: for an id this process
	// just wrote it is the write instant, so a first send that reads the clock
	// after UTC midnight still dates the install to the day before. A future
	// time can only come from a skewed clock, so it falls back to now.
	day := now.UTC().Format(dayLayout)
	if info, err := os.Lstat(filepath.Join(dir, installIDFile)); err == nil {
		if mt := info.ModTime(); !mt.IsZero() && !mt.After(now) {
			day = mt.UTC().Format(dayLayout)
		}
	}
	_ = atomicjson.WriteBytes(path, []byte(day))
	return day
}

const dayLayout = "2006-01-02"

// validDay accepts a real calendar day in the 2006-01-02 layout, nothing else;
// the same rule as cmd/trvl-telemetry and the Worker receiver.
func validDay(day string) bool {
	parsed, err := time.Parse(dayLayout, day)
	return err == nil && parsed.Format(dayLayout) == day
}

func machineID(home string) string {
	dir := filepath.Join(home, ".revaluator")
	path := filepath.Join(dir, "machine-id")
	if id, ok := readState(path); ok && machineIDOK(id) {
		return id
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	// Products can start together on a fresh machine. Write the id to a private
	// temp file, then hard-link it into place: the link succeeds for exactly one
	// writer, and every other one reads that winner's complete id back.
	tmp, err := os.CreateTemp(dir, "machine-id-*")
	if err != nil {
		return ""
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.WriteString(id)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		return ""
	}
	if err := os.Link(tmp.Name(), path); err == nil {
		return id
	}
	if winner, ok := readState(path); ok && machineIDOK(winner) {
		return winner
	}
	// The existing file is corrupt, or this filesystem has no hard links: replace it.
	// Two repairs at the same moment both rename; the last one wins, so report
	// what is on disk afterwards rather than this process's own candidate.
	// ponytail: a repair that reads before the other's rename still reports its
	// own id once; the next run reads the survivor. A lock file would close it,
	// and is not worth it for a once-per-machine repair.
	if err := os.Rename(tmp.Name(), path); err == nil {
		if onDisk, ok := readState(path); ok && machineIDOK(onDisk) {
			return onDisk
		}
		return id
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
