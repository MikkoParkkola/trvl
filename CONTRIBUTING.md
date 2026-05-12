# Contributing to trvl

## Purpose

trvl is a travel MCP server and CLI: 41 MCP tools and 42 CLI commands stitched
together against flight, hotel, ground-transport, destination, and deals
providers. Pull requests are welcome from anyone — bug reports, missing
airlines, parser breakages, new providers, documentation. Because trvl depends
on a long tail of third-party Go libraries (cookie jars, HTTP clients, JS
runtimes, CLI scaffolding), we strongly favor upstream fixes over local
patches. When the upstream is healthy, the correct contribution almost always
lives there, not here.

## Upstream-First Rule (blocking)

If a bug or missing feature belongs in a dependency — `kooky`, `sobek`,
`utls`, `nab`, `cobra`, or any other module we import — the fix **must be
contributed to that project first**. Landing a patch upstream is the default
path. A trvl-local workaround is only acceptable when one of the following is
true:

1. The upstream project is abandoned (no release or merged PR in the last 12
   months, and no response to a filed issue within 2 weeks).
2. Expected reviewer round-trip is longer than 2 weeks and the fix blocks a
   shipping release of trvl.
3. The behavior is genuinely trvl-specific (for example, an adapter layer we
   own, or a configuration surface the upstream would reasonably refuse).

A short-term pin to a fork branch is acceptable while an upstream PR is in
flight, but it must come with two artifacts:

- A `replace` directive in `go.mod` pointing at the fork branch, accompanied
  by a comment of the form
  `// TODO(https://github.com/owner/repo/pull/NNNN): unpin when merged`.
- A matching `CHANGELOG.md` entry under an "Upstream pins" subsection listing
  the same PR URL.

Reviewers will reject PRs that introduce a `replace` directive without both
artifacts in place.

## License Compatibility Gate

trvl is licensed under PolyForm Noncommercial 1.0.0. That constrains what we
can accept inbound.

**We cannot accept** code copied or translated from projects licensed under
GPL (any version), AGPL, SSPL, BSL, CC-BY-SA, or any other license whose
terms are incompatible with PolyForm Noncommercial distribution. This
includes code lifted from blog posts or Stack Overflow answers where the
author has not clearly licensed the snippet permissively.

**We can accept** code from projects licensed under MIT, BSD (2- or 3-clause),
Apache-2.0, ISC, MPL-2.0, or the Academic Software License, provided the
origin is attributed.

When porting code from a permissive source:

- Add a comment at the top of the ported block:
  `// Ported from github.com/owner/repo, LICENSE-MIT`.
- Add an entry to `LEGAL.md` under the attribution section, listing the
  project, upstream commit or tag, and the license.
- Retain the upstream copyright notice if the original carried one.

When contributing **outbound** to one of our permissively-licensed
dependencies (for example, `kooky`, which is MIT), the contributed code is a
separate work and is licensed under the upstream's terms. Do not copy trvl's
PolyForm header into an upstream PR.

## Code Quality Gates

Every PR must pass the existing trvl quality gates. Run these locally before
asking for review:

```bash
GOTOOLCHAIN=go1.26.3 go vet ./...
GOTOOLCHAIN=go1.26.3 go test -short -race ./...
staticcheck ./...
govulncheck ./...
```

The Makefile wraps these as `make lint` and `make test` and pins the
toolchain; use the Make targets if your host `go` is older than 1.26.3.

CI enforces a 50% line-coverage threshold. New packages are expected to land
with coverage at or above that threshold; regressions in existing packages
will block merge.

## Testing Discipline

- Unit tests run offline. HTTP must be mocked via `httptest` or an injected
  client; no new test may open a network socket by default.
- Live-probe tests that hit real upstream endpoints must be gated behind the
  `TRVL_TEST_LIVE_PROBES=1` environment variable and must live in a file
  ending in `_probe_test.go` so `go test -short` skips them.
- Live integration tests that exercise full provider or MCP wiring must be
  gated behind `TRVL_TEST_LIVE_INTEGRATIONS=1`.
