package trips

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const CurrentSchemaVersion = 2

// Workspace holds the planning state that does not fit cleanly into the
// legacy flat legs/bookings model.
type Workspace struct {
	Places            []Place            `json:"places"`
	Days              []DayPlan          `json:"days"`
	Candidates        []BookingCandidate `json:"candidates"`
	ImportedRecords   []ImportedRecord   `json:"imported_records"`
	Decisions         []Decision         `json:"decisions"`
	Evidence          []EvidenceRef      `json:"evidence"`
	UnresolvedActions []ActionItem       `json:"unresolved_actions"`
}

type Place struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Address     string   `json:"address,omitempty"`
	City        string   `json:"city,omitempty"`
	Country     string   `json:"country,omitempty"`
	Lat         float64  `json:"lat,omitempty"`
	Lon         float64  `json:"lon,omitempty"`
	Category    string   `json:"category,omitempty"`
	Source      string   `json:"source,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

type DayPlan struct {
	ID                    string   `json:"id"`
	Date                  string   `json:"date,omitempty"`
	Title                 string   `json:"title,omitempty"`
	PlaceIDs              []string `json:"place_ids"`
	EstimatedRouteMinutes int      `json:"estimated_route_minutes,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

type BookingCandidate struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	Provider         string    `json:"provider,omitempty"`
	Title            string    `json:"title"`
	Price            float64   `json:"price,omitempty"`
	Currency         string    `json:"currency,omitempty"`
	URL              string    `json:"url,omitempty"`
	CheckedAt        time.Time `json:"checked_at,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	FreeCancellation bool      `json:"free_cancellation,omitempty"`
	Refundable       bool      `json:"refundable,omitempty"`
	Status           string    `json:"status,omitempty"`
	EvidenceIDs      []string  `json:"evidence_ids,omitempty"`
	Notes            string    `json:"notes,omitempty"`
}

type ImportedRecord struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Provider    string    `json:"provider,omitempty"`
	Reference   string    `json:"reference,omitempty"`
	Source      string    `json:"source,omitempty"`
	RawHash     string    `json:"raw_hash,omitempty"`
	ImportedAt  time.Time `json:"imported_at"`
	TravelDate  string    `json:"travel_date,omitempty"`
	From        string    `json:"from,omitempty"`
	To          string    `json:"to,omitempty"`
	Price       float64   `json:"price,omitempty"`
	Currency    string    `json:"currency,omitempty"`
	EvidenceIDs []string  `json:"evidence_ids,omitempty"`
	Notes       string    `json:"notes,omitempty"`
}

type Decision struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Status  string   `json:"status,omitempty"`
	Options []string `json:"options,omitempty"`
	Chosen  string   `json:"chosen,omitempty"`
	DueAt   string   `json:"due_at,omitempty"`
	Notes   string   `json:"notes,omitempty"`
}

type EvidenceRef struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Provider    string    `json:"provider,omitempty"`
	URL         string    `json:"url,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Freshness   string    `json:"freshness"`  // fresh, stale, unknown
	Confidence  string    `json:"confidence"` // high, medium, low
	Explanation string    `json:"explanation,omitempty"`
}

