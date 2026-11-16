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
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ÖBB HAFAS timetable (fahrplan.oebb.at): the trains-only fallback SearchOebb
// uses when the shop refuses or fails.

// oebbMgateEndpoint is ÖBB's public HAFAS timetable API (fahrplan.oebb.at).
const oebbMgateEndpoint = "https://fahrplan.oebb.at/bin/mgate.exe"

// oebbRailClasses are the HAFAS product classes that are trains: RJ/RJX,
// IC/EC, D, RGJ, R/REX/CJX, S-Bahn and 4096 (RGJ again). Buses (64, 1024),
// ferries, U-Bahn (256) and trams (512) are left out so the timetable answers
// the shop's trains-only question (classes checked live on 2026-11-15).
const oebbRailClasses = 1 | 2 | 4 | 8 | 16 | 32 | 4096

// oebbProductFilter renders a class mask as the HAFAS BIT filter, where
// position i selects product class 2^i.
func oebbProductFilter(mask int) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = '0'
		if mask&(1<<i) != 0 {
			b[i] = '1'
		}
	}
	return string(b)
}

// oebbTripSearchRequest builds the HAFAS mgate JSON envelope for a rail-only
// trip search. HAFAS returns no fares for ÖBB, so none are requested.
func oebbTripSearchRequest(fromExtID, toExtID, dateStr, timeStr string) map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"aid":  "OWDL4fE4ixNiPBBm",
			"type": "AID",
		},
		"client": map[string]any{
			"id":   "OEBB",
			"name": "OEBB",
			"os":   "Windows NT 10.0",
			"type": "WEB",
			"ua":   "Mozilla/5.0",
			"v":    100,
		},
		"ext":       "OEBB.1",
		"formatted": false,
		"lang":      "en",
		"svcReqL": []map[string]any{
			{
				"cfg":  map[string]any{"polyEnc": "GPA"},
				"meth": "TripSearch",
				"req": map[string]any{
					"arrLocL": []map[string]any{
						{"extId": toExtID, "type": "S"},
					},
					"depLocL": []map[string]any{
						{"extId": fromExtID, "type": "S"},
					},
					"extChgTime":  -1,
					"getPasslist": false,
					"getPolyline": false,
					"jnyFltrL": []map[string]any{
						{"mode": "BIT", "type": "PROD", "value": oebbProductFilter(oebbRailClasses)},
					},
					"numF":    5,
					"outDate": dateStr,
					"outTime": timeStr,
					"outFrwd": true,
				},
			},
		},
		"ver": "1.45",
	}
}

type oebbTripRes struct {
	Common  oebbCommon `json:"common"`
	OutConL []oebbCon  `json:"outConL"`
}

type oebbCommon struct {
	LocL  []oebbLoc  `json:"locL"`
	OpL   []oebbOp   `json:"opL"`
	ProdL []oebbProd `json:"prodL"`
}

type oebbLoc struct {
	Name  string `json:"name"`
	ExtID string `json:"extId,omitempty"`
}

type oebbOp struct {
	Name string `json:"name"`
}

type oebbProd struct {
	Name  string `json:"name"`
	OpIdx int    `json:"oprX,omitempty"`
	Cls   int    `json:"cls,omitempty"` // product class bit (see oebbRailClasses)
}

type oebbCon struct {
	Dep    oebbConStop `json:"dep"`
	Arr    oebbConStop `json:"arr"`
	SecL   []oebbSec   `json:"secL"`
	TrfRes *oebbTrfRes `json:"trfRes,omitempty"`
	CHG    int         `json:"chg"`  // number of changes
	Dur    string      `json:"dur"`  // "HHMMSS" format (e.g. "041300" = 4h 13m)
	Date   string      `json:"date"` // connection date "YYYYMMDD" (e.g. "20260410")
}

type oebbConStop struct {
	// dTimeS/aTimeS = scheduled time (HHMMSS string), no date — use con.Date
	DTimeS string `json:"dTimeS,omitempty"`
	ATimeS string `json:"aTimeS,omitempty"`
	LocX   int    `json:"locX"`
}

type oebbSec struct {
	Type string      `json:"type"` // "JNY", "WALK"
	Dep  oebbConStop `json:"dep"`
	Arr  oebbConStop `json:"arr"`
	JnyL *oebbJny    `json:"jny,omitempty"`
}

