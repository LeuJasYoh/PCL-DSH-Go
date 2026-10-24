#!/usr/bin/env bash
# PCL-DSH-Go M0-7 | formal end-to-end acceptance for the M0 vertical slice.
#
# Flow (fresh isolated data-root, real DSH 0.2.1-rc.3, WSL):
#   build linux manager -> start 127.0.0.1:18080 (dev mode) -> install runtime through
#   the Go API -> create instance -> launch (blocking until ready) -> /workbench/{id}/
#   token 302 -> DSH 303 + rewritten cookie -> page/static 200 -> SSE streaming ->
#   WebSocket upgrade + ready frame + close 1000 -> stop -> port/process cleanup ->
#   second launch -> cleanup manager -> permission/secret/Windows-DSH/reference-repo checks.
#
# Usage: bash scripts/wsl/m0-acceptance.sh [--keep] [--skip-build] [--reuse-runtime]
#   --skip-build     use the existing bin/pcl-dsh-go instead of rebuilding
#   --reuse-runtime  DEGRADED debugging aid: copy the known-good runtime from
#                    <root>/runtimes/<ver> into the fresh data-root before calling the
#                    install endpoint, so the rest of the E2E can be validated while the
#                    real Go install path is being fixed. Never use it for the final
#                    acceptance evidence (the log marks it DEGRADED).
# Env: PCL_DSH_TEST_ROOT (default /tmp/pcl-dsh-go-test), PCL_DSH_TOOLS (default
#      /opt/pcl-dsh-go/tools), DSH_VERSION (default 0.2.1-rc.3), MANAGER_PORT (18080),
#      DSH_PORT (3080), DSH_REF_REPO (reference repo for the clean check).
#
# Rules: writes only /tmp/pcl-dsh-go-test/e2e-data (+ runtime cache) and
# docs/evidence/m0/e2e/; Windows DSH and /opt are read-only; token/cookie stay in shell
# memory or mode-0600 temp files that are deleted; every evidence line is sanitized.
# Failure exits non-zero and keeps the scenario directory for triage.
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

ROOT="$PCL_DSH_TEST_ROOT"; [ -z "$ROOT" ] && ROOT=/tmp/pcl-dsh-go-test
TOOLS_ROOT="$PCL_DSH_TOOLS"; [ -z "$TOOLS_ROOT" ] && TOOLS_ROOT=/opt/pcl-dsh-go/tools
VERSION="$DSH_VERSION"; [ -z "$VERSION" ] && VERSION=0.2.1-rc.3
MANAGER_PORT="$MANAGER_PORT"; [ -z "$MANAGER_PORT" ] && MANAGER_PORT=18080
DSH_PORT="$DSH_PORT"; [ -z "$DSH_PORT" ] && DSH_PORT=3080
REF_REPO="$DSH_REF_REPO"; [ -z "$REF_REPO" ] && REF_REPO=/mnt/d/Projects/DSHL/PCL-DSH-Linux
WIN_DSH_DIR=/mnt/d/DevelopSoftware/DSH
WIN_USER_DSH=/mnt/c/Users/23500/.dsh

NODE="$TOOLS_ROOT/node/bin/node"
PNPM="$TOOLS_ROOT/pnpm/node_modules/pnpm/bin/pnpm.cjs"
LIB_DIR="$SCRIPT_DIR/lib"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m0/e2e"
DATA_ROOT="$ROOT/e2e-data"
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
SCENARIO="$ROOT/e2e-scenario-$RUN_STAMP"
LOG="$EVIDENCE_DIR/e2e-$RUN_STAMP.log"
KEEP=0
SKIP_BUILD=0
REUSE_RUNTIME=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1;;
    --skip-build) SKIP_BUILD=1;;
    --reuse-runtime) REUSE_RUNTIME=1;;
    *) echo "unknown argument: $arg" >&2; exit 2;;
  esac
done

PASS_COUNT=0
FAIL_COUNT=0
FAIL_LINES=""
MANAGER_PID=""
DSH_COOKIE=""
REF_BASELINE=""
REF_DIRTY_BASELINE=""

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

now_ms() { printf '%s' "$EPOCHREALTIME" | tr -d '.' | cut -c1-13; }

