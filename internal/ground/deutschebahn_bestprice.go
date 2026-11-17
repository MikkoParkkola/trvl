package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// dbBestPriceResponse represents the response from the tagesbestpreis endpoint.
type dbBestPriceResponse struct {
	TagesbestPreisIntervalle []dbBestPriceInterval `json:"tagesbestPreisIntervalle"`
}

type dbBestPriceInterval struct {
	AngebotsPreis *dbPreis `json:"angebotsPreis,omitempty"`
}

// dbErrorExcerptBytes bounds how much of DB's error answer a warning quotes.
const dbErrorExcerptBytes = 200

// fetchDBBestPrice queries the DB tagesbestpreis endpoint for cross-border routes
// that return no price from the standard fahrplan search. It returns the day's
// best (price, currency); (0, "", nil) when DB answers cleanly with no fare on
// any interval (it sells nothing on that day); otherwise the failure.
//
// One request body: the fahrplan-shaped one. A flat second body, tried until
// v1.28.1, was refused with 400 VALIDIERUNG on every pair tried live on
// 2026-11-17, including Berlin -> München, which this body prices.
func fetchDBBestPrice(ctx context.Context, fromEVA, toEVA, date string) (float64, string, error) {
	// reiseDatum must be a full datetime; use 06:00 departure.
	body, err := json.Marshal(map[string]any{
		"autonomeReservierung":              false,
		"klasse":                            "KLASSE_2",
		"reservierungsKontingenteVorhanden": false,
		"fahrverguenstigungen": map[string]any{
			"deutschlandTicketVorhanden":       false,
			"nurDeutschlandTicketVerbindungen": false,
		},
		"reisendenProfil": map[string]any{
			"reisende": []map[string]any{
				{
					"ermaessigungen": []string{"KEINE_ERMAESSIGUNG KLASSENLOS"},
					"reisendenTyp":   "ERWACHSENER",
				},
			},
		},
		"reiseHin": map[string]any{
			"wunsch": map[string]any{
				"abgangsLocationId": fmt.Sprintf("A=1@L=%s@", fromEVA),
				"zielLocationId":    fmt.Sprintf("A=1@L=%s@", toEVA),
				"verkehrsmittel":    []string{"ALL"},
				"zeitWunsch": map[string]any{
					"reiseDatum":   date + "T06:00:00",
					"zeitPunktArt": "ABFAHRT",
				},
			},
		},
	})
	if err != nil {
		return 0, "", fmt.Errorf("db tagesbestpreis marshal: %w", err)
	}

	contentType := "application/x.db.vendo.mob.verbindungssuche.v9+json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dbBestPriceEndpoint, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("db tagesbestpreis: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", contentType)
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36")
	req.Header.Set("X-Correlation-ID", generateCorrelationID())

	resp, err := dbClient.Do(req)
	if err != nil {
		slog.Warn("db tagesbestpreis request failed", "err", logredact.Err(err))
		return 0, "", fmt.Errorf("db tagesbestpreis: %w", err)
	}
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// DB's endpoint is fixed, not caller-chosen, so its error text may be
		// logged; the excerpt is bounded and scrubbed of URLs and secrets.
		excerpt := respBody[:min(len(respBody), dbErrorExcerptBytes)]
		slog.Warn("db tagesbestpreis non-200", "status", resp.StatusCode, "body", logredact.Text(string(excerpt)))
		if bytes.Contains(respBody, []byte("OPS_BLOCKED")) {
			return 0, "", fmt.Errorf("db tagesbestpreis: HTTP %d OPS_BLOCKED: %w", resp.StatusCode, models.ErrRateLimited)
		}
		return 0, "", httpStatusError("db", "tagesbestpreis", resp.StatusCode)
	}
	if readErr != nil {
		return 0, "", fmt.Errorf("db tagesbestpreis read: %w", readErr)
	}

	var bpResp dbBestPriceResponse
	if err := json.Unmarshal(respBody, &bpResp); err != nil {
		slog.Warn("db tagesbestpreis decode failed", "err", logredact.Err(err))
		return 0, "", fmt.Errorf("db tagesbestpreis decode: %v: %w", err, models.ErrParseFailed)
	}
	// A 200 without the intervals field (an in-body error, a renamed field)
	// is not a clean "nothing on sale".
	if bpResp.TagesbestPreisIntervalle == nil {
		slog.Warn("db tagesbestpreis answer without intervals")
		return 0, "", fmt.Errorf("db tagesbestpreis: no tagesbestPreisIntervalle in the answer: %w", models.ErrParseFailed)
	}

	// Find cheapest price across all intervals.
	var bestPrice float64
	var bestCur string
	for _, itv := range bpResp.TagesbestPreisIntervalle {
		if itv.AngebotsPreis != nil && itv.AngebotsPreis.Betrag > 0 {
			if bestPrice == 0 || itv.AngebotsPreis.Betrag < bestPrice {
				bestPrice = itv.AngebotsPreis.Betrag
				bestCur = strings.ToUpper(itv.AngebotsPreis.Waehrung)
			}
		}
	}
	return bestPrice, bestCur, nil
}
