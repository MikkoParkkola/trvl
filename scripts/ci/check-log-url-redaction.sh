#!/usr/bin/env bash
# Keep user-journey URLs out of logs.
#
# On this codebase a full URL is user data. Rail and hotel search URLs carry
# origin, destination, travel date and passenger details in their query string,
# and a webhook URL carries the shared secret in its path for the common
# providers (Slack, Discord). A user who runs with debug logging, attaches logs
# to a bug report, or ships logs anywhere central discloses their travel plans
# (trvl#531).
#
# Twelve sites were found by hand. Without a guard the list regrows, because the
# leaking form is also the obvious one to write.
#
# The rule: a slog call with a URL-shaped key must pass its value through
# internal/logredact, which replaces the URL with a stable fingerprint
# ("url#abc123"). The fingerprint keeps log lines correlatable -- the same URL
# logs the same token -- without disclosing any part of the content.
#
# Why a fingerprint rather than a host-only reduction: #530 rounds 5-7
# established that no character-level reduction of a URL is provably
# non-sensitive, and its logHost helper was deleted rather than promoted. A
# hostname is attacker-influenced free-form text (url.URL.Host retains IPv6 zone
# identifiers), so keeping one would need a written justification per site.
# Fingerprinting sidesteps that argument entirely by keeping nothing.
#
# ---------------------------------------------------------------------------
# WHY THIS IS PER-FIELD AND NOT PER-LINE (trvl#531, corrected 2026-08-06)
#
# The first version skipped an entire slog line if the text "logredact."
# appeared anywhere on it. That made the guard unable to fail against the very
# shape it exists to catch: a line that redacts one field and leaks another.
#
# It was not hypothetical. internal/providers/enrichment.go redacted its "url"
# field and passed err.Error() raw in the same call -- and because client.Do
# wraps transport failures in a *url.Error whose Error() embeds the full request
# URL, the booking URL reached the log through the sibling field. This script
# reported "ok: 9 URL-keyed log site(s) redacted" and exited 0 against it.
#
# A guard that grades a line clean because some OTHER part of it was handled is
# not a weak guard, it is a decorative one. Each field is now judged on its own.
#
# SECOND RULE, deliberately narrow: on a line that already logs a URL-shaped
# key, a raw error value is flagged too. The original scope note (below) is
# still right that flagging EVERY logged error would cry wolf -- file and parse
# errors never touch a URL. But an error logged next to a URL is overwhelmingly
# the error FROM fetching that URL, which is exactly the *url.Error case. That
# co-occurrence is the high-signal subset, and it is the one that bit us.
#
# Scope, unchanged: URL-shaped KEYS, plus error values sharing a line with one.
# Errors logged anywhere else remain the call site's judgement via logredact.Err.
# ---------------------------------------------------------------------------
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0

# Field-level analysis. For each candidate slog line, look at the value that
# immediately follows each URL-shaped key, and separately at any raw error value
# on a line that carries such a key.
#
# Perl rather than a case-glob because this needs per-field granularity, and
# reaching for the pattern that reads the whole line as one string is precisely
# what produced the defect described above.
analysis=$(
  git grep -n -E 'slog\.(Debug|Info|Warn|Error)\(' -- '*.go' ':!:*_test.go' ':!:vendor/**' ':!:third_party/**' \
    | grep -E '"[a-z_]*url"' \
    | perl -ne '
      next unless /^([^:]+):(\d+):(.*)$/;
      my ($file, $lineno, $line) = ($1, $2, $3);
      my $bad = 0;

      # Rule 1: every URL-shaped key must be followed by a logredact call.
      while ($line =~ /"[a-z_]*url"\s*,\s*([^,)]*)/g) {
        my $value = $1;
        next if $value =~ /logredact\./;
        print "$file:$lineno:URLKEY:$value\n";
        $bad = 1;
      }

      # Rule 2: a raw error value on a line that also logs a URL.
      while ($line =~ /"(?:err|error)"\s*,\s*([^,)]*)/g) {
        my $value = $1;
        next if $value =~ /logredact\./;
        print "$file:$lineno:ERRVAL:$value\n";
        $bad = 1;
      }
      1;
    ' || true
)

checked=$(
  git grep -n -E 'slog\.(Debug|Info|Warn|Error)\(' -- '*.go' ':!:*_test.go' ':!:vendor/**' ':!:third_party/**' \
    | grep -c -E '"[a-z_]*url"' || true
)

while IFS=: read -r file lineno kind value; do
  [ -n "$file" ] || continue
  fail=1
  if [ "$kind" = "URLKEY" ]; then
    printf 'error: %s:%s logs a URL-shaped value without redaction\n' "$file" "$lineno" >&2
    printf '       value: %s\n' "$value" >&2
    printf '       A URL here carries the user journey (origin, destination, dates, passengers) and\n' >&2
    printf '       sometimes a credential. Wrap it: logredact.URL(v).\n' >&2
  else
    printf 'error: %s:%s logs a raw error beside a URL\n' "$file" "$lineno" >&2
    printf '       value: %s\n' "$value" >&2
    printf '       net/http wraps transport failures in a *url.Error whose Error() embeds the full\n' >&2
    printf '       request URL, so redacting the url field alone still leaks it here.\n' >&2
    printf '       Wrap it: logredact.Err(err).\n' >&2
  fi
done <<< "$analysis"

if [ "$fail" -eq 0 ]; then
  printf 'ok: %d URL-keyed log site(s), every field checked individually\n' "$checked"
fi

exit "$fail"