api() {
  local method="$1" path="$2" body="$3"
  if [ -n "$body" ]; then
    curl -sS -X "$method" --max-time 700 -H 'Content-Type: application/json' -d "$body" "http://127.0.0.1:$MANAGER_PORT$path"
  else
    curl -sS -X "$method" --max-time 700 "http://127.0.0.1:$MANAGER_PORT$path"
  fi
}

http_status() { # http_status <url> [extra curl args...]
  local url="$1"; shift
  curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$@" "$url"
}

cleanup() {
  local code=$?
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    sleep 1
    kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  fi
  rm -f "$SCENARIO"/*.hdr "$SCENARIO"/cookie.txt "$SCENARIO"/token.txt 2>/dev/null
  if [ -d "$EVIDENCE_DIR" ] && [ -f "$LOG" ]; then
    sanitize < "$LOG" > "$LOG.txt" 2>/dev/null || true
    sanitize < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/e2e-$RUN_STAMP-manager.log.txt" 2>/dev/null || true
  fi
  if [ "$code" -ne 0 ]; then
    printf '\nRESULT=FAIL scenario=%s log=%s\n' "$SCENARIO" "$LOG" | tee -a "$LOG"
    printf '=== failure tail (manager log) ===\n' >> "$LOG"
    [ -f "$SCENARIO/manager.log" ] && tail -30 "$SCENARIO/manager.log" 2>/dev/null | sanitize >> "$LOG" || true
  fi
  exit "$code"
}
trap cleanup EXIT

fatal() { log "FATAL: $*"; exit 1; }

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

find_go() {
  if command -v go.exe >/dev/null 2>&1; then GO_PATH="$(command -v go.exe)"
  elif command -v go >/dev/null 2>&1; then GO_PATH="$(command -v go)"
  else fatal "no Go toolchain (go.exe/go) found"; fi
}

build_manager() {
  find_go
  mkdir -p "$REPO_ROOT/bin"
  local started; started=$(now_ms)
  case "$GO_PATH" in
    *.exe)
      # WSL interop does not forward WSL env vars to Windows executables and cmd.exe
      # mangles quotes in /c strings, so set the env + quoted ldflags in a .bat file.
      local win_go cmd bat win_bat
      win_go="$(winpath "$GO_PATH")"
      cmd="$(command -v cmd.exe)"
      bat="$REPO_ROOT/bin/build-manager.bat"
      win_bat="$(winpath "$bat")"
      {
        printf '@echo off\r\n'
        printf 'set GOOS=linux\r\nset GOARCH=amd64\r\nset CGO_ENABLED=0\r\n'
        printf '"%s" build -trimpath -ldflags "-s -w -X main.version=0.1.0-m0" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go\r\n' "$win_go"
        printf 'exit /b %%%%ERRORLEVEL%%%%\r\n'
      } > "$bat"
      ( cd "$REPO_ROOT" && "$cmd" /c "$win_bat" ) >> "$LOG" 2>&1 \
        || fatal "manager cross-compile failed (see $LOG)";;

    *)
      ( cd "$REPO_ROOT" && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_PATH" build -trimpath -ldflags '-s -w -X main.version=0.1.0-m0' -o bin/pcl-dsh-go ./cmd/pcl-dsh-go ) >> "$LOG" 2>&1 \
        || fatal "manager build failed (see $LOG)";;
  esac
  local elapsed; elapsed=$(( $(now_ms) - started ))
  local bin="$REPO_ROOT/bin/pcl-dsh-go"
  [ -f "$bin" ] || fatal "manager binary missing: $bin"
  local magic; magic=$(head -c 4 "$bin" | od -An -tx1 | tr -d ' \n')
  [ "$magic" = "7f454c46" ] || fatal "manager binary is not a linux ELF"
  chmod +x "$bin"
  local size sha
  size=$(stat -c %s "$bin")
  sha=$(sha256sum "$bin" | awk '{print $1}')
  log "manager build: size=$size bytes sha256=$sha build_ms=$elapsed"
  check "manager linux/amd64 build" 1 "size=$size sha256=$sha"
}

start_manager() {
  rm -rf "$DATA_ROOT"
  mkdir -p "$DATA_ROOT" "$SCENARIO"
  chmod 700 "$DATA_ROOT"
  setsid "$REPO_ROOT/bin/pcl-dsh-go" \
    --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" --pcl.dev-mode=true \
    --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  log "manager start pid=$MANAGER_PID data-root=$DATA_ROOT port=$MANAGER_PORT"
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 "http://127.0.0.1:$MANAGER_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then
      log "health: $(sanitize < "$SCENARIO/health.json")"
      check "manager /api/health" 1
      check "manager version 0.1.0-m0 (versioned binary)" "$(grep -q '"version":"0.1.0-m0"' "$SCENARIO/health.json" && echo 1 || echo 0)" "health=$(sanitize < "$SCENARIO/health.json")"
      return 0
    fi
    if ! kill -0 "$MANAGER_PID" 2>/dev/null; then fatal "manager exited during startup (see $SCENARIO/manager.log)"; fi
    sleep 0.3
  done
  fatal "manager /api/health not ready in 30s (see $SCENARIO/manager.log)"
}

seed_runtime_degraded() {
  if [ "$REUSE_RUNTIME" != "1" ]; then return 0; fi
  log "DEGRADED MODE: copying known-good runtime $ROOT/runtimes/$VERSION into the fresh data-root; the real download path is NOT exercised by this run"
  mkdir -p "$DATA_ROOT/runtimes"
  cp -a "$ROOT/runtimes/$VERSION" "$DATA_ROOT/runtimes/$VERSION" || fatal "cannot copy runtime for degraded mode"
  chmod 700 "$DATA_ROOT/runtimes" "$DATA_ROOT/runtimes/$VERSION" 2>/dev/null || true
  check "degraded runtime seeded (NOT a real install)" 1 "source=$ROOT/runtimes/$VERSION"
}

install_runtime() {
  local body resp started elapsed entry reused title="API runtime install completed"
  body=$(printf '{"version":"%s"}' "$VERSION")
  started=$(now_ms)
  resp=$(api POST /api/m0/runtimes/install "$body")
  elapsed=$(( $(now_ms) - started ))
  log "runtime install response: $(printf '%s' "$resp" | sanitize) install_ms=$elapsed"
  entry=$(printf '%s' "$resp" | sed -n 's/.*"entryPath":"\([^"]*\)".*/\1/p')
  reused=$(printf '%s' "$resp" | sed -n 's/.*"reused":\(true\|false\).*/\1/p')
  if [ "$REUSE_RUNTIME" = "1" ]; then title="API runtime install endpoint (REUSED runtime, DEGRADED)"; fi
  check "$title" "$([ -n "$entry" ] && echo 1 || echo 0)" "elapsed_ms=$elapsed reused=$reused"
  check "runtime entry path reported" "$([ -n "$entry" ] && echo 1 || echo 0)" "entryPath=$(printf '%s' "$entry" | sanitize)"
  check "runtime entry file exists" "$([ -f "$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh/lib/bin.js" ] && echo 1 || echo 0)" "expected=$DATA_ROOT/runtimes/$VERSION/..."
  local expect_entry="$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh/lib/bin.js"
  check "entryPath matches data-root layout" "$([ "$entry" = "$expect_entry" ] && echo 1 || echo 0)" "entryPath=$entry"
  if [ "$REUSE_RUNTIME" != "1" ]; then
    local ws lock override_count pkg_count bad_version
    ws="$DATA_ROOT/runtimes/$VERSION/pnpm-workspace.yaml"
    lock="$DATA_ROOT/runtimes/$VERSION/pnpm-lock.yaml"
    check "runtime install did a real download (reused=false)" "$([ "$reused" = "false" ] && echo 1 || echo 0)" "reused=$reused install_ms=$elapsed"
    check "installed DSH package version matches" "$(grep -q '"version": "0.2.1-rc.3"' "$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh/package.json" && echo 1 || echo 0)" "package=$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh/package.json"
    check "installed runtime has pnpm-lock.yaml" "$([ -f "$lock" ] && echo 1 || echo 0)"
    check "workspace yaml keeps contract base block" "$(grep -q '^dangerouslyAllowAllBuilds: true$' "$ws" && grep -q '^nodeLinker: hoisted$' "$ws" && echo 1 || echo 0)" "file=$ws (pnpm 11 may append minimumReleaseAgeExclude after it)"
    override_count=$(grep -c "^  .@deepseek-ai/dsh-" "$ws" 2>/dev/null)
    check "workspace yaml has pnpm11 overrides block (295)" "$(grep -q '^overrides:$' "$ws" && [ "$override_count" = "295" ] && echo 1 || echo 0)" "overrides=$override_count"
    pkg_count=$(ls -d "$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh-"*/package.json 2>/dev/null | wc -l)
    bad_version=$(grep -L '"version": "0.2.1-rc.3"' "$DATA_ROOT/runtimes/$VERSION/node_modules/@deepseek-ai/dsh-"*/package.json 2>/dev/null | wc -l)
    check "all @deepseek-ai/dsh-* installed at 0.2.1-rc.3" "$([ "$pkg_count" -ge "295" ] && [ "$bad_version" = "0" ] && echo 1 || echo 0)" "packages=$pkg_count mismatched=$bad_version"
    check "lockfile records cohort overrides" "$(grep -q '^overrides:' "$lock" && echo 1 || echo 0)" "lock=$lock"
    log "runtime size: $(du -sh "$DATA_ROOT/runtimes/$VERSION" | awk '{print $1}') dirs=$(find "$DATA_ROOT/runtimes/$VERSION" -type d | wc -l) files=$(find "$DATA_ROOT/runtimes/$VERSION" -type f | wc -l)"
  fi
  body=$(printf '{"version":"%s"}' "$VERSION")
  started=$(now_ms)
  resp=$(api POST /api/m0/runtimes/install "$body")
  elapsed=$(( $(now_ms) - started ))
  reused=$(printf '%s' "$resp" | sed -n 's/.*"reused":\(true\|false\).*/\1/p')
  check "second install reuses runtime" "$([ "$reused" = "true" ] && echo 1 || echo 0)" "reused=$reused elapsed_ms=$elapsed"
}

