#!/usr/bin/env bash
# PCL-DSH-Go M5 acceptance (G9/G10): release layout, clean isolated reinstall smoke,
# and read-only live WSL deployment checks (Nginx :80 + systemd).
#
# Usage:
#   bash scripts/wsl/m5-acceptance.sh --selfcheck
#   M5_RELEASE=/path/to/pcl-dsh-go-1.0.0-linux-amd64 bash scripts/wsl/m5-acceptance.sh --isolated
#   M5_LIVE_READY=1 bash scripts/wsl/m5-acceptance.sh --live
#   bash scripts/wsl/m5-acceptance.sh --full
# Evidence: docs/evidence/m5/m5-acceptance-<ts>.log.txt (sanitized) + EXIT_CODE.
# Live is READ-ONLY: never changes the user password, /opt, systemd or nginx config.
set -o pipefail
export NO_PROXY='*' no_proxy='*'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="${PCL_DSH_TEST_ROOT:-/tmp/pcl-dsh-go-test}/m5"
STAMP="$(date +%Y%m%d-%H%M%S)"
SCENARIO="$ROOT/m5-scenario-$STAMP"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m5"
LOG="$EVIDENCE_DIR/m5-acceptance-$STAMP.log"
MODE=""
RELEASE=""
PASS=0; FAIL=0; INFO=0; FAILS=""
MANAGER_PID=""
ISO_PORT=18085
LIVE_URL="${M5_LIVE_URL:-http://127.0.0.1}"
LIVE_DATA="${M5_LIVE_DATA:-/var/lib/pcl-dsh-go}"
LIVE_PREFIX="${M5_LIVE_PREFIX:-/opt/pcl-dsh-go}"
EXPECTED_VERSION="${M5_VERSION:-1.0.0}"
NGINX_CONFIG=""

