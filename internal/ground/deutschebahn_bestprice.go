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

// fetchDBBestPrice queries the DB tagesbestpreis endpoint for cross-border routes
// that return no price from the standard fahrplan search. It returns the day's
// best (price, currency) as soon as one request body finds it; (0, "", nil)
// only when every attempted body answered cleanly with an empty interval list;
// otherwise the last failure.
func fetchDBBestPrice(ctx context.Context, fromEVA, toEVA, date string) (float64, string, error) {
	fromLid := fmt.Sprintf("A=1@L=%s@", fromEVA)
	toLid := fmt.Sprintf("A=1@L=%s@", toEVA)

	// reiseDatum must be a full datetime; use 06:00 departure.
	reiseDatum := date + "T06:00:00"

	// Format A — matches the fahrplan request structure (with required fields).
	reqBodyA := map[string]any{
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
				"abgangsLocationId": fromLid,
				"zielLocationId":    toLid,
				"verkehrsmittel":    []string{"ALL"},
				"zeitWunsch": map[string]any{
					"reiseDatum":   reiseDatum,
					"zeitPunktArt": "ABFAHRT",
				},
			},
		},
	}

	// Format B — simpler flat structure.
	reqBodyB := map[string]any{
		"abgangsLocationId": fromLid,
		"zielLocationId":    toLid,
		"klasse":            "KLASSE_2",
		"reisende": []map[string]any{
			{
				"ermaessigungen": []string{"KEINE_ERMAESSIGUNG KLASSENLOS"},
				"reisendenTyp":   "ERWACHSENER",
			},
		},
		"reiseDatum": date,
	}

	contentType := "application/x.db.vendo.mob.verbindungssuche.v9+json"

	// lastErr keeps the last failed attempt. "No fares" needs every attempt
	// to answer cleanly, so any failure without a price is a failed lookup.
	var lastErr error
	for _, reqBody := range []map[string]any{reqBodyA, reqBodyB} {
		body, err := json.Marshal(reqBody)
		if err != nil {
			lastErr = fmt.Errorf("db tagesbestpreis marshal: %w", err)
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, dbBestPriceEndpoint, bytes.NewReader(body))
		if err != nil {
			lastErr = fmt.Errorf("db tagesbestpreis: %w", err)
			continue
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", contentType)
		req.Header.Set("Accept-Language", "en")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36")
		req.Header.Set("X-Correlation-ID", generateCorrelationID())

		resp, err := dbClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("db tagesbestpreis: %w", err)
			slog.Warn("db tagesbestpreis request failed", "err", logredact.Err(err))
			continue
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			if bytes.Contains(respBody, []byte("OPS_BLOCKED")) {
				lastErr = fmt.Errorf("db tagesbestpreis: HTTP %d OPS_BLOCKED: %w", resp.StatusCode, models.ErrRateLimited)
			} else {
				lastErr = httpStatusError("db", "tagesbestpreis", resp.StatusCode)
			}
			slog.Warn("db tagesbestpreis non-200", "status", resp.StatusCode)
			continue
		}
		if readErr != nil {
			lastErr = fmt.Errorf("db tagesbestpreis read: %w", readErr)
			continue
		}

		var bpResp dbBestPriceResponse
		if err := json.Unmarshal(respBody, &bpResp); err != nil {
			lastErr = fmt.Errorf("db tagesbestpreis decode: %v: %w", err, models.ErrParseFailed)
			slog.Warn("db tagesbestpreis decode failed", "err", logredact.Err(err))
			continue
		}
		// A 200 without the intervals field (an in-body error, a renamed
		// field) is not a clean "nothing on sale".
		if bpResp.TagesbestPreisIntervalle == nil {
			lastErr = fmt.Errorf("db tagesbestpreis: no tagesbestPreisIntervalle in the answer: %w", models.ErrParseFailed)
			slog.Warn("db tagesbestpreis answer without intervals")
			continue
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
		if bestPrice > 0 {
			return bestPrice, bestCur, nil
		}
		// An empty 200 for body A says nothing about body B: try it (one
		// extra request at most) before giving up.
	}

	return 0, "", lastErr
}