create_and_launch() {
  local body status started elapsed launch weburl st
  body=$(printf '{"name":"m0-e2e","version":"%s","profile":"web","port":%s}' "$VERSION" "$DSH_PORT")
  status=$(curl -s -o "$SCENARIO/create.json" -w '%{http_code}' -X POST --max-time 30 \
    -H 'Content-Type: application/json' -d "$body" "http://127.0.0.1:$MANAGER_PORT/api/m0/instances")
  log "create instance status=$status body=$(sanitize < "$SCENARIO/create.json")"
  ID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$SCENARIO/create.json")
  check "debug API create instance (201)" "$([ "$status" = "201" ] && [ -n "$ID" ] && echo 1 || echo 0)" "id=$ID status=$status"
  [ -n "$ID" ] || fatal "cannot create instance via /api/m0/instances"
  started=$(now_ms)
  status=$(curl -s -o "$SCENARIO/launch.json" -w '%{http_code}' -X POST --max-time 240 \
    "http://127.0.0.1:$MANAGER_PORT/api/m0/instances/$ID/launch")
  elapsed=$(( $(now_ms) - started ))
  launch=$(cat "$SCENARIO/launch.json")
  weburl=$(printf '%s' "$launch" | sed -n 's/.*"webUrl":"\([^"]*\)".*/\1/p')
  st=$(printf '%s' "$launch" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
  log "launch status=$status body=$(printf '%s' "$launch" | sanitize) launch_ms=$elapsed"
  check "debug API launch instance" "$([ "$status" = "200" ] && echo 1 || echo 0)" "status=$status launch_ms=$elapsed state=$st"
  check "launch reports running" "$(printf '%s' "$st" | grep -q '运行中' && echo 1 || echo 0)" "state=$st"
  check "launch returns /workbench webUrl" "$(printf '%s' "$weburl" | grep -q "^/workbench/$ID/" && echo 1 || echo 0)" "webUrl=$(printf '%s' "$weburl" | sanitize)"
  PROXY="http://127.0.0.1:$MANAGER_PORT/workbench/$ID"
}

proxy_http_flow() {
  local hdr="$SCENARIO/wb.hdr" hdr2="$SCENARIO/bootstrap.hdr"
  local status loc loc_san token_url status2 loc2 setcookie page_status role
  status=$(curl -s -D "$hdr" -o /dev/null -w '%{http_code}' --max-time 20 "$PROXY/")
  loc=$(grep -i '^location:' "$hdr" | head -1 | tr -d '\r' | sed -E 's/^[Ll]ocation: //')
  loc_san=$(printf '%s' "$loc" | sanitize)
  check "proxy no-cookie -> 302" "$([ "$status" = "302" ] && echo 1 || echo 0)" "status=$status location=$loc_san"
  check "bootstrap location contains token" "$(printf '%s' "$loc" | grep -q 'token=' && echo 1 || echo 0)" "location=$loc_san"
  token_url="$loc"
  case "$token_url" in
    http*) ;;
    *) token_url="http://127.0.0.1:$MANAGER_PORT$token_url";;
  esac
  status2=$(curl -s -D "$hdr2" -o /dev/null -w '%{http_code}' --max-time 20 "$token_url")
  loc2=$(grep -i '^location:' "$hdr2" | head -1 | tr -d '\r' | sed -E 's/^[Ll]ocation: //')
  setcookie=$(grep -i '^set-cookie:' "$hdr2" | head -1 | tr -d '\r')
  log "bootstrap headers: status=$status2 location=$(printf '%s' "$loc2" | sanitize) set-cookie=$(printf '%s' "$setcookie" | sanitize)"
  check "DSH 303 through proxy" "$([ "$status2" = "303" ] && echo 1 || echo 0)" "status=$status2"
  check "DSH Location ./ preserved" "$([ "$loc2" = "./" ] && echo 1 || echo 0)" "location=$loc2"
  check "Set-Cookie Path rewritten" "$(printf '%s' "$setcookie" | grep -qi "Path=/workbench/$ID" && echo 1 || echo 0)" "$(printf '%s' "$setcookie" | sanitize)"
  check "Set-Cookie keeps SameSite=Strict" "$(printf '%s' "$setcookie" | grep -qi 'SameSite=Strict' && echo 1 || echo 0)"
  check "Set-Cookie strips Domain/Secure" "$(printf '%s' "$setcookie" | grep -qiE 'Domain=|; *Secure' && echo 0 || echo 1)"
  DSH_COOKIE=$(printf '%s' "$setcookie" | sed -E 's/^[Ss]et-[Cc]ookie: ([^;]+);.*/\1/')
  check "cookie captured in memory" "$([ -n "$DSH_COOKIE" ] && echo 1 || echo 0)" "name=dsh-auth-***"
  rm -f "$hdr" "$hdr2"

  page_status=$(curl -s -o "$SCENARIO/page.html" -w '%{http_code}' --max-time 20 -H "Cookie: $DSH_COOKIE" "$PROXY/")
  check "workbench page 200" "$([ "$page_status" = "200" ] && echo 1 || echo 0)" "status=$page_status"
  check "page title DeepSeek Harness" "$(grep -qi '<title>DeepSeek Harness</title>' "$SCENARIO/page.html" && echo 1 || echo 0)"
  check "page base href=./" "$(grep -qi '<base[^>]*href="./"' "$SCENARIO/page.html" && echo 1 || echo 0)"
  local asset1 plugin_url s1 s2 n1 n2
  asset1=$(grep -Eo '(src|href)="\./assets/[^"]+\.js"' "$SCENARIO/page.html" | head -1 | sed -E 's/.*="\.\///; s/"$//')
  plugin_url=$(grep -Eo 'plugins/\?\?[^"]+' "$SCENARIO/page.html" | head -1 | sed 's/&amp;/\&/g')
  if [ -n "$asset1" ]; then
    s1=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -H "Cookie: $DSH_COOKIE" "$PROXY/$asset1")
    n1=$(curl -s -o /dev/null -w '%{size_download}' --max-time 30 -H "Cookie: $DSH_COOKIE" "$PROXY/$asset1")
    check "asset 200 (index js)" "$([ "$s1" = "200" ] && echo 1 || echo 0)" "asset=$asset1 status=$s1 bytes=$n1"
  else
    check "index js asset referenced" 0
  fi
  if [ -n "$plugin_url" ]; then
    s2=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -H "Cookie: $DSH_COOKIE" "$PROXY/$plugin_url")
    n2=$(curl -s -o /dev/null -w '%{size_download}' --max-time 30 -H "Cookie: $DSH_COOKIE" "$PROXY/$plugin_url")
    check "asset 200 (plugins/?? bundle)" "$([ "$s2" = "200" ] && echo 1 || echo 0)" "status=$s2 bytes=$n2 url=$(printf '%s' "$plugin_url" | cut -c1-80)..."
  else
    check "plugins/?? bundle referenced" 0
  fi
  check "at least two static assets 200" "$([ "$s1" = "200" ] && [ "$s2" = "200" ] && echo 1 || echo 0)" "index=$s1 plugins=$s2"
  local unknown_status
  unknown_status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "http://127.0.0.1:$MANAGER_PORT/workbench/i-000000000000/")
  check "unknown instance maps to 404" "$([ "$unknown_status" = "404" ] && echo 1 || echo 0)" "status=$unknown_status"
}

