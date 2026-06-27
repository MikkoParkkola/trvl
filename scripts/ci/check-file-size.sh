#!/usr/bin/env bash
# Keep tracked text files reviewable. Append-only public history files may exceed
# the default line ceiling only when they have an explicit, justified allowlist.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

max_lines="${TRVL_MAX_TEXT_FILE_LINES:-800}"
allowlist=".github/loc-allowlist.txt"
fail=0

trim_line() {
  printf '%s\n' "$1" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//; s/\r$//'
}

allowlist_path() {
  printf '%s\n' "$1" | sed -E 's/[[:space:]]+#.*$//'
}

is_allowlisted() {
  local target=$1 line path
  [ -f "$allowlist" ] || return 1
  while IFS= read -r line; do
    line="$(trim_line "$line")"
    if [ -z "$line" ] || [[ "$line" == \#* ]]; then
      continue
    fi
    path="$(allowlist_path "$line")"
    if [ "$path" = "$target" ]; then
      return 0
    fi
  done < "$allowlist"
  return 1
}

is_binary() {
  case "$(file --mime "$1")" in
    *charset=binary*) return 0 ;;
    *) return 1 ;;
  esac
}

line_count() {
  wc -l < "$1" | tr -d '[:space:]'
}

if [ -f "$allowlist" ]; then
  while IFS= read -r line; do
    line="$(trim_line "$line")"
    if [ -z "$line" ] || [[ "$line" == \#* ]]; then
      continue
    fi

    path="$(allowlist_path "$line")"
    if [ "$path" = "$line" ] || [[ ! "$line" =~ [[:space:]]#[[:space:]]reason:[[:space:]].{15,} ]]; then
      printf '%s: allowlist entries must use: path # reason: specific justification\n' "$allowlist" >&2
      fail=1
      continue
    fi

    if ! git ls-files --error-unmatch "$path" >/dev/null 2>&1; then
      printf 'error: file-size allowlist references a missing tracked file: %s\n' "$path" >&2
      fail=1
    fi
  done < "$allowlist"
fi

checked=0
allowed_oversized=0
blocked_oversized=0
while IFS= read -r -d '' path; do
  if is_binary "$path"; then
    continue
  fi

  checked=$((checked + 1))
  lines="$(line_count "$path")"
  if [ "$lines" -le "$max_lines" ]; then
    continue
  fi

  if is_allowlisted "$path"; then
    allowed_oversized=$((allowed_oversized + 1))
    printf 'ok: %s has %s lines (> %s) and is allowlisted\n' "$path" "$lines" "$max_lines"
    continue
  fi

  blocked_oversized=$((blocked_oversized + 1))
  printf 'error: %s has %s lines (> %s). Split it, or add a justified exception to %s.\n' "$path" "$lines" "$max_lines" "$allowlist" >&2
  fail=1
done < <(git ls-files -z)

if [ "$blocked_oversized" -eq 0 ]; then
  printf 'ok: tracked text files respect the %s-line policy (%s checked, %s oversized allowlisted)\n' "$max_lines" "$checked" "$allowed_oversized"
fi

exit "$fail"
