#!/usr/bin/env bash
# PCL-DSH-Go M2-E | Real browser through Nginx (Windows Edge headless CDP).
#
# Two phases (driven by qa-release because Edge/CDP runs on Windows):
#   setup  : build/start manager + isolated Nginx(8088), login via Nginx, create+launch
#            instance, write a 0600 JSESSIONID temp file for the Windows browser and a
#            handoff env file; servers stay up for the CDP phase.
#   verify : consume the Windows CDP result JSON + screenshot, assert, scan evidence,
#            clean up (nginx/manager/cookie/temp dirs) and write RESULT/EXIT_CODE.
#
# Usage (WSL): bash scripts/wsl/m2-browser-e2e.sh setup|verify
# Never touches the system /etc/nginx or :80. Reuses the real DSH runtime via symlink.
set -o pipefail
export NO_PROXY='*' no_proxy='*'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="${PCL_DSH_TEST_ROOT:-/tmp/pcl-dsh-go-test}"
TOOLS_ROOT="${PCL_DSH_TOOLS:-/opt/pcl-dsh-go/tools}"
NODE="$TOOLS_ROOT/node/bin/node"
VERSION="${DSH_VERSION:-0.2.1-rc.3}"
MANAGER_PORT="${MANAGER_PORT:-18080}"
NGINX_PORT="${NGINX_PORT:-8088}"
RUNTIME_SRC="${RUNTIME_SRC:-$ROOT/e2e-data/runtimes/$VERSION}"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m2"
M2B_DIR="/mnt/d/Projects/DSHL/.m2-browser"
HANDOFF="$M2B_DIR/handoff.env"
RESULT_JSON="$M2B_DIR/result.json"
PNG_WSL="$EVIDENCE_DIR/browser-workbench-nginx.png"
PNG_WIN="D:\\Projects\\DSHL\\PCL-DSH-Go\\docs\\evidence\\m2\\browser-workbench-nginx.png"
COOKIE_WSL="$M2B_DIR/cookie"
COOKIE_WIN="D:\\Projects\\DSHL\\.m2-browser\\cookie"
HANDOFF_WIN="D:\\Projects\\DSHL\\.m2-browser\\handoff.env"
RESULT_WIN="D:\\Projects\\DSHL\\.m2-browser\\result.json"
MODE="$1"
PASS_COUNT=0
FAIL_COUNT=0
FAIL_LINES=""

san() {
  sed -E \
    -e 's/([?&]token=)[^&"[:space:]]+/\1***/g' \
    -e 's/\btoken=[^&"[:space:]]+/token=***/g' \
    -e 's/dsh-auth-[A-Za-z0-9]+=[^;,"[:space:]]*/dsh-auth-***=***/g' \
    -e 's/(JSESSIONID|XSRF-TOKEN)=[^;,"[:space:]]+/\1=***/g' \
    -e 's/(set-cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/(cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/("[A-Za-z]*[Pp]assword"[[:space:]]*:[[:space:]]*")[^"]*"/\1***"/g'
}
log() { printf '%s\n' "$*" | tee -a "$LOG"; }
info() { log "INFO  $*"; }
check() {
  local name="$1" ok="$2" detail="$3"
  if [ "$ok" = "1" ]; then PASS_COUNT=$((PASS_COUNT + 1)); log "PASS  $name  $detail";
  else FAIL_COUNT=$((FAIL_COUNT + 1)); FAIL_LINES="$FAIL_LINES
FAIL  $name  $detail"; log "FAIL  $name  $detail"; fi
}
fatal() { log "FATAL: $*"; exit 1; }
now_ms() { printf '%s' "$EPOCHREALTIME" | tr -d '.' | cut -c1-13; }
wait_port_free() {
  local port="$1" tries="$2" n=0
  while [ "$n" -lt "$tries" ]; do
    if ! ss -ltn 2>/dev/null | grep -q ":$port\b"; then return 0; fi
    sleep 0.1; n=$((n + 1))
  done
  return 1
}
port_listening() { ss -ltn 2>/dev/null | grep -q ":$1\b" && echo 1 || echo 0; }
file_mode() { stat -c '%a' "$1" 2>/dev/null || echo ""; }
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

