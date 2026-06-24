#!/usr/bin/env bash
# Install a git post-merge hook that lazily refreshes the GitNexus index in the
# background after every pull/merge (the "main advanced" event). The hook is a
# no-op when the index is already fresh, and never blocks git because it
# backgrounds the work. Existing non-gitnexus hooks are backed up, not clobbered.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
hook="$root/.git/hooks/post-merge"

if [ -f "$hook" ] && ! grep -q gitnexus "$hook"; then
  cp "$hook" "$hook.pre-gitnexus.bak"
  echo "backed up existing post-merge hook -> post-merge.pre-gitnexus.bak"
fi

cat > "$hook" <<'EOF'
#!/usr/bin/env bash
# gitnexus: lazily refresh the code-intelligence index after a merge/pull.
root="$(git rev-parse --show-toplevel)"
nohup "$root/scripts/gitnexus/refresh.sh" >/tmp/gitnexus-refresh.log 2>&1 &
EOF
chmod +x "$hook"
echo "installed gitnexus post-merge hook (background, lazy refresh)"
