#!/usr/bin/env bash
# PCL-DSH-Go M1 | acceptance harness (task-21 skeleton; task-19 fills the E2E phases).
#
# Modes:
#   --selfcheck   (default) validate script syntax, helper contract and required evidence
#                 inputs without starting the manager. Run this in CI for every change.
#   --baseline    capture the read-only Windows DSH baseline evidence (before/after hash).
#   --full        run the M1 contract + real-DSH E2E. Refuses to run until the wiring
#                 marker is present (M1_WIRING_READY=1) and the task-19 phases are filled.
#
# What is implemented here (task-21):
#   * fresh isolated data-root (0700) and manager lifecycle (start/health/cleanup)
#   * sanitized logging/evidence collection (token/cookie/password never written raw)
#   * HTTP helper with 0600 cookie jar (jar is deleted on every exit; never copied to evidence)
#   * JSON helpers, Job polling skeleton, Windows DSH read-only baseline integration
#   * failure keeps the scenario directory and prints manager log path
#   * placeholder phases for task-19 (login/password/CSRF/instances/runtimes/jobs/SSE/
#     workbench/direct-access/logs/settings), each marked TODO(M1-I)
#
# Usage (WSL, repo root):
#   bash scripts/wsl/m1-acceptance.sh --selfcheck
#   bash scripts/wsl/m1-acceptance.sh --baseline
#   M1_WIRING_READY=1 bash scripts/wsl/m1-acceptance.sh --full   # only after task-18/task-19
#
# Env: PCL_DSH_TEST_ROOT, PCL_DSH_TOOLS, MANAGER_PORT, DSH_PORT, M1_WIRING_READY
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="${PCL_DSH_TEST_ROOT:-/tmp/pcl-dsh-go-test}"
TOOLS_ROOT="${PCL_DSH_TOOLS:-/opt/pcl-dsh-go/tools}"
NODE="$TOOLS_ROOT/node/bin/node"
MANAGER_PORT="${MANAGER_PORT:-18080}"
DSH_PORT="${DSH_PORT:-3080}"
MANAGER_BIN="$REPO_ROOT/bin/pcl-dsh-go"
DATA_ROOT="$ROOT/m1-data"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m1"
SELFCHECK_EVIDENCE="$EVIDENCE_DIR/m1-acceptance-selfcheck.txt"
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
SCENARIO="$ROOT/m1-scenario-$RUN_STAMP"
LOG="$EVIDENCE_DIR/m1-acceptance-$RUN_STAMP.log"
COOKIE_JAR="$SCENARIO/cookies.txt"
MODE="selfcheck"
KEEP_DATA=0

PASS_COUNT=0
FAIL_COUNT=0
INFO_COUNT=0
FAIL_LINES=""
MANAGER_PID=""
API_STATUS=""
API_BODY=""
CSRF_TOKEN=""
INITIAL_PASSWORD=""
NEW_PASSWORD=""
WIN_DSH_PROG_BEFORE=""
WIN_DSH_USER_BEFORE=""
EXIT_CODE_OVERRIDE=""

# ---------- sanitization / logging ----------
san() {
  sed -E \
    -e 's/([?&]token=)[^&"[:space:]]+/\1***/g' \
    -e 's/\btoken=[^&"[:space:]]+/token=***/g' \
    -e 's/dsh-auth-[A-Za-z0-9]+=[^;,"[:space:]]*/dsh-auth-***=***/g' \
    -e 's/(JSESSIONID|XSRF-TOKEN)=[^;,"[:space:]]+/\1=***/g' \
    -e 's/(set-cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/(cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/(authorization:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/("[A-Za-z]*[Pp]assword"[[:space:]]*:[[:space:]]*")[^"]*"/\1***"/g' \
    -e 's/([?&](access_token|api_key|apikey|password|secret)=)[^&"[:space:]]+/\1***/g' \
    -e 's/\bsk-[A-Za-z0-9_-]{12,}/***/g'
}
log() { printf '%s\n' "$*" | tee -a "$LOG"; }
info() { INFO_COUNT=$((INFO_COUNT + 1)); log "INFO  $*"; }
check() {
  local name="$1" ok="$2" detail="$3"
  if [ "$ok" = "1" ]; then
    PASS_COUNT=$((PASS_COUNT + 1))
    log "PASS  $name  $detail"
  else
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAIL_LINES="$FAIL_LINES
FAIL  $name  $detail"
    log "FAIL  $name  $detail"
  fi
}
now_ms() { printf '%s' "$EPOCHREALTIME" | tr -d '.' | cut -c1-13; }
wiring_ready() { [ "${M1_WIRING_READY:-0}" = "1" ]; }