source "$SCRIPT_DIR/lib/windows-dsh-baseline.sh"

build_manager() {
  local go_bin win_go cmd bat win_bat size sha
  if command -v go.exe >/dev/null 2>&1; then go_bin="$(command -v go.exe)"
  elif command -v go >/dev/null 2>&1; then go_bin="$(command -v go)"
  else fatal "no Go toolchain"; fi
  mkdir -p "$REPO_ROOT/bin"
  case "$go_bin" in
    *.exe)
      win_go="$(winpath "$go_bin")"
      cmd="$(command -v cmd.exe)"
      bat="$REPO_ROOT/bin/m2b-build-manager.bat"
      win_bat="$(winpath "$bat")"
      {
        printf '@echo off\r\n'
        printf 'set GOOS=linux\r\nset GOARCH=amd64\r\nset CGO_ENABLED=0\r\n'
        printf '"%s" build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=0.3.0-m2" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go\r\n' "$win_go"
        printf 'exit /b %%%%ERRORLEVEL%%%%\r\n'
      } > "$bat"
      ( cd "$REPO_ROOT" && "$cmd" /c "$win_bat" ) >> "$LOG" 2>&1 || { rm -f "$bat"; fatal "manager build failed"; }
      rm -f "$bat";;
    *)
      ( cd "$REPO_ROOT" && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$go_bin" build -trimpath -buildvcs=false -ldflags '-s -w -X main.version=0.3.0-m2' -o bin/pcl-dsh-go ./cmd/pcl-dsh-go ) >> "$LOG" 2>&1 || fatal "manager build failed";;
  esac
  [ -f "$REPO_ROOT/bin/pcl-dsh-go" ] || fatal "manager binary missing"
  chmod +x "$REPO_ROOT/bin/pcl-dsh-go"
  size=$(stat -c %s "$REPO_ROOT/bin/pcl-dsh-go")
  sha=$(sha256sum "$REPO_ROOT/bin/pcl-dsh-go" | awk '{print $1}')
  log "manager built from current tree (M2-E): size=$size B sha256=$sha"
  check "manager linux/amd64 build from current tree" 1 "size=$size sha256=$sha"
}
init_data_root() {
  rm -rf "$DATA_ROOT"
  mkdir -p "$DATA_ROOT" "$SCENARIO"
  chmod 700 "$DATA_ROOT" "$SCENARIO"
  [ -f "$RUNTIME_SRC/node_modules/@deepseek-ai/dsh/lib/bin.js" ] || fatal "reusable runtime missing: $RUNTIME_SRC"
  mkdir -p "$DATA_ROOT/runtimes"
  ln -s "$RUNTIME_SRC" "$DATA_ROOT/runtimes/$VERSION"
  info "reused real DSH runtime via symlink: $RUNTIME_SRC -> $DATA_ROOT/runtimes/$VERSION (no reinstall)"
}
start_manager() {
  setsid "$REPO_ROOT/bin/pcl-dsh-go" --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" \
    --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  log "manager start pid=$MANAGER_PID port=$MANAGER_PORT"
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 "http://127.0.0.1:$MANAGER_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then
      check "manager /api/health" 1
      return 0
    fi
    kill -0 "$MANAGER_PID" 2>/dev/null || fatal "manager exited during startup"
    sleep 0.3
  done
  fatal "manager not ready in 30s"
}
prepare_nginx() {
  mkdir -p "$NGINX_ROOT"/{conf,logs,static/assets,body,proxy,tmp,pid}
  cat > "$NGINX_ROOT/static/index.html" <<'EOF'
<!doctype html><html><head><title>PCL-DSH-Go manager</title></head>
<body><div id="app">PCL-DSH-Go manager static placeholder</div></body></html>
EOF
  cat > "$NGINX_ROOT/static/assets/app.js" <<'EOF'
window.__PCL_M2_FIXTURE__ = true;
EOF
  sed -e 's/listen 80;/listen 8088;/' \
      -e "s#/opt/pcl-dsh-go/current/release/static#$NGINX_ROOT/static#g" \
      -e 's#http://127.0.0.1:8080#http://127.0.0.1:18080#g' \
      "$REPO_ROOT/packaging/nginx/pcl-dsh-go.conf" > "$NGINX_ROOT/conf/site.conf"
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
  nginx -t -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" > "$NGINX_ROOT/logs/nginx-t.log" 2>&1 || {
    check "nginx -t (production template, isolated instance)" 0 "$(tail -3 "$NGINX_ROOT/logs/nginx-t.log")"; fatal "nginx -t failed"; }
  check "nginx -t (production template, isolated instance)" 1
  nginx -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >> "$NGINX_ROOT/logs/nginx-start.log" 2>&1 || fatal "nginx start failed"
  for n in $(seq 1 30); do port_listening "$NGINX_PORT" | grep -q 1 && break; sleep 0.1; done
  check "isolated Nginx listening on 127.0.0.1:$NGINX_PORT" "$(port_listening "$NGINX_PORT")" "system :80 untouched"
}

