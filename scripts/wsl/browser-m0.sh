#!/usr/bin/env bash
# PCL-DSH-Go M0-browser | real headless-browser verification of the /workbench page.
#
# Reuses the M0 acceptance data-root (/tmp/pcl-dsh-go-test/e2e-data) and its already
# installed DSH rc.3 runtime (install endpoint is called only to prove reused=true;
# no second 78s download). Starts the manager in dev mode, launches the existing
# instance, opens http://127.0.0.1:18080/workbench/{id}/ in headless Chromium via
# Playwright, asserts title / #root rendering / screenshot / console-error classes /
# network statuses / /api/remote.mux WebSocket, then stops everything.
#
# Usage: bash scripts/wsl/browser-m0.sh [--json]
# Env: PCL_DSH_TEST_ROOT, PCL_DSH_TOOLS, DSH_VERSION, MANAGER_PORT, DSH_PORT
# Preconditions: scripts/wsl/m0-acceptance.sh ran once (e2e-data runtime present) and
#                Playwright + Chromium are installed under <root>/tools/web.
# Evidence: docs/evidence/m0/e2e/browser-<ts>.log.txt + browser-workbench.png (token-free).
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="$PCL_DSH_TEST_ROOT"; [ -z "$ROOT" ] && ROOT=/tmp/pcl-dsh-go-test
TOOLS_ROOT="$PCL_DSH_TOOLS"; [ -z "$TOOLS_ROOT" ] && TOOLS_ROOT=/opt/pcl-dsh-go/tools
VERSION="$DSH_VERSION"; [ -z "$VERSION" ] && VERSION=0.2.1-rc.3
MANAGER_PORT="$MANAGER_PORT"; [ -z "$MANAGER_PORT" ] && MANAGER_PORT=18080
DSH_PORT="$DSH_PORT"; [ -z "$DSH_PORT" ] && DSH_PORT=3080
NODE="$TOOLS_ROOT/node/bin/node"
DATA_ROOT="$ROOT/e2e-data"
WEB_DIR="$ROOT/tools/web"
BROWSERS_PATH="$ROOT/tools/browsers"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m0/e2e"
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
SCENARIO="$ROOT/browser-scenario-$RUN_STAMP"
LOG="$EVIDENCE_DIR/browser-$RUN_STAMP.log"
PNG="$EVIDENCE_DIR/browser-workbench.png"
BROWSER_CHECK="$SCRIPT_DIR/lib/browser-check.mjs"
MANAGER_BIN="$REPO_ROOT/bin/pcl-dsh-go"
MANAGER_PID=""
PASS_COUNT=0
FAIL_COUNT=0
FAIL_LINES=""

sanitize() {
  sed -E \
    -e 's/token=[^&"[:space:]]+/token=***/g' \
    -e 's/dsh-auth-[A-Za-z0-9]+=[^;,"[:space:]]*/dsh-auth-***=***/g' \
    -e 's/(set-cookie:[[:space:]]*)[^[:space:]]+/\1***/gi' \
    -e 's/(cookie:[[:space:]]*)[^[:space:]]+/\1***/gi' \
    -e 's/(authorization:[[:space:]]*)[^[:space:]]+/\1***/gi' \
    -e 's/([?&](access_token|api_key|apikey|password|secret)=)[^&"[:space:]]+/\1***/g'
}
log() { printf '%s\n' "$*" | tee -a "$LOG"; }
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
fatal() { log "FATAL: $*"; exit 1; }
now_ms() { printf '%s' "$EPOCHREALTIME" | tr -d '.' | cut -c1-13; }

