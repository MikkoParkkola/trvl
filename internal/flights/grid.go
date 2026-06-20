package flights

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// GridOptions configures a price grid search.
type GridOptions struct {
	DepartFrom string // Start of departure range (YYYY-MM-DD)
	DepartTo   string // End of departure range (YYYY-MM-DD)
	ReturnFrom string // Start of return range (YYYY-MM-DD)
	ReturnTo   string // End of return range (YYYY-MM-DD)
	Adults     int    // Number of adults (default: 1)
}

// defaults fills in zero-value fields.
func (o *GridOptions) defaults() {
	if o.Adults <= 0 {
		o.Adults = 1
	}
	now := time.Now()
	if o.DepartFrom == "" {
		o.DepartFrom = now.AddDate(0, 0, 1).Format("2006-01-02")
	}
	if o.DepartTo == "" {
		from, err := models.ParseDate(o.DepartFrom)
		if err == nil {
			o.DepartTo = from.AddDate(0, 0, 6).Format("2006-01-02")
		}
	}
	if o.ReturnFrom == "" {
		depTo, err := models.ParseDate(o.DepartTo)
		if err == nil {
			o.ReturnFrom = depTo.AddDate(0, 0, 1).Format("2006-01-02")
		}
	}
	if o.ReturnTo == "" {
		retFrom, err := models.ParseDate(o.ReturnFrom)
		if err == nil {
			o.ReturnTo = retFrom.AddDate(0, 0, 6).Format("2006-01-02")
		}
	}
}

// SearchPriceGrid searches for a 2D price matrix of departure x return dates
// using Google's GetCalendarGrid endpoint.
//
// Like CalendarGraph, this endpoint requires Google city codes rather than
// raw IATA airport codes. The grid is limited to 200 cells maximum.
func SearchPriceGrid(ctx context.Context, origin, dest string, opts GridOptions) (*models.PriceGrid, error) {
	opts.defaults()

	if origin == "" || dest == "" {
		return nil, fmt.Errorf("origin and destination are required")
	}

	client := DefaultClient()

	// Resolve IATA codes to Google city codes.
	srcCode, err := batchexec.ResolveCityCode(ctx, client, origin)
	if err != nil {
		return &models.PriceGrid{
			Error: fmt.Sprintf("could not resolve origin %q to city code: %v", origin, err),
		}, err
	}

	dstCode, err := batchexec.ResolveCityCode(ctx, client, dest)
	if err != nil {
		return &models.PriceGrid{
			Error: fmt.Sprintf("could not resolve destination %q to city code: %v", dest, err),
		}, err
	}

	encoded := encodePriceGridPayload(srcCode, origin, dstCode, dest, opts)

	status, body, err := client.PostCalendarGrid(ctx, encoded)
	if err != nil {
		return &models.PriceGrid{
			Error: fmt.Sprintf("grid request failed: %v", err),
		}, err
	}

	if status == 403 {
		return &models.PriceGrid{Error: "blocked by Google (403)"}, batchexec.ErrBlocked
	}

	if status != 200 {
		return &models.PriceGrid{
			Error: fmt.Sprintf("unexpected status %d", status),
		}, fmt.Errorf("unexpected status %d", status)
	}

	cells, err := parsePriceGridResponse(body)
	if err != nil {
		return &models.PriceGrid{
			Error: fmt.Sprintf("parse grid response: %v", err),
		}, fmt.Errorf("parse grid response: %w", err)
	}
	if len(cells) == 0 {
		// Google returned a parseable but empty grid (no priced cells for the
		// requested ranges — common when the departure×return window is wide).
		// Return a clear, descriptive error rather than fmt.Errorf("...: %w", nil),
		// which renders as the bogus "%!w(<nil>)".
		return &models.PriceGrid{
				Error: fmt.Sprintf("no priced grid cells for departure %s..%s / return %s..%s (try a narrower date range)",
					opts.DepartFrom, opts.DepartTo, opts.ReturnFrom, opts.ReturnTo),
			}, fmt.Errorf("grid: no priced cells for departure %s..%s return %s..%s",
				opts.DepartFrom, opts.DepartTo, opts.ReturnFrom, opts.ReturnTo)
	}

	// Detect the actual API currency and stamp all cells.
	apiCurrency := DetectSourceCurrency(ctx, origin, dest)
	for i := range cells {
		cells[i].Currency = apiCurrency
	}

	// Collect unique departure and return dates.
	depSet := make(map[string]bool)
	retSet := make(map[string]bool)
	for _, c := range cells {
		depSet[c.DepartureDate] = true
		retSet[c.ReturnDate] = true
	}

	depDates := sortedKeys(depSet)
	retDates := sortedKeys(retSet)

	return &models.PriceGrid{
		Success:        true,
		Count:          len(cells),
		DepartureDates: depDates,
		ReturnDates:    retDates,
		Cells:          cells,
	}, nil
}

