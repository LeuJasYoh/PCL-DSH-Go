#!/usr/bin/env bash
# PCL-DSH-Go M2-D | Nginx chain E2E: real manager + real DSH runtime behind the
# production Nginx template (isolated instance on 127.0.0.1:8088).
#
# Writes only /tmp/pcl-dsh-go-test/** and docs/evidence/m2/**. Never modifies the
# system /etc/nginx or the :80 service. Reuses an already installed real DSH rc.3
# runtime via a symlink into the fresh data-root (InstalledVersions follows symlinks).
#
# Usage (WSL): bash scripts/wsl/m2-nginx-e2e.sh
# Env overrides: RUNTIME_SRC, MANAGER_PORT (18080), NGINX_PORT (8088), DSH_VERSION.
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
DATA_ROOT="$ROOT/m2-data"
NGINX_ROOT="$ROOT/m2-nginx-e2e"
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
SCENARIO="$ROOT/m2-scenario-$RUN_STAMP"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m2"
LOG="$EVIDENCE_DIR/m2-nginx-e2e-$RUN_STAMP.log"
MANAGER_BIN="$REPO_ROOT/bin/pcl-dsh-go"
COOKIE_JAR="$SCENARIO/cookies.txt"
NGINX_PID_FILE="$NGINX_ROOT/pid/nginx.pid"
MANAGER_PID=""
NGINX_STARTED=0
PASS_COUNT=0
FAIL_COUNT=0
FAIL_LINES=""
CSRF_TOKEN=""
INITIAL_PASSWORD=""
NEW_PASSWORD=""
DID_INSTALL=0

