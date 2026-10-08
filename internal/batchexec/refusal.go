package batchexec

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"
)

const (
	// googleRefusalFloor is the cooldown when Google returns HTTP 429 and
	// names no wait. A present Retry-After or retryAfterSeconds replaces it.
	googleRefusalFloor = 60 * time.Second

	// googleRefusalLong is the cooldown for a wrb.fr status 13 (or a rate-limit
	// error object) that carries no wait hint. Google does not publish this
	// duration; 15 minutes is long enough that a round trip and a date scan
	// stop adding requests, and short enough that a later search can try again.
	googleRefusalLong = 15 * time.Minute

	// googleRefusalCap stops a hostile or broken Retry-After from sticking
	// for days. The same bound applies to retryAfterSeconds.
	googleRefusalCap = 15 * time.Minute

	googleRefusalFilename = "google-refusal"
)

// ErrRefused means this Google call was not sent. An earlier refusal on this
// machine is still inside its cooldown.
var ErrRefused = errors.New("google refused further requests until the cooldown ends")

// ErrDeclined means Google rejected this request (HTTP 429, a positive wrb.fr
// status, or a rate-limit error object). The caller must not treat that as a
// cue to call another Google endpoint.
var ErrDeclined = errors.New("google declined the request")

// WrbStatus returns the positive status code from a payload-less wrb.fr row,
// the shape ["wrb.fr", null, null, null, null, [code]]. A normal fare or city
// row carries its JSON in slot 3 and has no status array, so it is not a hit.
func WrbStatus(body []byte) (int, bool) {
	for _, row := range wrbRows(body) {
		if len(row) < 6 {
			continue
		}
		slot, ok := row[5].([]any)
		if !ok || len(slot) == 0 {
			continue
		}
		code, ok := asInt(slot[0])
		if ok && code > 0 {
			return code, true
		}
	}
	return 0, false
}

// QuotaRefusal reports a Google quota refusal: HTTP 429, wrb.fr status 13, or
// an embedded rate-limit error object. A positive wrb.fr code other than 13
// (for example 3, invalid argument) is a rejection but not a quota cooldown.
func QuotaRefusal(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if code, ok := WrbStatus(body); ok && code == 13 {
		return true
	}
	return rateLimitObject(body)
}

func isGoogleHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "google.com" || strings.HasSuffix(host, ".google.com")
}

// refusalBody is an HTTP 200 that must not be stored as a successful fare:
// a positive wrb.fr status, or an error object in the row's inner JSON.
func refusalBody(body []byte) bool {
	if code, ok := WrbStatus(body); ok && code > 0 {
		return true
	}
	return errorObject(body)
}

func (c *Client) cacheSuccess(endpoint, payload string, status int, body []byte, ttl time.Duration) {
	if status != http.StatusOK || refusalBody(body) {
		return
	}
	c.setCached(endpoint, payload, body, ttl)
}

// lockPoll is how often a waiting caller retries the in-process and file
// locks. Polling, rather than a blocking wait, lets the caller's context end
// the wait.
const lockPoll = 10 * time.Millisecond

// beginGoogle serializes Google calls on this client and, when a shared
// cooldown file is configured, across processes. release must be called when
// err is nil. refused means the deadline is still in the future and the caller
// must not dial. err is the context's error if it ended while waiting.
func (c *Client) beginGoogle(ctx context.Context) (release func(), refused bool, err error) {
	for !c.googleMu.TryLock() {
		if err := waitPoll(ctx); err != nil {
			return func() {}, false, err
		}
	}
	unlockFile, err := lockRefusalDir(ctx, c.refusalDir)
	if err != nil {
		c.googleMu.Unlock()
		return func() {}, false, err
	}
	done := func() {
		unlockFile()
		c.googleMu.Unlock()
	}
	now := time.Now()
	if now.Before(c.refuseUntil) {
		done()
		return func() {}, true, nil
	}
	if deadline, ok := c.readRefusalDeadline(); ok && now.Before(deadline) {
		// A deadline further out than the cap can only come from a clock that
		// moved backwards or a damaged file; bound it and store the bound.
		if limit := now.Add(googleRefusalCap); deadline.After(limit) {
			deadline = limit
			if err := c.writeRefusalDeadline(deadline); err != nil {
				slog.Debug("google refusal persist", "error", logredact.Err(err))
			}
		}
		c.refuseUntil = deadline
		done()
		return func() {}, true, nil
	}
	return done, false, nil
}

func waitPoll(ctx context.Context) error {
	t := time.NewTimer(lockPoll)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// lockRefusalDir takes an exclusive cross-process lock on the cooldown
// directory, polling so ctx can end the wait. An empty directory, or any
// failure to create or lock the file, degrades to no cross-process lock: the
// in-process mutex still covers this process, and a lock problem must not
// fail the search.
func lockRefusalDir(ctx context.Context, dir string) (func(), error) {
	if dir == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}, nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "google-refusal.lock"), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed lock name under the client's own refusal directory
	if err != nil {
		return func() {}, nil
	}
	for {
		locked, lockErr := tryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return func() {}, nil
		}
		if locked {
			return func() {
				unlockFile(f)
				_ = f.Close()
			}, nil
		}
		if err := waitPoll(ctx); err != nil {
			_ = f.Close()
			return func() {}, err
		}
	}
}

// armGoogleRefusal records a cooldown in memory and, when a directory is
// configured, in that directory. A disk failure leaves the in-memory deadline
// in place and does not fail the search.
func (c *Client) armGoogleRefusal(wait time.Duration) {
	if wait <= 0 {
		wait = googleRefusalFloor
	}
	until := time.Now().Add(wait)
	if disk, ok := c.readRefusalDeadline(); ok && disk.After(until) {
		until = disk
	}
	if until.After(c.refuseUntil) {
		c.refuseUntil = until
	}
	if err := c.writeRefusalDeadline(c.refuseUntil); err != nil {
		slog.Debug("google refusal persist", "error", logredact.Err(err))
	}
}

