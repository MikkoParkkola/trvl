# Traveller Painpoint Roadmap Follow-up

Continues [2026-05-13-traveller-painpoint-roadmap.md](2026-05-13-traveller-painpoint-roadmap.md).

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
