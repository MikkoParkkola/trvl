#!/usr/bin/env bash
# wizzair-discover-version.sh — discover Wizz Air's current API version path.
#
# Why this exists (#430): Wizz rotates the {version} segment in
# be.wizzair.com/{version}/Api/... with no announcement. When it rotates, the
# old path 404s and every Wizz search fails (ErrWizzVersionRotated). Bumping the
# version was a manual DevTools chore; this automates the discovery half.
#
# The oracle: GET be.wizzair.com/<v>/Api/asset/culture behind CloudFront returns
#   - 404            -> that version path does NOT exist (rotated away / future)
#   - 200 or 405     -> that version path EXISTS (405 = route present, wrong verb)
#   - 403/429/5xx    -> inconclusive (edge throttle / transient); do NOT act
# Verified reachable from datacenter IPs (no residential vantage needed), so this
# runs in plain CI. It proves the version PATH is live; it cannot prove a priced
# search works (the timetable endpoint edge-blocks datacenter IPs) — but a live
# path is exactly what clears the 404 rotation failure.
#
# Output (stdout, last line is machine-readable):
#   STATUS=ok        CURRENT=<v>                 -> current still live, no change
#   STATUS=rotated   CURRENT=<v> NEW=<w>         -> rotated; <w> is the new live version
#   STATUS=exhausted   CURRENT=<v>                 -> rotated but no candidate found in range
#   STATUS=inconclusive CURRENT=<v>              -> probes blocked/throttled; try later
# Exit codes: 0 ok | 10 rotated | 2 unknown | 3 inconclusive | 1 usage error
set -euo pipefail

UA="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
HOST="https://be.wizzair.com"

# probe <version> -> echoes live|absent|inconclusive
probe() {
	local v="$1" code
	code="$(curl -s -o /dev/null -m 12 -A "$UA" -w '%{http_code}' "$HOST/$v/Api/asset/culture" 2>/dev/null || echo 000)"
	case "$code" in
		404) echo absent ;;
		200|405) echo live ;;
		*) echo inconclusive ;;  # 403/429/5xx/000 -> transient, not a verdict
	esac
}

# Resolve current version: CLI arg, else WIZZAIR_API_VERSION, else the source const.
current="${1:-${WIZZAIR_API_VERSION:-}}"
if [ -z "$current" ]; then
	src="$(dirname "$0")/../internal/flights/wizzair.go"
	current="$(grep -oE 'wizzDefaultVersion = "[0-9]+\.[0-9]+\.[0-9]+"' "$src" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
fi
[ -n "$current" ] || { echo "STATUS=usage (could not resolve current version)"; exit 1; }

IFS=. read -r MA MI PA <<<"$current"

# Is the current version still live?
case "$(probe "$current")" in
	live) echo "STATUS=ok CURRENT=$current"; exit 0 ;;
	inconclusive) echo "STATUS=inconclusive CURRENT=$current"; exit 3 ;;
	absent) : ;;  # rotated — fall through to discovery
esac

# discover_from_config -> echoes a version, or nothing.
#
# Mirrors wizzDiscoverFromConfig in internal/flights/wizzair_selfheal.go: the
# homepage embeds its API base URL in an inline bootstrap config, so Wizz
# publishes the answer in plain HTML. That matters because the candidate walk
# below is blind to a whole class of rotation (see its comment), and #641 was
# exactly that class. Extraction lives in scripts/ci/wizzair-config-version.sh,
# which is tested against fixtures; this function only supplies the page.
#
# The result is a claim, never a verdict: nothing is reported until probe()
# confirms it against the live host.
discover_from_config() {
	# -L because a silent locale or canonicalization redirect would otherwise
	# yield an empty body and drop the sentinel back into the blind walk this
	# exists to bypass -- the runtime client (wizzClient.Do) follows redirects too.
	curl -sL -m 20 -A "$UA" "https://www.wizzair.com/en-gb" 2>/dev/null \
		| bash "$(dirname "$0")/ci/wizzair-config-version.sh"
}

# Declared before the config probe so a throttled edge there is carried into the
# final verdict. Discarding it reported STATUS=exhausted -- "rotated, and nothing
# is live" -- when the truth was "we could not tell", and exhausted is the status
# that files an issue at a human.
saw_inconclusive=false

config_version="$(discover_from_config || true)"
if [ -n "$config_version" ] && [ "$config_version" != "$current" ]; then
	case "$(probe "$config_version")" in
		live) echo "STATUS=rotated CURRENT=$current NEW=$config_version"; exit 10 ;;
		inconclusive) saw_inconclusive=true ;;
	esac
fi

# Discovery walk, most-likely first. Fallback only: it runs when the config read
# failed, named the stale version, or named one that did not probe live.
#
# History (10.1.0 -> 29.3.0 -> 29.4.0) shows minor +1 is the common rotation,
# with occasional larger jumps. Bounded probes. Note the blind spot this cannot
# close: shifted minors are probed at patch .0 only, so a rotation to X.Y.Z with
# Z > 0 on a new minor is unreachable here however wide the range. That is #641
# (29.15.0 absent, 29.15.1 live) and the reason config discovery runs first
# rather than as a second fallback. Widening the walk would not have found it.
candidates=()
for d in 1 2 3 4 5 6; do candidates+=("$MA.$((MI+d)).0"); done   # next minors
for d in 1 2 3;       do candidates+=("$MA.$MI.$((PA+d))"); done    # next patches
candidates+=("$((MA+1)).0.0" "$((MA+1)).1.0" "$((MA+2)).0.0")        # next majors

for c in "${candidates[@]}"; do
	case "$(probe "$c")" in
		live) echo "STATUS=rotated CURRENT=$current NEW=$c"; exit 10 ;;
		inconclusive) saw_inconclusive=true ;;
	esac
	sleep 1  # be polite to the edge
done

if $saw_inconclusive; then
	echo "STATUS=inconclusive CURRENT=$current"; exit 3
fi
echo "STATUS=exhausted CURRENT=$current"; exit 2
