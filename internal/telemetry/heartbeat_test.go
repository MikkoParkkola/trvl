package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	raw, err := json.Marshal(buildPayload("1.2.3", "0123456789abcdef0123456789abcdef", "2026-11-20", "fedcba9876543210fedcba9876543210"))
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
	first, created := installID(dir)
	if first == "" || !created {
		t.Fatalf("install id must be generated on first use: %q created=%v", first, created)
	}
	if second, again := installID(dir); second != first || again {
		t.Fatalf("install id not stable: %q != %q (created=%v)", first, second, again)
	}
	if _, err := os.Stat(filepath.Join(dir, installIDFile)); err != nil {
		t.Fatalf("install id not persisted: %v", err)
	}
}

// symlinkOrSkip points link at a fresh file outside dir holding "precious" and
// returns that target. Windows without the symlink privilege skips.
func symlinkOrSkip(t *testing.T, link string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(target, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return target
}

// assertLinkReplaced checks the state file at path is now a regular file and
// the file the link pointed at was not written through.
func assertLinkReplaced(t *testing.T, path, target string) {
	t.Helper()
	if got, _ := os.ReadFile(target); string(got) != "precious" {
		t.Fatalf("writing %s changed the link target to %q", filepath.Base(path), got)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file after the write: %v", filepath.Base(path), err)
	}
}

// A state file that is a link is replaced, never written through: the file it
// points at keeps its contents, and a link's target is never read as a value.
func TestStateFiles_ReplaceALinkNotItsTarget(t *testing.T) {
	t.Run("install-date", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, installDateFile)
		target := symlinkOrSkip(t, path)
		if day := installDate(dir, time.Now()); !validDay(day) {
			t.Fatalf("install date = %q", day)
		}
		assertLinkReplaced(t, path, target)
	})
	t.Run("install-id", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, installIDFile)
		target := symlinkOrSkip(t, path)
		if id, _ := installID(dir); id == "precious" || id == "" {
			t.Fatalf("install id = %q, want a fresh id, not the link target's contents", id)
		}
		assertLinkReplaced(t, path, target)
	})
	t.Run("heartbeat temp", func(t *testing.T) {
		dir := t.TempDir()
		target := symlinkOrSkip(t, filepath.Join(dir, heartbeatFile+".tmp"))
		if err := markSent(dir, time.Now()); err != nil {
			t.Fatalf("markSent: %v", err)
		}
		if got, _ := os.ReadFile(target); string(got) != "precious" {
			t.Fatalf("markSent wrote through a link to %q", got)
		}
	})
	t.Run("heartbeat", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, heartbeatFile)
		target := symlinkOrSkip(t, path)
		if err := markSent(dir, time.Now()); err != nil {
			t.Fatalf("markSent: %v", err)
		}
		assertLinkReplaced(t, path, target)
	})
	t.Run("machine-id", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".revaluator"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, ".revaluator", "machine-id")
		target := symlinkOrSkip(t, path)
		if id := machineID(home); !machineIDOK(id) {
			t.Fatalf("machine id = %q", id)
		}
		assertLinkReplaced(t, path, target)
	})
}

// A hard link is a regular file, so only the id's own format keeps a linked
// private file out of the heartbeat: anything but a generated id is replaced,
// and the file the link shares stays as it was.
func TestInstallID_HardLinkedPrivateFileIsNotSent(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(t.TempDir(), "notes")
	if err := os.WriteFile(private, []byte("my-secret-token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(dir, installIDFile)); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	id, created := installID(dir)
	if id == "my-secret-token-value" || !machineIDOK(id) || !created {
		t.Fatalf("install id = %q created=%v, want a fresh generated id", id, created)
	}
	if got, _ := readState(filepath.Join(dir, installIDFile)); got != id {
		t.Fatalf("install-id on disk = %q, want the returned id %q", got, id)
	}
	if got, _ := os.ReadFile(private); string(got) != "my-secret-token-value" {
		t.Fatalf("the linked private file changed to %q", got)
	}
}

// A hard-linked file that happens to hold 32 hex characters (an API key, a
// token) passes the format check, so install-id must also be the only link to
// its contents: trvl only ever replaces it, never links it.
func TestInstallID_HardLinkedHexSecretIsNotSent(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(t.TempDir(), "api-key")
	secret := "00112233445566778899aabbccddeeff"
	if err := os.WriteFile(private, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(dir, installIDFile)); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if id, created := installID(dir); id == secret || !machineIDOK(id) || !created {
		t.Fatalf("install id = %q created=%v, want a fresh id, not the hard-linked secret", id, created)
	} else if got, _ := readState(filepath.Join(dir, installIDFile)); got != id {
		t.Fatalf("install-id on disk = %q, want the returned id %q", got, id)
	}
	if got, _ := os.ReadFile(private); string(got) != secret {
		t.Fatalf("the linked private file changed to %q", got)
	}
}

// The link count must describe the file whose contents are returned, as it was
// when found. Unlinking the secret's other name after install-id is opened
// drops its live count to 1 on every platform, and must not make it pass.
func TestInstallID_LinkCountSurvivesAnUnlinkMidRead(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(t.TempDir(), "api-key")
	secret := "00112233445566778899aabbccddeeff"
	if err := os.WriteFile(private, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(dir, installIDFile)); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	ran, removed := false, false
	testHookAfterOpen = func(string) { ran, removed = true, os.Remove(private) == nil }
	t.Cleanup(func() { testHookAfterOpen = nil })
	id, created := installID(dir)
	testHookAfterOpen = nil
	if !ran {
		t.Fatal("the after-open hook never ran")
	}
	if id == secret || !machineIDOK(id) || !created {
		t.Fatalf("install id = %q created=%v (other name removed mid-read: %v, on %s), want a fresh id", id, created, removed, runtime.GOOS)
	}
	if got, _ := readState(filepath.Join(dir, installIDFile)); got != id {
		t.Fatalf("install-id on disk = %q, want the returned id %q", got, id)
	}
	// The race only means something if the other name really went.
	if !removed {
		t.Fatalf("removing the other name mid-read failed on %s, so the race was not exercised", runtime.GOOS)
	}
}

// readState accepts a small regular file and nothing else.
func TestReadState_Bounds(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, n int) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(strings.Repeat("a", n)), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if v, ok := readState(write("max", maxStateFile)); !ok || len(v) != maxStateFile {
		t.Fatalf("a %d-byte file must read: ok=%v len=%d", maxStateFile, ok, len(v))
	}
	if _, ok := readState(write("over", maxStateFile+1)); ok {
		t.Fatalf("a %d-byte file must read as absent", maxStateFile+1)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := readState(sub); ok {
		t.Fatal("a directory must read as absent")
	}
	if _, ok := readState(filepath.Join(dir, "missing")); ok {
		t.Fatal("a missing file must read as absent")
	}
	link := filepath.Join(dir, "link")
	symlinkOrSkip(t, link)
	if _, ok := readState(link); ok {
		t.Fatal("a symbolic link must read as absent")
	}
}

// An id that cannot be saved is not a new install: the stored day stays.
func TestInstallIdentity_UnsavedIDKeepsTheDay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block writes on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, installDateFile), []byte("2026-01-01"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if probe, err := os.CreateTemp(dir, "probe"); err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		t.Skip("directory is writable despite 0500 (running as root)")
	}
	id, created := installID(dir)
	if created || !machineIDOK(id) {
		t.Fatalf("unsaved id: %q created=%v, want a generated id with created=false", id, created)
	}
	if _, day := installIdentity(dir, time.Now()); day != "2026-01-01" {
		t.Fatalf("install day after an unsaved id = %q, want the stored 2026-01-01", day)
	}
}