san() {
  sed -E \
    -e 's/([?&]token=)[^&"[:space:]]+/\1***/g' \
    -e 's/\btoken=[^&"[:space:]]+/token=***/g' \
    -e 's/(JSESSIONID|XSRF-TOKEN)=[^;,"[:space:]]+/\1=***/g' \
    -e 's/([Pp]assword[^:]*:[[:space:]]*)"[^"]*"/\1"***"/g'
}
log() { printf '%s\n' "$*" | tee -a "$LOG"; }
info() { INFO=$((INFO + 1)); log "INFO  $*"; }
check() { local n="$1" ok="$2" d="$3"; if [ "$ok" = "1" ]; then PASS=$((PASS+1)); log "PASS  $n  $d"; else FAIL=$((FAIL+1)); FAILS="$FAILS
FAIL  $n  $d"; log "FAIL  $n  $d"; fi; }
fatal() { log "FATAL: $*"; exit 1; }
now_ms() { printf '%s' "$EPOCHREALTIME" | tr -d '.' | cut -c1-13; }
file_mode() { stat -c '%a' "$1" 2>/dev/null || echo ""; }
port_listening() { ss -ltn 2>/dev/null | grep -q ":$1\b" && echo 1 || echo 0; }
wait_port_free() { local p="$1" t="$2" n=0; while [ "$n" -lt "$t" ]; do ss -ltn 2>/dev/null | grep -q ":$p\b" || return 0; sleep 0.1; n=$((n+1)); done; return 1; }
http_code() {
  local url="$1" out="$2" code
  code=$(curl -sS --noproxy '*' --max-time 20 -o "$out" -w '%{http_code}' "$url" 2>"$SCENARIO/curl.err") || code="000"
  printf '%s' "$code"
}
jget() {
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    value = json.load(open(sys.argv[1], encoding='utf-8'))
except Exception:
    sys.exit(2)
for part in sys.argv[2].split('.'):
    if isinstance(value, dict) and part in value:
        value = value[part]
    elif isinstance(value, list) and part.isdigit() and int(part) < len(value):
        value = value[int(part)]
    else:
        sys.exit(2)
print(value if not isinstance(value, (dict, list)) else json.dumps(value, ensure_ascii=False))
PY
}
cleanup() {
  local code=$?
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true; sleep 1; kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true; fi
  if [ "$code" -ne 0 ]; then log ""; log "RESULT=FAIL scenario=$SCENARIO"; fi
  log "EXIT_CODE=$code scenario=$SCENARIO"
  mkdir -p "$EVIDENCE_DIR"
  if [ -f "$LOG" ]; then cp "$LOG" "$LOG.raw" 2>/dev/null || true; sand_ok=1; san < "$LOG.raw" > "$LOG.txt" 2>/dev/null || sand_ok=0; rm -f "$LOG.raw" 2>/dev/null || true; fi
  if [ -f "$SCENARIO/manager.log" ]; then san < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/m5-acceptance-$STAMP-manager.log.txt" 2>/dev/null || true; fi
  exit "$code"
}
trap cleanup EXIT

require_cmd() { command -v "$1" >/dev/null 2>&1 && check "tool available: $1" 1 || check "tool available: $1" 0; }

detect_release() {
  if [ -n "${M5_RELEASE:-}" ] && [ -e "$M5_RELEASE" ]; then RELEASE="$M5_RELEASE"; return 0; fi
  local candidate
  for candidate in "$REPO_ROOT/release"/pcl-dsh-go-*linux-amd64 "$REPO_ROOT"/pcl-dsh-go-*linux-amd64; do
    [ -d "$candidate" ] && { RELEASE="$candidate"; return 0; }
  done
  for candidate in "$REPO_ROOT/release"/*.tar.gz "$REPO_ROOT"/*.tar.gz; do
    [ -f "$candidate" ] && { RELEASE="$candidate"; return 0; }
  done
  return 1
}
unpack_release() {
  local release="$1"
  mkdir -p "$SCENARIO/release"
  if [ -d "$release" ]; then
    cp -a "$release" "$SCENARIO/release/"; REL_DIR="$SCENARIO/release/$(basename "$release")";
  else
    tar -xzf "$release" -C "$SCENARIO/release" || fatal "extract release failed"
    REL_DIR="$(find "$SCENARIO/release" -mindepth 1 -maxdepth 1 -type d | head -1)"
  fi
  [ -n "$REL_DIR" ] && [ -d "$REL_DIR" ] || fatal "release directory not found"
}
find_packaging_file() { # <name> -> path from release packaging or repo packaging
  local name="$1" c
  for c in "$REL_DIR/packaging/$name" "$REL_DIR/packaging/systemd/$name" "$REPO_ROOT/packaging/$name" "$REPO_ROOT/packaging/systemd/$name" "$REPO_ROOT/packaging/nginx/$name"; do
    [ -f "$c" ] && { printf '%s' "$c"; return 0; }
  done
  return 1
}

selfcheck() {
  log "=== M5 acceptance selfcheck $STAMP ==="
  bash -n "$0" && check "bash -n m5-acceptance.sh" 1 || check "bash -n m5-acceptance.sh" 0
  for doc in docs/M5_INTERFACES.md docs/IMPLEMENTATION_SPEC.md docs/ACCEPTANCE.md packaging/nginx/pcl-dsh-go.conf; do
    check "input exists: $doc" "$([ -f "$REPO_ROOT/$doc" ] && echo 1 || echo 0)"
  done
  for tool in curl sha256sum tar ss nginx python3 systemctl; do
    if command -v "$tool" >/dev/null 2>&1; then check "tool available: $tool" 1; else info "tool missing (some checks will be skipped): $tool"; fi
  done
  if [ -f "$REPO_ROOT/scripts/build-release.sh" ]; then bash -n "$REPO_ROOT/scripts/build-release.sh" && check "bash -n scripts/build-release.sh" 1 || check "bash -n scripts/build-release.sh" 0; else info "release build script not present yet (task-60 pending)"; fi
  local nginx_conf="$REPO_ROOT/packaging/nginx/pcl-dsh-go.conf"
  if [ -f "$nginx_conf" ] && command -v nginx >/dev/null 2>&1; then
    mkdir -p "$SCENARIO/nginx/conf" "$SCENARIO/nginx/static/assets"
    printf '<!doctype html><html><head><title>PCL-DSH-Go</title></head><body>static</body></html>' > "$SCENARIO/nginx/static/index.html"
    printf 'window.__M5__=true;' > "$SCENARIO/nginx/static/assets/app.js"
    sed -e 's/listen 80;/listen 18088;/' -e "s#/opt/pcl-dsh-go/current/release/static#$SCENARIO/nginx/static#g" -e 's#127.0.0.1:8080#127.0.0.1:18080#g' "$nginx_conf" > "$SCENARIO/nginx/conf/site.conf"
    cat > "$SCENARIO/nginx/conf/nginx.conf" <<EOF
user root;
worker_processes 1;
pid $SCENARIO/nginx/nginx.pid;
error_log $SCENARIO/nginx/error.log warn;
events { worker_connections 64; }
http {
  include /etc/nginx/mime.types;
  default_type application/octet-stream;
  access_log $SCENARIO/nginx/access.log;
  client_body_temp_path $SCENARIO/nginx/body;
  proxy_temp_path $SCENARIO/nginx/proxy;
  include $SCENARIO/nginx/conf/site.conf;
}
EOF
    nginx -t -c "$SCENARIO/nginx/conf/nginx.conf" -p "$SCENARIO/nginx" > "$SCENARIO/nginx/nginx-t.log" 2>&1
    check "nginx -t packaging template (adapted to isolated ports/root)" "$([ $? -eq 0 ] && echo 1 || echo 0)" "$(tail -1 "$SCENARIO/nginx/nginx-t.log")"
  else
    info "nginx template not tested (packaging or nginx binary missing)"
  fi
  if detect_release; then
    info "release candidate detected: $RELEASE (isolated mode can validate it)"
  else
    info "no release package found yet (task-60); isolated/full checks pending"
  fi
  if curl -sS --noproxy '*' --max-time 3 "$LIVE_URL/" 2>/dev/null | grep -q 'PCL-DSH-Go'; then
    info "live deployment looks present at $LIVE_URL (run --live for read-only checks)"
  else
    info "live deployment not ready yet (task-61); --live pending"
  fi
  log ""; log "selfcheck: pass=$PASS fail=$FAIL info=$INFO"
  if [ "$FAIL" -gt 0 ]; then log "SELFCHECK=FAIL"; exit 1; fi
  log "SELFCHECK=OK"
}

validate_release_tree() {
  local rel="$1"
  check "release has bin/pcl-dsh-go" "$([ -x "$rel/bin/pcl-dsh-go" ] && echo 1 || echo 0)"
  check "release has static/index.html" "$([ -f "$rel/static/index.html" ] && echo 1 || echo 0)"
  check "release has tools/node/bin/node" "$([ -x "$rel/tools/node/bin/node" ] && echo 1 || echo 0)"
  check "release has embedded pnpm" "$([ -d "$rel/tools/pnpm" ] && echo 1 || echo 0)"
  check "release has VERSION" "$([ -f "$rel/VERSION" ] && echo 1 || echo 0)" "value=$([ -f "$rel/VERSION" ] && tr -d '\n' < "$rel/VERSION")"
  for f in install.sh uninstall.sh reset.sh pcl-dsh-go.service; do
    if find_packaging_file "$f" >/dev/null; then check "packaging script present: $f" 1; else check "packaging script present: $f" 0; fi
  done
  if [ -f "$rel/SHA256SUMS" ]; then
    ( cd "$rel" && sha256sum -c SHA256SUMS > "$SCENARIO/sha256.log" 2>&1 )
    check "sha256sum -c release/SHA256SUMS" "$([ $? -eq 0 ] && echo 1 || echo 0)" "$(tail -1 "$SCENARIO/sha256.log")"
  else
    info "release has no SHA256SUMS (expected for directory payload)"
  fi
  local bad
  bad=$(find "$rel" -type f \( -name '*.log' -o -name 'initial-password' -o -name 'admin.json' -o -name '.credentials*' -o -name '*.token' \) 2>/dev/null | head -3)
  check "release payload contains no runtime data/secrets" "$([ -z "$bad" ] && echo 1 || echo 0)" "found=$bad"
}

isolated() {
  log "=== M5 isolated clean-install smoke $STAMP ==="
  if ! detect_release; then log "RESULT=PENDING (release package missing; task-60)"; log "ISOLATED=SKIPPED"; exit 3; fi
  log "release input: $RELEASE"
  unpack_release "$RELEASE"
  validate_release_tree "$REL_DIR"
  # Clean reinstall into an isolated prefix/data dir via the shipped install.sh.
  # --no-service ensures no systemd unit / Nginx site is written (live deployment untouched).
  local installer="$REL_DIR/packaging/install.sh"
  local install_prefix="$SCENARIO/prefix"
  local data="$SCENARIO/install-data"
  if [ -f "$installer" ]; then
    bash "$installer" --prefix "$install_prefix" --data-dir "$data" --no-service > "$SCENARIO/install.log" 2>&1
    check "isolated install.sh --no-service rc=0" "$([ $? -eq 0 ] && echo 1 || echo 0)" "$(tail -1 "$SCENARIO/install.log")"
    check "isolated prefix current -> releases/$EXPECTED_VERSION" "$([ -L "$install_prefix/current" ] && [ "$(readlink -f "$install_prefix/current" | xargs basename)" = "$EXPECTED_VERSION" ] && echo 1 || echo 0)" "current=$(readlink -f "$install_prefix/current" 2>/dev/null)"
    check "isolated install tree bin/static/tools present" "$([ -x "$install_prefix/current/bin/pcl-dsh-go" ] && [ -f "$install_prefix/current/static/index.html" ] && [ -x "$install_prefix/current/tools/node/bin/node" ] && echo 1 || echo 0)"
    check "isolated data dir 0700" "$([ "$(file_mode "$data")" = "700" ] && echo 1 || echo 0)" "mode=$(file_mode "$data")"
    local bin="$install_prefix/current/bin/pcl-dsh-go"
    local toolroot="$install_prefix/current/tools"
    local staticdir="$install_prefix/current/static"
  else
    info "release install.sh missing; falling back to extracted tree smoke"
    local bin="$REL_DIR/bin/pcl-dsh-go"
    local toolroot="$REL_DIR/tools"
    local staticdir="$REL_DIR/static"
    data="$SCENARIO/isolated-data"; mkdir -p "$data"; chmod 700 "$data"
  fi
  if port_listening "$ISO_PORT" | grep -q 1; then
    pkill -f "pcl.data-root=$data" 2>/dev/null || true
    sleep 1
    [ "$(port_listening "$ISO_PORT")" = "0" ] || fatal "isolated port $ISO_PORT is busy (stray manager)"
  fi
  setsid "$bin" --server.address=127.0.0.1 --server.port="$ISO_PORT" \
    --pcl.data-root="$data" --pcl.tool-root="$toolroot" --server.static-dir="$staticdir" \
    > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  local deadline=$(( $(now_ms) + 30000 )) health=0
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --noproxy '*' --max-time 2 "http://127.0.0.1:$ISO_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then health=1; break; fi
    kill -0 "$MANAGER_PID" 2>/dev/null || break
    sleep 0.3
  done
  check "isolated release manager /api/health 200" "$health"
  if [ "$health" = "1" ]; then
    check "health version=$EXPECTED_VERSION" "$([ "$(jget "$SCENARIO/health.json" version)" = "$EXPECTED_VERSION" ] && echo 1 || echo 0)" "value=$(jget "$SCENARIO/health.json" version)"
    local code
    code=$(http_code "http://127.0.0.1:$ISO_PORT/" "$SCENARIO/index.html")
    check "release static GET / 200 + title PCL-DSH-Go" "$([ "$code" = "200" ] && grep -q '<title>PCL-DSH-Go</title>' "$SCENARIO/index.html" && echo 1 || echo 0)" "status=$code"
    code=$(http_code "http://127.0.0.1:$ISO_PORT/instances" "$SCENARIO/spa.html")
    check "SPA fallback /instances 200 index" "$([ "$code" = "200" ] && grep -q '<title>PCL-DSH-Go</title>' "$SCENARIO/spa.html" && echo 1 || echo 0)" "status=$code"
    code=$(curl -sS --noproxy '*' --path-as-is --max-time 20 -o "$SCENARIO/trav.body" -w '%{http_code}' "http://127.0.0.1:$ISO_PORT/assets/%2e%2e/%2e%2e/etc/passwd" 2>/dev/null || echo 000)
    check "static path traversal -> 400/404" "$([ "$code" = "400" ] || [ "$code" = "404" ] && echo 1 || echo 0)" "status=$code"
    code=$(http_code "http://127.0.0.1:$ISO_PORT/api/state" "$SCENARIO/state.json")
    check "unauthenticated /api/state 401" "$([ "$code" = "401" ] && echo 1 || echo 0)" "status=$code"
    check "initial-password exists with mode 0600" "$([ -f "$data/initial-password" ] && [ "$(file_mode "$data/initial-password")" = "600" ] && echo 1 || echo 0)" "mode=$(file_mode "$data/initial-password")"
    check "release node runs" "$([ "$("$REL_DIR/tools/node/bin/node" -v 2>/dev/null | head -c 1)" = "v" ] && echo 1 || echo 0)" "value=$("$REL_DIR/tools/node/bin/node" -v 2>/dev/null)"
    local pw
    pw=$(cat "$data/initial-password" 2>/dev/null)
    if [ -n "$pw" ]; then
      code=$(printf '{"password":"%s"}' "$pw" | curl -sS --noproxy '*' --max-time 15 -o "$SCENARIO/login.json" -w '%{http_code}' -H 'Content-Type: application/json' --data-binary @- "http://127.0.0.1:$ISO_PORT/api/auth/login" 2>/dev/null)
      check "login with isolated initial password 204 (no change)" "$([ "$code" = "204" ] && echo 1 || echo 0)" "status=$code"
    fi
  fi
  kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
  wait_port_free "$ISO_PORT" 30 && check "isolated manager port released" 1 || check "isolated manager port released" 0
  MANAGER_PID=""
  # Upgrade/rollback demonstration inside the isolated prefix (live untouched):
  #  - same-version reinstall rebuilds current; data files must survive
  #  - current symlink switched to a "previous" release copy and restarted; data survives
  local data_admin="$data/config/admin.json"
  local fp_admin fp_init fp_inst
  fp_admin=$(sha256sum "$data_admin" 2>/dev/null | cut -d' ' -f1)
  fp_init=$(sha256sum "$data/initial-password" 2>/dev/null | cut -d' ' -f1)
  fp_inst=$(find "$data/instances" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
  start_iso_manager() {
    setsid "$install_prefix/current/bin/pcl-dsh-go" --server.address=127.0.0.1 --server.port="$ISO_PORT" \
      --pcl.data-root="$data" --pcl.tool-root="$install_prefix/current/tools" \
      --server.static-dir="$install_prefix/current/static" >> "$SCENARIO/manager.log" 2>&1 &
    MANAGER_PID=$!
    local deadline=$(( $(now_ms) + 30000 )) ok=0
    while [ "$(now_ms)" -lt "$deadline" ]; do
      if curl -sf --noproxy '*' --max-time 2 "http://127.0.0.1:$ISO_PORT/api/health" > "$SCENARIO/health2.json" 2>/dev/null; then ok=1; break; fi
      kill -0 "$MANAGER_PID" 2>/dev/null || break
      sleep 0.3
    done
    echo "$ok"
  }
  stop_iso_manager() {
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    wait_port_free "$ISO_PORT" 30 || true
    sleep 1.5
    if [ "$(port_listening "$ISO_PORT")" = "1" ]; then
      pkill -f "pcl.data-root=$data" 2>/dev/null || true
      sleep 1
    fi
    MANAGER_PID=""
  }
  if [ -f "$installer" ]; then
    bash "$installer" --prefix "$install_prefix" --data-dir "$data" --no-service >> "$SCENARIO/install.log" 2>&1
    check "upgrade: same-version reinstall rc=0" "$([ $? -eq 0 ] && echo 1 || echo 0)"
    check "upgrade: current -> releases/$EXPECTED_VERSION after reinstall" "$([ "$(readlink -f "$install_prefix/current" | xargs basename)" = "$EXPECTED_VERSION" ] && echo 1 || echo 0)"
    local ok; ok=$(start_iso_manager)
    check "upgrade: manager healthy after reinstall/restart" "$([ "$ok" = "1" ] && echo 1 || echo 0)"
    if [ "$ok" = "1" ]; then
      local pw2; pw2=$(cat "$data/initial-password" 2>/dev/null)
      local code2; code2=$(printf '{"password":"%s"}' "$pw2" | curl -sS --noproxy '*' --max-time 15 -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' --data-binary @- "http://127.0.0.1:$ISO_PORT/api/auth/login" 2>/dev/null)
      check "upgrade: login still works with existing data" "$([ "$code2" = "204" ] && echo 1 || echo 0)" "status=$code2"
    fi
    stop_iso_manager
    cp -a "$install_prefix/releases/$EXPECTED_VERSION" "$install_prefix/releases/$EXPECTED_VERSION-prev"
    ln -sfn "$install_prefix/releases/$EXPECTED_VERSION-prev" "$install_prefix/current"
    check "rollback: current switched to $EXPECTED_VERSION-prev" "$([ "$(readlink -f "$install_prefix/current" | xargs basename)" = "$EXPECTED_VERSION-prev" ] && echo 1 || echo 0)"
    ok=$(start_iso_manager)
    check "rollback: previous release starts with same data" "$([ "$ok" = "1" ] && echo 1 || echo 0)"
    if [ "$ok" = "1" ]; then
      local code3; code3=$(printf '{"password":"%s"}' "$(cat "$data/initial-password" 2>/dev/null)" | curl -sS --noproxy '*' --max-time 15 -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' --data-binary @- "http://127.0.0.1:$ISO_PORT/api/auth/login" 2>/dev/null)
      check "rollback: login still works with same data" "$([ "$code3" = "204" ] && echo 1 || echo 0)" "status=$code3"
    fi
    stop_iso_manager
    ln -sfn "$install_prefix/releases/$EXPECTED_VERSION" "$install_prefix/current"
    local fp_admin2 fp_init2 fp_inst2
    fp_admin2=$(sha256sum "$data_admin" 2>/dev/null | cut -d' ' -f1)
    fp_init2=$(sha256sum "$data/initial-password" 2>/dev/null | cut -d' ' -f1)
    fp_inst2=$(find "$data/instances" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
    check "upgrade/rollback: data files preserved (admin/initial-password/instances)" "$([ "$fp_admin2" = "$fp_admin" ] && [ "$fp_init2" = "$fp_init" ] && [ "$fp_inst2" = "$fp_inst" ] && echo 1 || echo 0)" "admin=$([ "$fp_admin2" = "$fp_admin" ] && echo same || echo changed) init=$([ "$fp_init2" = "$fp_init" ] && echo same || echo changed) instances=$fp_inst2/$fp_inst"
  fi
  local errs
  errs=$(grep -c '"level":"ERROR"' "$SCENARIO/manager.log" 2>/dev/null || true)
  [ -n "$errs" ] || errs=0
  check "isolated manager logs ERROR count 0" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
  local leaks
  leaks=$(grep -rEo 'token=[A-Za-z0-9._~%+/-]{8,}|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$SCENARIO/manager.log" "$LOG" 2>/dev/null | wc -l)
  check "isolated evidence/logs no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  rm -f "$data/initial-password" "$SCENARIO/jar.txt" 2>/dev/null || true
}

live() {
  log "=== M5 live WSL deployment read-only checks $STAMP ==="
  if [ "${M5_LIVE_READY:-0}" != "1" ] && ! curl -sS --noproxy '*' --max-time 3 "$LIVE_URL/" 2>/dev/null | grep -q 'PCL-DSH-Go'; then
    log "RESULT=PENDING (live deployment not ready; task-61)"; log "LIVE=SKIPPED"; exit 3
  fi
  local code
  code=$(http_code "$LIVE_URL/" "$SCENARIO/live-index.html")
  check "live :80 GET / 200" "$([ "$code" = "200" ] && echo 1 || echo 0)" "status=$code"
  check "live page title PCL-DSH-Go" "$(grep -q '<title>PCL-DSH-Go</title>' "$SCENARIO/live-index.html" && echo 1 || echo 0)"
  code=$(http_code "$LIVE_URL/instances" "$SCENARIO/live-spa.html")
  check "live SPA fallback /instances 200" "$([ "$code" = "200" ] && echo 1 || echo 0)" "status=$code"
  code=$(http_code "$LIVE_URL/api/health" "$SCENARIO/live-health.json")
  check "live /api/health via Nginx 200" "$([ "$code" = "200" ] && echo 1 || echo 0)" "status=$code"
  check "live health version=$EXPECTED_VERSION" "$([ "$(jget "$SCENARIO/live-health.json" version 2>/dev/null)" = "$EXPECTED_VERSION" ] && echo 1 || echo 0)" "value=$(jget "$SCENARIO/live-health.json" version 2>/dev/null)"
  code=$(http_code "$LIVE_URL/api/state" "$SCENARIO/live-state.json")
  check "live /api/state unauthenticated 401" "$([ "$code" = "401" ] && echo 1 || echo 0)" "status=$code"
  code=$(http_code "$LIVE_URL/api/accounts" "$SCENARIO/live-account.json")
  check "live removed /api/accounts -> 404 (or 401 fail-closed when anonymous)" "$([ "$code" = "404" ] || [ "$code" = "401" ] && echo 1 || echo 0)" "status=$code (anonymous 401 = auth-before-routing; authenticated 404 covered by M1/M4 tests)"
  code=$(http_code "$LIVE_URL/workbench/i-000000000000/" "$SCENARIO/live-wb.json")
  check "live /workbench unauthenticated 401" "$([ "$code" = "401" ] && echo 1 || echo 0)" "status=$code"
  local server
  server=$(curl -sS --noproxy '*' -I "$LIVE_URL/" 2>/dev/null | grep -i '^server:' | tr -d '\r')
  check "live Nginx server_tokens off" "$(printf '%s' "$server" | grep -qix 'server: nginx' && echo 1 || echo 0)" "value=$server"
  if command -v systemctl >/dev/null 2>&1; then
    check "systemd unit pcl-dsh-go active" "$(systemctl is-active pcl-dsh-go 2>/dev/null | grep -qx active && echo 1 || echo 0)" "state=$(systemctl is-active pcl-dsh-go 2>/dev/null)"
    check "systemd unit sandbox: NoNewPrivileges/PrivateTmp" "$(systemctl show pcl-dsh-go -p NoNewPrivileges -p PrivateTmp 2>/dev/null | grep -q 'NoNewPrivileges=yes' && systemctl show pcl-dsh-go -p PrivateTmp 2>/dev/null | grep -q 'PrivateTmp=yes' && echo 1 || echo 0)"
  else info "systemctl missing"; fi
  if command -v nginx >/dev/null 2>&1; then
    nginx -t > "$SCENARIO/live-nginx-t.log" 2>&1
    check "live nginx -t" "$([ $? -eq 0 ] && echo 1 || echo 0)" "$(tail -1 "$SCENARIO/live-nginx-t.log")"
  else info "nginx binary missing"; fi
  check "port :80 listening" "$(port_listening 80)" ""
  check "manager 127.0.0.1:8080 listening (behind Nginx)" "$(port_listening 8080)" ""
  check "live prefix current -> releases/$EXPECTED_VERSION" "$([ -L "$LIVE_PREFIX/current" ] && [ "$(readlink -f "$LIVE_PREFIX/current" | xargs -r basename)" = "$EXPECTED_VERSION" ] && echo 1 || echo 0)" "current=$(readlink -f "$LIVE_PREFIX/current" 2>/dev/null | xargs -r basename)"
  check "live data dir mode 0700" "$([ "$(file_mode "$LIVE_DATA")" = "700" ] && echo 1 || echo 0)" "mode=$(file_mode "$LIVE_DATA")"
  check "live initial-password present mode 0600 (content not printed)" "$([ -f "$LIVE_DATA/initial-password" ] && [ "$(file_mode "$LIVE_DATA/initial-password")" = "600" ] && echo 1 || echo 0)" "mode=$(file_mode "$LIVE_DATA/initial-password")"
  local cfg=""
  for cfg in /etc/nginx/sites-enabled/pcl-dsh-go /etc/nginx/sites-enabled/pcl-dsh-go.conf /etc/nginx/sites-available/pcl-dsh-go /etc/nginx/sites-available/pcl-dsh-go.conf /etc/nginx/conf.d/pcl-dsh-go.conf; do
    [ -f "$cfg" ] && { NGINX_CONFIG="$(readlink -f "$cfg")"; break; }
  done
  if [ -n "$NGINX_CONFIG" ]; then
    check "Nginx site config present" 1 "path=$NGINX_CONFIG"
    check "Nginx access_log off on /api and /workbench" "$(python3 -c 'import re,sys; t=open(sys.argv[1],encoding="utf-8",errors="replace").read(); b=re.findall(r"location\s+\^~\s+/(api|workbench)/\s*\{(.*?)\}",t,re.S); sys.exit(0 if len(b)>=2 and all("access_log off;" in x[1] for x in b) else 1)' "$NGINX_CONFIG" && echo 1 || echo 0)"
    check "Nginx config proxy_buffering off for SSE" "$(grep -q 'proxy_buffering off;' "$NGINX_CONFIG" && echo 1 || echo 0)"
  else
    check "Nginx site config present" 0 "not found"
  fi
  if [ -f "$LIVE_DATA/logs/manager.log" ] || ls "$LIVE_DATA/logs"/manager-*.log >/dev/null 2>&1; then
    local leaks
    leaks=$(grep -rEo 'token=[A-Za-z0-9._~%+/-]{8,}|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$LIVE_DATA/logs" 2>/dev/null | wc -l)
    check "live manager logs no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
    local errs
    errs=$(grep -rh '"level":"ERROR"' "$LIVE_DATA/logs" 2>/dev/null | wc -l || true)
    [ -n "$errs" ] || errs=0
    check "live manager logs ERROR count 0" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
  else
    info "live manager logs not readable (permissions); skipped log scan"
  fi
}

case "${1:-}" in
  --selfcheck) MODE=selfcheck;;
  --isolated) MODE=isolated;;
  --live) MODE=live;;
  --full) MODE=full;;
  *) echo "usage: $0 --selfcheck|--isolated|--live|--full" >&2; exit 2;;
esac
mkdir -p "$EVIDENCE_DIR" "$SCENARIO" "$ROOT"
if [ "$MODE" = "full" ]; then selfcheck; isolated; live; else
  case "$MODE" in selfcheck) selfcheck;; isolated) isolated;; live) live;; esac
fi
log ""
log "=== RESULT ==="
log "checks_passed=$PASS checks_failed=$FAIL info=$INFO scenario=$SCENARIO"
if [ "$FAIL" -gt 0 ]; then printf '%s\n' "$FAILS" | tee -a "$LOG"; log "RESULT=FAIL"; exit 1; fi
log "RESULT=PASS"
exit 0