func defaultRefusalDir() string {
	// Under go test the cooldown stays in memory. Packages run in parallel on
	// one machine with one home folder, so a shared file let one test's
	// refusal fail unrelated tests and wrote into the developer's real cache
	// (MIK-8095). Tests that exercise the file set refusalDir explicitly.
	if testing.Testing() {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".trvl", "cache")
}

func (c *Client) refusalPath() string {
	if c.refusalDir == "" {
		return ""
	}
	return filepath.Join(c.refusalDir, googleRefusalFilename)
}

func (c *Client) readRefusalDeadline() (time.Time, bool) {
	path := c.refusalPath()
	if path == "" {
		return time.Time{}, false
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is the fixed name under the client's own refusal directory
	if err != nil {
		return time.Time{}, false
	}
	deadline, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}, false
	}
	return deadline, true
}

func (c *Client) writeRefusalDeadline(until time.Time) error {
	path := c.refusalPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(c.refusalDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(until.Format(time.RFC3339Nano)), 0o600)
}

// googleRefusalWait chooses how long to stay quiet. A Retry-After header wins,
// then retryAfterSeconds in the body. Both are capped. With no hint, HTTP 429
// waits the short floor and a status-13 or rate-limit object waits the longer
// default.
func googleRefusalWait(status int, retryAfter string, body []byte, now time.Time) time.Duration {
	if hint := parseRetryAfterHeader(retryAfter, now); hint > 0 {
		return hint
	}
	if secs, ok := embeddedRetryAfterSeconds(body); ok && secs > 0 {
		return capSeconds(secs)
	}
	if status == http.StatusTooManyRequests {
		return googleRefusalFloor
	}
	return googleRefusalLong
}

// capSeconds converts a positive hint in seconds, comparing before
// multiplying so a huge value cannot overflow time.Duration and wrap small.
func capSeconds(secs int) time.Duration {
	if secs >= int(googleRefusalCap/time.Second) {
		return googleRefusalCap
	}
	return time.Duration(secs) * time.Second
}

func capRefusal(d time.Duration) time.Duration {
	if d > googleRefusalCap {
		return googleRefusalCap
	}
	return d
}

// parseRetryAfterHeader parses one Retry-After value. The providers package
// helper is not used here: it caps every hint at 60s, which would clip the
// cooldown this gate is for. Empty, non-positive, past, and unparseable
// values return 0 so the caller can apply its own default.
func parseRetryAfterHeader(value string, now time.Time) time.Duration {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return capSeconds(secs)
	}
	when, err := http.ParseTime(v)
	if err != nil || !when.After(now) {
		return 0
	}
	return capRefusal(when.Sub(now))
}

func shouldStopGoogle(status int, body []byte) bool {
	switch status {
	case http.StatusTooManyRequests:
		return true
	case http.StatusOK:
		return QuotaRefusal(status, body)
	default:
		return false
	}
}

func wrbRows(body []byte) [][]any {
	stripped := StripAntiXSSI(body)
	if len(stripped) == 0 {
		return nil
	}
	if rows := wrbRowsFromJSON(stripped); len(rows) > 0 {
		return rows
	}
	var rows [][]any
	for _, line := range strings.Split(string(stripped), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		rows = append(rows, wrbRowsFromJSON([]byte(line))...)
	}
	return rows
}

func wrbRowsFromJSON(raw []byte) [][]any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var rows [][]any
	collectWrbRows(v, &rows)
	return rows
}

func collectWrbRows(v any, rows *[][]any) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return
	}
	if s, ok := arr[0].(string); ok && s == "wrb.fr" {
		*rows = append(*rows, arr)
		return
	}
	for _, el := range arr {
		collectWrbRows(el, rows)
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

type googleErrorEnvelope struct {
	Error *struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Details []struct {
			Reason            string `json:"reason"`
			RetryAfterSeconds int    `json:"retryAfterSeconds"`
		} `json:"details"`
	} `json:"error"`
}

func rowErrorEnvelope(row []any) (googleErrorEnvelope, bool) {
	if len(row) < 3 {
		return googleErrorEnvelope{}, false
	}
	inner, ok := row[2].(string)
	if !ok || inner == "" {
		return googleErrorEnvelope{}, false
	}
	var env googleErrorEnvelope
	if err := json.Unmarshal([]byte(inner), &env); err != nil || env.Error == nil {
		return googleErrorEnvelope{}, false
	}
	return env, true
}

func errorObject(body []byte) bool {
	for _, row := range wrbRows(body) {
		if _, ok := rowErrorEnvelope(row); ok {
			return true
		}
	}
	return false
}

func rateLimitObject(body []byte) bool {
	for _, row := range wrbRows(body) {
		env, ok := rowErrorEnvelope(row)
		if !ok {
			continue
		}
		if env.Error.Code == http.StatusTooManyRequests || env.Error.Status == "RESOURCE_EXHAUSTED" {
			return true
		}
		for _, detail := range env.Error.Details {
			if detail.Reason == "RATE_LIMIT_EXCEEDED" {
				return true
			}
		}
	}
	return false
}

func embeddedRetryAfterSeconds(body []byte) (int, bool) {
	var best int
	found := false
	for _, row := range wrbRows(body) {
		env, ok := rowErrorEnvelope(row)
		if !ok {
			continue
		}
		for _, detail := range env.Error.Details {
			if detail.RetryAfterSeconds > best {
				best = detail.RetryAfterSeconds
				found = true
			}
		}
	}
	return best, found
}
