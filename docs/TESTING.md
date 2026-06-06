# Testing

This repo treats test coverage as a release gate, not a vanity metric. Use this
matrix when changing behaviour or before claiming Definition of Done.

## Local Matrix

| Test type | Command | Notes |
| --- | --- | --- |
| Unit and regression | `go test -short ./...` | Default local pass for pure logic, command helpers, MCP schemas, parsers, and storage. |
| Race and coverage | `go test -short -p=1 -race -coverprofile coverage.out ./...` | Mirrors CI's low-parallelism path for constrained runners. |
| Coverage summary | `go tool cover -func=coverage.out` | Use the `total:` row in handoffs. |
| Full integration | `TRVL_LIVE_INTEGRATION=1 go test ./...` | Runs tests guarded by `testutil.RequireLiveIntegration`; requires network and may hit provider rate limits. |
| E2E CLI smoke | `go test -short ./cmd/trvl ./mcp` | Covers CLI/MCP wiring and user-facing tool surfaces without live providers. |
| Fuzz seed corpus | `go test ./internal/nlsearch -run FuzzHeuristicKeepsStructuredFieldsValid` | Runs fuzz seeds as a normal test; use `-fuzz=FuzzHeuristicKeepsStructuredFieldsValid` for deeper campaigns. |
| Chaos | `go test ./internal/chaos ./internal/providers -run 'Chaos|Circuit|Timeout|Retry'` | Exercises deterministic failure injection, provider cooldown, timeout, and retry paths. |
| UAT-style travel workspace | `go test -short ./internal/trips ./internal/imports ./internal/evidence ./internal/itinerary ./internal/fareintel ./mcp -run 'Workspace|Reservation|Booking|Evidence|Itinerary|Fare'` | Proves the user workflow from import to booking-readiness remains wired. |

## Coverage Expectations

- Add positive, negative, and corner-case tests for every changed behaviour.
- Add regression tests for every bug fix before or alongside the fix.
- Use fuzz/property-style tests for untrusted free-text input, date parsing,
  price parsing, schema validation, and trip import/merge logic.
- Use `httptest` or injectable functions for command and integration coverage;
  tests must not require production secrets or live bookings.
- Record exact skipped or blocked evidence when local disk, network, provider
  rate limits, or credentials prevent a full run.

## CI

`.github/workflows/ci.yaml` runs `go test` with race detection and coverage.
The local low-parallelism command above is the closest reproduction path for
machines with limited disk or memory.
