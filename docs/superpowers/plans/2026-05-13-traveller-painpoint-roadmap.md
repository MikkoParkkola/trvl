# Traveller Painpoint Roadmap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the verified travel workspace users actually ask for: fewer tabs, trusted live data, reservation import, map-aware itinerary planning, and booking-ready handoff without automatic purchase.

**Architecture:** Keep `travel` as the compact MCP entrypoint, but make it operate on a richer local-first trip workspace under `internal/trips`. Add small focused packages for import, evidence, itinerary optimization, and fare intelligence rather than growing MCP handlers into product logic.

**Tech Stack:** Go, existing JSON stores under `~/.trvl`, MCP v2025-11-25 schemas, fixture-backed parser tests, existing provider/search packages, no hosted service required.

---

## Source Signals

Traveller forum research showed the recurring pains are not "I need more AI prose." They are:

- Too many tabs, maps, spreadsheets, emails, screenshots, and search permutations.
- Low trust in AI itineraries because of wrong drive times, closed places, invented hotels, and generic tourist lists.
- Strong demand for map plus itinerary plus route time in one workspace.
- Real value in TripIt-style confirmation import and post-booking alerts.
- Existing planners can be slow, cluttered, or too rigid.
- Award tools are valuable but users distrust stale, phantom, or missed availability.

## Current Open GitHub Issues

Live issue inventory checked on 2026-05-13:

Linear tracking:

