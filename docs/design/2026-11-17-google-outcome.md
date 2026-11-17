# #826 redesign, rev 6: one recorded Google outcome per lookup

Status: **implemented in pull request 826** (branch `fix/rooms-lookup`). The design was reviewed before the tests, and the tests were written before the code. The out-of-scope items below are tracked in #834.

Date: 2026-11-17

Base 949ddf3a. Scope is exactly #813's acceptance criteria: the room lookup's Google calls and the singular by-name lookup. Numbers in brackets are the lead's rev-6 items.

Out of scope, filed by the lead as one follow-up in the same family:
- the list-search status row for partial pages, its coalescing, and `GoogleRadiusApplied`;
- the plural `SearchHotelsByName`;
- SerpAPI's Google-worded notice;
- the prices command, reviews and amenity fetches.

`trvl hotels` output does not change in this PR.

## 1. Rule

A refusal matters only if it could have hidden data the lookup needs. Each lookup keeps one `googleOutcome`. After every Google call the caller merges that call's events, together with its own **settled** decision:

| Merge input | Outcome |
|---|---|
| settled by the caller | cleared |
| not settled, and the call holds a refusal | its strongest refusal is recorded (precedence below) |
| not settled, no refusal (answered empty, failed, or nothing sent) | unchanged |

The caller decides settled from the data it uses, whether or not a request went out [8]. A cached partner-price reply that is used therefore settles. Settled is never stored in a `googleCall` and never shared.

- Rooms settles only on Google-sourced data for the target [1]:
  - entity-page rooms;
  - partner prices from the price RPC, cached or live;
  - relay rates;
  - in the search fallback, a matched row whose price comes from a `google_hotels` source.

  A matched row with Google's ID but Booking's price does not settle.
- By-name settles when the target's Google ID is found, or when a property-scoped search answers live. "Property-scoped" means the targeted search and the name-bearing candidates; that counting is decided (a) [9]. A live answer settles even without the hotel, but only when both hold:
  - the search's query contains the original query's locality: its last comma part, or, with no comma, the whole query itself [2];
  - that search's `googleCall` holds no refusal on any page or sort [3].

## 2. Mechanism

- `googleCall` is the frozen, ordered event list of one Google call or one search run: answered with rows, answered empty, `refused(err)`, `failed(err)`. One goroutine writes it, the search's own, and `searchHotelsCore` makes its page and sort requests sequentially (the sort loop at search.go:287 and the page loop at :331). Every page and sort request is recorded.
- Classification happens once, in one function, using `ClassifyProviderError`. Sites hand over raw errors.
  - A refusal that lacks the sentinel (`ErrBlocked`, a bare 503) is wrapped as `fmt.Errorf("%w: %w", ErrRateLimited, err)`, keeping the original error [4].
  - Precedence: a cooldown-armed refusal (`errors.Is(err, batchexec.ErrRefused)`, which carries the retry clock) beats a plain one and is never replaced by one. Between equals, the newer one wins.
- Singleflight [5]: the shared value is an unexported struct `{result, call}` inside package hotels. A joiner merges the run's `call` on its own goroutine. The exported `SearchHotels` and `SearchHotelsWithClient` unwrap the struct and keep their signatures. models does not change.
- Cancellation [6]: singleflight detaches the run from the caller (`DetachedWithin`), so the shared call has no cancellation event. A caller whose own context ends gets `ctx.Err()`, which merges as `failed` and never clears anything.

## 3. Sites (from `rg` at 949ddf3a)

- Rooms, one outcome:
  - R1 entity page: rooms.go:113, :159.
  - R2 price RPC: :163.
  - R3 each fallback candidate, including its later pages through the child `googleCall`: rooms_helpers.go:365, :370; the pick happens at :384.
  - R4 the relay, `tryGoogleRoomDetail` (rooms.go:179; google_room_detail.go:169, :177) [7]. The relay returns its raw error, including its 503, and the rooms code merges it. Its own Google-worded notice goes away, so the single notice line, read after the relay, covers it.
  - rooms.go:176: the fallback replaces `hotelName` only with a non-empty one.
- `SearchHotelByName`, one outcome:
  - N1 candidates: search_by_name.go:374-383; the pick happens at :389.
  - N2 the targeted search: :418, :424.
  - N3 the returns: :440, :462.
  - Cancellation between candidates (:365) returns `errors.Join(ctx.Err(), outcome)`.
