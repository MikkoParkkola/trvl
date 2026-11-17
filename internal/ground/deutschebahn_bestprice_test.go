package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// dbDayBestAcceptedBody is the day-best request DB answered with HTTP 200 on
// 2026-11-17 (Praha hl.n. -> Wien Hbf, 2026-11-20). The flat second body the
// code also sent was refused with 400 VALIDIERUNG on that pair and on Berlin
// -> München, a pair DB prices.
const dbDayBestAcceptedBody = `{"autonomeReservierung":false,"fahrverguenstigungen":{"deutschlandTicketVorhanden":false,"nurDeutschlandTicketVerbindungen":false},"klasse":"KLASSE_2","reiseHin":{"wunsch":{"abgangsLocationId":"A=1@L=5400014@","verkehrsmittel":["ALL"],"zeitWunsch":{"reiseDatum":"2026-11-20T06:00:00","zeitPunktArt":"ABFAHRT"},"zielLocationId":"A=1@L=8103000@"}},"reisendenProfil":{"reisende":[{"ermaessigungen":["KEINE_ERMAESSIGUNG KLASSENLOS"],"reisendenTyp":"ERWACHSENER"}]},"reservierungsKontingenteVorhanden":false}`

// dbDayBestReplay answers the day-best endpoint as DB did on 2026-11-17: a
// body with reiseHin gets the recorded 200 (no fare on any interval), any
// other body the recorded 400. It returns every request body it saw.
func dbDayBestReplay(t *testing.T) (http.HandlerFunc, func() [][]byte) {
	t.Helper()
	answer := loadGroundFixture(t, "db_tagesbestpreis_praha_8103000_2026-11-17.json")
	var mu sync.Mutex
	var seen [][]byte
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, body)
		mu.Unlock()
		if bytes.Contains(body, []byte(`"reiseHin"`)) {
			_, _ = w.Write([]byte(answer))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"domain":"MOB","code":"VALIDIERUNG","status":"ERROR"}`))
	}
	return handler, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), seen...)
	}
}

func useDBDayBestServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	origClient := dbClient
	t.Cleanup(func() {
		srv.Close()
		dbClient = origClient
	})
	dbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
}

// TestFetchDBBestPrice_PragueViennaReplay: DB's clean "no fare on this route"
// is a clean answer after one request, and that request is the body DB
// accepts, field for field.
func TestFetchDBBestPrice_PragueViennaReplay(t *testing.T) {
	handler, seen := dbDayBestReplay(t)
	useDBDayBestServer(t, handler)

	price, currency, err := fetchDBBestPrice(context.Background(), "5400014", "8103000", "2026-11-20")
	if price != 0 || currency != "" || err != nil {
		t.Fatalf("fetchDBBestPrice = %v %q %v, want a clean answer with no fare", price, currency, err)
	}
	bodies := seen()
	if len(bodies) != 1 {
		t.Fatalf("day-best requests = %d, want 1", len(bodies))
	}
	var got, want any
	if err := json.Unmarshal(bodies[0], &got); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(dbDayBestAcceptedBody), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request body = %s\nwant the body DB accepted: %s", bodies[0], dbDayBestAcceptedBody)
	}
}

// TestFetchDBBestPrice_WarnCarriesBoundedRedactedExcerpt: a refused lookup
// logs the start of DB's error text, bounded and with any URL scrubbed.
func TestFetchDBBestPrice_WarnCarriesBoundedRedactedExcerpt(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	errBody := `{"domain":"MOB","code":"VALIDIERUNG","detail":"see https://example.invalid/x?token=SECRET123"}` + strings.Repeat("x", 2048)
	useDBDayBestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(errBody))
	})

	if _, _, err := fetchDBBestPrice(context.Background(), "5400014", "8103000", "2026-11-20"); err == nil {
		t.Fatal("a refused lookup returned no error")
	}
	logged := buf.String()
	if !strings.Contains(logged, "VALIDIERUNG") {
		t.Errorf("warning = %q, want DB's error code in it", logged)
	}
	if strings.Contains(logged, "SECRET123") {
		t.Errorf("warning = %q, leaks the URL's query", logged)
	}
	if strings.Contains(logged, strings.Repeat("x", 300)) {
		t.Errorf("warning carries more than a bounded excerpt (%d bytes logged)", len(logged))
	}
}

// TestFetchDBBestPrice_WarnExcerptIsExactly200Bytes: byte 200 of DB's error
// answer is logged, byte 201 is not.
func TestFetchDBBestPrice_WarnExcerptIsExactly200Bytes(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const last, next = "LASTBYTEMARK", "OVERMARK"
	head := `{"code":"VALIDIERUNG",`
	// Dots, not letters: a long letter run could read as a token to the
	// scrubber and be masked together with the marker.
	errBody := head + strings.Repeat(".", dbErrorExcerptBytes-len(head)-len(last)) + last + next + strings.Repeat(".", 100)
	useDBDayBestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(errBody))
	})

	_, _, _ = fetchDBBestPrice(context.Background(), "5400014", "8103000", "2026-11-20")
	if logged := buf.String(); !strings.Contains(logged, last) || strings.Contains(logged, next) {
		t.Errorf("warning = %q, want the excerpt to end exactly at byte %d", logged, dbErrorExcerptBytes)
	}
}