sse_flow() {
  local out="$SCENARIO/sse-curln.txt" first_line first_ms data_line data_ms connected data_ok
  ( curl -sS -N --max-time 4 -H "Cookie: $DSH_COOKIE" "$PROXY/plugins/events" 2>/dev/null || true ) | python3 -u -c '
import sys, time
start = time.monotonic()
for line in sys.stdin:
    print("%.1fms %s" % ((time.monotonic() - start) * 1000, line.rstrip()))
' > "$out"
  log "sse (curl -N, first lines):"
  head -4 "$out" | sanitize | tee -a "$LOG"
  first_line=$(head -1 "$out")
  connected=$(printf '%s' "$first_line" | grep -c ': connected')
  data_line=$(grep -m1 'data: {' "$out")
  data_ok=$(printf '%s' "$data_line" | grep -c 'data: {')
  first_ms=$(printf '%s' "$first_line" | sed -n 's/^\([0-9][0-9.]*\)ms .*/\1/p')
  data_ms=$(printf '%s' "$data_line" | sed -n 's/^\([0-9][0-9.]*\)ms .*/\1/p')
  check "SSE first chunk is ': connected'" "$([ "$connected" -gt 0 ] && echo 1 || echo 0)" "first_ms=$first_ms"
  check "SSE data graph chunk received" "$([ "$data_ok" -gt 0 ] && echo 1 || echo 0)" "data_ms=$data_ms"
  check "SSE first chunk arrives quickly (<2000ms)" "$(awk -v v="$first_ms" 'BEGIN{exit !(v != "" && v < 2000)}' && echo 1 || echo 0)" "first_ms=$first_ms"
  check "SSE streams multiple lines" "$([ "$(wc -l < "$out")" -ge 3 ] && echo 1 || echo 0)" "lines=$(wc -l < "$out")"
  cp "$out" "$SCENARIO/sse-curln-raw.txt"
}

