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
# Scope: URL-shaped KEYS only. Errors can also carry a URL -- net/http wraps
# every transport failure in a *url.Error whose Error() embeds the full request
# URL -- but flagging every logged error would fire on file and parse errors
# that never touch a URL. That is the "guard that cries wolf" the issue's kill
# criterion warns about, so error values are handled by logredact.Err at the
# call sites that need it rather than by this check.
set -euo pipefail

# The AST checker sees complete calls rather than physical lines, so multiline
# slog calls and slog.String("..._url", value) cannot bypass the guard.
exec go run ./scripts/ci/check-log-url-redaction