// Deleting the install id resets the install: the new id gets a new day, not
// the one stored for the id it replaced.
func TestInstallIdentity_NewIDDropsTheOldDay(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, installDateFile), []byte("2026-01-01"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id, day := installIdentity(dir, now)
	if id == "" || day == "2026-01-01" || !validDay(day) {
		t.Fatalf("after a reset: id=%q day=%q, want a new id dated by its creation", id, day)
	}
	again, sameDay := installIdentity(dir, now.Add(72*time.Hour))
	if again != id || sameDay != day {
		t.Fatalf("second call: id=%q day=%q, want %q %q", again, sameDay, id, day)
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
	if got := installDate(dir, time.Now()); got != "2026-09-30" {
		t.Fatalf("install date for an existing install = %q, want 2026-09-30", got)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "install-date"))
	if err != nil || strings.TrimSpace(string(stored)) != "2026-09-30" {
		t.Fatalf("install date not persisted: %q, %v", stored, err)
	}
}

// writeIDFile creates the install id file with the given modification time.
func writeIDFile(t *testing.T, dir string, mtime time.Time) {
	t.Helper()
	idPath := filepath.Join(dir, installIDFile)
	if err := os.WriteFile(idPath, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(idPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// An id file written seconds before UTC midnight belongs to that day, even
// when the first send reads the clock just after midnight.
func TestInstallDate_NewIDFileAcrossMidnightKeepsItsDay(t *testing.T) {
	dir := t.TempDir()
	writeIDFile(t, dir, time.Date(2026, 11, 19, 23, 59, 59, 0, time.UTC))
	now := time.Date(2026, 11, 20, 0, 0, 1, 0, time.UTC)
	if got := installDate(dir, now); got != "2026-11-19" {
		t.Fatalf("install date across midnight = %q, want 2026-11-19", got)
	}
}

// A stored valid day wins over the id file, so the day never drifts.
func TestInstallDate_StoredDayIsKept(t *testing.T) {
	dir := t.TempDir()
	writeIDFile(t, dir, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))
	if err := os.WriteFile(filepath.Join(dir, "install-date"), []byte("2026-10-01\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := installDate(dir, time.Now()); got != "2026-10-01" {
		t.Fatalf("stored install date = %q, want 2026-10-01", got)
	}
}

// A corrupt stored day is replaced with the id file's day and rewritten.
func TestInstallDate_CorruptFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	writeIDFile(t, dir, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	path := filepath.Join(dir, "install-date")
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for _, bad := range []string{"garbage", "2026-02-30", "2026-9-30", ""} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := installDate(dir, now); got != "2026-09-30" {
			t.Fatalf("install date over corrupt %q = %q, want 2026-09-30", bad, got)
		}
		if stored, _ := os.ReadFile(path); strings.TrimSpace(string(stored)) != "2026-09-30" {
			t.Fatalf("corrupt %q not rewritten: %q", bad, stored)
		}
	}
}

// With no id file, or one stamped in the future by a skewed clock, the day
// comes from the caller's clock.
func TestInstallDate_NoOrFutureIDFileUsesNow(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	if got := installDate(t.TempDir(), now); got != "2026-11-20" {
		t.Fatalf("install date without an id file = %q, want 2026-11-20", got)
	}
	dir := t.TempDir()
	writeIDFile(t, dir, now.Add(48*time.Hour))
	if got := installDate(dir, now); got != "2026-11-20" {
		t.Fatalf("install date with a future id file = %q, want 2026-11-20", got)
	}
}
