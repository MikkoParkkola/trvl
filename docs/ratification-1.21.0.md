# Ratification brief — trvl release/1.21.0

Brief ID: `trvl-1.21.0-readme-and-issue-sweep-2026-07-29`
Diff under ratification: the single unpushed commit on `release/1.21.0` — 11 files,
tests included (`git show --stat HEAD`). Nothing is pushed; the push is what
ratification releases.

---

## goal

Two things ship together.

**A rewritten README aimed at someone deciding whether to try trvl.** It now opens
with what trvl is and how it works, and the deep privacy and engineering material
moved into `docs/ARCHITECTURE.md`. Nothing was dropped in the move — the stealth
disclosure and the hotel-price caveat survive as prose rather than as their own
headings, and every section that existed before still has a home.

**A real security fix underneath it.** The room-lookup tool (`hotel_rooms`) used to
connect to whatever web address the caller handed it. Any client holding a
read-only token could point trvl's network connection at the machine it runs on,
at your private network, or at a cloud metadata service, and read the answer back.
The destination is now checked before the first connection is made, so a foreign
address gets no connection at all — not merely no credentials.

## scope

The room-lookup path in the hotel package, the cookie-permission helper it leans
on, README.md, and docs/ARCHITECTURE.md. Three new test files. No provider
behaviour changes, no dependency changes, no interface changes for MCP clients.

## non-goals

Deliberately not done in this change:

- **The remaining redirect risk.** If Booking.com itself redirects trvl to another
  host, trvl still follows it. Your session cookies are *not* leaked that way — Go's
  networking library refuses to carry them across a host change, verified in the
  library source. What remains is that the page contents of the redirect target
  come back to the caller. Fixing it means changing a network client shared by every
  provider, which is not a release-branch change. Tracked as issue #537 and disclosed
  in both README and ARCHITECTURE.
- **Fixing the other open bugs.** The sweep *triages*; it does not fix. See
  "what is still broken" below. This is the part of the third ask that is not done.
- **Rewriting the other docs** (PROVIDERS, CLI, COMPARISON) — untouched.

## tools/permissions

No new dependencies, no new network destinations, no new credential surface, no CI
changes. The change *removes* reachable network destinations rather than adding any.
Scratch files used by the review tooling were added to `.gitignore`, not deleted
(the repo is under a recovery freeze).

## evidence

### Tests, and proof they can fail

- `go test ./internal/cookies/ ./internal/hotels/ ./cmd/trvl/ ./internal/ground/ -count=1`
  → 4163 pass, 0 fail. `go build ./...` clean, `gofmt` clean.
- **Sabotage check on the destination pin.** The pin was deleted outright and the
  test re-run. It failed at exactly the claim it makes — `booking_rooms_origin_test.go:39`,
  *"the refused URLs still produced 1 request(s); the host check must run before the
  fetch, not after"*. That is the discriminating failure: it proves the check runs
  *before* the connection, not merely that it exists. Pin restored, both tests green.
- Three new test files: the destination pin, the cookie-permission table (13 cases
  including the `https://www.booking.com@evil.example/` trick), and a test that makes
  the README's "what trvl keeps on your machine" disclosure a checked claim rather
  than a remembered one.

### The redirect claim, verified rather than assumed

`GOROOT/src/net/http/client.go:1005-1021` — a `Cookie` header set explicitly on an
outgoing request is copied on redirect only when the destination is the same domain
or a subdomain. Booking.com → anywhere-else therefore strips it. This is read from
the library source, not inferred.

### Independent review

Five adversarial review rounds by a different frontier model, each returning
DO-NOT-SHIP on the next ring of the same problem: cookie transmission, then the
initial-address check, then the redirect hop, then a real bypass of the destination
check itself, then a second bypass of the fix for it. Every round's confirmed
findings are fixed below; the last round's verdict is DO-NOT-SHIP with its blocker
addressed and its stated mechanism corrected, not re-reviewed a sixth time.