- [MIK-3496](https://linear.app/parm/issue/MIK-3496/trvl-verified-traveller-workspace-mvp) is the umbrella implementation issue. It is scoped to the verified traveller workspace MVP, assigned to Mikko, linked to GitHub #96/#91/#92/#94, and moved to In Progress before implementation.

| Issue | Status in this plan | Notes |
| --- | --- | --- |
| [#96](https://github.com/MikkoParkkola/trvl/issues/96) strict array schema bug | Task 0, blocking | Fix before broad MCP roadmap work because strict OpenAI hosts reject the whole tool surface. |
| [#91](https://github.com/MikkoParkkola/trvl/issues/91) itinerary import/export | Tasks 1-3 | Expand from import/export into Trip Workspace v2 backbone. |
| [#94](https://github.com/MikkoParkkola/trvl/issues/94) booking readiness | Task 7 | Depends on workspace candidate and evidence fields. |
| [#92](https://github.com/MikkoParkkola/trvl/issues/92) provider quality/freshness | Task 4 | Feeds the trust layer and stale-result warnings. |
| [#90](https://github.com/MikkoParkkola/trvl/issues/90) richer hotel detail | Task 4 and Task 7 input | Gives cancellation, board, fee, and room metadata for decisions. |
| [#89](https://github.com/MikkoParkkola/trvl/issues/89) secure remote MCP | Parallel security track | Important, but not required for local-first traveller painpoint MVP. |
| [#88](https://github.com/MikkoParkkola/trvl/issues/88) rental cars | Later vertical expansion | Useful parity after workspace/trust is solid. |
| [#93](https://github.com/MikkoParkkola/trvl/issues/93) demo/comparison docs | Task 9 | Should demonstrate painpoint workflows, not raw tool count. |
| [#19](https://github.com/MikkoParkkola/trvl/issues/19) directory submissions | External blocker | Manual browser/account work by Mikko. |

## Target User Outcomes

1. A traveller can say "plan my Japan trip" and trvl creates a saved workspace with candidates, evidence, map points, day plan, and unresolved decisions.
2. A traveller can import booking confirmations and see flights, hotels, ground legs, references, and watch/recheck actions in one trip.
3. A traveller can ask "is this itinerary realistic?" and get opening-hour, route-time, provider-freshness, and overpacked-day warnings.
4. A traveller can ask "should I book now?" and get a buy/wait verdict with route baseline, confidence, and watch action.
5. A traveller can manually book through provider URLs, then mark the trip booked with stale-price warnings and no automatic purchase/cancellation claims.

## Design Units

### Existing Files To Modify

- `internal/trips/trips.go`: expand trip schema with workspace fields, schema version, candidates, evidence, places, day plans, and booking candidates.
- `internal/trips/trips_test.go`: migration, idempotency, and persistence tests.
- `mcp/tools_trips.go`: expose workspace read/write operations through existing trip tools and new import/export handlers.
- `mcp/tools_smart.go`: route `travel` intents such as `import_reservation`, `optimize_itinerary`, `fare_intelligence`, and `booking_ready`.
- `mcp/schema_helpers.go`: keep strict JSON schema helpers reused by all new fields.
- `README.md`, `AGENTS.md`, `.claude/skills/trvl.md`, `docs/COMPARISON.md`, `docs/POSITIONING.md`: update user-facing workflow docs.

### New Files To Create

- `internal/trips/workspace.go`: workspace data types and normalization helpers.
- `internal/trips/workspace_test.go`: schema defaults, stable IDs, duplicate detection.
- `internal/trips/export.go`: JSON and Markdown export for Trip Workspace v2.
- `internal/trips/export_test.go`: JSON round-trip and Markdown golden assertions.
- `internal/trips/import.go`: import Trip Workspace JSON and normalized reservation records.
- `internal/trips/import_test.go`: dry-run and idempotent merge tests.
- `internal/imports/reservation.go`: parse normalized reservations from profile/email extraction output, ICS-like fields, and raw text adapter results.
- `internal/imports/reservation_test.go`: fixture-backed flight, hotel, ground, and non-booking tests.
- `internal/evidence/evidence.go`: shared evidence/provenance/freshness types.
- `internal/evidence/evidence_test.go`: stale/fresh, redaction, and confidence tests.
- `internal/itinerary/optimizer.go`: map-aware day clustering and route-time sanity checks.
- `internal/itinerary/optimizer_test.go`: deterministic clustering and overpacked-day tests.
- `internal/fareintel/fareintel.go`: buy/wait verdicts from watch history, forecast output, and current price.
- `internal/fareintel/fareintel_test.go`: cheap/normal/expensive verdict tests.
- `mcp/tools_workspace.go`: MCP handlers for import/export/workspace/itinerary/fare intelligence if the existing trip file becomes too large.
- `docs/traveller-workspace.md`: product-facing design, limitations, and examples.

## Data Model Design

Add schema-versioned workspace fields while keeping existing trip JSON loadable:

```go
type Trip struct {
	ID            string             `json:"id"`
	SchemaVersion int                `json:"schema_version,omitempty"`
	Name          string             `json:"name"`
	Status        string             `json:"status"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Legs          []TripLeg          `json:"legs"`
	Bookings      []Booking          `json:"bookings,omitempty"`
	Workspace     *Workspace         `json:"workspace,omitempty"`
	Tags          []string           `json:"tags,omitempty"`
	Notes         string             `json:"notes,omitempty"`
}

type Workspace struct {
	Places            []Place             `json:"places,omitempty"`
	Days              []DayPlan           `json:"days,omitempty"`
	Candidates        []BookingCandidate  `json:"candidates,omitempty"`
	ImportedRecords   []ImportedRecord    `json:"imported_records,omitempty"`
	Decisions         []Decision          `json:"decisions,omitempty"`
	Evidence          []EvidenceRef       `json:"evidence,omitempty"`
	UnresolvedActions []ActionItem        `json:"unresolved_actions,omitempty"`
}

type EvidenceRef struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Provider    string    `json:"provider,omitempty"`
	URL         string    `json:"url,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Freshness   string    `json:"freshness"`   // fresh, stale, unknown
	Confidence  string    `json:"confidence"` // high, medium, low
	Explanation string    `json:"explanation,omitempty"`
}
```

Do not remove existing `legs` or `bookings`; compatibility aliases and older stored trips rely on them.

## Implementation Sequence

### Task 0: Fix Strict MCP Schema Blocker (#96)

**Files:**
- Modify: `mcp/tools_find.go`
- Test: `mcp/schema_helpers_test.go` or `mcp/tools_find_test.go`

- [ ] **Step 1: Write the failing strict-schema test**

```go
func TestFindInputSchemaArrayFieldsDeclareItems(t *testing.T) {
	schema := findInputSchema()
	prop, ok := schema.Properties["layover_at"]
	if !ok {
		t.Fatalf("layover_at schema missing")
	}
	if prop.Type != "array" {
		t.Fatalf("layover_at type = %q, want array", prop.Type)
	}
	if prop.Items == nil || prop.Items.Type != "string" {
		t.Fatalf("layover_at items = %#v, want string items", prop.Items)
	}
}
```

- [ ] **Step 2: Run the failing test**

Run: `go test -short ./mcp -run TestFindInputSchemaArrayFieldsDeclareItems -count=1`

Expected: FAIL because `layover_at` has no `Items`.

- [ ] **Step 3: Implement the minimal schema fix**

```go
"layover_at": schemaStringArrayDesc("Restrict qualifying layovers to these IATA codes (empty = any airport)"),
```

- [ ] **Step 4: Audit all MCP array schemas**

Run: `rg 'Type: "array"|\"type\": \"array\"' mcp`

Expected: every array property either uses `schemaArray`, `schemaStringArray`, `schemaArrayDesc`, or declares `Items`.

- [ ] **Step 5: Validate**

Run: `go test -short ./mcp`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add mcp/tools_find.go mcp/tools_find_test.go mcp/schema_helpers_test.go
git commit -m "fix: make find schemas strict-mode compatible"
```

### Task 1: Trip Workspace v2 Schema

**Files:**
- Create: `internal/trips/workspace.go`
- Create: `internal/trips/workspace_test.go`
- Modify: `internal/trips/trips.go`

- [ ] **Step 1: Write schema default and backward-compatibility tests**

```go
func TestNormalizeTripInitializesWorkspaceV2(t *testing.T) {
	tr := trips.Trip{Name: "Prague weekend", Status: "planning"}
	got := trips.NormalizeWorkspace(tr)
	if got.SchemaVersion != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", got.SchemaVersion)
	}
	if got.Workspace == nil {
		t.Fatalf("Workspace is nil")
	}
	if got.Legs == nil {
		t.Fatalf("Legs should be initialized to empty slice")
	}
}

func TestLoadLegacyTripStillWorks(t *testing.T) {
	legacy := `[
	  {"id":"trip_legacy","name":"Legacy","status":"planning","created_at":"2026-05-01T00:00:00Z","updated_at":"2026-05-01T00:00:00Z","legs":[]}
	]`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trips.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := trips.NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	got := store.List()[0]
	if got.SchemaVersion != 2 || got.Workspace == nil {
		t.Fatalf("legacy trip was not normalized: %#v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify missing API**

Run: `go test -short ./internal/trips -run 'TestNormalizeTripInitializesWorkspaceV2|TestLoadLegacyTripStillWorks' -count=1`

Expected: FAIL because `NormalizeWorkspace`, `Workspace`, and `SchemaVersion` are not implemented.

- [ ] **Step 3: Add workspace model**

```go
const CurrentSchemaVersion = 2

type Workspace struct {
	Places            []Place            `json:"places,omitempty"`
	Days              []DayPlan          `json:"days,omitempty"`
	Candidates        []BookingCandidate `json:"candidates,omitempty"`
	ImportedRecords   []ImportedRecord   `json:"imported_records,omitempty"`
	Decisions         []Decision         `json:"decisions,omitempty"`
	Evidence          []EvidenceRef      `json:"evidence,omitempty"`
	UnresolvedActions []ActionItem       `json:"unresolved_actions,omitempty"`
}

type Place struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Address   string   `json:"address,omitempty"`
	Lat       float64  `json:"lat,omitempty"`
	Lon       float64  `json:"lon,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
}
```

- [ ] **Step 4: Add normalization**

```go
func NormalizeWorkspace(t Trip) Trip {
	if t.SchemaVersion == 0 {
		t.SchemaVersion = CurrentSchemaVersion
	}
	if t.SchemaVersion < CurrentSchemaVersion {
		t.SchemaVersion = CurrentSchemaVersion
	}
	if t.Workspace == nil {
		t.Workspace = &Workspace{}
	}
	if t.Legs == nil {
		t.Legs = []TripLeg{}
	}
	return t
}
```

- [ ] **Step 5: Wire normalization into load/add/update**

After `loadJSON` in `Store.Load`, normalize each loaded trip:

```go
for i := range s.trips {
	s.trips[i] = NormalizeWorkspace(s.trips[i])
}
```

Before persisting in `Store.Add` and `Store.Update`, normalize the candidate trip.

- [ ] **Step 6: Validate**

Run: `go test -short ./internal/trips`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/trips/trips.go internal/trips/workspace.go internal/trips/workspace_test.go
git commit -m "feat: add trip workspace v2 schema"
```

### Task 2: Stable Import/Export (#91)

**Files:**
- Create: `internal/trips/export.go`
- Create: `internal/trips/export_test.go`
- Create: `internal/trips/import.go`
- Create: `internal/trips/import_test.go`
- Modify: `mcp/tools_trips.go` or create `mcp/tools_workspace.go`

- [ ] **Step 1: Write JSON export round-trip test**

```go
func TestExportImportRoundTripPreservesWorkspace(t *testing.T) {
	tr := trips.NormalizeWorkspace(trips.Trip{
		ID: "trip_1", Name: "Japan", Status: "planning",
		Legs: []trips.TripLeg{{Type: "flight", From: "HEL", To: "HND", StartTime: "2026-10-01T12:00:00Z"}},
	})
	tr.Workspace.Places = []trips.Place{{ID: "place_tokyo", Name: "Tokyo Station", Lat: 35.6812, Lon: 139.7671}}
	data, err := trips.ExportJSON(tr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := trips.ImportJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != tr.Name || len(got.Workspace.Places) != 1 {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}
```

- [ ] **Step 2: Write duplicate-detection test**

```go
func TestMergeImportedTripDoesNotDuplicateLegs(t *testing.T) {
	existing := trips.NormalizeWorkspace(trips.Trip{ID: "trip_1", Name: "Japan", Status: "planning"})
	incoming := trips.NormalizeWorkspace(trips.Trip{
		Name: "Japan",
		Legs: []trips.TripLeg{{Type: "flight", From: "HEL", To: "HND", StartTime: "2026-10-01T12:00:00Z"}},
	})
	merged := trips.MergeImportedTrip(existing, incoming)
	merged = trips.MergeImportedTrip(merged, incoming)
	if len(merged.Legs) != 1 {
		t.Fatalf("legs = %d, want 1", len(merged.Legs))
	}
}
```

- [ ] **Step 3: Implement export/import functions**

```go
func ExportJSON(t Trip) ([]byte, error) {
	t = NormalizeWorkspace(t)
	return json.MarshalIndent(t, "", "  ")
}

func ImportJSON(data []byte) (Trip, error) {
	var t Trip
	if err := json.Unmarshal(data, &t); err != nil {
		return Trip{}, fmt.Errorf("parse trip workspace json: %w", err)
	}
	return NormalizeWorkspace(t), nil
}
```

- [ ] **Step 4: Implement deterministic merge**

Use a key derived from leg type, endpoints, start time, provider, and reference:

```go
func tripLegKey(l TripLeg) string {
	return strings.ToLower(strings.Join([]string{l.Type, l.From, l.To, l.Provider, l.StartTime, l.Reference}, "|"))
}
```

- [ ] **Step 5: Add MCP handlers**

Add intents:

- `export_trip`
- `import_trip`
- `trip_workspace`

`import_trip` must support `dry_run: true` and return planned changes without writing.

- [ ] **Step 6: Validate**

Run: `go test -short ./internal/trips ./mcp -run 'Import|Export|Workspace|Trip'`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/trips/export.go internal/trips/export_test.go internal/trips/import.go internal/trips/import_test.go mcp/tools_trips.go mcp/tools_workspace.go
git commit -m "feat: add trip workspace import export"
```

### Task 3: Reservation Import Pipeline

**Files:**
- Create: `internal/imports/reservation.go`
- Create: `internal/imports/reservation_test.go`
- Modify: `internal/profile/email.go`
- Modify: `mcp/tools_profile.go` or `mcp/tools_workspace.go`

- [ ] **Step 1: Write normalized reservation tests**

```go
func TestNormalizeProfileBookingToTripLegFlight(t *testing.T) {
	b := profile.Booking{
		Type: "flight", TravelDate: "2026-10-01", From: "HEL", To: "HND",
		Provider: "Finnair", Reference: "ABC123", Price: 820, Currency: "EUR",
	}
	got, err := imports.FromProfileBooking(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Leg.Type != "flight" || got.Leg.Reference != "ABC123" {
		t.Fatalf("unexpected reservation: %#v", got)
	}
	if got.Record.Source != "profile_booking" {
		t.Fatalf("source = %q", got.Record.Source)
	}
}

func TestParseReservationRejectsNonBooking(t *testing.T) {
	_, err := imports.ParseReservationText("Newsletter", "Save 10 percent on travel inspiration")
	if err == nil {
		t.Fatalf("expected non-booking error")
	}
}
```

- [ ] **Step 2: Implement reservation type**

```go
type Reservation struct {
	Leg    trips.TripLeg
	Record trips.ImportedRecord
}

func FromProfileBooking(b profile.Booking) (Reservation, error) {
	if b.Type == "" || b.Type == "not_booking" {
		return Reservation{}, ErrNotBooking
	}
	return Reservation{
		Leg: trips.TripLeg{
			Type: b.Type, From: b.From, To: b.To, Provider: b.Provider,
			StartTime: b.TravelDate, Price: b.Price, Currency: b.Currency,
			Confirmed: true, Reference: b.Reference,
		},
		Record: trips.ImportedRecord{
			ID: stableReservationID(b.Type, b.Provider, b.Reference, b.TravelDate),
			Source: "profile_booking", Reference: b.Reference,
		},
	}, nil
}
```

- [ ] **Step 3: Add trip attach function**

```go
func AddReservation(t trips.Trip, r Reservation) trips.Trip {
	t = trips.NormalizeWorkspace(t)
	t.Legs = trips.MergeLegs(t.Legs, []trips.TripLeg{r.Leg})
	t.Workspace.ImportedRecords = trips.MergeImportedRecords(t.Workspace.ImportedRecords, []trips.ImportedRecord{r.Record})
	return t
}
```

- [ ] **Step 4: Expose MCP import**

`travel` with `intent: "import_reservation"` should accept:

```json
{
  "trip_id": "trip_abc",
  "subject": "Your Finnair booking",
  "body": "raw confirmation email",
  "dry_run": true
}
```

It should return `not_booking`, `would_add`, or `added`.

- [ ] **Step 5: Validate**

Run: `go test -short ./internal/imports ./internal/profile ./internal/trips ./mcp -run 'Reservation|Booking|Profile|Trip'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/imports/reservation.go internal/imports/reservation_test.go internal/profile/email.go mcp/tools_profile.go mcp/tools_workspace.go
git commit -m "feat: import reservations into trip workspaces"
```

### Task 4: Evidence And Freshness Trust Layer (#92, #90 Support)

**Files:**
- Create: `internal/evidence/evidence.go`
- Create: `internal/evidence/evidence_test.go`
- Modify: `internal/trips/workspace.go`
- Modify: `mcp/tools_hotels_details.go`
- Modify: `mcp/tools_providers.go`

- [ ] **Step 1: Write freshness classification tests**

```go
func TestFreshnessClassifiesStaleEvidence(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	ref := evidence.Ref{CheckedAt: now.Add(-3 * time.Hour), MaxAge: time.Hour}
	if got := ref.FreshnessAt(now); got != evidence.Stale {
		t.Fatalf("freshness = %q, want stale", got)
	}
}

func TestRedactEvidenceRemovesSecrets(t *testing.T) {
	ref := evidence.Ref{URL: "https://example.com/search?api_key=secret&city=Tokyo"}
	got := evidence.Redact(ref)
	if strings.Contains(got.URL, "secret") || strings.Contains(got.URL, "api_key") {
		t.Fatalf("secret leaked in URL: %s", got.URL)
	}
}
```

- [ ] **Step 2: Implement evidence package**

```go
type Freshness string

const (
	Fresh   Freshness = "fresh"
	Stale   Freshness = "stale"
	Unknown Freshness = "unknown"
)

type Ref struct {
	ID          string
	Source      string
	Provider    string
	URL         string
	CheckedAt   time.Time
	MaxAge      time.Duration
	Confidence  string
	Explanation string
}
```

- [ ] **Step 3: Attach evidence to hotel and provider outputs**

For hotel detail responses, include:

- provider name;
- checked timestamp;
- field provenance for cancellation/board/fee metadata when available;
- stale warning when detail data is older than accepted TTL;
- `detail_errors` for partial success.

- [ ] **Step 4: Attach evidence to workspace candidates**

Each `BookingCandidate` must include evidence IDs and a `stale_after` timestamp.

- [ ] **Step 5: Validate**

Run: `go test -short ./internal/evidence ./internal/trips ./internal/hotels ./mcp -run 'Evidence|Fresh|Hotel|Provider'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/evidence/evidence.go internal/evidence/evidence_test.go internal/trips/workspace.go mcp/tools_hotels_details.go mcp/tools_providers.go
git commit -m "feat: add evidence freshness to travel decisions"
```

### Task 5: Map-First Itinerary Optimizer

**Files:**
- Create: `internal/itinerary/optimizer.go`
- Create: `internal/itinerary/optimizer_test.go`
- Modify: `internal/trips/workspace.go`
- Modify: `mcp/tools_workspace.go`

- [ ] **Step 1: Write deterministic day clustering test**

```go
func TestOptimizerClustersNearbyPlacesIntoSameDay(t *testing.T) {
	input := itinerary.Input{
		TripStart: "2026-10-01",
		Days: 2,
		Places: []trips.Place{
			{ID: "ueno", Name: "Ueno Park", Lat: 35.7156, Lon: 139.7745},
			{ID: "akihabara", Name: "Akihabara", Lat: 35.6984, Lon: 139.7730},
			{ID: "yokohama", Name: "Yokohama", Lat: 35.4437, Lon: 139.6380},
		},
	}
	got := itinerary.Optimize(input)
	if len(got.Days) != 2 {
		t.Fatalf("days = %d, want 2", len(got.Days))
	}
	if !got.Days[0].Contains("ueno") || !got.Days[0].Contains("akihabara") {
		t.Fatalf("nearby Tokyo places were not clustered: %#v", got.Days[0])
	}
}
```

- [ ] **Step 2: Write overpacked-day test**

```go
func TestOptimizerFlagsOverpackedDay(t *testing.T) {
	input := itinerary.Input{Days: 1, MaxPlacesPerDay: 3}
	for i := 0; i < 6; i++ {
		input.Places = append(input.Places, trips.Place{ID: fmt.Sprintf("p%d", i), Name: fmt.Sprintf("Place %d", i)})
	}
	got := itinerary.Optimize(input)
	if len(got.Warnings) == 0 {
		t.Fatalf("expected overpacked warning")
	}
}
```

- [ ] **Step 3: Implement simple local optimizer**

Use haversine distance and greedy clustering. Do not add an external routing dependency in this task. Store warnings rather than pretending exact public transport time is known.

- [ ] **Step 4: Add MCP action**

`travel` with `intent: "trip"` and `action: "optimize_itinerary"` should read the trip workspace, produce day plans, and persist them only when `dry_run` is false.

- [ ] **Step 5: Validate**

Run: `go test -short ./internal/itinerary ./internal/trips ./mcp -run 'Itinerary|Optimize|Trip'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/itinerary/optimizer.go internal/itinerary/optimizer_test.go internal/trips/workspace.go mcp/tools_workspace.go
git commit -m "feat: add map-aware itinerary optimizer"
```

### Task 6: Fare Intelligence

**Files:**
- Create: `internal/fareintel/fareintel.go`
- Create: `internal/fareintel/fareintel_test.go`
- Modify: `internal/watch/watch.go`
- Modify: `internal/forecast/forecast.go`
- Modify: `mcp/tools_workspace.go`

- [ ] **Step 1: Write buy/wait verdict tests**

```go
func TestVerdictBuyWhenCurrentPriceBelowTypical(t *testing.T) {
	history := []fareintel.PricePoint{{Price: 200}, {Price: 220}, {Price: 210}, {Price: 205}}
	got := fareintel.Evaluate(fareintel.Request{CurrentPrice: 150, History: history, Currency: "EUR"})
	if got.Verdict != fareintel.Buy {
		t.Fatalf("verdict = %q, want buy", got.Verdict)
	}
	if got.Confidence == "" || got.Explanation == "" {
		t.Fatalf("missing confidence/explanation: %#v", got)
	}
}

func TestVerdictWatchWhenHistoryInsufficient(t *testing.T) {
	got := fareintel.Evaluate(fareintel.Request{CurrentPrice: 200, Currency: "EUR"})
	if got.Verdict != fareintel.Watch {
		t.Fatalf("verdict = %q, want watch", got.Verdict)
	}
}
```

- [ ] **Step 2: Implement verdict model**

```go
type Verdict string

const (
	Buy   Verdict = "buy"
	Wait  Verdict = "wait"
	Watch Verdict = "watch"
)

type Result struct {
	Verdict          Verdict `json:"verdict"`
	Confidence       string  `json:"confidence"`
	CurrentPrice     float64 `json:"current_price"`
	TypicalPrice     float64 `json:"typical_price,omitempty"`
	PercentVsTypical float64 `json:"percent_vs_typical,omitempty"`
	Currency         string  `json:"currency"`
	Explanation      string  `json:"explanation"`
}
```

- [ ] **Step 3: Source history from watch price points**

Add a helper that converts `watch.PricePoint` to `fareintel.PricePoint` without changing existing watch file format.

- [ ] **Step 4: Add MCP action**

`travel` with `intent: "fare_intelligence"` accepts current result metadata or a watch ID and returns verdict plus suggested watch threshold.

- [ ] **Step 5: Validate**

Run: `go test -short ./internal/fareintel ./internal/watch ./internal/forecast ./mcp -run 'Fare|Forecast|Watch'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/fareintel/fareintel.go internal/fareintel/fareintel_test.go internal/watch/watch.go internal/forecast/forecast.go mcp/tools_workspace.go
git commit -m "feat: add explainable fare intelligence"
```

### Task 7: Booking Readiness (#94)

**Files:**
- Modify: `internal/trips/workspace.go`
- Modify: `internal/trips/trips.go`
- Modify: `mcp/tools_trips.go`
- Modify: `mcp/tools_workspace.go`

- [ ] **Step 1: Write booking candidate stale-warning test**

```go
func TestBookingCandidateStaleWarning(t *testing.T) {
	c := trips.BookingCandidate{
		ID: "cand_1", Provider: "Google Flights", Price: 150, Currency: "EUR",
		CheckedAt: time.Date(2026, 5, 13, 9, 0, 0, 0, time.UTC),
		StaleAfter: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
	}
	if !c.IsStale(time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("candidate should be stale")
	}
}
```

- [ ] **Step 2: Implement booking candidate model**

```go
type BookingCandidate struct {
	ID                string    `json:"id"`
	Type              string    `json:"type"`
	Provider          string    `json:"provider"`
	URL               string    `json:"url,omitempty"`
	Price             float64   `json:"price,omitempty"`
	Currency          string    `json:"currency,omitempty"`
	CancellationNotes string    `json:"cancellation_notes,omitempty"`
	Refundable        *bool     `json:"refundable,omitempty"`
	CheckedAt         time.Time `json:"checked_at"`
	StaleAfter        time.Time `json:"stale_after"`
	Evidence          []string  `json:"evidence,omitempty"`
}
```

- [ ] **Step 3: Add save-candidate handler**

`travel` with `intent: "booking_ready"` and `action: "save_candidate"` stores the candidate under the trip workspace. It must return a warning that trvl does not book or cancel.

- [ ] **Step 4: Link `mark_trip_booked` to candidates**

Allow optional `candidate_id`. Preserve provider, URL, price, currency, reference, manual confirmation timestamp, and notes.

- [ ] **Step 5: Validate**

Run: `go test -short ./internal/trips ./mcp -run 'Booking|Candidate|MarkTripBooked'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/trips/workspace.go internal/trips/trips.go mcp/tools_trips.go mcp/tools_workspace.go
git commit -m "feat: add booking readiness candidates"
```

### Task 8: Smart Router And Compatibility Surface

**Files:**
- Modify: `mcp/tools_smart.go`
- Modify: `mcp/tools_smart_test.go`
- Modify: `mcp/tools.go`

- [ ] **Step 1: Write routing tests**

```go
func TestTravelRouterWorkspaceIntents(t *testing.T) {
	s := &Server{handlers: map[string]ToolHandler{
		"trip_workspace": func(context.Context, map[string]any, ElicitFunc, SamplingFunc, ProgressFunc) ([]ContentBlock, interface{}, error) {
			return []ContentBlock{{Type: "text", Text: "ok"}}, map[string]any{"ok": true}, nil
		},
	}}
	target, _ := s.resolveTravelTarget("trip_workspace", "export", "")
	if target != "trip_workspace" {
		t.Fatalf("target = %q, want trip_workspace", target)
	}
}
```

- [ ] **Step 2: Register aliases**

Add aliases:

- `trip_workspace`
- `import_reservation`
- `optimize_itinerary`
- `fare_intelligence`
- `booking_ready`

- [ ] **Step 3: Validate tool list stays compact**

Run: `go test -short ./mcp -run 'Travel|ToolSurface|Schema'`

Expected: default advertised surface remains one `travel` tool unless `TRVL_MCP_TOOL_MODE=legacy`.

- [ ] **Step 4: Commit**

```bash
git add mcp/tools_smart.go mcp/tools_smart_test.go mcp/tools.go
git commit -m "feat: route workspace workflows through travel"
```

### Task 9: Docs, Skills, Demo, And Public Claims (#93)

**Files:**
- Create: `docs/traveller-workspace.md`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `.claude/skills/trvl.md`
- Modify: `.claude/skills/providers.md` only if provider-health guidance changes.
- Modify: `docs/COMPARISON.md`
- Modify: `docs/POSITIONING.md`

- [ ] **Step 1: Add traveller workspace doc**

The doc must include:

- what gets stored locally;
- import/export formats;
- no automatic booking/cancellation;
- evidence/freshness language;
- example `travel` calls;
- limitations around stale prices, provider failures, and award availability.

- [ ] **Step 2: Update AGENTS and skills**

Add operational guidance:

```markdown
When a user asks for a plan, prefer creating or updating a trip workspace.
Do not present AI-generated itinerary text as verified unless each place,
opening-hour assumption, route-time assumption, and booking candidate has
fresh evidence or an explicit uncertainty warning.
```

- [ ] **Step 3: Update comparison positioning**

Position trvl as a verified local-first travel workspace for agents, not a better human travel website.

- [ ] **Step 4: Validate docs and claims**

Run:

```bash
go test -short ./cmd/trvl ./mcp
rg -n "automatic booking|automatically books|guaranteed availability|always current" README.md AGENTS.md docs .claude/skills
```

Expected: tests pass; unsafe claims are absent or explicitly negated.

- [ ] **Step 5: Commit**

```bash
git add docs/traveller-workspace.md README.md AGENTS.md .claude/skills/trvl.md .claude/skills/providers.md docs/COMPARISON.md docs/POSITIONING.md
git commit -m "docs: describe verified trip workspace workflows"
```

## Deferred Or Parallel Tracks

### Secure Remote MCP (#89)

Run in parallel after #96. Keep it separate from local workspace work because auth mistakes can expose personal trips, preferences, and watches.

### Rental Cars (#88)

Start after the workspace/trust spine exists. Car rentals should become another candidate type, not a standalone orphan surface.

### Directory Submission (#19)

Manual browser/account task. Keep blocked until Mikko can submit to mcp.so and Glama.

## Validation Matrix

| Gate | Evidence command |
| --- | --- |
| Unit tests | `go test -short ./internal/trips ./internal/imports ./internal/evidence ./internal/itinerary ./internal/fareintel ./mcp ./cmd/trvl` |
| Existing focused baseline | `go test ./internal/trips ./internal/profile ./internal/watch ./internal/route ./mcp` |
| Strict MCP schema | `go test -short ./mcp -run 'Schema|ToolSurface|Find'` |
| Docs unsafe-claim scan | `rg -n "automatic booking|guaranteed availability|always current" README.md AGENTS.md docs .claude/skills` |
| Secret scan | `git diff --check && git status --short` plus repo standard secret scan if configured |

## Implementation Status

2026-05-13 branch `codex/review-hardening-competitor-upgrades` implements the local-first MVP slices:

- Task 0: strict MCP input arrays now carry `items`, with a regression test over every legacy input schema.
- Task 1: Trip Workspace v2 schema, legacy trip normalization, stable IDs, stale candidate checks, and idempotent merges.
- Task 2: JSON import/export and Markdown export helpers.
- Task 3: reservation import adapters from user-approved text/profile bookings.
- Task 4: evidence/freshness/redaction helpers and cautious stale-language docs.
- Task 5: map-aware itinerary route-time estimator with overpacked-day warnings.
- Task 6: fare intelligence buy/watch/wait verdicts from watch history.
- Task 7: booking-candidate readiness and `mark_trip_booked` candidate linkage.
- Task 8: consolidated `trip_workspace` router/action surface through the primary `travel` tool.
- Task 9: README, AGENTS, bundled skill, positioning/comparison docs, and `docs/traveller-workspace.md`.

## DoR Gate For This Plan

DoR: PASS for planning and the first implementation slice.

- AC1 testable: PASS, each task has acceptance tests and commands.
- AC2 scoped: PASS, files and package boundaries are listed.
- AC3 ROI: PASS, roadmap maps to validated traveller painpoints.
- AC4 no duplicate: PASS, current GitHub and Linear issues were checked and mapped; overlapping Linear issue MIK-3088 remains a broader backlog item, while MIK-3496 is the active umbrella.
- AC5 target: PASS, target packages, MCP handlers, docs, and skills are named.
- AC6 unblocked: PASS for local-first implementation. External directory submission #19 remains explicitly out of scope; GitHub Projects board updates are blocked by missing `read:project` scope, so ordering is represented by the GitHub milestone/labels and Linear state.

## DoD Gate For Implementing This Plan

DoD is not satisfied by creating this plan alone. Before claiming any implementation task is done:

- All task-specific tests must pass.
- Relevant package tests must pass.
- MCP schemas must pass strict-mode validation for every advertised tool.
- New user-facing behavior must be documented in README, AGENTS, and skills.
- Any local file storing personal trip data must use `0700` directories and `0600` files.
- Every claim about availability, freshness, or booking must include evidence or uncertainty language.
- The branch must be pushed and linked to the relevant GitHub issue when code changes are made.
