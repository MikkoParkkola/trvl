package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHeartbeat_DailyCap proves the at-most-one-per-24h cap: once a send is
// recorded, a second attempt inside 24h is suppressed; after 24h it is due.
func TestHeartbeat_DailyCap(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

	if !dueForSend(dir, now) {
		t.Fatal("first call must be due (no prior record)")
	}
	if err := markSent(dir, now); err != nil {
		t.Fatalf("markSent: %v", err)
	}
	if dueForSend(dir, now.Add(time.Hour)) {
		t.Fatal("second call within 24h must be suppressed")
	}
	if dueForSend(dir, now.Add(23*time.Hour+59*time.Minute)) {
		t.Fatal("just under 24h must be suppressed")
	}
	if !dueForSend(dir, now.Add(24*time.Hour)) {
		t.Fatal("at/after 24h must be due again")
	}
}

// TestHeartbeat_PayloadFields locks the wire contract: exactly the documented
// fields, with no surprise additions that could carry identity.
func TestHeartbeat_PayloadFields(t *testing.T) {
	p := buildPayload("1.2.3", "abc123", "2026-10-02", "0123456789abcdef0123456789abcdef")
	if p.Project != "trvl" || p.Event != "heartbeat" || p.Version != "1.2.3" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	if p.Runtime == "" {
		t.Fatal("runtime must be populated")
	}

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	allowed := map[string]bool{
		"project": true, "event": true, "version": true, "runtime": true,
		"install_id": true, "install_date": true, "machine_id": true,
	}
	if m["install_date"] != "2026-10-02" || m["machine_id"] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("cohort fields missing: %+v", m)
	}
	for k := range m {
		if !allowed[k] {
			t.Fatalf("unexpected field %q in payload (possible identity leak)", k)
		}
	}
}

// TestHeartbeat_OptOut proves every opt-out path suppresses, and that a clean
// (non-CI, opted-in, released) build is NOT suppressed.
func TestHeartbeat_OptOut(t *testing.T) {
	// Establish a clean baseline: clear CI signals and every opt-out var so the
	// negative case is deterministic even when the suite itself runs in CI.
	clearEnv(t)
	if suppressedExceptTest("1.0.0") {
		t.Fatal("clean released build must not be suppressed")
	}

	for _, env := range optOutEnvs {
		clearEnv(t)
		t.Setenv(env, "1")
		if !suppressedExceptTest("1.0.0") {
			t.Fatalf("%s=1 must suppress the heartbeat", env)
		}
	}

	clearEnv(t)
	t.Setenv("CI", "true")
	if !suppressedExceptTest("1.0.0") {
		t.Fatal("CI must suppress the heartbeat")
	}

	for _, v := range []string{"", "dev"} {
		clearEnv(t)
		if !suppressedExceptTest(v) {
			t.Fatalf("dev build (version=%q) must suppress the heartbeat", v)
		}
	}

	// The public entrypoint is always suppressed under `go test`.
	if !suppressed("1.0.0") {
		t.Fatal("suppressed() must be true under testing.Testing()")
	}
}

// TestHeartbeat_Timeout proves a hanging collector does not block: a short
// client deadline returns an error well before the real 3s budget.
func TestHeartbeat_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slower than the client timeout below, but bounded so srv.Close()
		// (which waits for in-flight handlers) does not hang the test.
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 50 * time.Millisecond}
	start := time.Now()
	err := sendWithClient(context.Background(), srv.URL, client, buildPayload("1.0.0", "id", "", ""))
	if err == nil {
		t.Fatal("hanging collector must surface a timeout error to the caller")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

// TestHeartbeat_PayloadSize proves the real payload is well under the 2KB cap
// and that an oversize payload is rejected rather than sent.
func TestHeartbeat_PayloadSize(t *testing.T) {
	raw, err := json.Marshal(buildPayload("1.2.3", "0123456789abcdef0123456789abcdef", "", ""))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) > maxPayloadSize {
		t.Fatalf("payload %d bytes exceeds cap %d", len(raw), maxPayloadSize)
	}

	huge := buildPayload(string(make([]byte, maxPayloadSize+1)), "id", "", "")
	if err := sendWithClient(context.Background(), "http://127.0.0.1:0", http.DefaultClient, huge); err != errPayloadTooLarge {
		t.Fatalf("oversize payload: want errPayloadTooLarge, got %v", err)
	}
}

// TestHeartbeat_ServerError proves a 5xx from the collector is swallowed
// (failure-open): the product path sees success.
func TestHeartbeat_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := sendWithClient(context.Background(), srv.URL, http.DefaultClient, buildPayload("1.0.0", "id", "", "")); err != nil {
		t.Fatalf("5xx must be swallowed (failure-open), got %v", err)
	}
}

