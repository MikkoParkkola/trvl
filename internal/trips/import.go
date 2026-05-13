package trips

type MergeSummary struct {
	LegsAdded            int `json:"legs_added"`
	ImportedRecordsAdded int `json:"imported_records_added"`
	CandidatesAdded      int `json:"candidates_added"`
	ActionsAdded         int `json:"actions_added"`
}

func MergeTripWorkspace(dst, src Trip) (Trip, MergeSummary) {
	dst = NormalizeWorkspace(dst)
	src = NormalizeWorkspace(src)

	beforeLegs := len(dst.Legs)
	dst.Legs = MergeLegs(dst.Legs, src.Legs)

	beforeRecords := len(dst.Workspace.ImportedRecords)
	dst.Workspace.ImportedRecords = MergeImportedRecords(dst.Workspace.ImportedRecords, src.Workspace.ImportedRecords)

	beforeCandidates := len(dst.Workspace.Candidates)
	dst.Workspace.Candidates = MergeCandidates(dst.Workspace.Candidates, src.Workspace.Candidates)

	beforeActions := len(dst.Workspace.UnresolvedActions)
	dst.Workspace.UnresolvedActions = mergeActions(dst.Workspace.UnresolvedActions, src.Workspace.UnresolvedActions)

	dst.Workspace.Places = mergePlaces(dst.Workspace.Places, src.Workspace.Places)
	dst.Workspace.Days = mergeDays(dst.Workspace.Days, src.Workspace.Days)
	dst.Workspace.Decisions = mergeDecisions(dst.Workspace.Decisions, src.Workspace.Decisions)
	dst.Workspace.Evidence = mergeEvidence(dst.Workspace.Evidence, src.Workspace.Evidence)

	return dst, MergeSummary{
		LegsAdded:            len(dst.Legs) - beforeLegs,
		ImportedRecordsAdded: len(dst.Workspace.ImportedRecords) - beforeRecords,
		CandidatesAdded:      len(dst.Workspace.Candidates) - beforeCandidates,
		ActionsAdded:         len(dst.Workspace.UnresolvedActions) - beforeActions,
	}
}

func MergeReservationArtifacts(t Trip, records []ImportedRecord, legs []TripLeg, actions []ActionItem) (Trip, MergeSummary) {
	t = NormalizeWorkspace(t)
	beforeLegs := len(t.Legs)
	beforeRecords := len(t.Workspace.ImportedRecords)
	beforeActions := len(t.Workspace.UnresolvedActions)

	t.Legs = MergeLegs(t.Legs, legs)
	t.Workspace.ImportedRecords = MergeImportedRecords(t.Workspace.ImportedRecords, records)
	t.Workspace.UnresolvedActions = mergeActions(t.Workspace.UnresolvedActions, actions)

	return t, MergeSummary{
		LegsAdded:            len(t.Legs) - beforeLegs,
		ImportedRecordsAdded: len(t.Workspace.ImportedRecords) - beforeRecords,
		ActionsAdded:         len(t.Workspace.UnresolvedActions) - beforeActions,
	}
}

func mergePlaces(existing, incoming []Place) []Place {
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]Place(nil), existing...)
	for _, p := range existing {
		if p.ID == "" {
			p.ID = StableID("place", p.Name, p.City, p.Address)
		}
		seen[p.ID] = true
	}
	for _, p := range incoming {
		if p.ID == "" {
			p.ID = StableID("place", p.Name, p.City, p.Address)
		}
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out
}

func mergeDays(existing, incoming []DayPlan) []DayPlan {
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]DayPlan(nil), existing...)
	for _, d := range existing {
		if d.ID == "" {
			d.ID = StableID("day", d.Date, d.Title)
		}
		seen[d.ID] = true
	}
	for _, d := range incoming {
		if d.ID == "" {
			d.ID = StableID("day", d.Date, d.Title)
		}
		if seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		out = append(out, d)
	}
	return out
}

func mergeDecisions(existing, incoming []Decision) []Decision {
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]Decision(nil), existing...)
	for _, d := range existing {
		if d.ID == "" {
			d.ID = StableID("decision", d.Title, d.DueAt)
		}
		seen[d.ID] = true
	}
	for _, d := range incoming {
		if d.ID == "" {
			d.ID = StableID("decision", d.Title, d.DueAt)
		}
		if seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		out = append(out, d)
	}
	return out
}

func mergeEvidence(existing, incoming []EvidenceRef) []EvidenceRef {
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]EvidenceRef(nil), existing...)
	for _, e := range existing {
		if e.ID == "" {
			e.ID = StableID("evidence", e.Source, e.Provider, e.URL, e.CheckedAt.String())
		}
		seen[e.ID] = true
	}
	for _, e := range incoming {
		if e.ID == "" {
			e.ID = StableID("evidence", e.Source, e.Provider, e.URL, e.CheckedAt.String())
		}
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out
}

func mergeActions(existing, incoming []ActionItem) []ActionItem {
	seen := make(map[string]bool, len(existing)+len(incoming))
	out := append([]ActionItem(nil), existing...)
	for _, a := range existing {
		if a.ID == "" {
			a.ID = StableID("action", a.Type, a.Title, a.RelatedID)
		}
		seen[a.ID] = true
	}
	for _, a := range incoming {
		if a.ID == "" {
			a.ID = StableID("action", a.Type, a.Title, a.RelatedID)
		}
		if a.Status == "" {
			a.Status = "open"
		}
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	return out
}
