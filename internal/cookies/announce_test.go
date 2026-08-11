package cookies

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// noticeRecorder captures log records and, for each one at Warn or above,
// touches a file. The file is what lets a shell script observe the ordering:
// the fake nab below can ask "had the notice already been emitted when I ran?",
// which is the property under test and not something a captured slice can show.
type noticeRecorder struct {
	slog.Handler
	records []slog.Record
	marker  string
}

func (r *noticeRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *noticeRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.records = append(r.records, rec)
	if rec.Level >= slog.LevelWarn && r.marker != "" {
		f, err := os.Create(r.marker)
		if err == nil {
			_ = f.Close()
		}
	}
	return nil
}

func (r *noticeRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *noticeRecorder) WithGroup(string) slog.Handler      { return r }

// noticeText flattens a record to message plus attribute values, because the
// disclosure is carried partly by structured attrs and a test that read only the
// message would miss the domain and the opt-out variable.
func noticeText(rec slog.Record) string {
	var b strings.Builder
	b.WriteString(rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		return true
	})
	return b.String()
}

// TestCookieReadIsAnnouncedBeforeTheHelperStarts is the half of #521 the env var
// does not answer.
//
// The opt-out only helps someone who already knows it exists. Everything else
// about this read is silent: it begins from an ordinary search, it reaches a
// local credential store, and on macOS it can raise a Keychain prompt that never
// mentions trvl. So the requirement is not merely that a notice exists, but that
// it precedes the helper process -- a disclosure printed after the prompt has
// appeared discloses nothing.
//
// Ordering is proven at the seam rather than by inspecting timestamps: the fake
// nab on PATH records whether the notice marker was already on disk when it ran.
// Moving the announcement below safeexec.Output leaves the notice present and
// this assertion failing, which is the point.
func TestCookieReadIsAnnouncedBeforeTheHelperStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake nab is a shell script")
	}

	dir := t.TempDir()
	notice := filepath.Join(dir, "notice-emitted")
	ran := filepath.Join(dir, "nab-ran")
	sawNotice := filepath.Join(dir, "nab-saw-notice")

	// `: > file` and not `touch`: PATH below holds only this directory, so the
	// fake nab cannot call any external program.
	script := "#!/bin/sh\n" +
		"if [ -f " + notice + " ]; then : > " + sawNotice + "; fi\n" +
		": > " + ran + "\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "nab"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake nab: %v", err)
	}
	t.Setenv("PATH", dir)
	useNabPath(t, filepath.Join(dir, "nab"))
	t.Setenv(DisableEnv, "")

	rec := &noticeRecorder{marker: notice}
	prev := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(prev) })

	resetCookieCache()
	if _, err := extractViaNab(context.Background(), "brave", "thetrainline.com"); err != nil {
		t.Fatalf("extractViaNab with a fake nab on PATH: %v", err)
	}

	if _, err := os.Stat(ran); err != nil {
		t.Fatal("the fake nab never ran, so this test proves nothing about ordering")
	}
	if _, err := os.Stat(sawNotice); err != nil {
		t.Error("the cookie-store read started before the user was told it would; " +
			"the notice must precede the helper, not follow it")
	}

	var announcements []slog.Record
	for _, r := range rec.records {
		if strings.Contains(r.Message, "about to read your browser's cookie store") {
			announcements = append(announcements, r)
		}
	}
	if len(announcements) != 1 {
		t.Fatalf("got %d cookie-read notices, want exactly 1", len(announcements))
	}

	got := noticeText(announcements[0])
	// The domain, so the user knows which site prompted it, and the variable,
	// so the notice is actionable rather than merely informative. A disclosure
	// that does not say how to refuse leaves the user exactly where #521 found
	// them.
	for _, want := range []string{"thetrainline.com", consent.CookiesEnv} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice does not mention %q; it reads: %s", want, got)
		}
	}
	if announcements[0].Level < slog.LevelWarn {
		t.Errorf("the notice is logged at %v, below Warn, so a default handler drops it",
			announcements[0].Level)
	}
}

// TestCookieReadNoticeIsSaidOnceAndNotAtAllOnADecline pins the two ways the
// notice itself becomes a defect.
//
// Repeated, it is noise: a WAF challenge fires for every property in a result
// set, so a per-domain notice puts one line per property on the terminal to say
// the same thing, and a user learns to skip it. Emitted on a decline, it is
// worse than noise -- it announces a read that did not happen, which is the
// disclosure the opt-out exists to prevent.
func TestCookieReadNoticeIsSaidOnceAndNotAtAllOnADecline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake nab is a shell script")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "nab"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake nab: %v", err)
	}
	t.Setenv("PATH", dir)
	useNabPath(t, filepath.Join(dir, "nab"))

	count := func(rec *noticeRecorder) int {
		n := 0
		for _, r := range rec.records {
			if strings.Contains(r.Message, "about to read your browser's cookie store") {
				n++
			}
		}
		return n
	}

	t.Run("once per process across browsers and domains", func(t *testing.T) {
		rec := &noticeRecorder{}
		prev := slog.Default()
		slog.SetDefault(slog.New(rec))
		t.Cleanup(func() { slog.SetDefault(prev) })

		t.Setenv(DisableEnv, "")
		resetCookieCache()
		for _, d := range []string{"thetrainline.com", "sncf-connect.com"} {
			for _, b := range []string{"brave", "chrome"} {
				_, _ = extractViaNab(context.Background(), b, d)
			}
		}
		if n := count(rec); n != 1 {
			t.Errorf("got %d notices across 4 reads, want 1", n)
		}
	})

	t.Run("silent on a decline", func(t *testing.T) {
		rec := &noticeRecorder{}
		prev := slog.Default()
		slog.SetDefault(slog.New(rec))
		t.Cleanup(func() { slog.SetDefault(prev) })

		t.Setenv(DisableEnv, "1")
		resetCookieCache()
		_, _ = extractViaNab(context.Background(), "brave", "thetrainline.com")
		if n := count(rec); n != 0 {
			t.Errorf("got %d notices after an explicit decline, want 0", n)
		}
	})
}