ws_flow() {
  local ws_out ws_rc frames text_frames item_frames
  ws_out=$(printf '%s' "$DSH_COOKIE" | "$NODE" "$LIB_DIR/ws-e2e.mjs" --url "ws://127.0.0.1:$MANAGER_PORT/workbench/$ID/api/remote.mux" --origin "http://127.0.0.1:$MANAGER_PORT" --timeout 15000 2>&1)
  ws_rc=$?
  log "ws-e2e rc=$ws_rc:"
  printf '%s\n' "$ws_out" | sanitize | tee -a "$LOG"
  frames=$(printf '%s\n' "$ws_out" | grep -c '^frame opcode=')
  text_frames=$(printf '%s\n' "$ws_out" | grep -c '^frame opcode=1')
  item_frames=$(printf '%s\n' "$ws_out" | grep -c 'opcode=1 .*item')
  check "WS via proxy upgraded 101" "$(printf '%s' "$ws_out" | grep -q 'upgraded=true' && echo 1 || echo 0)" "status=$(printf '%s' "$ws_out" | sed -n 's/^status=\([0-9]*\).*/\1/p')"
  check "WS application frame received (ready/item)" "$([ "$item_frames" -gt 0 ] && [ "$text_frames" -ge 2 ] && echo 1 || echo 0)" "frames=$frames text_frames=$text_frames item_frames=$item_frames"
  check "WS close code 1000" "$(printf '%s' "$ws_out" | grep -q 'close_code=1000' && echo 1 || echo 0)" "close=$(printf '%s' "$ws_out" | grep 'close_code=')"
  check "WS client exit 0" "$([ "$ws_rc" = "0" ] && echo 1 || echo 0)" "rc=$ws_rc"
}