type ActionItem struct {
	ID        string `json:"id"`
	Type      string `json:"type,omitempty"`
	Title     string `json:"title"`
	Status    string `json:"status,omitempty"`
	DueAt     string `json:"due_at,omitempty"`
	RelatedID string `json:"related_id,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

// NormalizeWorkspace upgrades old trips into the v2 workspace shape without
// changing legacy fields that existing tools still read and write.
func NormalizeWorkspace(t Trip) Trip {
	if t.SchemaVersion < CurrentSchemaVersion {
		t.SchemaVersion = CurrentSchemaVersion
	}
	if t.Workspace == nil {
		t.Workspace = &Workspace{}
	}
	if t.Legs == nil {
		t.Legs = []TripLeg{}
	}
	if t.Workspace.Places == nil {
		t.Workspace.Places = []Place{}
	}
	if t.Workspace.Days == nil {
		t.Workspace.Days = []DayPlan{}
	}
	if t.Workspace.Candidates == nil {
		t.Workspace.Candidates = []BookingCandidate{}
	}
	if t.Workspace.ImportedRecords == nil {
		t.Workspace.ImportedRecords = []ImportedRecord{}
	}
	if t.Workspace.Decisions == nil {
		t.Workspace.Decisions = []Decision{}
	}
	if t.Workspace.Evidence == nil {
		t.Workspace.Evidence = []EvidenceRef{}
	}
	if t.Workspace.UnresolvedActions == nil {
		t.Workspace.UnresolvedActions = []ActionItem{}
	}
	normalizeWorkspaceIDs(t.Workspace)
	return t
}

func normalizeWorkspaceIDs(w *Workspace) {
	for i := range w.Places {
		if w.Places[i].ID == "" {
			w.Places[i].ID = StableID("place", w.Places[i].Name, w.Places[i].City, w.Places[i].Address)
		}
	}
	for i := range w.Days {
		if w.Days[i].ID == "" {
			w.Days[i].ID = StableID("day", w.Days[i].Date, w.Days[i].Title)
		}
		if w.Days[i].PlaceIDs == nil {
			w.Days[i].PlaceIDs = []string{}
		}
	}
	for i := range w.Candidates {
		if w.Candidates[i].ID == "" {
			w.Candidates[i].ID = CandidateID(w.Candidates[i])
		}
	}
	for i := range w.ImportedRecords {
		if w.ImportedRecords[i].ID == "" {
			w.ImportedRecords[i].ID = ImportedRecordID(w.ImportedRecords[i])
		}
		if w.ImportedRecords[i].ImportedAt.IsZero() {
			w.ImportedRecords[i].ImportedAt = time.Now()
		}
	}
	for i := range w.Decisions {
		if w.Decisions[i].ID == "" {
			w.Decisions[i].ID = StableID("decision", w.Decisions[i].Title, w.Decisions[i].DueAt)
		}
	}
	for i := range w.Evidence {
		if w.Evidence[i].ID == "" {
			w.Evidence[i].ID = StableID("evidence", w.Evidence[i].Source, w.Evidence[i].Provider, w.Evidence[i].URL, w.Evidence[i].CheckedAt.Format(time.RFC3339))
		}
	}
	for i := range w.UnresolvedActions {
		if w.UnresolvedActions[i].ID == "" {
			w.UnresolvedActions[i].ID = StableID("action", w.UnresolvedActions[i].Type, w.UnresolvedActions[i].Title, w.UnresolvedActions[i].RelatedID)
		}
		if w.UnresolvedActions[i].Status == "" {
			w.UnresolvedActions[i].Status = "open"
		}
	}
}

func StableID(prefix string, parts ...string) string {
	h := sha1.New()
	for _, part := range parts {
		p := strings.ToLower(strings.Join(strings.Fields(part), " "))
		if p == "" {
			continue
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if prefix == "" {
		return sum[:12]
	}
	return fmt.Sprintf("%s_%s", prefix, sum[:12])
}

func CandidateID(c BookingCandidate) string {
	return StableID("cand", c.Type, c.Provider, c.Title, fmt.Sprintf("%.2f", c.Price), c.Currency, c.URL)
}

func ImportedRecordID(r ImportedRecord) string {
	if r.Reference != "" {
		return StableID("imp", r.Type, r.Provider, r.Reference)
	}
	return StableID("imp", r.Type, r.Provider, r.TravelDate, r.From, r.To, r.RawHash)
}

func (c BookingCandidate) IsStale(now time.Time, ttl time.Duration) bool {
	if !c.ExpiresAt.IsZero() {
		return now.After(c.ExpiresAt)
	}
	if c.CheckedAt.IsZero() {
		return true
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return now.Sub(c.CheckedAt) > ttl
}

func MergeLegs(existing, incoming []TripLeg) []TripLeg {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]TripLeg(nil), existing...)
	for _, leg := range existing {
		seen[tripLegKey(leg)] = true
	}
	for _, leg := range incoming {
		key := tripLegKey(leg)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, leg)
	}
	return out
}

func MergeImportedRecords(existing, incoming []ImportedRecord) []ImportedRecord {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]ImportedRecord(nil), existing...)
	for _, rec := range existing {
		seen[ImportedRecordID(rec)] = true
	}
	for _, rec := range incoming {
		if rec.ID == "" {
			rec.ID = ImportedRecordID(rec)
		}
		if rec.ImportedAt.IsZero() {
			rec.ImportedAt = time.Now()
		}
		key := ImportedRecordID(rec)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rec)
	}
	return out
}

func MergeCandidates(existing, incoming []BookingCandidate) []BookingCandidate {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]BookingCandidate(nil), existing...)
	for _, cand := range existing {
		if cand.ID == "" {
			cand.ID = CandidateID(cand)
		}
		seen[cand.ID] = true
	}
	for _, cand := range incoming {
		if cand.ID == "" {
			cand.ID = CandidateID(cand)
		}
		if seen[cand.ID] {
			continue
		}
		seen[cand.ID] = true
		out = append(out, cand)
	}
	return out
}

func tripLegKey(leg TripLeg) string {
	return StableID("leg", leg.Type, leg.From, leg.To, leg.Provider, leg.StartTime, leg.EndTime, leg.Reference)
}
