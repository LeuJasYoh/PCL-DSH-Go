#!/usr/bin/env bash
# PCL-DSH-Go M2-F | Security / error-path E2E: SSRF/host/path/control-char injection,
# auth boundaries, 409/404/502, four-field errors, log-secret scan, platform no-touch.
set -o pipefail
export NO_PROXY='*' no_proxy='*'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="${PCL_DSH_TEST_ROOT:-/tmp/pcl-dsh-go-test}"
TOOLS_ROOT="${PCL_DSH_TOOLS:-/opt/pcl-dsh-go/tools}"
NODE="$TOOLS_ROOT/node/bin/node"
VERSION="${DSH_VERSION:-0.2.1-rc.3}"
MANAGER_PORT=18080
NGINX_PORT=8088
RUNTIME_SRC="${RUNTIME_SRC:-$ROOT/e2e-data/runtimes/$VERSION}"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m2"
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
DATA_ROOT="$ROOT/m2s-data"
NGINX_ROOT="$ROOT/m2s-nginx"
SCENARIO="$ROOT/m2s-scenario-$RUN_STAMP"
LOG="$EVIDENCE_DIR/m2-security-e2e-$RUN_STAMP.log"
COOKIE_JAR="$SCENARIO/cookies.txt"
MANAGER_PID=""
PASS=0
FAIL=0
FAILS=""
BASE="http://127.0.0.1:$NGINX_PORT"
MNGR="http://127.0.0.1:$MANAGER_PORT"

