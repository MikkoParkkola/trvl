#!/usr/bin/env bash
# Keep the repository Go-first. Python is allowed only when it is explicitly
# justified in .github/python-allowlist.txt, so accidental helper scripts do not
# become part of production or CI by default.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

allowlist=".github/python-allowlist.txt"
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
      printf 'error: Python allowlist references a missing tracked file: %s\n' "$path" >&2
      fail=1
    fi
    case "$path" in
      *.py) ;;
      *)
        printf 'error: Python allowlist entry is not a .py file: %s\n' "$path" >&2
        fail=1
        ;;
    esac
  done < "$allowlist"
fi

python_count=0
while IFS= read -r path; do
  python_count=$((python_count + 1))
  if ! is_allowlisted "$path"; then
    printf 'error: tracked Python file is not allowlisted: %s\n' "$path" >&2
    printf '       Go is the default language for this repo. Add a Go implementation, or document why Python is truly the best tool in %s.\n' "$allowlist" >&2
    fail=1
  fi
done < <(git ls-files '*.py' ':!:vendor/**' ':!:third_party/**')

if [ "$python_count" -eq 0 ]; then
  printf 'ok: no tracked Python files\n'
else
  printf 'ok: tracked Python files are explicitly allowlisted\n'
fi

exit "$fail"