// sortedKeys returns the sorted keys of a map.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// encodePriceGridPayload builds the f.req body for GetCalendarGrid
// using resolved Google city codes.
//
// Matches gflights' getPriceGridReqData format: segments wrapped in array,
// airport codes with flag 0 before city codes with flag 5.
func encodePriceGridPayload(srcCityCode, srcAirport, dstCityCode, dstAirport string, opts GridOptions) string {
	// Airport code with flag 0, city code with flag 5.
	serSrc := fmt.Sprintf(`[\"%s\",0],[\"%s\",5]`, srcAirport, srcCityCode)
	serDst := fmt.Sprintf(`[\"%s\",0],[\"%s\",5]`, dstAirport, dstCityCode)

	// Grid is always round-trip. rawData opens settings + segments arrays,
	// does NOT close them (suffix handles closing with additional elements).
	rawData := fmt.Sprintf(`[null,null,%d,null,[],%d,[%d,0,0,0],null,null,null,null,null,null,[`,
		1, 1, opts.Adults) // tripType=1 (round-trip), class=1 (economy)

	// Outbound segment
	rawData += fmt.Sprintf(`[[[%s]],[[%s]],null,0,null,null,\"%s\",null,null,null,null,null,null,null,3]`,
		serSrc, serDst, opts.DepartFrom)

	// Return segment
	rawData += fmt.Sprintf(`,[[[%s]],[[%s]],null,0,null,null,\"%s\",null,null,null,null,null,null,null,1]`,
		serDst, serSrc, opts.ReturnFrom)

	// NOTE: rawData left unclosed. Suffix provides:
	//   ] -> close segments, ,null,null,null,1] -> close settings,
	//   ,["depFrom","depTo"],["retFrom","retTo"]] -> date ranges + close outer

	prefix := `[null,"[null,`
	suffix := fmt.Sprintf(`],null,null,null,1],[\"%s\",\"%s\"],[\"%s\",\"%s\"]]"]`,
		opts.DepartFrom, opts.DepartTo, opts.ReturnFrom, opts.ReturnTo)

	return url.QueryEscape(prefix + rawData + suffix)
}

// parsePriceGridResponse parses the response from GetCalendarGrid.
func parsePriceGridResponse(body []byte) ([]models.GridCell, error) {
	stripped := batchexec.StripAntiXSSI(body)
	if len(stripped) == 0 {
		return nil, batchexec.ErrEmptyResponse
	}

	if len(stripped) < 200 {
		return nil, fmt.Errorf("response too small (%d bytes), likely error code", len(stripped))
	}

	var cells []models.GridCell

	// Parse length-prefixed response lines.
	text := string(stripped)
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}

		var outer [][]any
		if err := json.Unmarshal([]byte(line), &outer); err != nil {
			continue
		}

		for _, entry := range outer {
			if len(entry) < 3 {
				continue
			}
			innerStr, ok := entry[2].(string)
			if !ok || len(innerStr) < 10 {
				continue
			}

			parsed := parseGridPriceData([]byte(innerStr))
			cells = append(cells, parsed...)
		}
	}

	// Also try batch decode.
	entries, err := batchexec.DecodeBatchResponse(body)
	if err == nil {
		for _, entry := range entries {
			entryArr, ok := entry.([]any)
			if !ok {
				continue
			}
			for _, elem := range entryArr {
				s, ok := elem.(string)
				if !ok || len(s) < 10 {
					continue
				}
				parsed := parseGridPriceData([]byte(s))
				cells = append(cells, parsed...)
			}
		}
	}

	// Deduplicate.
	seen := make(map[string]bool)
	var unique []models.GridCell
	for _, c := range cells {
		key := c.DepartureDate + "|" + c.ReturnDate
		if !seen[key] {
			seen[key] = true
			unique = append(unique, c)
		}
	}

	return unique, nil
}

// parseGridPriceData extracts price cells from a decoded grid section.
// Uses the same format as calendar: [null, [[depDate, retDate, [[null, price], ""], 1], ...]]
func parseGridPriceData(data []byte) []models.GridCell {
	var cells []models.GridCell

	// Try format: [null, [offer1, offer2, ...]]
	var section []json.RawMessage
	if err := json.Unmarshal(data, &[]any{nil, &section}); err == nil {
		for _, raw := range section {
			if cell := parseGridOffer(raw); cell != nil {
				cells = append(cells, *cell)
			}
		}
		return cells
	}

	return cells
}

// parseGridOffer parses a single grid offer entry.
func parseGridOffer(raw json.RawMessage) *models.GridCell {
	var depDate, retDate string
	var price float64

	err := json.Unmarshal(raw, &[]any{&depDate, &retDate, &[]any{&[]any{nil, &price}}})
	if err != nil || price <= 0 {
		return nil
	}

	if _, err := models.ParseDate(depDate); err != nil {
		return nil
	}
	if _, err := models.ParseDate(retDate); err != nil {
		return nil
	}

	return &models.GridCell{
		DepartureDate: depDate,
		ReturnDate:    retDate,
		Price:         price,
		Currency:      "", // Filled by caller via DetectSourceCurrency
	}
}