san() {
  sed -E \
    -e 's/([?&]token=)[^&"[:space:]]+/\1***/g' \
    -e 's/\btoken=[^&"[:space:]]+/token=***/g' \
    -e 's/(JSESSIONID|XSRF-TOKEN)=[^;,"[:space:]]+/\1=***/g' \
    -e 's/(set-cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/(cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/("[A-Za-z]*[Pp]assword"[[:space:]]*:[[:space:]]*")[^"]*"/\1***"/g'
}
log() { printf '%s\n' "$*" | tee -a "$LOG"; }
info() { log "INFO  $*"; }
check() {
  local name="$1" ok="$2" detail="$3"
  if [ "$ok" = "1" ]; then PASS=$((PASS + 1)); log "PASS  $name  $detail";
  else FAIL=$((FAIL + 1)); FAILS="$FAILS
FAIL  $name  $detail"; log "FAIL  $name  $detail"; fi
}
fatal() { log "FATAL: $*"; exit 1; }
now_ms() { printf '%s' "$EPOCHREALTIME" | tr -d '.' | cut -c1-13; }
wait_port_free() {
  local port="$1" tries="$2" n=0
  while [ "$n" -lt "$tries" ]; do ss -ltn 2>/dev/null | grep -q ":$port\b" || return 0; sleep 0.1; n=$((n + 1)); done
  return 1
}
port_listening() { ss -ltn 2>/dev/null | grep -q ":$1\b" && echo 1 || echo 0; }
file_mode() { stat -c '%a' "$1" 2>/dev/null || echo ""; }
winpath() {
  local p="$1"
  case "$p" in
    /mnt/[a-zA-Z]/*) local d r; d="$(printf '%s' "$p" | cut -c6 | tr '[:lower:]' '[:upper:]')"; r="$(printf '%s' "$p" | cut -c8- | sed 's|/|\\|g')"; printf '%s:\\%s' "$d" "$r";;
    *) printf '%s' "$p";;
  esac
}
source "$SCRIPT_DIR/lib/windows-dsh-baseline.sh"

cleanup() {
  local code=$?
  if [ -f "$NGINX_ROOT/pid/nginx.pid" ]; then nginx -s quit -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >/dev/null 2>&1 || true; fi
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true; sleep 1; kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true; fi
  rm -f "$COOKIE_JAR" 2>/dev/null || true
  rm -f "$SCENARIO"/*.hdr "$SCENARIO"/*.body 2>/dev/null || true
  if [ -f "$SCENARIO/manager.log" ]; then san < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/m2-security-e2e-$RUN_STAMP-manager.log.txt" 2>/dev/null || true; fi
  if [ "$code" -ne 0 ]; then log ""; log "RESULT=FAIL scenario=$SCENARIO"; fi
  log "EXIT_CODE=$code scenario=$SCENARIO"
  if [ -f "$LOG" ]; then san < "$LOG" > "$LOG.txt" 2>/dev/null || true; fi
  exit "$code"
}
trap cleanup EXIT

build_manager() {
  local go_bin win_go cmd bat win_bat size sha
  if command -v go.exe >/dev/null 2>&1; then go_bin="$(command -v go.exe)"
  elif command -v go >/dev/null 2>&1; then go_bin="$(command -v go)"; else fatal "no Go toolchain"; fi
  mkdir -p "$REPO_ROOT/bin"
  case "$go_bin" in
    *.exe)
      win_go="$(winpath "$go_bin")"; cmd="$(command -v cmd.exe)"; bat="$REPO_ROOT/bin/m2s-build.bat"; win_bat="$(winpath "$bat")"
      { printf '@echo off\r\n'; printf 'set GOOS=linux\r\nset GOARCH=amd64\r\nset CGO_ENABLED=0\r\n'; printf '"%s" build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=0.3.0-m2" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go\r\n' "$win_go"; printf 'exit /b %%%%ERRORLEVEL%%%%\r\n'; } > "$bat"
      ( cd "$REPO_ROOT" && "$cmd" /c "$win_bat" ) >> "$LOG" 2>&1 || { rm -f "$bat"; fatal "manager build failed"; }
      rm -f "$bat";;
    *) ( cd "$REPO_ROOT" && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$go_bin" build -trimpath -buildvcs=false -ldflags '-s -w -X main.version=0.3.0-m2' -o bin/pcl-dsh-go ./cmd/pcl-dsh-go ) >> "$LOG" 2>&1 || fatal "manager build failed";;
  esac
  chmod +x "$REPO_ROOT/bin/pcl-dsh-go"
  size=$(stat -c %s "$REPO_ROOT/bin/pcl-dsh-go"); sha=$(sha256sum "$REPO_ROOT/bin/pcl-dsh-go" | awk '{print $1}')
  log "manager built from current tree (M2-F): size=$size B sha256=$sha"
  check "manager linux/amd64 build from current tree" 1 "sha256=$sha"
}
start_manager() {
  setsid "$REPO_ROOT/bin/pcl-dsh-go" --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 "$MNGR/api/health" > "$SCENARIO/health.json" 2>/dev/null; then check "manager /api/health" 1; return 0; fi
    kill -0 "$MANAGER_PID" 2>/dev/null || fatal "manager exited during startup"
    sleep 0.3
  done
  fatal "manager not ready in 30s"
}
prepare_nginx() {
  mkdir -p "$NGINX_ROOT"/{conf,logs,static/assets,body,proxy,tmp,pid}
  printf '<!doctype html><html><head><title>PCL-DSH-Go manager</title></head><body>placeholder</body></html>\n' > "$NGINX_ROOT/static/index.html"
  printf 'window.__PCL_M2_FIXTURE__ = true;\n' > "$NGINX_ROOT/static/assets/app.js"
  sed -e 's/listen 80;/listen 8088;/' -e "s#/opt/pcl-dsh-go/current/release/static#$NGINX_ROOT/static#g" \
      -e 's#http://127.0.0.1:8080#http://127.0.0.1:18080#g' "$REPO_ROOT/packaging/nginx/pcl-dsh-go.conf" > "$NGINX_ROOT/conf/site.conf"
  cat > "$NGINX_ROOT/conf/nginx.conf" <<EOF
user root;
worker_processes 1;
pid $NGINX_ROOT/pid/nginx.pid;
error_log $NGINX_ROOT/logs/error.log warn;
events { worker_connections 128; }
http {
    include /etc/nginx/mime.types;
    default_type application/octet-stream;
    access_log $NGINX_ROOT/logs/access.log;
    client_body_temp_path $NGINX_ROOT/body;
    proxy_temp_path $NGINX_ROOT/proxy;
    fastcgi_temp_path $NGINX_ROOT/tmp/fastcgi;
    uwsgi_temp_path $NGINX_ROOT/tmp/uwsgi;
    scgi_temp_path $NGINX_ROOT/tmp/scgi;
    include $NGINX_ROOT/conf/site.conf;
}
EOF
  nginx -t -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" > "$NGINX_ROOT/logs/nginx-t.log" 2>&1 || fatal "nginx -t failed"
  check "nginx -t (production template adapted)" 1
  nginx -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >> "$NGINX_ROOT/logs/nginx-start.log" 2>&1 || fatal "nginx start failed"
  for n in $(seq 1 30); do port_listening "$NGINX_PORT" | grep -q 1 && break; sleep 0.1; done
  check "isolated Nginx :$NGINX_PORT up" "$(port_listening "$NGINX_PORT")"
}
req() { # req <manager|nginx> <anon|auth> <METHOD> <PATH> [BODY] [extra curl args...]
  local target="$1" mode="$2" method="$3" path="$4" body="$5"; shift 5
  local base="$BASE"; [ "$target" = "manager" ] && base="$MNGR"
  local hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body"
  : > "$hdr"; : > "$bodyf"
  local args=(-sS --max-time 30 -X "$method" -D "$hdr" -o "$bodyf" -w '%{http_code}')
  if [ "$mode" = "auth" ]; then args+=(-b "$COOKIE_JAR" -c "$COOKIE_JAR"); [ -n "$CSRF_TOKEN" ] && args+=(-H "X-XSRF-TOKEN: $CSRF_TOKEN"); fi
  args+=("$@")
  args+=("$base$path")
  if [ -z "$body" ]; then API_STATUS=$(curl "${args[@]}" 2>/dev/null); else API_STATUS=$(printf '%s' "$body" | curl "${args[@]}" -H 'Content-Type: application/json' --data-binary @- 2>/dev/null); fi
  API_BODY="$bodyf"; API_HDRS="$hdr"
}
json_get() {
  "$NODE" -e '
const fs=require("fs");const v=(function(o,p){for(const k of p.split(".")){if(o==null)process.exit(2);o=o[k];}return o;})(JSON.parse(fs.readFileSync(process.argv[1],"utf8")),process.argv[2]);
if(v===null||v===undefined)process.exit(2);process.stdout.write(typeof v==="object"?JSON.stringify(v):String(v));' "$1" "$2" 2>/dev/null || true
}
four() { # four <name> <want-status> <want-code>
  local n="$1" want="$2" code="$3" c m d t good=1
  [ "$API_STATUS" = "$want" ] || good=0
  c=$(json_get "$API_BODY" code); m=$(json_get "$API_BODY" message); d=$(json_get "$API_BODY" details); t=$(json_get "$API_BODY" traceId)
  [ "$c" = "$code" ] || good=0; [ -n "$m" ] || good=0
  printf '%s' "$d" | grep -q '^{' || good=0
  printf '%s' "$t" | grep -Eq '^[0-9a-f]{12}$' || good=0
  check "$n" "$good" "status=$API_STATUS code=$c details=$d trace=$t"
}
jar_cookie() { awk -F '\t' -v n="$1" '$6==n {print $7; exit}' "$COOKIE_JAR" 2>/dev/null; }
refresh_me() { req nginx auth GET /api/auth/me ""; CSRF_TOKEN=$(json_get "$API_BODY" csrfToken); }

main() {
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO"
  : > "$LOG"
  log "=== PCL-DSH-Go M2-F security/error-path E2E $RUN_STAMP ==="
  local etc_before ref_before ref_path="/mnt/d/Projects/DSHL/PCL-DSH-Linux"
  etc_before=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  ref_before=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  win_dsh_baseline_capture
  check "system :80 untouched (isolated Nginx on :8088)" "$(port_listening 80)"

  build_manager
  rm -rf "$DATA_ROOT"; mkdir -p "$DATA_ROOT" "$SCENARIO"; chmod 700 "$DATA_ROOT" "$SCENARIO"
  mkdir -p "$DATA_ROOT/runtimes"; ln -s "$RUNTIME_SRC" "$DATA_ROOT/runtimes/$VERSION"
  info "reused real DSH runtime via symlink: $RUNTIME_SRC (no reinstall)"
  start_manager
  prepare_nginx

  INITIAL_PASSWORD=$(cat "$DATA_ROOT/initial-password" 2>/dev/null || true)
  [ -n "$INITIAL_PASSWORD" ] || fatal "initial password missing"
  req nginx auth POST /api/auth/login "$(printf '{"password":"%s"}' "$INITIAL_PASSWORD")"
  check "login via Nginx 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  NEW_PASSWORD="M2s$(head -c 12 /dev/urandom | base64 | tr -d '/+=' | head -c 12)"
  req nginx auth PUT /api/auth/password "$(printf '{"oldPassword":"%s","newPassword":"%s"}' "$INITIAL_PASSWORD" "$NEW_PASSWORD")"
  check "change password via Nginx 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me

  local body iid iport lid
  body=$(printf '{"name":"m2-sec","version":"%s","profile":"web","port":0}' "$VERSION")
  req nginx auth POST /api/instances "$body"
  check "create instance 201" "$([ "$API_STATUS" = "201" ] && echo 1 || echo 0)"
  iid=$(json_get "$API_BODY" id); iport=$(json_get "$API_BODY" port)
  [ -n "$iid" ] || fatal "no instance id"
  req nginx auth POST "/api/instances/$iid/launch" ""
  check "launch 202" "$([ "$API_STATUS" = "202" ] && echo 1 || echo 0)"
  lid=$(json_get "$API_BODY" id)
  local deadline=$(( $(now_ms) + 120000 )) state
  while [ "$(now_ms)" -lt "$deadline" ]; do
    req nginx auth GET "/api/jobs/$lid" ""
    state=$(json_get "$API_BODY" state)
    case "$state" in SUCCEEDED|FAILED|CANCELLED) break;; esac
    sleep 0.5
  done
  check "launch Job terminal SUCCEEDED" "$([ "$state" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$state"

  log "--- auth boundaries ---"
  req manager anon GET /api/state ""; four "direct manager unauthenticated /api/state 401 four-field" 401 UNAUTHORIZED
  req manager anon GET "/workbench/$iid/" ""; four "direct manager unauthenticated /workbench 401 four-field" 401 UNAUTHORIZED
  req nginx anon GET /api/state ""; four "via Nginx unauthenticated /api/state 401 four-field" 401 UNAUTHORIZED
  req nginx anon GET "/workbench/$iid/" ""; four "via Nginx unauthenticated /workbench 401 four-field" 401 UNAUTHORIZED

  log "--- SSRF: Host / forwarded-host / port injection (direct manager + Nginx) ---"
  req manager auth GET /api/health "" -H 'Host: evil.example'
  check "SSRF: manager Host evil.example still local health 200" "$([ "$API_STATUS" = "200" ] && json_get "$API_BODY" status | grep -qx UP && echo 1 || echo 0)" "status=$API_STATUS"
  req manager auth GET /api/health "" -H 'Host: 127.0.0.1:18080@evil.example'
  check "SSRF: manager Host with @ injection not 5xx" "$([ "$API_STATUS" != "500" ] && [ "$API_STATUS" != "502" ] && echo 1 || echo 0)" "status=$API_STATUS"
  req manager auth GET /api/health "" -H 'Host: [::1]:9999'
  check "SSRF: manager IPv6 Host injection not 5xx" "$([ "$API_STATUS" != "500" ] && [ "$API_STATUS" != "502" ] && echo 1 || echo 0)" "status=$API_STATUS"
  req nginx auth GET /api/health "" -H 'Host: evil.example'
  check "SSRF: Nginx Host evil.example upstream still manager health 200" "$([ "$API_STATUS" = "200" ] && json_get "$API_BODY" status | grep -qx UP && echo 1 || echo 0)" "status=$API_STATUS"
  req nginx anon GET /workbench/anything/ "" -H 'Host: evil.example'
  check "SSRF: Nginx workbench Host evil.example still auth-gated 401" "$([ "$API_STATUS" = "401" ] && echo 1 || echo 0)" "status=$API_STATUS"
  req manager auth GET "/api/instances/$iid/direct-access" "" -H 'X-Forwarded-Host: evil.example'
  local ssh_host
  ssh_host=$(json_get "$API_BODY" sshHost)
  check "SSRF: X-Forwarded-Host ignored for direct-access sshHost" "$(printf '%s' "$ssh_host" | grep -qx '127.0.0.1' && echo 1 || echo 0)" "sshHost=$ssh_host"
  req manager auth GET "/api/instances/$iid/direct-access" "" -H 'Host: 127.0.0.1:9999'
  check "SSRF: direct-access tunnel port from instance, not Host" "$(json_get "$API_BODY" port | grep -qx "$iport" && echo 1 || echo 0)" "port=$(json_get "$API_BODY" port) instance_port=$iport"

  log "--- path traversal / control chars ---"
  local bad_paths="/workbench/$iid/../../etc/passwd
/workbench/$iid/%2e%2e/%2e%2e/etc/passwd
/workbench/$iid/..%2f..%2fetc%2fpasswd
/workbench/$iid/%2e%2e%5c%2e%2e%5cetc%5cpasswd
/workbench/$iid/%00evil
/workbench/$iid/%0d%0aX-Injected:%20evil
/workbench/$iid/http://evil.example/../../x"
  log "--- path traversal / control chars (direct manager) ---"
  local p dm_ok=0 nx_bad=0 nx_samples=0
  while IFS= read -r p; do
    req manager auth GET "$p" "" --path-as-is
    local loc="" code="" det="" trc=""
    loc=$(grep -i '^location:' "$SCENARIO/last.hdr" 2>/dev/null | head -1 | tr -d '\r')
    code=$(json_get "$API_BODY" code); det=$(json_get "$API_BODY" details); trc=$(json_get "$API_BODY" traceId)
    local ok=1
    [ "$API_STATUS" = "400" ] || ok=0
    [ "$code" = "BAD_REQUEST" ] || ok=0
    [ -n "$det" ] || ok=0
    printf '%s' "$trc" | grep -Eq '^[0-9a-f]{12}$' || ok=0
    [ -z "$loc" ] || ok=0
    [ "$ok" = "1" ] && dm_ok=$((dm_ok + 1))
    check "direct manager traversal/control-char 400 BAD_REQUEST four-field, no Location" "$ok" "path=$p status=$API_STATUS code=$code loc=$loc"
    check "direct manager sample leaks no /etc/passwd" "$(grep -q 'root:.*:0:0:' "$API_BODY" 2>/dev/null && echo 0 || echo 1)" "path=$p"
  done <<< "$bad_paths"
  check "direct manager all 7 traversal/control-char samples 400 four-field" "$([ "$dm_ok" = "7" ] && echo 1 || echo 0)" "ok=$dm_ok/7"

  log "--- absolute-form (proxy-style) request target with traversal ---"
  : > "$SCENARIO/abs.hdr"; : > "$SCENARIO/abs.body"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 30 -b "$COOKIE_JAR" -c "$COOKIE_JAR" --path-as-is \
    --request-target "http://127.0.0.1:18080/workbench/$iid/../../etc/passwd" \
    -D "$SCENARIO/abs.hdr" -o "$SCENARIO/abs.body" -w '%{http_code}' "http://127.0.0.1:18080/" 2>/dev/null)
  API_BODY="$SCENARIO/abs.body"; API_HDRS="$SCENARIO/abs.hdr"
  four "absolute-form traversal direct manager 400 four-field" 400 BAD_REQUEST
  check "absolute-form traversal has no Location redirect" "$(grep -qi '^location:' "$SCENARIO/abs.hdr" && echo 0 || echo 1)"
  check "absolute-form traversal leaks no /etc/passwd" "$(grep -q 'root:.*:0:0:' "$SCENARIO/abs.body" 2>/dev/null && echo 0 || echo 1)"

  log "--- raw path guard runs before auth (unauth traversal -> 400, not 401) ---"
  req manager anon GET "/workbench/i-aaaaaaaaaaaa/../../etc/passwd" "" --path-as-is
  four "unauth raw dot-segment direct manager 400 four-field (guard before session)" 400 BAD_REQUEST
  log "--- error four-field shapes (via Nginx) ---"
  req nginx auth GET /api/instances/i-000000000000 ""; four "unknown instance 404 four-field" 404 NOT_FOUND
  req nginx auth POST /api/instances '{"name":"","version":"'"$VERSION"'","profile":"web","port":0}'
  four "invalid instance body 400 four-field" 400 VALIDATION
  req nginx auth PUT /api/auth/password '{"oldPassword":"wrong-old","newPassword":"whatever-1234"}'
  check "wrong old password rejected" "$([ "$API_STATUS" = "400" ] || [ "$API_STATUS" = "401" ] && echo 1 || echo 0)" "status=$API_STATUS code=$(json_get "$API_BODY" code)"

  log "--- stopped instance 409 via Nginx; manager down -> Nginx 502 ---"
  req nginx auth POST "/api/instances/$iid/stop" ""
  check "stop instance 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
  req nginx auth GET "/workbench/$iid/" ""
  four "stopped instance workbench 409 four-field via Nginx" 409 INSTANCE_NOT_READY
  req nginx auth GET "/api/instances/$iid/direct-access" ""
  check "stopped instance direct-access ready=false" "$(json_get "$API_BODY" ready | grep -qx false && echo 1 || echo 0)"

  kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
  for n in $(seq 1 50); do kill -0 "$MANAGER_PID" 2>/dev/null || break; sleep 0.1; done
  MANAGER_PID=""
  wait_port_free "$MANAGER_PORT" 30 || true
  check "manager stopped -> Nginx /api/health 502" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$BASE/api/health")" = "502" ] && echo 1 || echo 0)"
  check "manager stopped -> Nginx /workbench 502" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$BASE/workbench/x/")" = "502" ] && echo 1 || echo 0)"

  log "--- Nginx access_log off + secret scan ---"
  local access="$NGINX_ROOT/logs/access.log" api_n query_n sens_n
  api_n=$(grep -c '/api/' "$access" 2>/dev/null || true)
  query_n=$(grep -Ec '/workbench/[^ "]*[?]' "$access" 2>/dev/null || true)
  sens_n=$(grep -Eci 'token=|dsh-auth-|JSESSIONID' "$access" 2>/dev/null || true)
  check "Nginx access_log off for /api/ locations" "$([ "$api_n" = "0" ] && echo 1 || echo 0)" "api_lines=$api_n access_lines=$(wc -l < "$access" 2>/dev/null || echo 0)"
  check "Nginx access log has no query string/token/cookie" "$([ "$query_n" = "0" ] && [ "$sens_n" = "0" ] && echo 1 || echo 0)" "query=$query_n sensitive=$sens_n"
  local leaks pwleak errs
  leaks=$(grep -rEo 'token=[A-Za-z0-9._~%+/-]{8,}|dsh-auth-[A-Za-z0-9]+=[A-Za-z0-9._~%+/=-]{8,}|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$DATA_ROOT/logs" "$NGINX_ROOT/logs" "$LOG" 2>/dev/null | wc -l)
  check "manager/Nginx logs + evidence no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  pwleak=0
  [ -n "$INITIAL_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$INITIAL_PASSWORD" "$DATA_ROOT/logs" "$NGINX_ROOT/logs" "$LOG" 2>/dev/null | wc -l)))
  [ -n "$NEW_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$NEW_PASSWORD" "$DATA_ROOT/logs" "$NGINX_ROOT/logs" "$LOG" 2>/dev/null | wc -l)))
  check "passwords never appear in logs/evidence" "$([ "$pwleak" = "0" ] && echo 1 || echo 0)" "matches=$pwleak"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs ERROR count 0" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"

  log "--- platform no-touch ---"
  local etc_after ref_after prog_after
  etc_after=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  check "/etc/nginx unchanged" "$([ "$etc_after" = "$etc_before" ] && echo 1 || echo 0)"
  ref_after=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  check "reference repo unchanged (baseline git status hash)" "$([ "$ref_after" = "$ref_before" ] && echo 1 || echo 0)" "before=$(printf '%s' "$ref_before" | cut -c1-16)... after=$(printf '%s' "$ref_after" | cut -c1-16)..."
  prog_after=$(win_dsh_tree_hash "$WIN_DSH_DIR")
  check "Windows DSH program tree unchanged (read-only)" "$([ "$prog_after" = "$WIN_DSH_PROG_BEFORE" ] && echo 1 || echo 0)"
  check "data-root 0700" "$([ "$(file_mode "$DATA_ROOT")" = "700" ] && echo 1 || echo 0)"

  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS checks_failed=$FAIL scenario=$SCENARIO"
  if [ "$FAIL" -gt 0 ]; then printf '%s\n' "$FAILS" | tee -a "$LOG"; log "RESULT=FAIL"; exit 1; fi
  log "RESULT=PASS"
  exit 0
}

main