type oebbJny struct {
	ProdX int `json:"prodX"`
}

// oebbTrfRes is HAFAS's tariff block. ÖBB sends no fares in it, only a shop
// deep link for some connections.
type oebbTrfRes struct {
	Clickout string `json:"clickout"`
}

// oebbMgateResponse is the mgate envelope. HAFAS reports failures with HTTP
// 200 and a non-"OK" svcResL[0].err.
type oebbMgateResponse struct {
	SvcResL []struct {
		Err    string      `json:"err"`
		ErrTxt string      `json:"errTxt"`
		Res    oebbTripRes `json:"res"`
	} `json:"svcResL"`
}

// searchOebbTimetable asks ÖBB's HAFAS timetable for the same trains-only
// departures the shop would list. HAFAS has no fares, so every route is
// unpriced; it answers errors with HTTP 200, so svcResL[0].err is checked.
func searchOebbTimetable(ctx context.Context, fromStation, toStation oebbStation, dt time.Time, date, currency string) ([]models.GroundRoute, error) {
	body, err := json.Marshal(oebbTripSearchRequest(fromStation.ExtID, toStation.ExtID, dt.Format("20060102"), "080000"))
	if err != nil {
		return nil, fmt.Errorf("oebb timetable marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oebbMgateEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := oebbClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oebb timetable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return nil, httpStatusError("oebb", "timetable", resp.StatusCode)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("oebb timetable read: %w", err)
	}

	var env oebbMgateResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("oebb timetable decode: %v: %w", err, models.ErrParseFailed)
	}
	if len(env.SvcResL) == 0 {
		return nil, fmt.Errorf("oebb timetable: empty svcResL: %w", models.ErrParseFailed)
	}
	svc := env.SvcResL[0]
	switch svc.Err {
	case "OK":
	case "H890": // HAFAS: no connections found for this search
		return nil, nil
	default:
		return nil, fmt.Errorf("oebb timetable: HAFAS %s: %s", svc.Err, svc.ErrTxt)
	}
	routes := parseOebbConnections(svc.Res, fromStation, toStation, date, currency)
	slog.Debug("oebb timetable results", "routes", len(routes))
	return routes, nil
}

// parseOebbConnections converts HAFAS connections to unpriced GroundRoutes
// (HAFAS has no ÖBB fares). A connection with any non-rail leg (bus, U-Bahn,
// tram) is dropped: the result answers the shop's trains-only question.
func parseOebbConnections(res oebbTripRes, fromStation, toStation oebbStation, searchDate, currency string) []models.GroundRoute {
	var routes []models.GroundRoute

	for _, con := range res.OutConL {
		if !oebbAllRail(con, res.Common.ProdL) {
			continue
		}
		// Parse departure and arrival times.
		// HAFAS puts the date on the connection (con.Date = "YYYYMMDD"),
		// not on the individual stop. Arrival may be next-day when time wraps past 2359.
		depTime := oebbParseDateTime(con.Date, con.Dep.DTimeS)
		arrTime := oebbParseDateTime(con.Date, con.Arr.ATimeS)

		// Duration from "dHHMMSS" field, fallback to computed.
		duration := oebbParseDuration(con.Dur)
		if duration == 0 {
			duration = computeDurationMinutes(depTime, arrTime)
		}

		bookingURL := buildOebbBookingURL(fromStation, toStation, searchDate)
		if con.TrfRes != nil && strings.HasPrefix(con.TrfRes.Clickout, oebbShopBase+"/") {
			bookingURL = con.TrfRes.Clickout // shop deep link for this connection
		}

		// Count JNY (journey) sections to determine transfers.
		jnySections := 0
		for _, sec := range con.SecL {
			if sec.Type == "JNY" {
				jnySections++
			}
		}
		transfers := jnySections - 1
		if transfers < 0 {
			transfers = 0
		}

		// Build legs.
		var legs []models.GroundLeg
		for _, sec := range con.SecL {
			if sec.Type != "JNY" {
				continue
			}
			legDep := oebbParseDateTime(con.Date, sec.Dep.DTimeS)
			legArr := oebbParseDateTime(con.Date, sec.Arr.ATimeS)

			legProvider := ""
			if sec.JnyL != nil && sec.JnyL.ProdX >= 0 && sec.JnyL.ProdX < len(res.Common.ProdL) {
				legProvider = res.Common.ProdL[sec.JnyL.ProdX].Name
			}

			depName := fromStation.City
			if sec.Dep.LocX >= 0 && sec.Dep.LocX < len(res.Common.LocL) {
				depName = res.Common.LocL[sec.Dep.LocX].Name
			}
			arrName := toStation.City
			if sec.Arr.LocX >= 0 && sec.Arr.LocX < len(res.Common.LocL) {
				arrName = res.Common.LocL[sec.Arr.LocX].Name
			}

			legs = append(legs, models.GroundLeg{
				Type:     "train",
				Provider: legProvider,
				Departure: models.GroundStop{
					City: depName,
					Time: legDep,
				},
				Arrival: models.GroundStop{
					City: arrName,
					Time: legArr,
				},
				Duration: computeDurationMinutes(legDep, legArr),
			})
		}

		routes = append(routes, models.GroundRoute{
			Provider:        "oebb",
			Type:            "train",
			PriceProvenance: models.PriceUnavailable,
			Currency:        strings.ToUpper(currency),
			Duration:        duration,
			Departure: models.GroundStop{
				City:    fromStation.City,
				Station: fromStation.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toStation.City,
				Station: toStation.Name,
				Time:    arrTime,
			},
			Transfers:  transfers,
			Legs:       legs,
			BookingURL: bookingURL,
		})
	}

	return routes
}

