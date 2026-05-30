#!/bin/bash

# Standalone Restic Round-Trip Verification
#
# Proves the backup ENGINE independently of the running app — init, backup,
# list-snapshots, restore — against a throwaway temp repo.
#
# No app, no Docker, no auth required. Only restic on PATH.
#
# Usage:
#   bash testing/tests/core/backup-restic-standalone.sh
#
# Exit code: 0 = all steps passed, 1 = any step failed
#
# Security:
#   Password is written to a 0600 temp file and passed via RESTIC_PASSWORD_FILE.
#   The password never appears in process argv. Temp files are removed on exit.

set -euo pipefail

# ─── Colors ───────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $*"; }
log_error()   { echo -e "${RED}[FAIL]${NC} $*"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $*"; }

# ─── Setup ────────────────────────────────────────────────────────────────────

WORK_DIR="$(mktemp -d /tmp/capstan-restic-roundtrip-XXXXXX)"
RESTIC_REPO="${WORK_DIR}/repo"
DATA_DIR="${WORK_DIR}/data"
RESTORE_DIR="${WORK_DIR}/restore"
PW_FILE="${WORK_DIR}/pw"

PASSPHRASE="capstan-e2e-standalone-test-passphrase"
STACK_NAME="test-app"
MARKER_CONTENT="restic-roundtrip-$(date -Iseconds)"

PASSED=0
FAILED=0

cleanup() {
  log_info "Removing temp dir: $WORK_DIR"
  rm -rf "$WORK_DIR" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

step_pass() {
  local msg="$1"
  PASSED=$((PASSED + 1))
  log_success "$msg"
}

step_fail() {
  local msg="$1"
  FAILED=$((FAILED + 1))
  log_error "$msg"
}

# ─── Step 0: Check prerequisites ──────────────────────────────────────────────

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "RESTIC STANDALONE ROUND-TRIP TEST"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if ! command -v restic > /dev/null 2>&1; then
  log_error "restic not found on PATH"
  exit 1
fi

RESTIC_VERSION=$(restic version | head -1)
log_info "restic: $RESTIC_VERSION"
log_info "Work dir: $WORK_DIR"

# ─── Create test data (mirrors testing/docker-test-stacks/test-app/) ─────────

mkdir -p "$RESTIC_REPO" "$DATA_DIR" "$RESTORE_DIR"

cat > "${DATA_DIR}/docker-compose.yaml" << 'COMPOSE'
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    environment:
      - APP_NAME=${APP_NAME:-TestApp}
COMPOSE

cat > "${DATA_DIR}/.env" << 'DOTENV'
APP_NAME=TestApp
DOTENV

printf '%s\n' "$MARKER_CONTENT" > "${DATA_DIR}/marker.txt"

# ─── Create password file (0600) ──────────────────────────────────────────────

printf '%s' "$PASSPHRASE" > "$PW_FILE"
chmod 0600 "$PW_FILE"

# Export for convenience in all restic calls
export RESTIC_REPOSITORY="$RESTIC_REPO"
export RESTIC_PASSWORD_FILE="$PW_FILE"

# ─── Step 1: restic init ──────────────────────────────────────────────────────

echo ""
log_info "STEP 1: Initialize repository"

if restic init --json 2>&1 | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('message_type') == 'initialized', f'unexpected: {d}'
print(f'  Repo ID: {d[\"id\"][:16]}...')
" 2>&1; then
  step_pass "restic init succeeded"
else
  step_fail "restic init failed"
  exit 1
fi

# ─── Step 2: restic backup with stack tag ────────────────────────────────────

echo ""
log_info "STEP 2: Backup data with tag 'stack:${STACK_NAME}'"

BACKUP_OUT=$(restic backup --json \
  --tag "stack:${STACK_NAME}" \
  --tag "$(date +%Y-%m-%d)" \
  "$DATA_DIR" 2>&1)

SNAPSHOT_ID_FULL=$(echo "$BACKUP_OUT" | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
        if d.get('message_type') == 'summary':
            print(d['snapshot_id'])
            break
    except Exception:
        pass
" 2>/dev/null || true)

if [ -n "$SNAPSHOT_ID_FULL" ]; then
  SNAPSHOT_SHORT="${SNAPSHOT_ID_FULL:0:8}"
  step_pass "Backup succeeded — snapshot ID: ${SNAPSHOT_SHORT}"
  log_info "  Files: $(echo "$BACKUP_OUT" | python3 -c "import sys,json; [print(d.get('files_new',0)) for l in sys.stdin for d in [json.loads(l)] if d.get('message_type')=='summary']" 2>/dev/null || echo '?')"
else
  step_fail "Backup did not produce a snapshot ID"
  log_error "Backup output: $BACKUP_OUT"
  exit 1
fi

# ─── Step 3: restic snapshots with tag filter ────────────────────────────────

echo ""
log_info "STEP 3: List snapshots filtered by tag 'stack:${STACK_NAME}'"

SNAPSHOTS_JSON=$(restic snapshots --json --tag "stack:${STACK_NAME}" 2>&1)

LISTED_COUNT=$(echo "$SNAPSHOTS_JSON" | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
        if isinstance(d, list):
            print(len(d))
            break
    except Exception:
        pass
" 2>/dev/null || echo "0")

LISTED_SHORT=$(echo "$SNAPSHOTS_JSON" | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
        if isinstance(d, list) and d:
            print(d[0]['short_id'])
            break
    except Exception:
        pass
" 2>/dev/null || true)

if [ "$LISTED_COUNT" -ge 1 ] && [ -n "$LISTED_SHORT" ]; then
  step_pass "Snapshots listed: count=${LISTED_COUNT}, short_id=${LISTED_SHORT}"
else
  step_fail "Snapshot not found after backup (count=${LISTED_COUNT})"
  log_error "Snapshots JSON: $SNAPSHOTS_JSON"
  exit 1
fi

# Verify the tag matches
TAG_OK=$(echo "$SNAPSHOTS_JSON" | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
        if isinstance(d, list) and d:
            tags = d[0].get('tags', [])
            print('yes' if any('stack:${STACK_NAME}' in t for t in tags) else 'no')
            break
    except Exception:
        pass
" 2>/dev/null || echo "no")

if [ "$TAG_OK" = "yes" ]; then
  step_pass "Snapshot has expected tag 'stack:${STACK_NAME}'"
else
  step_fail "Snapshot is missing expected tag 'stack:${STACK_NAME}'"
fi

# ─── Step 4: restic restore ───────────────────────────────────────────────────

echo ""
log_info "STEP 4: Restore snapshot ${LISTED_SHORT} to ${RESTORE_DIR}"

RESTORE_OUT=$(restic restore "${LISTED_SHORT}" --target "$RESTORE_DIR" --json 2>&1)

RESTORE_BYTES=$(echo "$RESTORE_OUT" | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
        if d.get('message_type') == 'summary':
            print(d.get('bytes_restored', 0))
            break
    except Exception:
        pass
" 2>/dev/null || echo "0")

if [ "$RESTORE_BYTES" -gt 0 ]; then
  step_pass "Restore succeeded — bytes_restored=${RESTORE_BYTES}"
else
  log_warning "JSON bytes_restored=0 or not found; checking restored files directly"
fi

# ─── Step 5: Verify restored files match originals ────────────────────────────

echo ""
log_info "STEP 5: Verify restored file integrity"

RESTORED_COMPOSE=$(find "$RESTORE_DIR" -name "docker-compose.yaml" | head -1)
RESTORED_ENV=$(find "$RESTORE_DIR" -name ".env" | head -1)
RESTORED_MARKER=$(find "$RESTORE_DIR" -name "marker.txt" | head -1)

if [ -n "$RESTORED_COMPOSE" ]; then
  if cmp -s "${DATA_DIR}/docker-compose.yaml" "$RESTORED_COMPOSE"; then
    step_pass "docker-compose.yaml restored correctly (byte-for-byte match)"
  else
    step_fail "docker-compose.yaml content mismatch after restore"
  fi
else
  step_fail "docker-compose.yaml not found in restore target"
fi

if [ -n "$RESTORED_ENV" ]; then
  if cmp -s "${DATA_DIR}/.env" "$RESTORED_ENV"; then
    step_pass ".env restored correctly"
  else
    step_fail ".env content mismatch after restore"
  fi
else
  step_fail ".env not found in restore target"
fi

if [ -n "$RESTORED_MARKER" ]; then
  RESTORED_CONTENT=$(cat "$RESTORED_MARKER")
  if [ "$RESTORED_CONTENT" = "$MARKER_CONTENT" ]; then
    step_pass "marker.txt content matches: '$MARKER_CONTENT'"
  else
    step_fail "marker.txt mismatch: expected '$MARKER_CONTENT' got '$RESTORED_CONTENT'"
  fi
else
  step_fail "marker.txt not found in restore target"
fi

# ─── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "RESTIC ROUND-TRIP SUMMARY"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Passed: $PASSED"
echo "Failed: $FAILED"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if [ $FAILED -gt 0 ]; then
  log_error "Round-trip verification FAILED ($FAILED step(s) failed)"
  exit 1
fi

log_success "All steps passed — restic backup engine is functional"
exit 0