BASE="http://127.0.0.1:$NGINX_PORT"
req() {
  local method="$1" path="$2" body="$3" noc="$4"
  local hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body"
  : > "$hdr"; : > "$bodyf"
  local args=(-sS --max-time 180 -X "$method" -D "$hdr" -o "$bodyf" -w '%{http_code}')
  args+=(-b "$COOKIE_JAR" -c "$COOKIE_JAR")
  if [ "$noc" != "no-csrf" ] && [ -n "$CSRF_TOKEN" ]; then args+=(-H "X-XSRF-TOKEN: $CSRF_TOKEN"); fi
  args+=("$BASE$path")
  if [ -z "$body" ]; then API_STATUS=$(curl "${args[@]}" 2>/dev/null)
  else API_STATUS=$(printf '%s' "$body" | curl "${args[@]}" -H 'Content-Type: application/json' --data-binary @- 2>/dev/null); fi
  API_BODY="$bodyf"
  log "api $method $path -> $API_STATUS"
}
json_get() {
  "$NODE" -e '
const fs = require("fs");
const v = (function (o, p) { for (const k of p.split(".")) { if (o == null) process.exit(2); o = o[k]; } return o; })(JSON.parse(fs.readFileSync(process.argv[1], "utf8")), process.argv[2]);
if (v === null || v === undefined) process.exit(2);
process.stdout.write(typeof v === "object" ? JSON.stringify(v) : String(v));
' "$1" "$2" 2>/dev/null || true
}
jar_cookie() { awk -F '\t' -v n="$1" '$6==n {print $7; exit}' "$COOKIE_JAR" 2>/dev/null; }
refresh_me() { req GET /api/auth/me ""; CSRF_TOKEN=$(json_get "$API_BODY" csrfToken); }
do_login() {
  local pw="$1" body
  body=$(printf '{"password":"%s"}' "$pw")
  req POST /api/auth/login "$body" no-csrf
}
job_poll() {
  local id="$1" deadline state
  [ -n "$id" ] || return 1
  deadline=$(( $(now_ms) + 240000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    req GET "/api/jobs/$id" ""
    state=$(json_get "$API_BODY" state)
    case "$state" in
      SUCCEEDED|FAILED|CANCELLED) JOB_STATUS="$state"; return 0;;
    esac
    sleep 0.5
  done
  JOB_STATUS="TIMEOUT"; return 1
}

setup() {
  RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
  DATA_ROOT="$ROOT/m2b-data"
  NGINX_ROOT="$ROOT/m2b-nginx"
  SCENARIO="$ROOT/m2b-scenario-$RUN_STAMP"
  LOG="$EVIDENCE_DIR/m2-browser-e2e-$RUN_STAMP.log"
  COOKIE_JAR="$SCENARIO/cookies.txt"
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO" "$M2B_DIR"
  chmod 700 "$M2B_DIR"
  # defensive pre-clean of a previous aborted run (same data-root/nginx root)
  pkill -f "pcl-dsh-go --pcl.data-root=$DATA_ROOT" 2>/dev/null || true
  if [ -f "$NGINX_ROOT/pid/nginx.pid" ]; then
    nginx -s quit -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" 2>/dev/null || true
  fi
  sleep 0.5
  rm -f "$M2B_DIR/cookie" "$M2B_DIR/handoff.env" "$M2B_DIR/result.json" 2>/dev/null || true
  : > "$LOG"
  log "=== PCL-DSH-Go M2-E browser-through-Nginx setup $RUN_STAMP ==="
  check "system :80 untouched (isolated Nginx uses :$NGINX_PORT)" "$(port_listening 80)" ""
  build_manager
  init_data_root
  start_manager
  prepare_nginx

  INITIAL_PASSWORD=$(cat "$DATA_ROOT/initial-password" 2>/dev/null || true)
  [ -n "$INITIAL_PASSWORD" ] || fatal "initial password missing"
  do_login "$INITIAL_PASSWORD"
  check "login via Nginx 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  NEW_PASSWORD="M2b$(head -c 12 /dev/urandom | base64 | tr -d '/+=' | head -c 12)"
  req PUT /api/auth/password "$(printf '{"oldPassword":"%s","newPassword":"%s"}' "$INITIAL_PASSWORD" "$NEW_PASSWORD")" ""
  check "change password via Nginx 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  rm -f "$COOKIE_JAR"
  do_login "$NEW_PASSWORD"
  check "login (new password) via Nginx 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  local jsid
  jsid="$(jar_cookie JSESSIONID)"
  check "JSESSIONID obtained via Nginx" "$([ -n "$jsid" ] && echo 1 || echo 0)" "len=$(printf '%s' "$jsid" | wc -c)"
  local secret_dir="$ROOT/m2b-secrets-$RUN_STAMP"
  mkdir -p "$secret_dir"
  chmod 700 "$secret_dir"
  ( umask 077; printf '%s' "$jsid" > "$secret_dir/cookie" )
  chmod 600 "$secret_dir/cookie"
  COOKIE_WSL="$secret_dir/cookie"
  COOKIE_WIN="$(wslpath -w "$COOKIE_WSL" 2>/dev/null || true)"
  [ -n "$COOKIE_WIN" ] || COOKIE_WIN="$COOKIE_WSL"
  check "browser cookie temp file 0600 (WSL /tmp, outside repo)" "$([ "$(file_mode "$COOKIE_WSL")" = "600" ] && echo 1 || echo 0)" "path=$COOKIE_WSL"

  local curl_exe="/mnt/c/Windows/System32/curl.exe" host_reach=0 browser_host="127.0.0.1" wsl_ip=""
  if [ -x "$curl_exe" ]; then
    local code
    code=$("$curl_exe" -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$NGINX_PORT/api/health" 2>/dev/null || true)
    if [ "$code" = "200" ]; then host_reach=1; fi
    if [ "$host_reach" = "0" ]; then
      wsl_ip=$(hostname -I 2>/dev/null | awk '{print $1}')
      [ -n "$wsl_ip" ] && code=$("$curl_exe" -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$wsl_ip:$NGINX_PORT/api/health" 2>/dev/null || true)
      if [ "$code" = "200" ]; then host_reach=1; browser_host="$wsl_ip"; fi
    fi
    check "Windows -> WSL Nginx reachable" "$host_reach" "browser_host=$browser_host"
  else
    info "curl.exe not available from WSL; assuming Windows localhost forwarding for $NGINX_PORT"
  fi

  local body iid iport launch_id
  body=$(printf '{"name":"m2-browser","version":"%s","profile":"web","port":0}' "$VERSION")
  req POST /api/instances "$body" ""
  check "create instance via Nginx 201" "$([ "$API_STATUS" = "201" ] && echo 1 || echo 0)"
  iid=$(json_get "$API_BODY" id); iport=$(json_get "$API_BODY" port)
  check "instance id/port" "$([ -n "$iid" ] && [ -n "$iport" ] && echo 1 || echo 0)" "id=$iid port=$iport"
  [ -n "$iid" ] || fatal "create instance returned no id (setup cannot continue)"
  req POST "/api/instances/$iid/launch" ""
  check "launch 202 via Nginx" "$([ "$API_STATUS" = "202" ] && echo 1 || echo 0)"
  launch_id=$(json_get "$API_BODY" id)
  [ -n "$launch_id" ] || fatal "launch returned no job id (setup cannot continue)"
  job_poll "$launch_id"
  check "launch Job SUCCEEDED via Nginx" "$([ "$JOB_STATUS" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATUS"

  BROWSER_URL="http://$browser_host:$NGINX_PORT/workbench/$iid/"
  REF_REPO_BASELINE=$(git -C /mnt/d/Projects/DSHL/PCL-DSH-Linux status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  {
    printf 'STAMP=%s\n' "$RUN_STAMP"
    printf 'LOG=%s\n' "$LOG"
    printf 'DATA_ROOT=%s\n' "$DATA_ROOT"
    printf 'SCENARIO=%s\n' "$SCENARIO"
    printf 'MANAGER_PID=%s\n' "$MANAGER_PID"
    printf 'NGINX_ROOT=%s\n' "$NGINX_ROOT"
    printf 'IID=%s\n' "$iid"
    printf 'IPORT=%s\n' "$iport"
    printf 'BROWSER_URL=%s\n' "$BROWSER_URL"
    printf 'COOKIE_WSL=%s\n' "$COOKIE_WSL"
    printf 'COOKIE_WIN=%s\n' "$COOKIE_WIN"
    printf 'PNG_WSL=%s\n' "$PNG_WSL"
    printf 'PNG_WIN=%s\n' "$PNG_WIN"
    printf 'RESULT_JSON=%s\n' "$RESULT_JSON"
    printf 'RESULT_WIN=%s\n' "$RESULT_WIN"
    printf 'HANDOFF_WIN=%s\n' "$HANDOFF_WIN"
    printf 'EXPECT_TITLE="DeepSeek Harness"\n'
    printf 'NGINX_ETC_BEFORE=%s\n' "$NGINX_ETC_BEFORE"
    printf 'WIN_DSH_PROG_BEFORE=%s\n' "$WIN_DSH_PROG_BEFORE"
    printf 'REF_REPO_BASELINE=%s\n' "$REF_REPO_BASELINE"
    printf 'PASS_COUNT=%s\n' "$PASS_COUNT"
    printf 'FAIL_COUNT=%s\n' "$FAIL_COUNT"
  } > "$HANDOFF"
  chmod 600 "$HANDOFF"
  log "handoff written: $HANDOFF ($(wc -l < "$HANDOFF") vars); servers stay up for the CDP phase"
  log "BROWSER_URL=$BROWSER_URL"
  log "PNG_WIN=$PNG_WIN"
  log "PASS=$PASS_COUNT FAIL=$FAIL_COUNT"
  exit 0
}

verify() {
  [ -f "$HANDOFF" ] || { echo "FATAL: no handoff ($HANDOFF)"; exit 2; }
  # shellcheck disable=SC1090
  source "$HANDOFF"
  export LOG DATA_ROOT SCENARIO NGINX_ROOT
  log ""
  log "=== M2-E browser verify phase $(date -Is) ==="
  check "handoff present" 1 "stamp=$STAMP"
  check "browser URL points at Nginx :$NGINX_PORT" "$(printf '%s' "$BROWSER_URL" | grep -q ":$NGINX_PORT/workbench/$IID/" && echo 1 || echo 0)" "url=$BROWSER_URL"
  check "browser result JSON present" "$([ -f "$RESULT_JSON" ] && echo 1 || echo 0)" "path=$RESULT_JSON"
  check "screenshot PNG present (Windows CDP wrote it)" "$([ -f "$PNG_WSL" ] && echo 1 || echo 0)" "path=$PNG_WSL"
  local r="$RESULT_JSON"
  local ok title rootlen boot ws101 png_bytes png_w png_h console_n exc_n
  ok=$(json_get "$r" ok); title=$(json_get "$r" title); rootlen=$(json_get "$r" rootTextLength)
  boot=$(json_get "$r" dshBoot.present); ws101=$(json_get "$r" ws101)
  png_bytes=$(json_get "$r" screenshot.bytes); png_w=$(json_get "$r" screenshot.width); png_h=$(json_get "$r" screenshot.height)
  console_n=$("$NODE" -e 'const r=require(process.argv[1]);console.log((r.consoleErrors||[]).length)' "$r" 2>/dev/null || echo "?")
  exc_n=$("$NODE" -e 'const r=require(process.argv[1]);console.log((r.pageExceptions||[]).length)' "$r" 2>/dev/null || echo "?")
  log "browser result: ok=$ok title=$(printf '%s' "$title" | san) root_len=$rootlen boot=$boot ws101=$ws101 png=${png_bytes}B ${png_w}x${png_h} console_errors=$console_n page_exceptions=$exc_n"
  check "browser title=DeepSeek Harness" "$([ "$title" = "DeepSeek Harness" ] && echo 1 || echo 0)" "title=$(printf '%s' "$title" | san)"
  check "browser rendered #root content" "$([ -n "$rootlen" ] && [ "$rootlen" -gt 0 ] 2>/dev/null && echo 1 || echo 0)" "root_len=$rootlen"
  check "window.__DSH_BOOT__ present" "$([ "$boot" = "true" ] && echo 1 || echo 0)"
  check "browser observed /api/remote.mux WS 101" "$([ "$ws101" = "true" ] && echo 1 || echo 0)" "ws101=$ws101 (optional observation)"
  check "screenshot non-blank (bytes/dimensions)" "$([ -n "$png_bytes" ] && [ "$png_bytes" -ge 5000 ] 2>/dev/null && [ "$png_w" -ge 200 ] 2>/dev/null && [ "$png_h" -ge 200 ] 2>/dev/null && [ "$(stat -c %s "$PNG_WSL" 2>/dev/null || echo 0)" -ge 5000 ] && echo 1 || echo 0)" "bytes=$png_bytes dims=${png_w}x${png_h} file=$(stat -c %s "$PNG_WSL" 2>/dev/null || echo 0)B"
  info "console errors (records; login-before-page 401 noise would be reported here): $console_n; page exceptions: $exc_n"
  local fail_msgs
  fail_msgs=$("$NODE" -e 'const r=require(process.argv[1]);console.log(JSON.stringify(r.failures||[]))' "$r" 2>/dev/null || echo "?")
  check "CDP client reports no failures" "$(printf '%s' "$fail_msgs" | grep -qx '\[\]' && echo 1 || echo 0)" "failures=$fail_msgs"

  if [ -f "$NGINX_ROOT/pid/nginx.pid" ]; then
    nginx -s quit -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >/dev/null 2>&1 || true
  fi
  for n in $(seq 1 30); do port_listening "$NGINX_PORT" | grep -q 0 && break; sleep 0.1; done
  kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
  sleep 1
  kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  wait_port_free "$MANAGER_PORT" 30 && check "nginx/manager stopped, ports released" 1 || check "nginx/manager stopped, ports released" 0
  if [ -f "$RESULT_JSON" ]; then san < "$RESULT_JSON" > "$EVIDENCE_DIR/browser-workbench-nginx.result.json.txt" 2>/dev/null || true; fi
  rm -f "$RESULT_JSON" "$HANDOFF" 2>/dev/null || true
  [ -n "$COOKIE_WSL" ] && rm -rf "$(dirname "$COOKIE_WSL")" 2>/dev/null || true
  rm -rf "$M2B_DIR/edge-profile" "$M2B_DIR/edge.pid" 2>/dev/null || true
  check "browser cookie temp file deleted" "$([ ! -f "$COOKIE_WSL" ] && echo 1 || echo 0)"
  check "Edge profile/temp files deleted" "$([ ! -d "$M2B_DIR/edge-profile" ] && echo 1 || echo 0)"

  rm -f "$SCENARIO/cookies.txt" 2>/dev/null || true
  local leaks errs
  leaks=$(grep -rEo 'token=[A-Za-z0-9._~%+/-]{8,}|dsh-auth-[A-Za-z0-9]+=[A-Za-z0-9._~%+/=-]{8,}|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)
  check "manager logs/evidence no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs ERROR count 0" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
  check "data-root 0700" "$([ "$(file_mode "$DATA_ROOT")" = "700" ] && echo 1 || echo 0)"
  local prog_after etc_after
  prog_after=$(win_dsh_tree_hash "$WIN_DSH_DIR")
  check "Windows DSH program tree unchanged (read-only)" "$([ "$prog_after" = "$WIN_DSH_PROG_BEFORE" ] && echo 1 || echo 0)" "hash=$(printf '%s' "$prog_after" | cut -c1-16)..."
  etc_after=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  check "/etc/nginx unchanged by browser E2E" "$([ "$etc_after" = "$NGINX_ETC_BEFORE" ] && echo 1 || echo 0)" "hash=$(printf '%s' "$etc_after" | cut -c1-16)..."
  local ref_after ref_path="/mnt/d/Projects/DSHL/PCL-DSH-Linux"
  ref_after=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  check "reference repo unchanged (baseline git status hash)" "$([ -n "$REF_REPO_BASELINE" ] && [ "$ref_after" = "$REF_REPO_BASELINE" ] && echo 1 || echo 0)" "before=$(printf '%s' "$REF_REPO_BASELINE" | cut -c1-16)... after=$(printf '%s' "$ref_after" | cut -c1-16)..."
  local ps_exe="/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe" edge_n="unknown"
  if [ -x "$ps_exe" ]; then
    edge_n=$("$ps_exe" -NoProfile -Command "(Get-CimInstance Win32_Process -Filter \"Name='msedge.exe'\" | Where-Object { \$_.CommandLine -like '*m2-browser*' } | Measure-Object).Count" 2>/dev/null | tr -d '\r' | tr -d ' ')
    [ -n "$edge_n" ] || edge_n="unknown"
  fi
  check "Edge headless instance killed (no m2-browser msedge process)" "$([ "$edge_n" = "0" ] && echo 1 || echo 0)" "processes=$edge_n"
  check "Edge pid file removed" "$([ ! -f "$M2B_DIR/edge.pid" ] && echo 1 || echo 0)"
  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS_COUNT checks_failed=$FAIL_COUNT scenario=$SCENARIO"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '%s\n' "$FAIL_LINES" | tee -a "$LOG"
    log "RESULT=FAIL"
    code=1
  else
    log "RESULT=PASS"
    code=0
  fi
  if [ -f "$SCENARIO/manager.log" ]; then san < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/m2-browser-e2e-$STAMP-manager.log.txt" 2>/dev/null || true; fi
  log "EXIT_CODE=$code scenario=$SCENARIO"
  san < "$LOG" > "$LOG.txt" 2>/dev/null || true
  if [ "$code" -eq 0 ]; then
    rm -rf "$SCENARIO" 2>/dev/null || true
  fi
  exit "$code"
}

case "$MODE" in
  setup)
    NGINX_ETC_BEFORE=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
    win_dsh_baseline_capture
    setup;;
  verify) verify;;
  *) echo "usage: $0 setup|verify" >&2; exit 2;;
esac