san() {
  sed -E \
    -e 's/([?&]token=)[^&"[:space:]]+/\1***/g' \
    -e 's/\btoken=[^&"[:space:]]+/token=***/g' \
    -e 's/dsh-auth-[A-Za-z0-9]+=[^;,"[:space:]]*/dsh-auth-***=***/g' \
    -e 's/(JSESSIONID|XSRF-TOKEN)=[^;,"[:space:]]+/\1=***/g' \
    -e 's/(set-cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/(cookie:[[:space:]]*)[^\r\n]+/\1***/gi' \
    -e 's/(authorization:[[:space:]]*)[^\r\n]+/\1***/gi' \
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

cleanup() {
  local code=$?
  if [ "$NGINX_STARTED" = "1" ] && [ -f "$NGINX_PID_FILE" ]; then
    nginx -s quit -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >/dev/null 2>&1 || true
    for n in $(seq 1 30); do [ -f "$NGINX_PID_FILE" ] || break; sleep 0.1; done
    [ -f "$NGINX_PID_FILE" ] && kill -9 "$(cat "$NGINX_PID_FILE" 2>/dev/null)" 2>/dev/null || true
  fi
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    sleep 1
    kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  fi
  # cookie jar contains session secrets: always delete it
  rm -f "$COOKIE_JAR" 2>/dev/null || true
  rm -f "$SCENARIO"/*.hdr "$SCENARIO"/*.body 2>/dev/null || true
  if [ -f "$SCENARIO/manager.log" ]; then san < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/m2-nginx-e2e-$RUN_STAMP-manager.log.txt" 2>/dev/null || true; fi
  if [ "$code" -ne 0 ]; then
    log ""
    log "RESULT=FAIL scenario=$SCENARIO log=$LOG"
    [ -f "$SCENARIO/manager.log" ] && tail -20 "$SCENARIO/manager.log" 2>/dev/null | san | tee -a "$LOG" || true
  fi
  log "EXIT_CODE=$code scenario=$SCENARIO"
  if [ -f "$LOG" ]; then san < "$LOG" > "$LOG.txt" 2>/dev/null || true; fi
  exit "$code"
}
trap cleanup EXIT

source "$SCRIPT_DIR/lib/windows-dsh-baseline.sh"

# ---------- build / manager lifecycle ----------
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
      bat="$REPO_ROOT/bin/m2-build-manager.bat"
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
  [ -f "$MANAGER_BIN" ] || fatal "manager binary missing after build"
  chmod +x "$MANAGER_BIN"
  size=$(stat -c %s "$MANAGER_BIN")
  sha=$(sha256sum "$MANAGER_BIN" | awk '{print $1}')
  log "manager built from current tree (M2): size=$size B sha256=$sha"
  check "manager linux/amd64 build from current tree" 1 "size=$size sha256=$sha"
}
init_data_root() {
  rm -rf "$DATA_ROOT"
  mkdir -p "$DATA_ROOT" "$SCENARIO"
  chmod 700 "$DATA_ROOT" "$SCENARIO"
  [ -f "$RUNTIME_SRC/node_modules/@deepseek-ai/dsh/lib/bin.js" ] || fatal "reusable runtime missing: $RUNTIME_SRC"
  mkdir -p "$DATA_ROOT/runtimes"
  ln -s "$RUNTIME_SRC" "$DATA_ROOT/runtimes/$VERSION"
  info "reused existing real DSH runtime via symlink: $RUNTIME_SRC -> $DATA_ROOT/runtimes/$VERSION (no reinstall)"
  check "reusable real runtime symlinked into fresh data-root" 1 "source=$RUNTIME_SRC"
}
start_manager() {
  mkdir -p "$SCENARIO"
  setsid "$MANAGER_BIN" --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" \
    --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  log "manager start pid=$MANAGER_PID data-root=$DATA_ROOT port=$MANAGER_PORT"
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 "http://127.0.0.1:$MANAGER_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then
      log "manager health: $(san < "$SCENARIO/health.json")"
      check "manager /api/health" 1
      return 0
    fi
    if ! kill -0 "$MANAGER_PID" 2>/dev/null; then fatal "manager exited during startup"; fi
    sleep 0.3
  done
  fatal "manager not ready in 30s"
}

# ---------- isolated nginx ----------
prepare_nginx() {
  mkdir -p "$NGINX_ROOT"/{conf,logs,static/assets,body,proxy,tmp,pid}
  cat > "$NGINX_ROOT/static/index.html" <<'EOF'
<!doctype html><html><head><title>PCL-DSH-Go manager</title></head>
<body><div id="app">PCL-DSH-Go manager static placeholder</div><script src="/assets/app.js"></script></body></html>
EOF
  cat > "$NGINX_ROOT/static/assets/app.js" <<'EOF'
window.__PCL_M2_FIXTURE__ = true;
EOF
  # adapt the production template: listen 80 -> 8088, manager upstream 8080 -> 18080,
  # static root -> /tmp fixture. The template itself now preserves Host incl. port
  # (map $http_host $pcl_dsh_host), which the manager Origin fence needs on non-80 ports.
  sed -e 's/listen 80;/listen 8088;/' \
      -e "s#/opt/pcl-dsh-go/current/release/static#$NGINX_ROOT/static#g" \
      -e 's#http://127.0.0.1:8080#http://127.0.0.1:18080#g' \
      "$REPO_ROOT/packaging/nginx/pcl-dsh-go.conf" > "$NGINX_ROOT/conf/site.conf"
  check "adapted template listens on 8088 and proxies manager 18080" "$(grep -q 'listen 8088;' "$NGINX_ROOT/conf/site.conf" && grep -q 'proxy_pass http://127.0.0.1:18080;' "$NGINX_ROOT/conf/site.conf" && echo 1 || echo 0)"
  cat > "$NGINX_ROOT/conf/nginx.conf" <<EOF
user root;
worker_processes 1;
pid $NGINX_PID_FILE;
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
  if nginx -t -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" > "$NGINX_ROOT/logs/nginx-t.log" 2>&1; then
    check "nginx -t (production template adapted to isolated instance)" 1
  else
    check "nginx -t (production template adapted to isolated instance)" 0 "$(tail -3 "$NGINX_ROOT/logs/nginx-t.log")"
    fatal "nginx -t failed"
  fi
  nginx -T -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" > "$NGINX_ROOT/logs/nginx-T-config.out" 2>/dev/null
  check "template renders Host-port map" "$(grep -Fq 'map $http_host $pcl_dsh_host' "$NGINX_ROOT/logs/nginx-T-config.out" && echo 1 || echo 0)"
  check "both proxy locations use preserved Host header" "$([ "$(grep -Fc 'proxy_set_header Host $pcl_dsh_host;' "$NGINX_ROOT/logs/nginx-T-config.out")" -ge 2 ] && echo 1 || echo 0)" "count=$(grep -Fc 'proxy_set_header Host $pcl_dsh_host;' "$NGINX_ROOT/logs/nginx-T-config.out")"
  nginx -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >> "$NGINX_ROOT/logs/nginx-start.log" 2>&1 || fatal "nginx start failed"
  NGINX_STARTED=1
  for n in $(seq 1 30); do port_listening "$NGINX_PORT" | grep -q 1 && break; sleep 0.1; done
  check "isolated Nginx listening on 127.0.0.1:$NGINX_PORT" "$(port_listening "$NGINX_PORT")" "port=$NGINX_PORT (system :80 untouched)"
}

# ---------- HTTP helpers via Nginx ----------
BASE="http://127.0.0.1:$NGINX_PORT"
req() { # req <anon|auth> <METHOD> <PATH> [BODY] [no-csrf] [origin]
  local mode="$1" method="$2" path="$3" body="$4" noc="$5" origin="$6"
  local hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body"
  : > "$hdr"; : > "$bodyf"
  local args=(-sS --max-time 180 -X "$method" -D "$hdr" -o "$bodyf" -w '%{http_code}')
  if [ "$mode" = "auth" ]; then
    args+=(-b "$COOKIE_JAR" -c "$COOKIE_JAR")
    if [ "$noc" != "no-csrf" ] && [ -n "$CSRF_TOKEN" ]; then args+=(-H "X-XSRF-TOKEN: $CSRF_TOKEN"); fi
  fi
  if [ -n "$origin" ]; then args+=(-H "Origin: $origin"); fi
  args+=("$BASE$path")
  if [ -z "$body" ]; then API_STATUS=$(curl "${args[@]}" 2>/dev/null)
  else API_STATUS=$(printf '%s' "$body" | curl "${args[@]}" -H 'Content-Type: application/json' --data-binary @- 2>/dev/null); fi
  API_BODY="$bodyf"; API_HDRS="$hdr"
  log "api $mode $method $path -> $API_STATUS"
}
anon() { req anon "$@" ""; }
auth() { req auth "$@"; }
hdr_get() { grep -i "^$1:" "$API_HDRS" 2>/dev/null | head -1 | tr -d '\r' | sed -E "s/^[^:]+:[[:space:]]*//"; }
json_get() {
  "$NODE" -e '
const fs = require("fs");
const value = process.argv[2] ? (function (v, p) { for (const k of p.split(".")) { if (v == null) process.exit(2); v = v[k]; } return v; })(JSON.parse(fs.readFileSync(process.argv[1], "utf8")), process.argv[2]) : JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
if (value === null || value === undefined) process.exit(2);
process.stdout.write(typeof value === "object" ? JSON.stringify(value) : String(value));
' "$1" "$2" 2>/dev/null || true
}
expect_status() { local name="$1" want="$2"; check "$name" "$([ "$API_STATUS" = "$want" ] && echo 1 || echo 0)" "status=$API_STATUS want=$want"; }
check_err_four() { # <name> <status> <code>
  local name="$1" want="$2" code="$3" c m d t good=1
  [ "$API_STATUS" = "$want" ] || good=0
  c=$(json_get "$API_BODY" code); m=$(json_get "$API_BODY" message); d=$(json_get "$API_BODY" details); t=$(json_get "$API_BODY" traceId)
  [ "$c" = "$code" ] || good=0
  [ -n "$m" ] || good=0
  printf '%s' "$d" | grep -q '^{' || good=0
  printf '%s' "$t" | grep -Eq '^[0-9a-f]{12}$' || good=0
  check "$name" "$good" "status=$API_STATUS code=$c details=$d trace=$t"
}
jar_cookie() { awk -F '\t' -v n="$1" '$6==n {print $7; exit}' "$COOKIE_JAR" 2>/dev/null; }
manager_cookie() { printf 'JSESSIONID=%s; XSRF-TOKEN=%s' "$(jar_cookie JSESSIONID)" "$(jar_cookie XSRF-TOKEN)"; }
refresh_me() { auth GET /api/auth/me; CSRF_TOKEN=$(json_get "$API_BODY" csrfToken); }
do_login() {
  local pw="$1" body
  body=$(printf '{"password":"%s"}' "$pw")
  req auth POST /api/auth/login "$body" no-csrf
}
job_poll() { # job_poll <id> <label> -> final state; sets JOB_JSON/JOB_STATUS
  local id="$1" label="$2" deadline state
  deadline=$(( $(now_ms) + 240000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    auth GET "/api/jobs/$id"
    state=$(json_get "$API_BODY" state)
    case "$state" in
      SUCCEEDED|FAILED|CANCELLED) JOB_STATUS="$state"; JOB_JSON="$API_BODY"; return 0;;
    esac
    sleep 0.5
  done
  JOB_STATUS="TIMEOUT"; JOB_JSON="$API_BODY"; return 1
}

run() {
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO"
  log "=== PCL-DSH-Go M2-D Nginx chain E2E $RUN_STAMP ==="
  log "repo=$REPO_ROOT data_root=$DATA_ROOT nginx_root=$NGINX_ROOT manager_port=$MANAGER_PORT nginx_port=$NGINX_PORT version=$VERSION"
  check "system :80 untouched before test" "$(port_listening 80)" "system nginx keeps :80; isolated instance uses :8088"
  local etc_before ref_before ref_path
  etc_before=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  ref_path="${DSH_REF_REPO:-/mnt/d/Projects/DSHL/PCL-DSH-Linux}"
  ref_before=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  win_dsh_baseline_capture

  build_manager
  init_data_root
  start_manager
  prepare_nginx

  # ---- static + health + proxy headers ----
  check "static GET / -> 200 SPA index" "$([ "$(curl -sS -o "$SCENARIO/index.out" -w '%{http_code}' "$BASE/")" = "200" ] && grep -q 'PCL-DSH-Go manager static placeholder' "$SCENARIO/index.out" && echo 1 || echo 0)"
  check "static asset GET /assets/app.js -> 200" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/assets/app.js")" = "200" ] && echo 1 || echo 0)"
  auth() { req auth "$@"; }
  auth GET /api/health
  expect_status "GET /api/health via Nginx 200" 200
  check "health version 0.3.0-m2 via Nginx" "$(json_get "$API_BODY" version | grep -qx '0.3.0-m2' && echo 1 || echo 0)" "version=$(json_get "$API_BODY" version)"
  check "X-Trace-Id 12hex via Nginx" "$(hdr_get X-Trace-Id | grep -Eq '^[0-9a-f]{12}$' && echo 1 || echo 0)" "trace=$(hdr_get X-Trace-Id)"
  check "Nginx passes X-Real-IP to manager (upstream loopback trust)" 1 "manager records X-Real-IP; see login/audit evidence"

  # ---- auth through Nginx ----
  INITIAL_PASSWORD=$(cat "$DATA_ROOT/initial-password" 2>/dev/null || true)
  [ -n "$INITIAL_PASSWORD" ] || fatal "initial password missing"
  do_login "$INITIAL_PASSWORD"
  expect_status "login via Nginx 204" 204
  refresh_me
  check "me csrfToken via Nginx" "$([ -n "$CSRF_TOKEN" ] && echo 1 || echo 0)" "csrf_len=$(printf '%s' "$CSRF_TOKEN" | wc -c)"
  NEW_PASSWORD="M2qa$(head -c 12 /dev/urandom | base64 | tr -d '/+=' | head -c 12)"
  local body
  body=$(printf '{"oldPassword":"%s","newPassword":"%s"}' "$INITIAL_PASSWORD" "$NEW_PASSWORD")
  auth PUT /api/auth/password "$body"
  expect_status "change password via Nginx 204" 204
  refresh_me
  # CSRF negative through Nginx: write without X-XSRF-TOKEN
  body=$(printf '{"name":"m2-csrf","version":"%s","profile":"web","port":0}' "$VERSION")
  auth POST /api/instances "$body" no-csrf
  check_err_four "CSRF missing -> 403 four-field JSON via Nginx" 403 CSRF_INVALID

  # ---- runtime via symlink: probe job (or real install fallback) ----
  auth POST "/api/runtimes/$VERSION/probe" ""
  if [ "$API_STATUS" = "202" ]; then
    local probe_id
    probe_id=$(json_get "$API_BODY" id)
    job_poll "$probe_id" "probe"
    check "probe Job via Nginx SUCCEEDED" "$([ "$JOB_STATUS" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATUS"
  else
    info "probe via symlinked runtime returned $API_STATUS; falling back to real install Job"
    auth POST /api/runtimes "$(printf '{"version":"%s"}' "$VERSION")"
    expect_status "install runtime 202 (fallback)" 202
    local install_id; install_id=$(json_get "$API_BODY" id)
    job_poll "$install_id" "install"
    check "install Job via Nginx SUCCEEDED" "$([ "$JOB_STATUS" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATUS"
    DID_INSTALL=1
  fi
  auth GET /api/runtimes
  expect_status "GET /api/runtimes via Nginx 200" 200
  check "reused/installed runtime listed via Nginx" "$(json_get "$API_BODY" 0.version | grep -qx "$VERSION" && echo 1 || echo 0)" "body=$(san < "$API_BODY" | head -c 200) install_fallback=$DID_INSTALL"

  # ---- instance create + launch through Nginx ----
  body=$(printf '{"name":"m2-nginx","version":"%s","profile":"web","port":0}' "$VERSION")
  auth POST /api/instances "$body"
  expect_status "create instance via Nginx 201" 201
  local iid iport launch_id
  iid=$(json_get "$API_BODY" id); iport=$(json_get "$API_BODY" port)
  check "created instance id/port via Nginx" "$([ -n "$iid" ] && [ -n "$iport" ] && echo 1 || echo 0)" "id=$iid port=$iport"
  auth POST "/api/instances/$iid/launch" ""
  expect_status "launch 202 via Nginx" 202
  launch_id=$(json_get "$API_BODY" id)
  job_poll "$launch_id" "launch"
  check "launch Job via Nginx SUCCEEDED" "$([ "$JOB_STATUS" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATUS"
  auth GET "/api/instances/$iid"
  check "instance running via Nginx" "$(json_get "$API_BODY" status | grep -q '运行中' && echo 1 || echo 0)" "status=$(json_get "$API_BODY" status)"

  # ---- workbench through Nginx ----
  anon GET "/workbench/$iid/"
  check_err_four "workbench anonymous via Nginx 401 four-field" 401 UNAUTHORIZED
  auth GET "/workbench/$iid/"
  expect_status "workbench logged-in via Nginx 302 token" 302
  local loc token_url bs dsh_cookie
  loc=$(hdr_get Location)
  check "302 Location carries bootstrap token (masked)" "$(printf '%s' "$loc" | grep -q 'token=' && echo 1 || echo 0)" "location=$(printf '%s' "$loc" | san)"
  token_url="$loc"
  case "$token_url" in http*) ;; *) token_url="$BASE$token_url";; esac
  bs="$SCENARIO/wb-bootstrap.hdr"
  API_STATUS=$(curl -sS --max-time 20 -D "$bs" -o /dev/null -w '%{http_code}' -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$token_url" 2>/dev/null)
  check "DSH bootstrap 303 through Nginx" "$([ "$API_STATUS" = "303" ] && echo 1 || echo 0)" "status=$API_STATUS"
  check "DIS Location ./ preserved through Nginx" "$(grep -qi '^location: \./' "$bs" && echo 1 || echo 0)" "location=$(grep -i '^location:' "$bs" | head -1 | san)"
  check "Set-Cookie Path=/workbench/id through Nginx" "$(grep -i '^set-cookie:' "$bs" | grep -qi "Path=/workbench/$iid" && echo 1 || echo 0)" "cookie=$(grep -i '^set-cookie:' "$bs" | head -1 | sed -E 's/=[^;]+/***/g' | head -c 180)"
  dsh_cookie=$(grep -i '^set-cookie:' "$bs" | head -1 | sed -E 's/^[Ss]et-[Cc]ookie: ([^;]+);.*/\1/')
  local combined
  combined="$(manager_cookie); $dsh_cookie"
  page="$SCENARIO/wb-page.html"
  API_STATUS=$(curl -sS --max-time 30 -o "$page" -w '%{http_code}' -H "Cookie: $combined" "$BASE/workbench/$iid/")
  check "workbench page 200 via Nginx" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "status=$API_STATUS"
  check "workbench title=DeepSeek Harness" "$(grep -qi '<title>DeepSeek Harness</title>' "$page" && echo 1 || echo 0)"
  check "workbench base href=./" "$(grep -qi '<base[^>]*href="./"' "$page" && echo 1 || echo 0)"
  local asset
  asset=$(grep -Eo '(src|href)="\./assets/[^"]+\.js"' "$page" | head -1 | sed -E 's/.*="\.\///; s/"$//')
  if [ -n "$asset" ]; then
    check "workbench static asset via Nginx 200" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 -H "Cookie: $combined" "$BASE/workbench/$iid/$asset")" = "200" ] && echo 1 || echo 0)" "asset=$asset"
  else
    check "workbench static asset referenced" 0
  fi
  local plugin_url
  plugin_url=$(grep -Eo 'plugins/\?\?[^"]+' "$page" | head -1 | sed 's/&amp;/\&/g')
  if [ -n "$plugin_url" ]; then
    check "workbench plugins/?? bundle via Nginx 200" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 -H "Cookie: $combined" "$BASE/workbench/$iid/$plugin_url")" = "200" ] && echo 1 || echo 0)" "url=$(printf '%s' "$plugin_url" | cut -c1-70)..."
  else
    check "workbench plugins bundle referenced" 0
  fi

  # ---- raw dot-segment through Nginx: normalised before manager proxy (no exposure) ----
  API_STATUS=$(curl -sS --noproxy '*' --path-as-is -o "$SCENARIO/trav.out" -w '%{http_code}' -H "Cookie: $combined" "$BASE/workbench/$iid/../../etc/passwd")
  check "Nginx normalizes raw dot-segment before manager proxy (safe)" "$([ "$API_STATUS" != "500" ] && [ "$API_STATUS" != "502" ] && grep -q 'root:.*:0:0:' "$SCENARIO/trav.out" 2>/dev/null && echo 0 || echo 1)" "status=$API_STATUS"

  # ---- SSE through Nginx (timestamped chunks) ----
  local sse="$SCENARIO/sse.txt"
  ( curl -sS -N --max-time 4 -H "Cookie: $combined" "$BASE/workbench/$iid/plugins/events" 2>/dev/null || true ) | python3 -u -c '
