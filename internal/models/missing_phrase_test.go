package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// statusFrom builds a status the way providers do: the status from
// ClassifyProviderError, the error text kept verbatim.
func statusFrom(id string, err error) ProviderStatus {
	return ProviderStatus{ID: id, Name: id, Status: ClassifyProviderError(err), Error: err.Error()}
}

// breakerStatus mirrors the row the hotel search emits for a tripped breaker
// (internal/hotels/search.go addAux).
func breakerStatus(id string) ProviderStatus {
	return ProviderStatus{
		ID:      id,
		Name:    id,
		Status:  StatusCircuitBroken,
		Error:   "skipped: recent failures tripped the circuit breaker",
		FixHint: "wait for the cooldown to elapse, then it retries automatically",
	}
}

func okStatus(id string) ProviderStatus {
	return ProviderStatus{ID: id, Name: id, Status: StatusCheckedHit, Results: 3}
}

// TestMissingPhrase_WordsWhatHappened is the #807 fail-fast: a provider that
// answered (404, 403, 429, 5xx) is never described as not responding.
func TestMissingPhrase_WordsWhatHappened(t *testing.T) {
	cases := []struct {
		name string
		st   ProviderStatus
		want string
	}{
		{"404 answer", statusFrom("blueground", errors.New("blueground fetch: unexpected status 404 for https://www.theblueground.com/furnished-apartments-vienna")), "failed"},
		{"403 bot wall", statusFrom("rome2rio", errors.New("rome2rio: blocked: bot wall (HTTP 403)")), "refused the request"},
		{"403 answer", statusFrom("trainline", errors.New("trainline: HTTP 403: forbidden")), "refused the request"},
		{"429", statusFrom("flixbus", errors.New("flixbus: HTTP 429 Too Many Requests")), "rate-limited the request"},
		{"503", statusFrom("regiojet", errors.New("regiojet: HTTP 503 Service Unavailable")), "returned a server error"},
		{"deadline", statusFrom("kiwi", fmt.Errorf("kiwi: %w", context.DeadlineExceeded)), "did not respond"},
		{"dns", statusFrom("ferry", errors.New(`Get "https://x.example/api": dial tcp: lookup x.example: no such host`)), "did not respond"},
		{"404 on a URL containing 503", statusFrom("landing", errors.New("landing fetch: unexpected status 404 for https://example.com/hotels-503")), "failed"},
		{"404 on a URL containing timeout", statusFrom("flatio", errors.New("flatio fetch: unexpected status 404 for https://example.com/timeout-deals")), "failed"},
		{"circuit breaker", breakerStatus("expedia"), "not tried (circuit breaker open)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.MissingPhrase(); got != tc.want {
				t.Fatalf("MissingPhrase() = %q, want %q (status %q, reason %q)", got, tc.want, tc.st.Status, tc.st.EffectiveReason())
			}
			note := ComputeCompleteness([]ProviderStatus{okStatus("google_hotels"), tc.st}).IncompleteNote()
			if !strings.Contains(note, tc.st.ID+" "+tc.want) {
				t.Fatalf("note %q does not say %q", note, tc.st.ID+" "+tc.want)
			}
			if tc.want != "did not respond" && strings.Contains(note, "did not respond") {
				t.Fatalf("note %q calls a provider that answered silent", note)
			}
		})
	}
}

func TestMissingPhrase_EmptyForAnsweredProviders(t *testing.T) {
	for _, s := range []string{StatusOK, StatusCheckedHit, StatusCheckedNoHit, StatusStale, StatusSkipped, StatusDisabled, StatusNotConfigured, StatusNotAuthorized} {
		if got := (ProviderStatus{ID: "x", Status: s, Error: "HTTP 403"}).MissingPhrase(); got != "" {
			t.Errorf("status %q: MissingPhrase() = %q, want empty", s, got)
		}
	}
}