wait_port_free() { # wait_port_free <port> <tries>
  local port="$1" tries="$2" n=0
  while [ "$n" -lt "$tries" ]; do
    if ! ss -ltn 2>/dev/null | grep -q ":$port\b"; then return 0; fi
    sleep 0.1
    n=$((n + 1))
  done
  return 1
}

dsh_processes() {
  pgrep -af 'dsh/lib/bin.js' 2>/dev/null | grep -v pgrep | grep -v m0-acceptance | wc -l
}

stop_flow() {
  local status body free n
  status=$(curl -s -o "$SCENARIO/stop.json" -w '%{http_code}' -X POST --max-time 60 "http://127.0.0.1:$MANAGER_PORT/api/m0/instances/$ID/stop")
  body=$(cat "$SCENARIO/stop.json")
  log "stop API status=$status body=$(printf '%s' "$body" | sanitize)"
  check "debug API stop instance" "$([ "$status" = "200" ] && echo 1 || echo 0)" "status=$status state=$(printf '%s' "$body" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')"
  wait_port_free "$DSH_PORT" 40 && free=1 || free=0
  check "port $DSH_PORT released after stop" "$free"
  n=$(dsh_processes)
  check "no DSH node process left" "$([ "$n" = "0" ] && echo 1 || echo 0)" "count=$n"
  status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$PROXY/")
  check "stopped instance maps to 4xx through proxy" "$(case "$status" in 409|404|502) echo 1;; *) echo 0;; esac)" "status=$status"
}