- `googleStatusErr` (search_scope.go:70) is deleted; the one classifier in section 2 handles every status it used to turn into an error.
- Cooldown marker, unchanged from rev 2: `googleCooldownErr` is used only where batchexec arms a cooldown.
  - C1: search GET 429.
  - C2: search GET 200 quota body.
  - C3: consent retry 429, or a quota body on the retry.
  - C4: entity page.
  - C5: price RPC.
  - C3': consent retry 403 or 503, which gets `ErrRateLimited` and no marker.
  - `googleCooldown` becomes `errors.Is(err, batchexec.ErrRefused)`.
- AST guard over internal/hotels non-test files. It fails on:
  - any assignment to `googleOutcome` or `googleCall` fields outside search_scope.go;
  - the name `googleStatusErr`;
  - an error built from a `ProviderStatus.Error` string;
  - a "refused (HTTP" refusal outside `googleCooldownErr`;
  - `providerRateLimitNotice("Google Hotels", …)` anywhere except the rooms.go notice line and serpapi_fallback.go:122, which is out of scope and listed by name.

## 4. Tests

- Merge and precedence table, over real errors:
  - every row of section 1;
  - clock then plain, and plain then clock (the clock stays);
  - two plain refusals (the newer wins);
  - `ErrBlocked` and bare-503 rows asserting `errors.Is(ErrRateLimited)` and that the original error is kept.
- Settle rules:
  - mixed source: Google ID with Booking's price does not settle, so the notice names Google [1];
  - a "Hotel Sacher" property answer without the locality does not settle [2];
  - an empty property answer whose page 2 refused does not settle [3];
  - a cached partner price that is used settles [8].
- Child call:
  - page 1 answered, then page 2 refused, with the target not found: the refusal is recorded;
  - the same with the target found on page 1: cleared.
- Singleflight:
  - two callers with different prior outcomes merge one run;
  - separate keys never share;
  - a caller whose context ends gets `failed`, and its prior refusal is kept.
- Real paths for C1-C5 and C3': 429, 200 quota body, 403, 503 and a cooldown skip, asserting the refusal class and whether the marker is set.
- Relay: a 503 is recorded, and rates after a refusal clear it.
- Per site R1-R4 and N1-N3, each with a sabotage check: bypass the outcome at that site and the test goes red.
- Guard: one negative fixture per banned pattern. A small source snippet that must trip the guard.
- Existing tests: `LaterGoogleAnswerClearsAnEarlierBlock` and the :470 test keep their current assertions (decision (a)).

Not changed: notice wording, CLI, MCP, batchexec, models, `trvl hotels` output, and the 403/503 "blocked" wording.

## 5. Rev 6 final (review folded in; no new round)

1. The single classifier takes `(status, err)`. batchexec returns a refusal as a status with a nil error, so a rate-limited HTTP status with a nil error becomes a refusal wrapped in `ErrRateLimited`. It gets no cooldown marker unless `ErrRefused` is present. This is the raw status from the client, not a rebuilt `ProviderStatus` string, so the guard ban stands. Tests stub status-with-nil-error at R1, R2 and R3 (#813's offline test).
2. Paired CLI and MCP assertions: the by-name cooldown case and the plain-refusal case each give the same sentence on both.
3. Singleflight tests prove callers still get independently mutable results after the envelope change (the clone at search.go:792).
4. Pinned: a name-bearing candidate (N1) cannot settle while its own pages or sorts carry a refusal.
5. Locality matching trims, case-folds and matches on whole tokens. Decided by the lead (review on 706aff4d): the locality is every word after the FIRST comma, and a query settles only if all of them appear in it as whole words. "Naoussa, Paros" needs both words, so "Paros" alone does not settle, and the comma-dropped candidate "Summer Shades hotel, Naoussa Paros" does.

## 6. Decisions after rev 6 (reviews on 706aff4d and 738ac158)

1. `googleCooldownErr` reads the retry time with a probe through the batchexec client. The probe runs only where batchexec has just armed its cooldown: a 429, or a 200 quota body. There the client answers it locally and nothing leaves the machine. A quota body on any other status armed nothing, so it is a plain refusal with no probe and no cooldown marker.
2. A read-only batchexec accessor for the retry time would remove the probe. It widens batchexec's API, so it waits for the operator's approval; until then the probe stays.
3. The guard bans writes to the outcome's own fields, not to every field named `err`. `googleOutcome.err` is renamed `strongest`, and any write through `strongest`, `settled` or `events` outside google_outcome.go fails.
4. MCP's empty-rooms summary no longer repeats the notice. It gives a short reason and points to the notice, which travels in the same reply. This replaces "MCP" in the not-changed list above.
5. The CLI and MCP refusal tests read the sentences from one golden file that the hotels package checks against `SearchHotelByName`'s real output. The file is pinned to LF line endings and must hold exactly one cooldown case and one blocked case.
6. Room-lookup tests fail on any request their Google router does not route, the probe included.