# ---------- evidence / cleanup ----------
cleanup() {
  local code=$?
  if [ -n "${EXIT_CODE_OVERRIDE:-}" ]; then code="$EXIT_CODE_OVERRIDE"; fi
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    sleep 1
    kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  fi
  # The cookie jar holds session secrets: always delete it, on success and failure.
  rm -f "$COOKIE_JAR" 2>/dev/null || true
  rm -f "$SCENARIO"/*.body "$SCENARIO"/*.hdr "$SCENARIO"/login.json 2>/dev/null || true
  if [ "$code" -eq 3 ]; then
    log ""
    log "RESULT=REFUSED (wiring/phases pending; not a test failure) scenario=$SCENARIO log=$LOG"
  elif [ "$code" -ne 0 ]; then
    log ""
    log "RESULT=FAIL scenario=$SCENARIO log=$LOG"
    log "evidence tail (manager.log):"
    [ -f "$SCENARIO/manager.log" ] && tail -30 "$SCENARIO/manager.log" 2>/dev/null | san | tee -a "$LOG" || true
  fi
  log "EXIT_CODE=$code scenario=$SCENARIO"
  if [ -f "$LOG" ]; then san < "$LOG" > "$LOG.txt" 2>/dev/null || true; fi
  if [ -f "$SCENARIO/manager.log" ]; then san < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/m1-acceptance-$RUN_STAMP-manager.log.txt" 2>/dev/null || true; fi
  exit "$code"
}
trap cleanup EXIT

# ---------- Windows DSH read-only baseline + E2E phases ----------
source "$SCRIPT_DIR/lib/windows-dsh-baseline.sh"
# M1 E2E phase implementations live in a separate file; sourcing is side-effect free.
if [ -f "$SCRIPT_DIR/m1-phases.sh" ]; then source "$SCRIPT_DIR/m1-phases.sh"; fi
baseline_capture() {
  win_dsh_baseline_capture
  log "Windows DSH baseline: program=$(printf '%s' "$WIN_DSH_PROG_BEFORE" | cut -c1-16)... user=$(printf '%s' "$WIN_DSH_USER_BEFORE" | cut -c1-16)..."
}
baseline_verify() {
  win_dsh_baseline_verify "$WIN_DSH_PROG_BEFORE" baseline_log
}
baseline_log() {
  local status="$1" name="$2" detail="$3"
  case "$status" in
    PASS|FAIL) check "$name" "$([ "$status" = "PASS" ] && echo 1 || echo 0)" "$detail";;
    *) info "$name $detail";;
  esac
}

# ---------- filesystem / manager ----------
init_data_root() {
  rm -rf "$DATA_ROOT"
  mkdir -p "$DATA_ROOT" "$SCENARIO"
  chmod 700 "$DATA_ROOT" "$SCENARIO"
  log "fresh data-root created: $DATA_ROOT (0700)"
}
wait_port_free() {
  local port="$1" tries="$2" n=0
  while [ "$n" -lt "$tries" ]; do
    if ! ss -ltn 2>/dev/null | grep -q ":$port\b"; then return 0; fi
    sleep 0.1
    n=$((n + 1))
  done
  return 1
}
preflight_ports() {
  if ss -ltn 2>/dev/null | grep -q ":$MANAGER_PORT\b"; then fail_preflight "manager port $MANAGER_PORT busy"; fi
  if ss -ltn 2>/dev/null | grep -q ":$DSH_PORT\b"; then fail_preflight "DSH port $DSH_PORT busy"; fi
}
fail_preflight() { log "PREFLIGHT FAIL: $*"; exit 2; }

build_linux_manager() {
  # Always rebuild from the current working tree so the evidence proves the
  # committed + working-tree source (release builds use -buildvcs=false).
  local go_bin win_go cmd bat win_bat
  if command -v go.exe >/dev/null 2>&1; then go_bin="$(command -v go.exe)"
  elif command -v go >/dev/null 2>&1; then go_bin="$(command -v go)"
  else fail_preflight "no Go toolchain (go.exe/go)"; fi
  mkdir -p "$REPO_ROOT/bin"
  case "$go_bin" in
    *.exe)
      win_go="$(winpath "$go_bin")"
      cmd="$(command -v cmd.exe)"
      bat="$REPO_ROOT/bin/m1-build-manager.bat"
      win_bat="$(winpath "$bat")"
      {
        printf '@echo off\r\n'
        printf 'set GOOS=linux\r\nset GOARCH=amd64\r\nset CGO_ENABLED=0\r\n'
        printf '"%s" build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=0.2.0-m1" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go\r\n' "$win_go"
        printf 'exit /b %%%%ERRORLEVEL%%%%\r\n'
      } > "$bat"
      ( cd "$REPO_ROOT" && "$cmd" /c "$win_bat" ) >> "$LOG" 2>&1 || { rm -f "$bat"; fail_preflight "manager cross-compile failed (see $LOG)"; }
      rm -f "$bat";;
    *)
      ( cd "$REPO_ROOT" && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$go_bin" build -trimpath -buildvcs=false -ldflags '-s -w -X main.version=0.2.0-m1' -o bin/pcl-dsh-go ./cmd/pcl-dsh-go ) >> "$LOG" 2>&1 \
        || fail_preflight "manager build failed (see $LOG)";;
  esac
  [ -f "$MANAGER_BIN" ] || fail_preflight "manager binary missing after build"
  chmod +x "$MANAGER_BIN"
  local build_size build_sha
  build_size=$(stat -c %s "$MANAGER_BIN")
  build_sha=$(sha256sum "$MANAGER_BIN" | awk '{print $1}')
  log "manager built from current tree: size=$(printf '%s' "$build_size")B sha256=$(printf '%s' "$build_sha")"
  check "manager linux/amd64 build from current tree" 1 "size=$build_size sha256=$build_sha"
}
winpath() {
  local p="$1"
  case "$p" in
    /mnt/[a-zA-Z]/*)
      local drive rest
      drive="$(printf '%s' "$p" | cut -c6 | tr '[:lower:]' '[:upper:]')"
      rest="$(printf '%s' "$p" | cut -c8- | sed 's|/|\\|g')"
      printf '%s:\\%s' "$drive" "$rest";;
    *) printf '%s' "$p";;
  esac
}

start_manager() {
  mkdir -p "$SCENARIO"
  setsid "$MANAGER_BIN" \
    --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" \
    --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  log "manager start pid=$MANAGER_PID data-root=$DATA_ROOT port=$MANAGER_PORT"
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 "http://127.0.0.1:$MANAGER_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then
      log "health: $(san < "$SCENARIO/health.json")"
      check "manager /api/health" 1
      return 0
    fi
    if ! kill -0 "$MANAGER_PID" 2>/dev/null; then fail_preflight "manager exited during startup (see $SCENARIO/manager.log)"; fi
    sleep 0.3
  done
  fail_preflight "manager /api/health not ready in 30s"
}

stop_manager() {
  local free=0
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    for n in $(seq 1 50); do kill -0 "$MANAGER_PID" 2>/dev/null || break; sleep 0.1; done
    kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  fi
  MANAGER_PID=""
  wait_port_free "$MANAGER_PORT" 30 && free=1 || free=0
  check "manager port $MANAGER_PORT released" "$free"
}

# ---------- HTTP / JSON helpers (cookie jar 0600; never logged) ----------
api() {
  local method="$1" path="$2" body="$3"
  local hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body" args
  : > "$hdr"; : > "$bodyf"
  args=(-sS --max-time 120 -X "$method" -D "$hdr" -o "$bodyf" -b "$COOKIE_JAR" -c "$COOKIE_JAR" "http://127.0.0.1:$MANAGER_PORT$path")
  if [ -n "$body" ]; then
    args+=(-H 'Content-Type: application/json' --data-binary "$body")
  fi
  API_STATUS=$(curl "${args[@]}" -w '%{http_code}' 2>/dev/null)
  API_BODY="$bodyf"
  log "api $method $path -> status=$API_STATUS"
}
api_json_post() { # api_json_post <path> <json-body>
  api POST "$1" "$2"
}
api_get() { api GET "$1" ""; }
api_post_empty() { api POST "$1" ""; }
json_get() { # json_get <file> <dot.path>
  "$NODE" -e '
const fs = require("fs");
const file = process.argv[1];
const pathArg = process.argv[2] || "";
let value;
try { value = JSON.parse(fs.readFileSync(file, "utf8")); } catch (e) { process.exit(3); }
for (const key of pathArg.split(".")) {
  if (value === null || value === undefined) { process.exit(2); }
  if (/^\d+$/.test(key)) value = value[Number(key)];
  else value = value[key];
}
if (value === undefined || value === null) { process.exit(2); }
if (typeof value === "object") process.stdout.write(JSON.stringify(value));
else process.stdout.write(String(value));
' "$1" "$2"
}
json_ok() { [ -s "$1" ] && "$NODE" -e 'try { JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")); process.exit(0); } catch (e) { process.exit(1); }' "$1"; }
current_csrf() { # GET /api/auth/me returns csrfToken; refresh cookie too
  api_get /api/auth/me
  local token
  token="$(json_get "$API_BODY" csrfToken 2>/dev/null || true)"
  if [ -n "$token" ]; then CSRF_TOKEN="$token"; fi
}
csrf_headers() { printf '%s' "-H X-XSRF-TOKEN:$CSRF_TOKEN"; }

# ---------- M1 phases (task-19 fills the bodies; skeleton only) ----------
phase_login() {
  # TODO(M1-I): read initial password from <data-root>/initial-password (0600, never echoed),
  # POST /api/auth/login via stdin body, assert 204 + JSESSIONID(HttpOnly/SameSite=Lax) +
  # XSRF-TOKEN cookies + audit line; assert /api/state 401 before login.
  info "TODO(M1-I) phase_login: pending task-19 implementation"
}
phase_password() {
  # TODO(M1-I): assert pending-password 403 PASSWORD_CHANGE_REQUIRED on /api/state,
  # PUT /api/auth/password (CSRF), then initial-password removed and old password rejected.
  info "TODO(M1-I) phase_password: pending task-19 implementation"
}
phase_csrf_and_sessions() {
  # TODO(M1-I): write without X-XSRF-TOKEN -> 403 CSRF_INVALID; with header OK;
  # logout invalidates session (old cookie 401); login again; 5 wrong passwords -> 423
  # LOGIN_LOCKED with details.retryAfterSeconds.
  info "TODO(M1-I) phase_csrf_and_sessions: pending task-19 implementation"
}
phase_instances() {
  # TODO(M1-I): create/validate instance (201/400/409), edit version/profile -> 409
  # CONFLICT_UNSUPPORTED_CHANGE, selection persistence, delete -> trash, running delete 409.
  info "TODO(M1-I) phase_instances: pending task-19 implementation"
}
phase_runtime_jobs_sse() {
  # TODO(M1-I): POST /api/runtimes -> 202 JobView; SSE /api/jobs/{id}/events
  # (state/log/result, id monotonic, 15s ping, Last-Event-ID resume); /api/runtimes
  # installed=true; launch -> 202 Job; /api/state 运行中; logs tail; settings.
  info "TODO(M1-I) phase_runtime_jobs_sse: pending task-19 implementation"
}
phase_workbench_direct_access() {
  # TODO(M1-I): /workbench login gate (401 anonymous, 200/302 logged-in), direct-access
  # ready=false while stopped and ready=true+directUrl while running (no-store, token not logged).
  info "TODO(M1-I) phase_workbench_direct_access: pending task-19 implementation"
}
phase_stop_cleanup() {
  # TODO(M1-I): stop instance -> port released/no process; delete instance/runtime -> trash;
  # verify no token/cookie in data logs; Windows DSH program tree unchanged.
  info "TODO(M1-I) phase_stop_cleanup: pending task-19 implementation"
}

# ---------- modes ----------
run_selfcheck() {
  local failures=0
  log "=== M1 acceptance selfcheck $RUN_STAMP ==="
  if bash -n "$0"; then check "bash -n m1-acceptance.sh" 1; else check "bash -n m1-acceptance.sh" 0; failures=$((failures + 1)); fi
  if bash -n "$SCRIPT_DIR/lib/windows-dsh-baseline.sh"; then check "bash -n windows-dsh-baseline.sh" 1; else check "bash -n windows-dsh-baseline.sh" 0; failures=$((failures + 1)); fi
  if bash -n "$SCRIPT_DIR/m1-phases.sh"; then check "bash -n m1-phases.sh" 1; else check "bash -n m1-phases.sh" 0; failures=$((failures + 1)); fi
  for required in "$REPO_ROOT/docs/M1_INTERFACES.md" "$REPO_ROOT/docs/IMPLEMENTATION_SPEC.md" "$REPO_ROOT/docs/ACCEPTANCE.md" "$EVIDENCE_DIR/contract-matrix.md" "$SCRIPT_DIR/m1-phases.sh" "$SCRIPT_DIR/lib/job-sse.mjs" "$SCRIPT_DIR/lib/sse-e2e.mjs" "$SCRIPT_DIR/lib/ws-e2e.mjs"; do
    check "required input $(basename "$required")" "$([ -f "$required" ] && echo 1 || echo 0)" "path=$required"
    [ -f "$required" ] || failures=$((failures + 1))
  done
  for fn in san log check info now_ms api json_get baseline_capture baseline_verify start_manager stop_manager init_data_root wait_port_free build_linux_manager do_login refresh_me job_sse phase_anon phase_pending_password phase_security phase_instances_crud phase_prelaunch phase_runtime_jobs phase_launch_workbench phase_stop_delete_restart phase_cleanup_final run_m1_full; do
    if declare -F "$fn" >/dev/null 2>&1; then check "helper defined: $fn" 1; else check "helper defined: $fn" 0; failures=$((failures + 1)); fi
  done
  local masked
  masked="$(printf '%s\n' 'http://x/?token=SECRETTOKEN dsh-auth-abc=SECRETCOOKIE JSESSIONID=SECRETSESSION X-XSRF-TOKEN: abc Authorization: Bearer xyz' | san)"
  if printf '%s' "$masked" | grep -q 'SECRET' || printf '%s' "$masked" | grep -q 'Bearer xyz'; then
    check "sanitizer masks token/cookie/session/authorization" 0 "masked=$masked"
    failures=$((failures + 1))
  else
    check "sanitizer masks token/cookie/session/authorization" 1 "masked=$masked"
  fi
  for tool in curl "$NODE" ss setsid find sha256sum timeout; do
    if command -v "$tool" >/dev/null 2>&1; then check "tool available: $tool" 1; else check "tool available: $tool" 0; failures=$((failures + 1)); fi
  done
  if [ -z "$WIN_DSH_PROG_BEFORE" ]; then
    # verify the baseline helper loads and produces a 64-hex digest without touching writes
    win_dsh_baseline_capture
  fi
  if printf '%s' "$WIN_DSH_PROG_BEFORE" | grep -Eq '^[a-f0-9]{64}$'; then
    check "Windows DSH baseline hash computed (read-only)" 1 "program_hash=$(printf '%s' "$WIN_DSH_PROG_BEFORE" | cut -c1-16)..."
  else
    check "Windows DSH baseline hash computed (read-only)" 0 "value=$WIN_DSH_PROG_BEFORE"
    failures=$((failures + 1))
  fi
  log ""
  log "selfcheck: pass=$PASS_COUNT fail=$FAIL_COUNT info=$INFO_COUNT"
  if [ "$FAIL_COUNT" -gt 0 ]; then log "SELFCHECK=FAIL"; return 1; fi
  log "SELFCHECK=OK (helpers + matrix + phase definitions; --full runs the real E2E)"
  return 0
}

run_baseline() {
  mkdir -p "$EVIDENCE_DIR"
  log "=== Windows DSH baseline capture $RUN_STAMP ==="
  win_dsh_baseline_capture
  win_dsh_baseline_write "$EVIDENCE_DIR/windows-dsh-baseline-before.txt"
  log "baseline written: $EVIDENCE_DIR/windows-dsh-baseline-before.txt"
  log "program_hash=$(printf '%s' "$WIN_DSH_PROG_BEFORE" | cut -c1-16)... user_hash=$(printf '%s' "$WIN_DSH_USER_BEFORE" | cut -c1-16)..."
  log "RESULT=PASS"
}

run_full() {
  if ! wiring_ready; then
    log "REFUSED: M1 wiring not marked ready (set M1_WIRING_READY=1 only after task-18/task-19)."
    log "Skeleton helpers are available via --selfcheck / --baseline."
    log "RESULT=REFUSED (expected pre-wiring state; exit 3, not a test failure)"
    log "EXIT_CODE=3"
    trap - EXIT
    exit 3
  fi
  if ! declare -F run_m1_full >/dev/null 2>&1; then
    log "REFUSED: m1-phases.sh is missing or failed to load."
    log "EXIT_CODE=3"
    trap - EXIT
    exit 3
  fi
  run_m1_full
  exit $?
}

main() {
  local arg
  for arg in "$@"; do
    case "$arg" in
      --selfcheck) MODE=selfcheck;;
      --baseline) MODE=baseline;;
      --full) MODE=full;;
      --keep-data) KEEP_DATA=1;;
      -h|--help) sed -n '2,40p' "$0"; exit 0;;
      *) echo "unknown argument: $arg" >&2; exit 2;;
    esac
  done
  mkdir -p "$EVIDENCE_DIR"
  case "$MODE" in
    selfcheck) run_selfcheck;;
    baseline) run_baseline;;
    full) run_full;;
    *) echo "unknown mode" >&2; exit 2;;
  esac
}

main "$@"