**The fourth round found a live hole and it is now fixed.** Go's URL parser keeps
an IPv6 zone identifier inside the hostname, so `https://[::1%25.booking.com]/`
produced the hostname string `::1%.booking.com`. That ends in `.booking.com`, so it
passed the guard — while the network layer connected to loopback. I reproduced it
before changing anything (`allowed=true` for both a loopback and a mapped-IPv4
form). The guard now additionally requires the host to be an ordinary DNS name.
Sabotage-checked: neutralising that one clause fails exactly the two new cases,
`ipv6_zone_id_smuggling_loopback` and `ipv6_zone_id_smuggling_mapped_v4`.

Two review findings about the documentation were also correct and are fixed: the
architecture page still claimed non-Booking URLs "are still fetched" (no longer
true — they are refused before connecting), and both pages stated a same-site
guarantee stronger than the code enforces across redirects. Both now describe the
redirect boundary accurately, including which half of it the standard library
closes and which half remains at #537.

A third finding — that the cookie test could pass with browser cookies switched
off, proving nothing — was also correct. The test now carries a positive control
that fails if the gate withholds cookies from Booking.com itself.

### The fifth round, and a reviewer claim that did not survive checking

A scoped confirmation pass over the fixes above returned DO-NOT-SHIP once more, on
a host written with **fullwidth look-alike characters** — `：：１％.booking.com`. It
carries none of the ordinary punctuation the new guard looks for, so it walked past
the check and matched `.booking.com`. That part was true and is now fixed: the guard
requires an ordinary-ASCII hostname, which every real Booking.com address is.

The reviewer's *explanation* did not hold up. It claimed Go would quietly fold those
characters into `::1%` and connect to your own machine. I ran it rather than believing
it: Go **rejects** that character outright, and the address would have resolved to an
ordinary name underneath booking.com — never to your machine. So the guard had a real
hole in it, but not the hole described, and no route to loopback was ever demonstrated.
I am recording the correction because the fix reads like a confirmed exploit otherwise,
and it is not one. Sabotage-checked: disabling that one clause fails exactly the one
new case, `fullwidth_homoglyph_host`, and nothing else.

### Two claims the docs were making that the code does not enforce everywhere

The same review round was right that both pages promised more than one code path
delivers, and both are now narrowed to the truth:

- **The same-site promise was global; the check is not.** Only the room lookup accepts
  a web address from the caller, so only it checks the destination. The three rail
  providers send to addresses written into trvl's own source — there is nowhere else
  for those cookies to go, but that is safety by construction rather than by check, and
  the docs now say so, including the condition under which it would stop being true.
- **The redirect protection has a hole the earlier wording hid.** Go refuses to carry
  cookies to a different host, but it compares hosts and ignores `https` versus `http`.
  A site redirecting its own secure address to an insecure one would keep the cookies,
  putting a session on the wire unencrypted. Now disclosed in both README and
  ARCHITECTURE rather than implied away.

A fourth test finding was also correct and is fixed. `TestProvidersDoNotSendBrowserCookiesDirectly`
accepted either cookie wrapper on the line that actually transmits, including the one
with no destination check — so the test would have passed against the very bug it is
named for. It now requires the destination-checked wrapper on that line. Sabotage-checked:
downgrading the Booking path to the weaker wrapper fails it at that assertion.

### Issue sweep — and a correction to it

The sweep closed issue **#527** (`trvl share` publishes a public gist without saying
so) and I **reopened it**. The fix is on `release/1.21.0` only; `main` still creates
a public gist today. The operator's criterion was "a fix already merged", and against
the default branch it is not merged. #527 closes when the release merges. A comment
on the issue records exactly that, so the reopen is not mysterious to anyone reading
later.

This matters beyond one issue: the sweep was triaging against the release branch,
which is 52 commits ahead of `main`. Every other verdict in it carries the same
caveat, which is why the second pass judges each issue explicitly against `main`.

### What is still broken (not fixed by this change)

Reported, not repaired. Each verdict below was reached by reading code against
`main`, not by trusting the issue text.

**Fixed on this branch only — same shape as #527, closes on merge:**