// TestHeartbeat_SendPath proves the happy path: the collector receives a POST
// with the exact JSON contract.
func TestHeartbeat_SendPath(t *testing.T) {
	got := make(chan heartbeatPayload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		var p heartbeatPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := sendWithClient(context.Background(), srv.URL, http.DefaultClient, buildPayload("9.9.9", "deadbeef", "", "")); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case p := <-got:
		if p.Project != "trvl" || p.Event != "heartbeat" || p.Version != "9.9.9" || p.InstallID != "deadbeef" {
			t.Fatalf("collector received %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collector never received the heartbeat")
	}
}

// TestEndpoint_DefaultIsOwnedHost checks the compiled collector. An empty
// TRVL_TELEMETRY_ENDPOINT is unset, so the default host is used.
func TestEndpoint_DefaultIsOwnedHost(t *testing.T) {
	t.Setenv("TRVL_TELEMETRY_ENDPOINT", "")
	const want = "https://telemetry.revaluator.ai/v1/heartbeat"
	if got := endpoint(); got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestEndpoint_ExplicitIsHonored(t *testing.T) {
	const want = "http://127.0.0.1:9/v1/heartbeat"
	t.Setenv("TRVL_TELEMETRY_ENDPOINT", want)
	if got := endpoint(); got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestDialHeartbeat_EmptyURLWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dialHeartbeat(context.Background(), "1.2.3", "")
	if _, err := os.Stat(filepath.Join(home, ".trvl")); !os.IsNotExist(err) {
		t.Fatalf("empty endpoint must not create ~/.trvl: %v", err)
	}
}

func TestDialHeartbeat_ExplicitEndpointSendsAndCaps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got := make(chan heartbeatPayload, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p heartbeatPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dialHeartbeat(context.Background(), "1.2.3", srv.URL)
	select {
	case p := <-got:
		if p.Project != "trvl" || p.Event != "heartbeat" || p.Version != "1.2.3" || p.InstallID == "" {
			t.Fatalf("collector received %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("explicit endpoint never received the heartbeat")
	}
	if _, err := os.Stat(filepath.Join(home, ".trvl", installIDFile)); err != nil {
		t.Fatalf("install id not written for an explicit endpoint: %v", err)
	}

	dialHeartbeat(context.Background(), "1.2.3", srv.URL)
	select {
	case p := <-got:
		t.Fatalf("second send inside 24h: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHeartbeatInBackground_TestBinaryWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TRVL_TELEMETRY_ENDPOINT", "http://127.0.0.1:1/v1/heartbeat")
	HeartbeatInBackground(context.Background(), "1.2.3")
	if _, err := os.Stat(filepath.Join(home, ".trvl")); !os.IsNotExist(err) {
		t.Fatalf("go test must not write telemetry state: %v", err)
	}
}

// TestHeartbeat_NoNetworkOnImport proves HeartbeatInBackground is a no-op under
// test (testing.Testing() suppresses) — importing the package never beacons.
func TestHeartbeat_NoNetworkOnImport(t *testing.T) {
	// Must not panic, must not block, must return immediately.
	HeartbeatInBackground(context.Background(), "1.0.0")
}

func TestInstallID_StableAndPersisted(t *testing.T) {
	dir := t.TempDir()
	first := installID(dir)
	if first == "" {
		t.Fatal("install id must be generated")
	}
	if second := installID(dir); second != first {
		t.Fatalf("install id not stable: %q != %q", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, installIDFile)); err != nil {
		t.Fatalf("install id not persisted: %v", err)
	}
}

// clearEnv blanks the CI signals and opt-out vars so suppression tests are
// deterministic regardless of the host environment (including CI runners).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CI", "CONTINUOUS_INTEGRATION", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI",
		"TRAVIS", "BUILDKITE", "DRONE", "JENKINS_URL", "TEAMCITY_VERSION",
		"BITBUCKET_BUILD_NUMBER", "APPVEYOR", "CODEBUILD_BUILD_ID",
		"TRVL_DISABLE_UPDATE_CHECK", "DO_NOT_TRACK", "NO_TELEMETRY", "TRVL_NO_TELEMETRY",
	} {
		t.Setenv(k, "")
	}
}

func TestMachineID_StableAndPersisted(t *testing.T) {
	home := t.TempDir()
	first := machineID(home)
	if !machineIDOK(first) {
		t.Fatalf("machine id %q is not 32 lowercase hex characters", first)
	}
	if second := machineID(home); second != first {
		t.Fatalf("machine id not stable: %q != %q", first, second)
	}
	data, err := os.ReadFile(filepath.Join(home, ".revaluator", "machine-id"))
	if err != nil || strings.TrimSpace(string(data)) != first {
		t.Fatalf("machine id not persisted: %q, %v", data, err)
	}
}

func TestMachineID_CorruptFileIsReplaced(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".revaluator")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "machine-id"), []byte("not-an-id"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := machineID(home)
	if !machineIDOK(id) {
		t.Fatalf("corrupt file not replaced, got %q", id)
	}
	if again := machineID(home); again != id {
		t.Fatalf("replacement not persisted: %q != %q", again, id)
	}
}

// Two products starting at once on a fresh machine must agree on one id.
func TestMachineID_ConcurrentFirstRunsAgree(t *testing.T) {
	for round := 0; round < 20; round++ {
		home := t.TempDir()
		const n = 8
		ids := make(chan string, n)
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			go func() {
				<-start
				ids <- machineID(home)
			}()
		}
		close(start)
		want := ""
		for i := 0; i < n; i++ {
			got := <-ids
			if want == "" {
				want = got
			}
			if got != want || !machineIDOK(got) {
				t.Fatalf("round %d: concurrent first runs disagree: %q vs %q", round, got, want)
			}
		}
		data, _ := os.ReadFile(filepath.Join(home, ".revaluator", "machine-id"))
		if strings.TrimSpace(string(data)) != want {
			t.Fatalf("round %d: returned %q but stored %q", round, want, data)
		}
	}
}

func TestInstallDate_OldIDFileGivesItsDay(t *testing.T) {
	dir := t.TempDir()
	idPath := filepath.Join(dir, installIDFile)
	if err := os.WriteFile(idPath, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(idPath, old, old); err != nil {
		t.Fatal(err)
	}
	if got := installDate(dir); got != "2026-09-30" {
		t.Fatalf("install date for an existing install = %q, want 2026-09-30", got)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "install-date"))
	if err != nil || strings.TrimSpace(string(stored)) != "2026-09-30" {
		t.Fatalf("install date not persisted: %q, %v", stored, err)
	}
}