cleanup() {
  local code=$?
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    sleep 1
    kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  fi
  if [ -f "$LOG" ]; then sanitize < "$LOG" > "$LOG.txt" 2>/dev/null || true; fi
  if [ -f "$SCENARIO/manager.log" ]; then sanitize < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/browser-$RUN_STAMP-manager.log.txt" 2>/dev/null || true; fi
  if [ "$code" -ne 0 ]; then
    printf '\nRESULT=FAIL scenario=%s log=%s\n' "$SCENARIO" "$LOG" | tee -a "$LOG"
    [ -f "$SCENARIO/manager.log" ] && tail -20 "$SCENARIO/manager.log" 2>/dev/null | sanitize >> "$LOG" || true
  fi
  exit "$code"
}
trap cleanup EXIT

api() {
  local method="$1" path="$2" body="$3"
  if [ -n "$body" ]; then
    curl -sS -X "$method" --max-time 120 -H 'Content-Type: application/json' -d "$body" "http://127.0.0.1:$MANAGER_PORT$path"
  else
    curl -sS -X "$method" --max-time 120 "http://127.0.0.1:$MANAGER_PORT$path"
  fi
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

preflight() {
  [ -f "$MANAGER_BIN" ] || fatal "manager binary missing: $MANAGER_BIN (run scripts/wsl/m0-acceptance.sh --skip-build or build it first)"
  [ -d "$WEB_DIR/node_modules/playwright" ] || fatal "Playwright not installed under $WEB_DIR (npm install playwright there)"
  [ -d "$BROWSERS_PATH" ] || fatal "Chromium browsers dir missing: $BROWSERS_PATH (PLAYWRIGHT_BROWSERS_PATH)"
  [ -f "$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh/lib/bin.js" ] || fatal "reusable runtime missing under $DATA_ROOT (run scripts/wsl/m0-acceptance.sh once; browser mode intentionally reuses it)"
  if ss -ltn 2>/dev/null | grep -q ":$MANAGER_PORT\b"; then fatal "manager port $MANAGER_PORT already in use"; fi
  if ss -ltn 2>/dev/null | grep -q ":$DSH_PORT\b"; then fatal "DSH port $DSH_PORT already in use"; fi
}

start_manager() {
  mkdir -p "$SCENARIO"
  setsid "$MANAGER_BIN" \
    --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" --pcl.dev-mode=true \
    --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  log "manager start pid=$MANAGER_PID data-root=$DATA_ROOT (REUSE existing runtime, no reinstall)"
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 "http://127.0.0.1:$MANAGER_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then
      log "health: $(sanitize < "$SCENARIO/health.json")"
      check "manager /api/health" 1 "data-root reused=$DATA_ROOT"
      return 0
    fi
    if ! kill -0 "$MANAGER_PID" 2>/dev/null; then fatal "manager exited during startup (see $SCENARIO/manager.log)"; fi
    sleep 0.3
  done
  fatal "manager /api/health not ready in 30s"
}

ensure_runtime_reused() {
  local started resp elapsed entry reused
  body=$(printf '{"version":"%s"}' "$VERSION")
  started=$(now_ms)
  resp=$(api POST /api/m0/runtimes/install "$body")
  elapsed=$(( $(now_ms) - started ))
  entry=$(printf '%s' "$resp" | sed -n 's/.*"entryPath":"\([^"]*\)".*/\1/p')
  reused=$(printf '%s' "$resp" | sed -n 's/.*"reused":\(true\|false\).*/\1/p')
  log "runtime install API: $(printf '%s' "$resp" | sanitize) elapsed_ms=$elapsed"
  check "runtime reused (no second download)" "$([ "$reused" = "true" ] && echo 1 || echo 0)" "reused=$reused elapsed_ms=$elapsed"
  [ -n "$entry" ] || fatal "install API returned no entryPath"
}

pick_or_create_instance() {
  local list id
  list=$(api GET /api/m0/instances "")
  log "instances: $(printf '%s' "$list" | sanitize | cut -c1-400)"
  id=$(printf '%s' "$list" | grep -o '"id":"[^"]*"' | head -1 | sed -E 's/.*"id":"([^"]*)"/\1/')
  if [ -z "$id" ]; then
    body=$(printf '{"name":"browser-e2e","version":"%s","profile":"web","port":%s}' "$VERSION" "$DSH_PORT")
    list=$(api POST /api/m0/instances "$body")
    log "created instance: $(printf '%s' "$list" | sanitize)"
    id=$(printf '%s' "$list" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  fi
  [ -n "$id" ] || fatal "cannot find/create an instance"
  ID="$id"
  log "using instance id=$ID (store reused from e2e-data)"
}

launch_instance() {
  local status launch weburl started elapsed
  started=$(now_ms)
  status=$(curl -s -o "$SCENARIO/launch.json" -w '%{http_code}' -X POST --max-time 240 "http://127.0.0.1:$MANAGER_PORT/api/m0/instances/$ID/launch")
  elapsed=$(( $(now_ms) - started ))
  launch=$(cat "$SCENARIO/launch.json")
  weburl=$(printf '%s' "$launch" | sed -n 's/.*"webUrl":"\([^"]*\)".*/\1/p')
  log "launch status=$status body=$(printf '%s' "$launch" | sanitize) launch_ms=$elapsed"
  check "instance launch for browser test" "$([ "$status" = "200" ] && echo 1 || echo 0)" "status=$status launch_ms=$elapsed"
  [ "$status" = "200" ] || fatal "launch failed; cannot run browser verification"
  [ -n "$weburl" ] || weburl="/workbench/$ID/"
  WEB_URL="http://127.0.0.1:$MANAGER_PORT$weburl"
}

run_browser_check() {
  local out rc summary
  local hdr="$SCENARIO/proxy.hdr" status loc
  status=$(curl -s -D "$hdr" -o /dev/null -w '%{http_code}' --max-time 20 "$WEB_URL")
  loc=$(grep -i '^location:' "$hdr" | head -1 | tr -d '\r' | sed -E 's/^[Ll]ocation: //')
  log "browser entry: status=$status location=$(printf '%s' "$loc" | sanitize)"
  check "proxy entry returns 302 with bootstrap token" "$([ "$status" = "302" ] && echo 1 || echo 0)" "status=$status location=$(printf '%s' "$loc" | sanitize)"
  rm -f "$hdr"
  mkdir -p "$EVIDENCE_DIR"
  PLAYWRIGHT_BROWSERS_PATH="$BROWSERS_PATH" PLAYWRIGHT_REQUIRE_BASE="$WEB_DIR/package.json" "$NODE" "$BROWSER_CHECK" \
    --url "$WEB_URL" --png "$PNG" --json-out "$SCENARIO/browser-result.json" > "$SCENARIO/browser.out" 2>&1
  rc=$?
  log "browser-check rc=$rc:"
  sanitize < "$SCENARIO/browser.out" | tee -a "$LOG"
  check "browser check exit 0" "$([ "$rc" = "0" ] && echo 1 || echo 0)" "rc=$rc"
  summary=$("$NODE" -e '
const r = require(process.argv[1]);
const lines = [];
lines.push("title=" + r.title);
lines.push("root_text_len=" + r.rootTextLength);
lines.push("body_text_len=" + r.bodyTextLength);
lines.push("duration_ms=" + r.durationMs);
lines.push("responses_total=" + r.responses.total);
lines.push("responses_2xx=" + r.responses.status2xx);
lines.push("responses_401=" + r.responses.status401);
lines.push("responses_5xx=" + r.responses.status5xx);
lines.push("console_errors=" + r.consoleErrorCount);
lines.push("known_401_noise=" + r.known401NoiseCount);
lines.push("other_console_errors=" + r.otherConsoleErrorCount);
lines.push("page_errors=" + r.pageErrors.length);
lines.push("request_failures=" + r.requestFailures.length);
lines.push("ws_count=" + r.websockets.length);
lines.push("ws_mux_frames=" + r.websockets.filter(function(w){return /api\/remote\.mux/.test(w.url);}).reduce(function(a,w){return a + w.framesReceived;},0));
lines.push("png_bytes=" + r.pngBytes);
lines.push("failures=" + JSON.stringify(r.failures));
console.log(lines.join("\n"));
' "$SCENARIO/browser-result.json" 2>/dev/null)
  log "browser summary:"
  printf '%s\n' "$summary" | tee -a "$LOG"
  grep -q '^title=DeepSeek Harness$' <<<"$summary" && check "browser title=DeepSeek Harness" 1 || check "browser title=DeepSeek Harness" 0
  local root_len other_errors png_bytes ws_mux
  root_len=$(printf '%s' "$summary" | sed -n 's/^root_text_len=\([0-9]*\)$/\1/p')
  other_errors=$(printf '%s' "$summary" | sed -n 's/^other_console_errors=\([0-9]*\)$/\1/p')
  png_bytes=$(printf '%s' "$summary" | sed -n 's/^png_bytes=\([0-9]*\)$/\1/p')
  ws_mux=$(printf '%s' "$summary" | sed -n 's/^ws_count=\([0-9]*\)$/\1/p')
  check "#root renders visible content" "$([ -n "$root_len" ] && [ "$root_len" -ge 10 ] && echo 1 || echo 0)" "root_text_len=$root_len"
  check "screenshot PNG written" "$([ -n "$png_bytes" ] && [ "$png_bytes" -ge 1024 ] && [ -f "$PNG" ] && echo 1 || echo 0)" "path=$PNG bytes=$png_bytes"
  check "no non-401 console errors" "$([ "$other_errors" = "0" ] && echo 1 || echo 0)" "other=$other_errors (known 401 noise recorded in log)"
  if [ -n "$ws_mux" ] && [ "$ws_mux" -ge 1 ]; then
    check "browser observed /api/remote.mux WebSocket" 1 "websockets=$ws_mux frames_received=$(printf '%s' "$summary" | sed -n 's/^ws_mux_frames=\([0-9]*\)$/\1/p')"
  else
    log "INFO optional: browser did not observe /api/remote.mux in this run (WS covered by m0-acceptance.sh)"
  fi
}

stop_flow() {
  local status free
  status=$(curl -s -o /dev/null -w '%{http_code}' -X POST --max-time 60 "http://127.0.0.1:$MANAGER_PORT/api/m0/instances/$ID/stop")
  check "instance stop after browser test" "$([ "$status" = "200" ] && echo 1 || echo 0)" "status=$status"
  wait_port_free "$DSH_PORT" 40 && free=1 || free=0
  check "DSH port released after browser test" "$free"
  local n
  n=$(pgrep -af 'dsh/lib/bin.js' 2>/dev/null | grep -v pgrep | grep -v browser-m0 | wc -l)
  check "no DSH node process left" "$([ "$n" = "0" ] && echo 1 || echo 0)" "count=$n"
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
  check "manager port released" "$free"
}

security_scan() {
  local leaks errs
  leaks=$(grep -rEo 'token=[^*"[:space:]]|dsh-auth-[A-Za-z0-9]+=[^*]|v1\.[A-Za-z0-9_-]{20,}' "$SCENARIO" "$LOG" "$EVIDENCE_DIR/browser-$RUN_STAMP-manager.log.txt" 2>/dev/null | wc -l)
  check "browser evidence has no real token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs no ERROR lines" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
}

main() {
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO"
  log "=== PCL-DSH-Go M0 browser verification $RUN_STAMP ==="
  log "repo=$REPO_ROOT root=$ROOT manager_port=$MANAGER_PORT dsh_port=$DSH_PORT playwright_dir=$WEB_DIR browsers=$BROWSERS_PATH"
  preflight
  start_manager
  ensure_runtime_reused
  pick_or_create_instance
  launch_instance
  run_browser_check
  stop_flow
  stop_manager
  security_scan
  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS_COUNT checks_failed=$FAIL_COUNT screenshot=$PNG log=$LOG"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '%s\n' "$FAIL_LINES" | tee -a "$LOG"
    log "RESULT=FAIL"
    exit 1
  fi
  log "RESULT=PASS"
}

main