// TestIncompleteNote_IssueExamples pins the two summaries quoted in #807.
func TestIncompleteNote_IssueExamples(t *testing.T) {
	hotels := []ProviderStatus{statusFrom("blueground", errors.New("blueground fetch: unexpected status 404 for https://www.theblueground.com/furnished-apartments-vienna"))}
	for i := 0; i < 11; i++ {
		hotels = append(hotels, okStatus(fmt.Sprintf("h%d", i)))
	}
	if got, want := ComputeCompleteness(hotels).IncompleteNote(), "Checked 11 of 12 providers; blueground failed — results may be incomplete."; got != want {
		t.Errorf("hotel note:\n got %q\nwant %q", got, want)
	}

	ground := []ProviderStatus{
		statusFrom("rome2rio", errors.New("rome2rio: blocked: bot wall (HTTP 403)")),
		statusFrom("trainline", errors.New("trainline: HTTP 403: forbidden")),
	}
	for i := 0; i < 5; i++ {
		ground = append(ground, okStatus(fmt.Sprintf("g%d", i)))
	}
	if got, want := ComputeCompleteness(ground).IncompleteNote(), "Checked 5 of 7 providers; rome2rio, trainline refused the request — results may be incomplete."; got != want {
		t.Errorf("ground note:\n got %q\nwant %q", got, want)
	}
}

func mixedStatuses() []ProviderStatus {
	return []ProviderStatus{
		breakerStatus("expedia"),
		statusFrom("blueground", errors.New("blueground fetch: unexpected status 404 for https://www.theblueground.com/furnished-apartments-vienna")),
		statusFrom("rome2rio", errors.New("rome2rio: blocked: bot wall (HTTP 403)")),
		okStatus("google_hotels"),
		statusFrom("kiwi", fmt.Errorf("kiwi: %w", context.DeadlineExceeded)),
		statusFrom("trainline", errors.New("trainline: HTTP 403: forbidden")),
	}
}

// TestIncompleteNote_MixedOutcomes pins grouping (ids keep their input order
// inside a group) and the fixed group order.
func TestIncompleteNote_MixedOutcomes(t *testing.T) {
	got := ComputeCompleteness(mixedStatuses()).IncompleteNote()
	want := "Checked 1 of 6 providers; kiwi did not respond; rome2rio, trainline refused the request; blueground failed; expedia not tried (circuit breaker open) — results may be incomplete."
	if got != want {
		t.Fatalf("note:\n got %q\nwant %q", got, want)
	}
}

// A Completeness built without statuses has no detail: the note stays neutral
// and never claims silence.
func TestIncompleteNote_HandBuiltIsNeutral(t *testing.T) {
	c := Completeness{State: CompletenessPartial, Queried: 2, Succeeded: 1, Missing: []string{"x"}}
	if got, want := c.IncompleteNote(), "Checked 1 of 2 providers; x failed — results may be incomplete."; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
}

func TestCompleteness_JSONKeysUnchanged(t *testing.T) {
	raw, err := json.Marshal(ComputeCompleteness(mixedStatuses()))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got, want := strings.Join(keys, ","), "missing,queried,state,succeeded"; got != want {
		t.Fatalf("completeness JSON keys = %s, want %s", got, want)
	}
}

// A timeout recorded with no error text is still silence (#817 review).
func TestMissingPhrase_TimeoutWithoutErrorText(t *testing.T) {
	if got := (ProviderStatus{ID: "kiwi", Status: StatusTimeout}).MissingPhrase(); got != "did not respond" {
		t.Fatalf("MissingPhrase() = %q, want did not respond", got)
	}
}

// An explicit Reason wins over the error text, as it does in the JSON.
func TestMissingPhrase_ExplicitReasonWins(t *testing.T) {
	cases := []struct {
		st   ProviderStatus
		want string
	}{
		{ProviderStatus{ID: "a", Status: StatusRateLimited, Reason: ReasonTimeout, Error: "HTTP 403 forbidden"}, "did not respond"},
		{ProviderStatus{ID: "b", Status: StatusFailed, Reason: ReasonBlocked, Error: "unexpected status 404"}, "refused the request"},
	}
	for _, tc := range cases {
		if got := tc.st.MissingPhrase(); got != tc.want {
			t.Errorf("%s: MissingPhrase() = %q, want %q", tc.st.ID, got, tc.want)
		}
	}
}