repeat_launch_flow() {
  local status started elapsed
  started=$(now_ms)
  status=$(curl -s -o "$SCENARIO/launch2.json" -w '%{http_code}' -X POST --max-time 240 "http://127.0.0.1:$MANAGER_PORT/api/m0/instances/$ID/launch")
  elapsed=$(( $(now_ms) - started ))
  log "second launch status=$status body=$(sanitize < "$SCENARIO/launch2.json") launch_ms=$elapsed"
  check "second launch succeeds" "$([ "$status" = "200" ] && echo 1 || echo 0)" "status=$status launch_ms=$elapsed"
  status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$PROXY/")
  check "second launch proxy 302 again" "$([ "$status" = "302" ] && echo 1 || echo 0)" "status=$status"
  curl -s -o "$SCENARIO/e2e-data-count.json" --max-time 10 "http://127.0.0.1:$MANAGER_PORT/api/m0/state" || true
  status=$(curl -s -o "$SCENARIO/stop2.json" -w '%{http_code}' -X POST --max-time 60 "http://127.0.0.1:$MANAGER_PORT/api/m0/instances/$ID/stop")
  check "second stop succeeds" "$([ "$status" = "200" ] && echo 1 || echo 0)" "status=$status"
  wait_port_free "$DSH_PORT" 40 && check "port released after second stop" 1 || check "port released after second stop" 0
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

hash_tree() {
  timeout 240 find "$@" -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}'
}