// oebbAllRail reports whether con has at least one vehicle leg and every
// vehicle leg is a train. A leg whose product is missing or unclassified
// counts as not rail; a walk-only connection is not a train.
func oebbAllRail(con oebbCon, prods []oebbProd) bool {
	trains := 0
	for _, sec := range con.SecL {
		if sec.Type != "JNY" {
			continue
		}
		if sec.JnyL == nil || sec.JnyL.ProdX < 0 || sec.JnyL.ProdX >= len(prods) ||
			prods[sec.JnyL.ProdX].Cls&oebbRailClasses == 0 {
			return false
		}
		trains++
	}
	return trains > 0
}

// oebbParseDateTime converts HAFAS date (YYYYMMDD) + time (HHMMSS) to ISO 8601.
// HAFAS puts the date on the connection, not the stop. Stops only carry HHMMSS.
// Time may be ≥240000 when the journey crosses midnight (e.g. "250000" = 01:00 next day).
func oebbParseDateTime(dateS, timeS string) string {
	if dateS == "" || timeS == "" {
		return ""
	}
	// Pad time to 6 digits in case leading zeros were dropped.
	for len(timeS) < 6 {
		timeS = "0" + timeS
	}

	// Handle day-overflow: HAFAS encodes next-day arrivals as hour ≥ 24.
	extraDays := 0
	hh := 0
	if len(timeS) >= 2 {
		if _, err := fmt.Sscanf(timeS[:2], "%d", &hh); err == nil && hh >= 24 {
			extraDays = hh / 24
			hh = hh % 24
			timeS = fmt.Sprintf("%02d%s", hh, timeS[2:])
		}
	}

	t, err := time.Parse("20060102150405", dateS+timeS)
	if err != nil {
		return ""
	}
	if extraDays > 0 {
		t = t.Add(time.Duration(extraDays) * 24 * time.Hour)
	}
	return t.Format("2006-01-02T15:04:05")
}

// oebbParseDuration parses the HAFAS duration string to minutes.
// HAFAS returns "HHMMSS" (6 chars, e.g. "041300" = 4h 13m 0s = 253 min)
// or occasionally "DHHMMSS" (7 chars with a leading day digit).
func oebbParseDuration(dur string) int {
	switch len(dur) {
	case 6:
		// "HHMMSS"
		hh, mm := 0, 0
		if _, err := fmt.Sscanf(dur[0:2], "%d", &hh); err != nil {
			return 0
		}
		if _, err := fmt.Sscanf(dur[2:4], "%d", &mm); err != nil {
			return 0
		}
		return hh*60 + mm
	case 7:
		// "DHHMMSS" where D is days digit
		days := int(dur[0] - '0')
		hh, mm := 0, 0
		if _, err := fmt.Sscanf(dur[1:3], "%d", &hh); err != nil {
			return 0
		}
		if _, err := fmt.Sscanf(dur[3:5], "%d", &mm); err != nil {
			return 0
		}
		return days*24*60 + hh*60 + mm
	default:
		return 0
	}
}