// Clone shares no slice with the original, the unexported phrases included,
// and keeps nil slices nil.
func TestCompleteness_CloneSharesNothing(t *testing.T) {
	c := ComputeCompleteness(mixedStatuses())
	cp := c.Clone()
	if !reflect.DeepEqual(cp, c) {
		t.Fatalf("clone differs:\n got %+v\nwant %+v", cp, c)
	}
	if len(cp.Missing) == 0 || len(cp.missingWhy) == 0 {
		t.Fatalf("fixture has no missing providers: %+v", cp)
	}
	cp.Missing[0] = "changed"
	cp.missingWhy[0] = "changed"
	if c.Missing[0] == "changed" || c.missingWhy[0] == "changed" {
		t.Error("clone shares a slice with the original")
	}
	if got := (Completeness{}).Clone(); got.Missing != nil || got.missingWhy != nil {
		t.Errorf("nil slices became %#v", got)
	}
}

// A provider refused partway (a later page or sort order) after returning
// results answered in part: not silent, not refused outright, and the search
// is partial, never blocked (t-rooms #826 reports google_hotels this way).
func TestMissingPhrase_PartialAnswer(t *testing.T) {
	partial := ProviderStatus{ID: "google_hotels", Status: StatusRateLimited, Results: 40, Error: "google hotels page 2: HTTP 429 Too Many Requests"}
	if got := partial.MissingPhrase(); got != "answered only in part" {
		t.Fatalf("MissingPhrase() = %q, want answered only in part", got)
	}
	c := ComputeCompleteness([]ProviderStatus{partial})
	if c.State != CompletenessPartial {
		t.Errorf("state = %q, want partial: google_hotels returned 40 results", c.State)
	}
	if got, want := c.IncompleteNote(), "Checked 1 of 1 providers; google_hotels answered only in part — results may be incomplete."; got != want {
		t.Errorf("note:\n got %q\nwant %q", got, want)
	}

	mixed := ComputeCompleteness([]ProviderStatus{
		partial,
		statusFrom("trivago", errors.New("trivago: HTTP 403: forbidden")),
	})
	if got, want := mixed.IncompleteNote(), "Checked 1 of 2 providers; trivago refused the request; google_hotels answered only in part — results may be incomplete."; got != want {
		t.Errorf("mixed note:\n got %q\nwant %q", got, want)
	}
	if mixed.State != CompletenessPartial {
		t.Errorf("mixed state = %q, want partial", mixed.State)
	}
}

// The same single provider with nothing returned still blocks the search: the
// partial rule moves only a search that got results.
func TestComputeCompleteness_SingleProviderBlockedOnlyWithoutResults(t *testing.T) {
	for _, tc := range []struct {
		results   int
		wantState string
		wantNote  string
	}{
		{0, CompletenessBlocked, "Checked 0 of 1 providers; google_hotels rate-limited the request — results may be incomplete."},
		{40, CompletenessPartial, "Checked 1 of 1 providers; google_hotels answered only in part — results may be incomplete."},
	} {
		c := ComputeCompleteness([]ProviderStatus{{ID: "google_hotels", Status: StatusRateLimited, Results: tc.results, Error: "HTTP 429"}})
		if c.State != tc.wantState || c.MayClaimExhaustive() {
			t.Errorf("results %d: state %q (exhaustive %v), want %q and not exhaustive", tc.results, c.State, c.MayClaimExhaustive(), tc.wantState)
		}
		if got := c.IncompleteNote(); got != tc.wantNote {
			t.Errorf("results %d: note\n got %q\nwant %q", tc.results, got, tc.wantNote)
		}
	}
}

// Every missing-type status that still returned results is a partial answer
// with the same completeness JSON; without results it blocks (#832 review).
func TestComputeCompleteness_PartialAcrossStatuses(t *testing.T) {
	for _, status := range []string{StatusTimeout, StatusFailed, StatusError, StatusRateLimited} {
		for _, tc := range []struct {
			results int
			want    string
		}{
			{40, `{"state":"partial","queried":1,"succeeded":0,"missing":["google_hotels"]}`},
			{0, `{"state":"blocked","queried":1,"succeeded":0,"missing":["google_hotels"]}`},
		} {
			raw, err := json.Marshal(ComputeCompleteness([]ProviderStatus{{ID: "google_hotels", Status: status, Results: tc.results, Error: "page 2: HTTP 503"}}))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Errorf("%s, %d results: %s, want %s", status, tc.results, raw, tc.want)
			}
		}
	}
}
