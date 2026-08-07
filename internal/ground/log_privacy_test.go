package ground

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TRVL.LOGLEAK.5 -- assert on the EMITTED LOG RECORD, by driving the real call
// site.
//
// The tempting version of this test calls slog.Debug("...", "url",
// logredact.URL(u)) itself and checks the output. That proves nothing: it tests
// the helper, and passes whether or not SearchFlixBus ever calls it. The
// criterion names that mistake explicitly -- "a helper unit test passes whether
// or not the call site actually uses it" -- and four review rounds on #521 each
// found it.
//
// So this calls SearchFlixBus. The log line fires before the HTTP request is
// even built, so a dead context makes the search fail immediately while still
// exercising the line under test. No network, no fixture server.
//
// The journey values are the point. A rail or coach search URL carries origin,
// destination, date and passenger count in its query string, so a user who runs
// with debug logging and attaches the output to a bug report discloses where
// they are going and when (trvl#531).
func TestSearchFlixBusLogDoesNotCarryTheJourney(t *testing.T) {
	const (
		fromCity = "FROMCITYUUID1234"
		toCity   = "TOCITYUUID5678"
		date     = "2026-08-01"
	)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the log line precedes the request; the request may fail freely

	_, _ = SearchFlixBus(ctx, fromCity, toCity, date, SearchOptions{Currency: "EUR"})

	got := buf.String()
	if !strings.Contains(got, "flixbus search") {
		t.Fatalf("the line under test never fired, so this test asserts nothing. Log was: %q", got)
	}

	for _, leak := range []string{fromCity, toCity, date, "01.08.2026", "adult", "flixbus.com", "from_city_id"} {
		if strings.Contains(got, leak) {
			t.Errorf("the emitted log record carries %q -- a search URL in a log discloses the "+
				"user's journey. Record was: %s", leak, got)
		}
	}
	if !strings.Contains(got, "url#") {
		t.Errorf("the record has no fingerprint, so the URL was dropped entirely rather than "+
			"reduced; log lines must stay correlatable. Record was: %s", got)
	}
}

// The same, for the second ground search site named in the issue.
func TestSearchRegiojetLogDoesNotCarryTheJourney(t *testing.T) {
	const (
		fromID = 987654
		toID   = 123456
		date   = "2026-08-01"
	)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = SearchRegioJet(ctx, fromID, toID, date, SearchOptions{Currency: "EUR"})

	got := buf.String()
	if !strings.Contains(got, "regiojet search") {
		t.Fatalf("the line under test never fired, so this test asserts nothing. Log was: %q", got)
	}

	for _, leak := range []string{"987654", "123456", date, "fromLocationId", "regiojet.cz"} {
		if strings.Contains(got, leak) {
			t.Errorf("the emitted log record carries %q -- a search URL in a log discloses the "+
				"user's journey. Record was: %s", leak, got)
		}
	}
	if !strings.Contains(got, "url#") {
		t.Errorf("the record has no fingerprint, so the URL was dropped rather than reduced. "+
			"Record was: %s", got)
	}
}