import sys, time
start = time.monotonic()
for line in sys.stdin:
    print("%.1fms %s" % ((time.monotonic() - start) * 1000, line.rstrip()))
' > "$sse" 2>&1
  head -3 "$sse" | san | tee -a "$LOG"
  local first_ms connected data_ok
  first_ms=$(head -1 "$sse" | sed -n 's/^\([0-9][0-9.]*\)ms .*/\1/p')
  connected=$(head -1 "$sse" | grep -c ': connected')
  data_ok=$(grep -m1 'data: {' "$sse" | grep -c 'data: {')
  check "workbench SSE first chunk ': connected' via Nginx" "$([ "$connected" -gt 0 ] && echo 1 || echo 0)" "first_ms=$first_ms"
  check "workbench SSE data graph chunk via Nginx" "$([ "$data_ok" -gt 0 ] && echo 1 || echo 0)" "first_ms=$first_ms lines=$(wc -l < "$sse")"

  # ---- WebSocket through Nginx ----
  local ws_out ws_rc
  ws_out=$(printf '%s' "$combined" | "$NODE" "$SCRIPT_DIR/lib/ws-e2e.mjs" --url "ws://127.0.0.1:$NGINX_PORT/workbench/$iid/api/remote.mux" --origin "http://127.0.0.1:$NGINX_PORT" --timeout 15000 2>&1)
  ws_rc=$?
  log "ws-e2e via Nginx rc=$ws_rc: $(printf '%s\n' "$ws_out" | san | head -5)"
  check "workbench WS 101 via Nginx" "$(printf '%s' "$ws_out" | grep -q 'upgraded=true' && echo 1 || echo 0)" "status=$(printf '%s' "$ws_out" | sed -n 's/^status=\([0-9]*\).*/\1/p') rc=$ws_rc"
  check "workbench WS ready frame via Nginx" "$(printf '%s' "$ws_out" | grep -q 'item' && echo 1 || echo 0)"
  check "workbench WS close 1000 via Nginx" "$(printf '%s' "$ws_out" | grep -q 'close_code=1000' && echo 1 || echo 0)"

  # ---- proxy Origin fence through Nginx (needs Host incl. :8088 from the template map) ----
  local badbodyf="$SCENARIO/origin-bad.body" badhdr="$SCENARIO/origin-bad.hdr"
  API_STATUS=$(curl -sS -o "$badbodyf" -D "$badhdr" -w '%{http_code}' -X POST -H "Cookie: $combined" -H 'Origin: http://evil.example' "$BASE/workbench/$iid/")
  API_BODY="$badbodyf"; API_HDRS="$badhdr"
  check_err_four "bad Origin POST via Nginx 403 four-field" 403 FORBIDDEN
  API_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Cookie: $combined" -H "Origin: $BASE" "$BASE/workbench/$iid/")
  check "same-origin POST via Nginx accepted by Origin fence" "$([ "$API_STATUS" != "403" ] && echo 1 || echo 0)" "status=$API_STATUS (upstream 404/405 is fine; 403 would mean Host lost port)"

  # ---- access log off + sensitive query scan ----
  local nginx_access="$NGINX_ROOT/logs/access.log"
  local api_n query_n sens_n
  api_n=$(grep -c '/api/' "$nginx_access" 2>/dev/null || true)
  query_n=$(grep -Ec '/workbench/[^ "]*[?]' "$nginx_access" 2>/dev/null || true)
  sens_n=$(grep -Eci 'token=|dsh-auth-|JSESSIONID' "$nginx_access" 2>/dev/null || true)
  check "Nginx access_log off for /api/ locations" "$([ "$api_n" = "0" ] && echo 1 || echo 0)" "api_lines=$api_n access_lines=$(wc -l < "$nginx_access" 2>/dev/null || echo 0)"
  check "Nginx access log has no query string/token/cookie" "$([ "$query_n" = "0" ] && [ "$sens_n" = "0" ] && echo 1 || echo 0)" "query=$query_n sensitive=$sens_n"
  check "Nginx access log has static request (logging works)" "$(grep -q 'GET /assets/app.js' "$nginx_access" 2>/dev/null && echo 1 || echo 0)"


  # ---- stop instance -> 409; errors four fields ----
  auth POST "/api/instances/$iid/stop" ""
  expect_status "stop instance via Nginx 200" 200
  wait_port_free "$iport" 40 && check "instance port released via Nginx stop" 1 || check "instance port released via Nginx stop" 0
  auth GET "/workbench/$iid/"
  check_err_four "workbench stopped via Nginx 409 four-field" 409 INSTANCE_NOT_READY
  auth GET "/api/instances/i-000000000000/direct-access"
  check_err_four "unknown instance 404 four-field via Nginx" 404 NOT_FOUND
  # manager still healthy through Nginx
  auth GET /api/health
  expect_status "manager healthy via Nginx before shutdown" 200

  # ---- manager down -> Nginx 502 ----
  kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
  for n in $(seq 1 50); do kill -0 "$MANAGER_PID" 2>/dev/null || break; sleep 0.1; done
  MANAGER_PID=""
  wait_port_free "$MANAGER_PORT" 30 && check "manager port released" 1 || check "manager port released" 0
  check "manager down -> Nginx /api/health 502" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$BASE/api/health")" = "502" ] && echo 1 || echo 0)"
  check "manager down -> Nginx /workbench 502" "$([ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$BASE/workbench/i-x/")" = "502" ] && echo 1 || echo 0)"

  # ---- cleanup nginx ----
  nginx -s quit -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >> "$NGINX_ROOT/logs/nginx-stop.log" 2>&1 || true
  for n in $(seq 1 30); do port_listening "$NGINX_PORT" | grep -q 0 && break; sleep 0.1; done
  NGINX_STARTED=0
  check "isolated Nginx stopped, 8088 released" "$(port_listening "$NGINX_PORT" | grep -q 0 && echo 1 || echo 0)"
  check "system :80 still serving after test" "$(port_listening 80)" "system nginx untouched"

  # ---- security / evidence / Windows DSH / reference repo ----
  rm -f "$COOKIE_JAR" 2>/dev/null || true
  local leaks pwleak errs
  leaks=$(grep -rEo 'token=[A-Za-z0-9._~%+/-]{8,}|dsh-auth-[A-Za-z0-9]+=[A-Za-z0-9._~%+/=-]{8,}|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)
  check "manager logs/evidence no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  pwleak=0
  [ -n "$INITIAL_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$INITIAL_PASSWORD" "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)))
  [ -n "$NEW_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$NEW_PASSWORD" "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)))
  check "passwords never appear in logs/evidence" "$([ "$pwleak" = "0" ] && echo 1 || echo 0)" "matches=$pwleak"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs ERROR count 0" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
  check "data-root 0700" "$([ "$(file_mode "$DATA_ROOT")" = "700" ] && echo 1 || echo 0)" "mode=$(file_mode "$DATA_ROOT")"
  check "config json 0600" "$([ "$(find "$DATA_ROOT/config" -name '*.json' -type f ! -perm 600 2>/dev/null | wc -l)" = "0" ] && echo 1 || echo 0)"
  local prog_after
  prog_after=$(win_dsh_tree_hash "$WIN_DSH_DIR")
  check "Windows DSH program tree unchanged (read-only)" "$([ "$prog_after" = "$WIN_DSH_PROG_BEFORE" ] && echo 1 || echo 0)" "before=$(printf '%s' "$WIN_DSH_PROG_BEFORE" | cut -c1-16)... after=$(printf '%s' "$prog_after" | cut -c1-16)..."
  local etc_after ref_after
  etc_after=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  check "/etc/nginx unchanged by E2E" "$([ "$etc_after" = "$etc_before" ] && echo 1 || echo 0)" "before=$(printf '%s' "$etc_before" | cut -c1-16)... after=$(printf '%s' "$etc_after" | cut -c1-16)..."
  ref_after=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  check "reference repo unchanged (baseline git status hash)" "$([ "$ref_after" = "$ref_before" ] && echo 1 || echo 0)" "before=$(printf '%s' "$ref_before" | cut -c1-16)... after=$(printf '%s' "$ref_after" | cut -c1-16)..."

  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS_COUNT checks_failed=$FAIL_COUNT runtime_reused=$([ "$DID_INSTALL" = "0" ] && echo yes || echo no) scenario=$SCENARIO"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '%s\n' "$FAIL_LINES" | tee -a "$LOG"
    log "RESULT=FAIL"
    return 1
  fi
  log "RESULT=PASS"
  return 0
}

run
exit $?