- Every new provider ships with at least three unit tests and one live
  probe:
  - `TestX_Success` — happy-path parsing against a recorded fixture.
  - `TestX_Error_Auth` — authentication or authorization failure.
  - `TestX_Error_Parse` — malformed response body or unexpected shape.
  - `TestLiveProbe_X` — single live call, guarded by the probe env var.
- Test names follow `TestX_Success`, `TestX_Error_<Cause>`, and
  `TestLiveProbe_<Name>`. Stick to that scheme; CI log-grep depends on it.

## Security and Legal

- Never commit API keys, OAuth tokens, cookies, or personal data. Tests that
  need credentials must read them from environment variables and skip when
  unset (`t.Skip` with a clear message).
- Do not add a provider whose terms of service require us to bypass
  authentication, CAPTCHA, device fingerprinting, or rate limits. If a
  provider uses a CAPTCHA service, we do not solve it — we either use the
  provider's official API or we do not integrate.
- Rate limiters on new providers must default to conservative values. See
  the per-provider table in `LEGAL.md` for the current ceilings; a new
  provider must have a row in that table before it ships.
- Any new built-in or optional data source gets an entry in the `LEGAL.md`
  provider table: name, license, ToS URL, default rate limit, whether an
  API key is required, and whether it is enabled by default.

## Commit Style

We use Conventional Commits:

```
type(scope): subject

Optional body explaining why the change was necessary, what alternatives
were considered, and any follow-up work required.

Co-Authored-By: Someone <someone@example.com>
```

Rules:

- `type` is one of `feat`, `fix`, `chore`, `docs`, `refactor`, `test`,
  `perf`, or `build`.
- `scope` is the top-level package or area (for example, `flights`,
  `hotels`, `mcp`, `ci`).
- Subject is 60 characters or fewer, lowercase, and in the imperative mood
  ("add", not "added" or "adds"). No trailing period.
- The body explains **why**, not **what**. The diff already shows what.
- When a commit was produced with AI assistance, include a `Co-Authored-By`
  trailer identifying the tool. The human author is still accountable for
  the change.

## Upstream PR Workflow

When the fix belongs upstream, follow this checklist before opening a trvl
PR that depends on it:

- [ ] Issue filed or located in the upstream repository.
- [ ] Branch created on your fork, named after the upstream issue number
      (for example, `fix/123-cookie-decrypt`).
- [ ] Local trvl pins to the fork branch via a `go.mod` `replace`
      directive with the `TODO(PR-URL)` comment described above.
- [ ] Tests pass against both the upstream `main` commit and the fork
      branch (document both runs in the PR description).
- [ ] PR submitted upstream, including any CLA or DCO signature the
      project requires.
- [ ] Upstream PR URL linked from the trvl PR description.
- [ ] Once the upstream PR is merged and released: bump trvl's `go.mod` to
      the merged tag, remove the `replace` directive, remove the
      `CHANGELOG.md` "Upstream pins" entry, and confirm `go mod tidy` is
      clean.

If the upstream refuses the patch or goes silent past the thresholds in the
Upstream-First Rule, document that in the trvl PR description and proceed
with the local workaround.

## Known Open Upstream Items

This list is not exhaustive; it seeds the current backlog so contributors
know where effort is already in flight.

- `kooky` — macOS v20 app-bound cookie decryption. Draft PR body lives at
  `/tmp/kooky-v20-pr-body.md`. If you pick this up, coordinate with the
  maintainer before filing to avoid duplicate work.
- `nab` — internal project, maintained by the same team. Contributions are
  direct commits against the canonical repository; the upstream-first rule
  still applies in spirit (fix in `nab` rather than wrapping around it in
  trvl), but there is no external review gate.

If you start work on a new upstream contribution, add it to this list in
the same PR.

## Reviewers and Maintainers

trvl currently has a single maintainer (Mikko Parkkola). All PRs require
maintainer review and a green CI run before merge. AI-assisted PRs are
welcome and common, but they must be reviewable by a human: keep diffs
focused, keep commit messages honest about scope, and do not submit changes
you cannot explain in review. "The agent wrote it" is not an acceptable
answer to a review question.

If you plan a large change (new provider, new MCP tool family, a refactor
that touches more than a handful of packages), open an issue first and get
rough agreement on the shape before you write the code. That is the single
biggest thing you can do to make your PR merge quickly.
