# Watch store: cross-process coordination gap

Status: **partially fixed 2026-08-04 (#553).** The cross-process file-level
lock described as option 1 below now wraps every hot-path mutation (`Add`,
`Remove`, `UpdateWatch`, `UpdateWatchAndRecordPrice`, `RecordPrice`,
`PurgeHistory`, `RecordObservation`) -- the multi-watch loss scenario in
"What is not safe" below no longer happens. What remains open: same-record
field merge (a concurrent edit to one watch's own fields can still be
reverted by a wholesale in-memory replace -- tracked as #561), and `Migrate`
takes the lock without reloading first (#562). See PR closing #553 for the
full closed/partial/open split. Left the rest of this doc as-is below for
history; do not re-read "not safe" as still describing today's behavior for
the multi-watch case.
Date: 2026-07-26 (status updated 2026-08-04)

## What is safe today

`internal/watch` persists via `atomicjson`, so a reader never sees a torn file.
Within one process, `Store`'s mutex serialises mutations.

## What is not safe

**(Pre-#553 behavior; the multi-watch case below is now fixed -- see status
above. Kept verbatim as the historical record of the failure this doc was
written to document.)**

The store is **last-writer-wins across processes**. Every `trvl mcp` process
holds its own in-memory copy of the whole store and persists by rewriting both
files completely (`Store.persistLocked`).

MCP clients spawn one server process per session, and some leak them — 15
orphaned `trvl mcp` processes were observed alive simultaneously on one machine.
So multi-process is the normal case, not an edge case.

Failure sequence:

1. Process A loads the store: `{W1}`.
2. Process B loads the store: `{W1}`.
3. B adds `W2` and persists: disk is `{W1, W2}`.
4. A adds `W3` and persists its own snapshot: disk is `{W1, W3}`.
5. **`W2` is gone.** No error, no torn file, no warning.

Atomic rename guarantees the file is never half-written. It does nothing about
one process overwriting another's committed state.

The same hazard exists within a single process between a scheduler round and a
concurrent tool call: a check round takes detached `Watch` copies, and
`UpdateWatch` replaces the whole record, so a `RenewedAt` refresh or a threshold
change made mid-round can be reverted when a later check writes its stale copy
back.

Both predate the 2026-07-26 changes.

## Why batching was reverted

An earlier revision of that work added `BeginBatch`/`EndBatch` and wrapped each
scheduler round in a batch, so a round wrote once instead of once per watch.

That is correct in isolation and wrong here: it widens the window above from
milliseconds to the length of a whole check round (minutes), during which the
scheduler holds a stale snapshot that it then writes over everything. A watch
created by another process during the round would be silently deleted.

The trade did not justify it. The other four fixes already removed essentially
all of the write volume:

| | whole-file rewrites per 30-min round |
|---|---|
| Before | 468 x 39.3MB = **17.96 GB** |
| After dedup + history cap + scheduler singleton | 5 x 6.1MB = **31 MB** (99.83% less) |
| Batching would add | 1 x 6.1MB = 6 MB (a further **25 MB**) |

Daily: ~862 GB/day to ~1.44 GB/day without batching. Batching bought a further
~1.2 GB/day in exchange for a multi-minute data-loss window.

## What a real fix looks like

Batching is fine once store mutations are coordinated. Rough shape, in
increasing order of cost:

1. **Advisory file lock around the whole read-modify-write cycle.** The lock
   primitive already exists (`internal/watch/lock.go`, flock / LockFileEx,
   released by the OS on process death). Every mutation would take it, reload,
   apply, write, release. Simple and correct; costs a lock round-trip per
   mutation and needs care to avoid holding it across network calls.
   **Shipped 2026-08-04 (#553)** via `withTxnLocked`, wired into the mutators
   listed in the status note above. Closes the multi-watch loss scenario in
   "What is not safe"; does not by itself close same-record field merge
   (#561) or Migrate's stale-read (#562) -- those still need option 2's
   approach applied narrowly to those two remaining call sites.
2. **Field-level merge on write.** Reload before persisting and merge by watch
   ID rather than overwriting wholesale. Avoids blocking but needs an explicit
   conflict rule per field (newest `RenewedAt` wins, highest `BaselinePrice`
   wins, and so on).
3. **Move the store off whole-file JSON.** An append-only log or embedded KV
   removes whole-file rewrites entirely and makes both the batching question and
   the history cap moot.

Only after one of those lands should batching be reconsidered.

## Provenance

Adversarial review 2026-07-26 (`gpt-review`, codex/gpt-5.6-sol), verdict
DO-NOT-SHIP on the batched revision. Findings 1 and 2 (cross-process loss,
within-process stale copies) were accepted; batching was reverted rather than
patched, and the remaining four fixes shipped independently.