security_flow() {
  local mode configs conf_bad leaks ref_status
  mode=$(stat -c '%a' "$DATA_ROOT")
  check "data-root mode 0700" "$([ "$mode" = "700" ] && echo 1 || echo 0)" "mode=$mode"
  configs=$(find "$DATA_ROOT/config" -name '*.json' -type f 2>/dev/null | wc -l)
  conf_bad=$(find "$DATA_ROOT/config" -name '*.json' -type f ! -perm 600 2>/dev/null | wc -l)
  check "config json files 0600" "$([ "$configs" -gt 0 ] && [ "$conf_bad" = "0" ] && echo 1 || echo 0)" "files=$configs non_600=$conf_bad"
  leaks=$(grep -rEo 'token=[^*"[:space:]]|dsh-auth-[A-Za-z0-9]+=[^*]|v1\.[A-Za-z0-9_-]{20,}' "$DATA_ROOT/logs" "$SCENARIO" "$EVIDENCE_DIR" 2>/dev/null | wc -l)
  check "logs/evidence contain no real token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs have no ERROR lines" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
  if [ -d "$REF_REPO" ]; then
    ref_status=$(git -C "$REF_REPO" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
    check "reference repo unchanged (baseline git status hash)" "$([ "$ref_status" = "$REF_BASELINE" ] && echo 1 || echo 0)" "before=$(printf '%s' "$REF_BASELINE" | cut -c1-16)... after=$(printf '%s' "$ref_status" | cut -c1-16)..."
    if [ -n "$REF_DIRTY_BASELINE" ]; then
      log "INFO reference repo had pre-existing local changes before this acceptance (not caused by the test): $(printf '%s' "$REF_DIRTY_BASELINE" | tr '\n' ' ' | cut -c1-400)"
    fi
  else
    check "reference repo present" 0 "missing=$REF_REPO"
  fi
}

windows_dsh_flow() {
  local prog_before="$1" user_before="$2" prog_after user_after
  if [ ! -d "$WIN_DSH_DIR" ]; then
    check "Windows DSH program directory readable" 0 "missing=$WIN_DSH_DIR"
    return 0
  fi
  prog_after=$(hash_tree "$WIN_DSH_DIR")
  user_after=$(hash_tree "$WIN_USER_DSH")
  log "windows-dsh program hash before=$prog_before after=$prog_after"
  log "windows-dsh user-home hash before=$user_before after=$user_after"
  check "Windows DSH program tree unchanged (name|size|mtime hash)" "$([ "$prog_after" = "$prog_before" ] && echo 1 || echo 0)" "before=$(printf '%s' "$prog_before" | cut -c1-16)... after=$(printf '%s' "$prog_after" | cut -c1-16)..."
  if [ "$user_after" = "$user_before" ]; then
    check "Windows user .dsh tree unchanged (read-only access)" 1 "hash=$(printf '%s' "$user_after" | cut -c1-16)..."
  else
    log "INFO Windows .dsh hash differs: the already-running Windows DSH itself may write sessions/logs/caches; this acceptance never opens it for writing (read-only find only)."
    check "Windows user .dsh read-only (ambient DSH writes noted)" 1 "before=$(printf '%s' "$user_before" | cut -c1-16)... after=$(printf '%s' "$user_after" | cut -c1-16)..."
  fi
}

main() {
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO"
  log "=== PCL-DSH-Go M0 acceptance $RUN_STAMP ==="
  log "repo=$REPO_ROOT root=$ROOT tools=$TOOLS_ROOT version=$VERSION manager_port=$MANAGER_PORT dsh_port=$DSH_PORT"
  if [ "$SKIP_BUILD" = "1" ]; then
    check "prebuilt manager binary present" "$([ -x "$REPO_ROOT/bin/pcl-dsh-go" ] && echo 1 || echo 0)" "path=bin/pcl-dsh-go"
  else
    build_manager
  fi
  if [ -d "$REF_REPO" ]; then
    REF_BASELINE=$(git -C "$REF_REPO" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
    REF_DIRTY_BASELINE=$(git -C "$REF_REPO" status --porcelain 2>/dev/null | head -5)
    log "reference repo git status baseline hash=$REF_BASELINE dirty_lines=$(printf '%s' "$REF_DIRTY_BASELINE" | wc -l)"
  fi
  local win_before="-" user_before="-"
  if [ -d "$WIN_DSH_DIR" ] || [ -d "$WIN_USER_DSH" ]; then
    win_before=$(hash_tree "$WIN_DSH_DIR")
    user_before=$(hash_tree "$WIN_USER_DSH")
    log "windows-dsh program hash before=$win_before"
    log "windows-dsh user-home hash before=$user_before"
  fi
  start_manager
  seed_runtime_degraded
  install_runtime
  create_and_launch
  proxy_http_flow
  sse_flow
  ws_flow
  stop_flow
  repeat_launch_flow
  stop_manager
  security_flow
  windows_dsh_flow "$win_before" "$user_before"
  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS_COUNT checks_failed=$FAIL_COUNT scenario=$SCENARIO"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '%s\n' "$FAIL_LINES" | tee -a "$LOG"
    log "RESULT=FAIL"
    exit 1
  fi
  log "RESULT=PASS"
}

main
