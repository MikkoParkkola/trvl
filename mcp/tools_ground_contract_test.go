package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// groundCall is one serialized search_ground tools/call result, split into
// the copies a client sees.
type groundCall struct {
	size       int // bytes of the whole tools/call result
	userText   string
	assistant  string
	structured map[string]any
	isError    bool
	errText    string
}

func callSearchGround(t *testing.T, args map[string]any) groundCall {
	t.Helper()
	resp := NewServer().HandleRequest(jsonRequest(t, "tools/call", 1, map[string]any{"name": "search_ground", "arguments": args}))
	if resp == nil {
		t.Fatal("nil response")
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Content           []ContentBlock `json:"content"`
		StructuredContent map[string]any `json:"structuredContent"`
		IsError           bool           `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	out := groundCall{size: len(raw), structured: res.StructuredContent, isError: res.IsError}
	for _, c := range res.Content {
		switch {
		case c.Annotations != nil && slices.Contains(c.Annotations.Audience, "assistant"):
			out.assistant = c.Text
		case c.Annotations != nil && slices.Contains(c.Annotations.Audience, "user"):
			out.userText = c.Text
		case res.IsError:
			out.errText += c.Text
		}
	}
	return out
}

// stubGroundRoutes makes search_ground return routes without any network.
// Each call gets a fresh copy because the handler enriches routes in place.
func stubGroundRoutes(t *testing.T, routes []models.GroundRoute) {
	t.Helper()
	orig := searchGroundByNameFunc
	t.Cleanup(func() { searchGroundByNameFunc = orig })
	searchGroundByNameFunc = func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
		cp := slices.Clone(routes)
		return &models.GroundSearchResult{
			Success: true,
			Count:   len(cp),
			Routes:  cp,
			ProviderStatuses: []models.ProviderStatus{
				{ID: "flixbus", Name: "FlixBus", Status: "ok", Results: len(cp)},
			},
		}, nil
	}
}

// stubDestination installs a populated destination so the budget counts the
// enrichment a real result carries in both copies.
func stubDestination(t *testing.T) {
	t.Helper()
	prev := destinationEnricher
	t.Cleanup(func() { destinationEnricher = prev })
	destinationEnricher = func(context.Context, string, models.DateRange) (*models.DestinationInfo, error) {
		return &models.DestinationInfo{
			Location: "Vienna",
			Country:  models.CountryInfo{Name: "Austria", Code: "AT", Capital: "Vienna", Languages: []string{"German"}, Currencies: []string{"EUR"}, Region: "Europe"},
			Weather: models.WeatherInfo{
				Current: models.WeatherDay{Date: "2027-03-12", TempHigh: 11.2, TempLow: 2.4, Precipitation: 0.8, Description: "Partly cloudy"},
				Forecast: []models.WeatherDay{
					{Date: "2027-03-12", TempHigh: 11.2, TempLow: 2.4, Precipitation: 0.8, Description: "Partly cloudy"},
					{Date: "2027-03-13", TempHigh: 9.8, TempLow: 3.1, Precipitation: 4.2, Description: "Light rain"},
					{Date: "2027-03-14", TempHigh: 12.5, TempLow: 1.9, Precipitation: 0, Description: "Clear sky"},
				},
			},
			Holidays: []models.Holiday{{Date: "2027-03-29", Name: "Easter Monday", Type: "public"}},
			Safety:   models.SafetyInfo{Level: 1.2, Advisory: "Exercise normal caution", Source: "travel-advisory.info", LastUpdated: "2027-03-01"},
			Currency: models.CurrencyInfo{LocalCurrency: "EUR", ExchangeRate: 1, BaseCurrency: "EUR"},
			Timezone: "Europe/Vienna",
		}, nil
	}
}

// groundContractFixture builds n Prague to Vienna routes shaped like a merged
// ground search: every route carries its single source, a confidence and
// legs, as ResolveGroundSources and the providers emit them. No ferries, so
// the sea-state lookup never runs.
func groundContractFixture(n int) []models.GroundRoute {
	providers := []string{"flixbus", "regiojet", "db", "trainline", "oebb"}
	routes := make([]models.GroundRoute, n)
	for i := range routes {
		p := providers[i%len(providers)]
		mode := "train"
		if p == "flixbus" || p == "regiojet" && i%2 == 0 {
			mode = "bus"
		}
		price, prov := 19.0+float64(i), models.PriceLive
		switch {
		case i%11 == 5:
			prov = models.PriceEstimated
		case i%13 == 7:
			price, prov = 0, models.PriceUnavailable
		}
		dep := fmt.Sprintf("2027-03-12T%02d:%02d:00+01:00", 5+i%18, (i*7)%60)
		arr := fmt.Sprintf("2027-03-12T%02d:%02d:00+01:00", 9+i%14, (i*11)%60)
		url := fmt.Sprintf("https://shop.%s.example/search?departureCity=40de8964-8646-11e6-9066-549f350fcb0c&arrivalCity=40de6527-8646-11e6-9066-549f350fcb0c&rideDate=12.03.2027&adult=1&_locale=en&currency=EUR&journey=%d", p, i)
		r := models.GroundRoute{
			Provider: p, Type: mode, Price: price, Currency: "EUR", PriceProvenance: prov, ComparablePrice: price,
			Duration:  245 + (i%5)*15,
			Departure: models.GroundStop{City: "Prague", Station: "Praha hlavní nádraží", Time: dep},
			Arrival:   models.GroundStop{City: "Vienna", Station: "Wien Hauptbahnhof", Time: arr},
			Legs: []models.GroundLeg{{
				Type: mode, Provider: p, Duration: 245 + (i%5)*15,
				Departure: models.GroundStop{City: "Prague", Station: "Praha hlavní nádraží", Time: dep},
				Arrival:   models.GroundStop{City: "Vienna", Station: "Wien Hauptbahnhof", Time: arr},
				Amenities: []string{"wifi", "power_outlets"},
			}},
			Amenities:      []string{"wifi", "power_outlets", "air_conditioning"},
			BookingURL:     url,
			CheapestSource: p,
			Sources: []models.PriceSource{{
				Provider: p, Price: price, Currency: "EUR", Provenance: prov, ComparablePrice: price,
				BookingURL: url, Freshness: "live",
			}},
			Confidence: &models.Confidence{
				Rated: true, Score: 0.82, Label: "high", Freshness: "live",
				Basis: "live API price from " + p, Signals: []string{"live_api", "single_source"},
			},
		}
		if i%3 == 0 {
			r.Transfers = 1
			r.Legs = []models.GroundLeg{
				{Type: mode, Provider: p, Duration: 120,
					Departure: models.GroundStop{City: "Prague", Station: "Praha hlavní nádraží", Time: dep},
					Arrival:   models.GroundStop{City: "Brno", Station: "Brno hlavní nádraží", Time: dep}},
				{Type: mode, Provider: p, Duration: 110,
					Departure: models.GroundStop{City: "Brno", Station: "Brno hlavní nádraží", Time: dep},
					Arrival:   models.GroundStop{City: "Vienna", Station: "Wien Hauptbahnhof", Time: arr}},
			}
		}
		routes[i] = r
	}
	return routes
}

func structuredRoutes(t *testing.T, c groundCall) []map[string]any {
	t.Helper()
	raw, ok := c.structured["routes"].([]any)
	if !ok {
		t.Fatalf("structured routes missing: %v", c.structured["routes"])
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var groundContractArgs = map[string]any{"from": "Prague", "to": "Vienna", "date": "2027-03-12"}

func withArgs(extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range groundContractArgs {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// TestSearchGroundOutputSchemaDeclaresProvenance pins the E2 schema: routes
// and their sources classify the price, and total_count sits next to count.
func TestSearchGroundOutputSchemaDeclaresProvenance(t *testing.T) {
	schema := groundSearchOutputSchema().(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["total_count"]; !ok {
		t.Error("output schema lacks total_count")
	}
	route := props["routes"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
	assertProvenanceEnum(t, "routes[].price_provenance", route["price_provenance"])
	sources, ok := route["sources"].(map[string]interface{})
	if !ok {
		t.Fatal("routes[] schema lacks sources")
	}
	src := sources["items"].(map[string]interface{})["properties"].(map[string]interface{})
	assertProvenanceEnum(t, "routes[].sources[].price_provenance", src["price_provenance"])
}

func assertProvenanceEnum(t *testing.T, where string, field any) {
	t.Helper()
	f, ok := field.(map[string]interface{})
	if !ok {
		t.Errorf("%s missing from schema", where)
		return
	}
	var got []string
	switch e := f["enum"].(type) {
	case []string:
		got = e
	case []interface{}:
		for _, v := range e {
			got = append(got, fmt.Sprint(v))
		}
	}
	for _, want := range []string{"live", "estimated", "unavailable"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s enum = %v, want it to include %q", where, got, want)
		}
	}
}

// TestSearchGroundSummaryLabelsPriceProvenance pins the text copy to the CLI
// table: a live fare is plain, an estimate reads "~€22 (est.)" and a route
// whose price is unavailable never shows a number.
func TestSearchGroundSummaryLabelsPriceProvenance(t *testing.T) {
	stubGroundRoutes(t, []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 25, Currency: "EUR", PriceProvenance: models.PriceLive, Duration: 240},
		{Provider: "regiojet", Type: "train", Price: 19, PriceMax: 29, Currency: "EUR", PriceProvenance: models.PriceLive, Duration: 250},
		{Provider: "taxi", Type: "taxi", Price: 22, Currency: "EUR", PriceProvenance: models.PriceEstimated, Duration: 30},
		{Provider: "snalltaget", Type: "train", Price: 120, Currency: "SEK", PriceProvenance: models.PriceEstimated, Duration: 300},
		{Provider: "db", Type: "train", Price: 30, Currency: "EUR", PriceProvenance: models.PriceUnavailable, Duration: 260},
	})
	got := callSearchGround(t, groundContractArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	line := func(provider string) string {
		for _, l := range strings.Split(got.userText, "\n") {
			if strings.Contains(l, " "+provider+" ") {
				return l
			}
		}
		t.Fatalf("no summary line for %s in:\n%s", provider, got.userText)
		return ""
	}
	cases := []struct {
		provider, want, notWant string
	}{
		{"flixbus", "EUR 25.00", "(est.)"},
		{"regiojet", "EUR 19.00-29.00", "(est.)"},
		{"taxi", "~€22 (est.)", "EUR 22.00"},
		{"snalltaget", "~SEK 120 (est.)", "SEK 120.00"},
		{"db", "price unavailable", "30"},
	}
	for _, tc := range cases {
		l := line(tc.provider)
		if !strings.Contains(l, tc.want) {
			t.Errorf("%s line = %q, want %q", tc.provider, l, tc.want)
		}
		if strings.Contains(l, tc.notWant) {
			t.Errorf("%s line = %q, must not contain %q", tc.provider, l, tc.notWant)
		}
	}
}

func routeURLs(routes []map[string]any) []string {
	out := make([]string, len(routes))
	for i, r := range routes {
		out[i], _ = r["booking_url"].(string)
	}
	return out
}

// TestSearchGroundDefaultCapsBothCopiesAt20 pins the default result size: the
// first 20 rows in search order reach both copies, count says 20 and
// total_count says how many were found.
func TestSearchGroundDefaultCapsBothCopiesAt20(t *testing.T) {
	fixture := groundContractFixture(53)
	stubGroundRoutes(t, fixture)
	got := callSearchGround(t, groundContractArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	routes := structuredRoutes(t, got)
	if len(routes) != 20 {
		t.Fatalf("structured routes = %d, want 20", len(routes))
	}
	for i, u := range routeURLs(routes) {
		if u != fixture[i].BookingURL {
			t.Errorf("route %d = %s, want fixture row %d in order", i, u, i)
		}
	}
	if c, _ := got.structured["count"].(float64); c != 20 {
		t.Errorf("count = %v, want 20 returned rows", got.structured["count"])
	}
	if tc, _ := got.structured["total_count"].(float64); tc != 53 {
		t.Errorf("total_count = %v, want 53 rows found", got.structured["total_count"])
	}
	if !strings.Contains(got.userText, "\n20. ") || strings.Contains(got.userText, "\n21. ") {
		t.Errorf("text copy should list exactly 20 rows:\n%s", got.userText)
	}
	if !strings.Contains(got.userText, "20 of 53") {
		t.Errorf("text copy should say it shows 20 of 53 routes:\n%s", got.userText)
	}
	assertCompactJSONOf(t, got.assistant, got.structured)
}

// TestSearchGroundCapIsAutomatic pins the operator rule that trvl sizes the
// result itself: there is no limit argument, and the summary says how many
// more routes exist and which filters narrow them.
func TestSearchGroundCapIsAutomatic(t *testing.T) {
	if p, ok := searchGroundTool().InputSchema.Properties["limit"]; ok {
		t.Errorf("input schema declares limit %+v; the cap is automatic", p)
	}
	stubGroundRoutes(t, groundContractFixture(53))
	got := callSearchGround(t, groundContractArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	for _, want := range []string{"20 of 53", "type", "provider", "max_price"} {
		if !strings.Contains(got.userText, want) {
			t.Errorf("summary lacks %q:\n%s", want, got.userText)
		}
	}
}

func roundTripFixture(n int, lean bool) []models.GroundRoute {
	out, in := groundContractFixture(n), groundContractFixture(n)
	for i := range out {
		out[i].Direction, in[i].Direction = "outbound", "inbound"
		in[i].BookingURL += "&inbound"
	}
	routes := append(out, in...)
	if lean {
		for i, r := range routes {
			routes[i] = models.GroundRoute{
				Provider: r.Provider, Type: r.Type, Direction: r.Direction, Price: r.Price,
				Currency: r.Currency, PriceProvenance: r.PriceProvenance, Duration: r.Duration,
				Departure:  models.GroundStop{City: r.Departure.City, Time: r.Departure.Time},
				Arrival:    models.GroundStop{City: r.Arrival.City, Time: r.Arrival.Time},
				BookingURL: fmt.Sprintf("https://b.example/%s/%d", r.Direction, i),
			}
		}
	}
	return routes
}

// TestSearchGroundRoundTripCapKeepsInbound pins the cap per direction: ground
// search appends every outbound row before any inbound row, so a single cap
// would drop the whole return leg. Lean rows fit 20 per direction; rich rows
// overflow the budget and the guard trims both directions evenly.
func TestSearchGroundRoundTripCapKeepsInbound(t *testing.T) {
	args := withArgs(map[string]any{"return_date": "2027-03-15"})
	dirCounts := func(c groundCall) map[string]int {
		dirs := map[string]int{}
		for _, r := range structuredRoutes(t, c) {
			d, _ := r["direction"].(string)
			dirs[d]++
		}
		return dirs
	}

	stubGroundRoutes(t, roundTripFixture(25, true))
	got := callSearchGround(t, args)
	if dirs := dirCounts(got); dirs["outbound"] != 20 || dirs["inbound"] != 20 {
		t.Errorf("lean directions = %v, want 20 outbound and 20 inbound", dirs)
	}
	if total, _ := got.structured["total_count"].(float64); total != 50 {
		t.Errorf("total_count = %v, want 50", got.structured["total_count"])
	}
	for _, want := range []string{"20 of 25 outbound", "20 of 25 inbound"} {
		if !strings.Contains(got.userText, want) {
			t.Errorf("summary lacks %q:\n%s", want, got.userText)
		}
	}

	stubGroundRoutes(t, roundTripFixture(25, false))
	got = callSearchGround(t, args)
	dirs := dirCounts(got)
	if dirs["outbound"] == 0 || dirs["inbound"] == 0 || dirs["outbound"]-dirs["inbound"] > 1 || dirs["inbound"]-dirs["outbound"] > 1 {
		t.Errorf("rich directions = %v, want both kept and within one row of each other", dirs)
	}
	if got.size >= 50000 {
		t.Errorf("rich round trip = %d bytes, want under 50000", got.size)
	}
	for _, want := range []string{"of 25 outbound", "of 25 inbound", "50 KB"} {
		if !strings.Contains(got.userText, want) {
			t.Errorf("rich summary lacks %q:\n%s", want, got.userText)
		}
	}
}

// TestSearchGroundDropsSingleSourceArrays pins sources to routes that more
// than one provider sold; a lone source only repeats the route.
func TestSearchGroundDropsSingleSourceArrays(t *testing.T) {
	routes := groundContractFixture(2)
	routes[1].Sources = append(routes[1].Sources, models.PriceSource{
		Provider: "omio", Price: 27, Currency: "EUR", Provenance: models.PriceEstimated,
	})
	stubGroundRoutes(t, routes)
	got := structuredRoutes(t, callSearchGround(t, groundContractArgs))
	if _, ok := got[0]["sources"]; ok {
		t.Errorf("single-source route kept sources: %v", got[0]["sources"])
	}
	srcs, _ := got[1]["sources"].([]any)
	if len(srcs) != 2 {
		t.Fatalf("two-source route sources = %v, want both", got[1]["sources"])
	}
	for _, s := range srcs {
		if p, _ := s.(map[string]any)["price_provenance"].(string); p == "" {
			t.Errorf("source %v lacks price_provenance", s)
		}
	}
}

// TestSearchGroundDropsLegCityRepeats pins leg stops to new information: a
// city already known from the route or the previous leg is dropped, a
// transfer city and every station stay.
func TestSearchGroundDropsLegCityRepeats(t *testing.T) {
	routes := groundContractFixture(1) // row 0 has a Brno transfer
	stubGroundRoutes(t, routes)
	r := structuredRoutes(t, callSearchGround(t, groundContractArgs))[0]
	legs, _ := r["legs"].([]any)
	if len(legs) != 2 {
		t.Fatalf("legs = %v, want 2", r["legs"])
	}
	stop := func(leg int, end string) map[string]any {
		return legs[leg].(map[string]any)[end].(map[string]any)
	}
	for _, c := range []struct {
		leg      int
		end      string
		wantCity string
	}{
		{0, "departure", ""}, {0, "arrival", "Brno"},
		{1, "departure", ""}, {1, "arrival", ""},
	} {
		s := stop(c.leg, c.end)
		city, _ := s["city"].(string)
		if city != c.wantCity {
			t.Errorf("legs[%d].%s.city = %q, want %q", c.leg, c.end, city, c.wantCity)
		}
		if st, _ := s["station"].(string); st == "" {
			t.Errorf("legs[%d].%s lost its station", c.leg, c.end)
		}
	}
}

// TestSearchGroundConfidenceKeepsLabelScoreBasisSignals pins the compact
// confidence object.
func TestSearchGroundConfidenceKeepsLabelScoreBasisSignals(t *testing.T) {
	stubGroundRoutes(t, groundContractFixture(1))
	r := structuredRoutes(t, callSearchGround(t, groundContractArgs))[0]
	conf, ok := r["confidence"].(map[string]any)
	if !ok {
		t.Fatalf("confidence missing: %v", r["confidence"])
	}
	want := []string{"basis", "label", "score", "signals"}
	if got := sortedKeys(conf); !slices.Equal(got, want) {
		t.Errorf("confidence keys = %v, want %v", got, want)
	}
}

// TestSearchGroundResultBudget pins the size of a full tools/call result for
// a 53-route search at the default cap, both copies and the destination
// enrichment included.
func TestSearchGroundResultBudget(t *testing.T) {
	stubGroundRoutes(t, groundContractFixture(53))
	stubDestination(t)
	got := callSearchGround(t, groundContractArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	t.Logf("search_ground result for 53 routes: %d bytes", got.size)
	if got.size >= 50000 {
		t.Errorf("tools/call result = %d bytes, want under 50000", got.size)
	}
}

// TestSearchGroundBadProviderIsError pins provider validation: a misspelled
// provider is a tool error that lists the valid names, not an empty search.
// Validation runs before any provider is dialled, so this stays offline.
func TestSearchGroundBadProviderIsError(t *testing.T) {
	orig := searchGroundByNameFunc
	t.Cleanup(func() { searchGroundByNameFunc = orig })
	searchGroundByNameFunc = ground.SearchByName
	got := callSearchGround(t, withArgs(map[string]any{"provider": "flixbuss"}))
	if !got.isError {
		t.Fatalf("misspelled provider was not isError: %+v", got)
	}
	for _, want := range []string{`"flixbuss"`, "valid providers:", "flixbus", "regiojet"} {
		if !strings.Contains(got.errText, want) {
			t.Errorf("error %q lacks %q", got.errText, want)
		}
	}
}

// TestSearchGroundDropsSingleRepeatLeg pins the leg trim: a lone leg with the
// route's own type, provider, stops and times says nothing new, so legs goes;
// a lone leg that differs, and every multi-leg route, keep their legs.
func TestSearchGroundDropsSingleRepeatLeg(t *testing.T) {
	routes := groundContractFixture(3) // row 0: Brno transfer; rows 1, 2: one leg
	routes[2].Legs[0].Provider = "cd"
	stubGroundRoutes(t, routes)
	got := structuredRoutes(t, callSearchGround(t, groundContractArgs))
	if legs, _ := got[0]["legs"].([]any); len(legs) != 2 {
		t.Errorf("two-leg route legs = %v, want both kept", got[0]["legs"])
	}
	if legs, ok := got[1]["legs"]; ok {
		t.Errorf("single repeat leg kept: %v", legs)
	}
	if legs, _ := got[2]["legs"].([]any); len(legs) != 1 {
		t.Errorf("single leg with another provider = %v, want it kept", got[2]["legs"])
	}
}

// TestSearchGroundOversizeGuardKeepsBudget pins the size guard: when 20 rows
// still overflow the budget, the lowest-ranked rows go until the result fits,
// and the summary says the result was trimmed to fit.
func TestSearchGroundOversizeGuardKeepsBudget(t *testing.T) {
	fixture := groundContractFixture(53)
	for i := range fixture {
		fixture[i].BookingURL += "&ctx=" + strings.Repeat("x", 2000)
	}
	stubGroundRoutes(t, fixture)
	got := callSearchGround(t, groundContractArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	if got.size >= 50000 {
		t.Errorf("tools/call result = %d bytes, want under 50000", got.size)
	}
	routes := structuredRoutes(t, got)
	if len(routes) == 0 || len(routes) >= 20 {
		t.Fatalf("routes = %d, want the guard to keep between 1 and 19", len(routes))
	}
	for i, u := range routeURLs(routes) {
		if u != fixture[i].BookingURL {
			t.Errorf("route %d is not fixture row %d; the guard must drop from the bottom", i, i)
		}
	}
	if c, _ := got.structured["count"].(float64); int(c) != len(routes) {
		t.Errorf("count = %v, want %d", got.structured["count"], len(routes))
	}
	if tc, _ := got.structured["total_count"].(float64); tc != 53 {
		t.Errorf("total_count = %v, want 53", got.structured["total_count"])
	}
	if !strings.Contains(got.userText, fmt.Sprintf("%d of 53", len(routes))) || !strings.Contains(got.userText, "50 KB") {
		t.Errorf("summary should report %d of 53 and the 50 KB trim:\n%s", len(routes), got.userText)
	}
}