- **#536** — a webhook address containing a Slack/Discord secret was written to the
  log where anyone reading logs could see it. Fixed here (commit `fe02da1`), still
  live on `main`. Two tests assert on the log record itself, not on the helper, so
  they would catch a regression.

**Still open on both branches:**

- **#512** — price-watch storage has no cross-process lock; two writers can lose or
  corrupt each other's data.
- **#531** — journey-bearing web addresses still reach the log at eleven other
  places. Lower stakes than #536 (no secret in them), same class of leak.
- **#529** — the browser-cookie fallback silently returns nothing for three rail
  providers, and the error is discarded rather than reported, so those searches
  quietly degrade with no signal.
- **#534** — the saved cookie file records no origin, so the browser-cookie opt-out
  still has to refuse the whole file rather than just the browser-derived entries.
- **#533** — a genuinely flaky test, reproduced failing 24 times out of 24 under
  load. It is a test-design defect, not a product bug.
- **#532** — no security-scanner job in CI. Worth correcting the alarming issue
  title: of the findings, the two real ones are **not remotely reachable** (a backup
  filename built from a local preference, and an unbounded copy that only runs after
  a signature check passes). The gap is the missing gate, not 22 live holes.
- **#535**, **#513**, **#506**, **#509**, **#510**, **#511**, **#514**, **#528** —
  design/feature work and lower-severity items; full detail in the sweep output.

### One triage verdict was wrong, and why

The agent triaging **#537** reported the destination pin as missing. It had read the
file during the forty seconds the pin was deleted for the sabotage check above. The
pin is present at `booking_rooms.go:62` and both tests pass. I am flagging this rather
than quietly dropping it: it is a reminder that an automated verdict is only as good
as the tree it read, and this one raced against my own edit.

## stopping-condition

**Ratify** = ship the README rewrite and the room-lookup fix as briefed, with the
listed bugs staying open and #537 disclosed rather than fixed.

**Redirect** = anything above is the wrong call.

## escalation

Three things are worth your attention specifically, in RED:

1. **A public issue was closed against unmerged code and I reopened it.** Reopening
   is visible on a public repo. If you would rather it stayed closed on the strength
   of the release branch, say so and I will re-close with a note.
2. **"Make sure all bugs are fixed" is not satisfied, and cannot be here.** Of the
   eighteen open issues, one security bug (#536, the logged webhook secret) is fixed
   on this branch and reaches users when it merges. The rest are triaged, not fixed.
   I did not fold them in because each is a separate change with its own blast radius,
   and adding them to a branch that just passed adversarial review would invalidate
   that review. If you want any of them in before shipping, that is a redirect — #512
   (storage corruption) is the one I would argue for.
3. **#537 ships disclosed, not fixed.** The narrow risk that remains needs an attack
   surface on Booking.com's own servers to exploit.
4. **The independent gap-check did not report, and my own account is standing in for
   it.** The process that is supposed to list, independently of me, the decisions I
   made that you never asked for, was launched and never returned. Everything in this
   brief is therefore my self-report. That is exactly the arrangement the process
   exists to avoid, so treat the list of judgement calls here as unaudited. I would
   rather tell you that than let silence read as a clean bill.
5. **Each review round found the previous fix incomplete, five times running.** Three
   of those five were genuine holes in the check itself. I stopped after a scoped
   confirmation pass rather than a sixth open-ended round, because each round has been
   finding a narrower variant of the same idea and the last one's reasoning was wrong
   on the mechanics even where its finding was right. That is a judgement call about
   when to stop looking, and it is yours to overrule.

## budget

Blast radius of shipping now is small and the change is cheaply reversible — it is a
docs rewrite plus one guard clause and three test files, all revertable with a single
commit revert. Time-to-detect for a regression is immediate: the destination pin and
the cookie gate each have a named test that fails when the guard is removed, and the
disclosure test fails the moment a new file starts writing under `~/.trvl` without
being disclosed. The cost of *not* shipping is that the room-lookup hole stays open on
a branch that is otherwise ready.
