#!/usr/bin/env bash
set -euo pipefail

# Verifies that compose.demo.yaml maintains strict isolation from private/NAS
# host mounts, enforces resource bounds, and keeps Worker/DB networks internal.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FILE="$ROOT/compose.demo.yaml"

grep -q '^name: 101conv-demo$' "$FILE"
grep -q 'DEMO_MODE: "true"' "$FILE"
grep -q 'AUTOMATION_MOUNT_ROOTS: ""' "$FILE"
grep -q 'demo-core-db:' "$FILE"
grep -q 'demo-worker-db:' "$FILE"
grep -q 'CONVERSION_TIMEOUT:.*90s' "$FILE"
grep -q 'DEMO_ALLOWED_PROFILES:' "$FILE"

if grep -qE '/mounts|AUTOMATION_HOST_PATH|docker\.sock|privileged:\s*true|devices:' "$FILE"; then
  echo "FAIL: compose.demo.yaml must not expose host mounts, Docker socket, privileged mode, or GPU devices" >&2
  exit 1
fi

for svc in core web worker; do
  block="$(awk -v svc="$svc" '
    $0 == "  " svc ":" { in_svc = 1; next }
    in_svc && ($0 ~ /^  [a-z]/ || $0 ~ /^volumes:/ || $0 ~ /^networks:/) { in_svc = 0 }
    in_svc { print }
  ' "$FILE")"
  if ! printf '%s\n' "$block" | grep -q 'read_only: true'; then
    echo "FAIL: service ${svc} in compose.demo.yaml missing read_only: true" >&2
    exit 1
  fi
  if ! printf '%s\n' "$block" | grep -q 'cap_drop: \[ALL\]'; then
    echo "FAIL: service ${svc} in compose.demo.yaml missing cap_drop: [ALL]" >&2
    exit 1
  fi
done

echo "Public demo Compose isolation contract passed."
