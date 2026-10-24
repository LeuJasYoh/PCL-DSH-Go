#!/usr/bin/env bash
# PCL-DSH-Go M3-K | Real acceptance E2E: G6 packs (preview/import/export/artifact)
# and G7 plugins (list/add/update/remove) against the real manager + real DSH runtime.
# Isolated Nginx(8088) covers the production chain subset; system /etc/nginx/:80 untouched.
# Evidence: docs/evidence/m3/m3-acceptance-<ts>.log.txt (+ manager/nginx logs, 脱敏).
set -o pipefail
export NO_PROXY='*' no_proxy='*'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="${PCL_DSH_TEST_ROOT:-/tmp/pcl-dsh-go-test}"
TOOLS_ROOT="${PCL_DSH_TOOLS:-/opt/pcl-dsh-go/tools}"
NODE="$TOOLS_ROOT/node/bin/node"
VERSION="${DSH_VERSION:-0.2.1-rc.3}"
RUNTIME_SRC="${RUNTIME_SRC:-$ROOT/e2e-data/runtimes/$VERSION}"
MANAGER_PORT="${MANAGER_PORT:-18080}"
NGINX_PORT="${NGINX_PORT:-8088}"
DATA_ROOT="$ROOT/m3-data"
NGINX_ROOT="$ROOT/m3-nginx"
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
SCENARIO="$ROOT/m3-scenario-$RUN_STAMP"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m3"
LOG="$EVIDENCE_DIR/m3-acceptance-$RUN_STAMP.log"
MANAGER_BIN="$REPO_ROOT/bin/pcl-dsh-go"
COOKIE_JAR="$SCENARIO/cookies.txt"
MANAGER_PID=""
NGINX_STARTED=0
PASS=0
FAIL=0
FAILS=""
CSRF_TOKEN=""
PREVIEW_V2=""
PREVIEW_V3=""
PREVIEW_HOME=""
PREVIEW_OVERTURE=""
INSTANCE_ID=""
ARTIFACT_ID=""
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
  if [ "$NGINX_STARTED" = "1" ] && [ -f "$NGINX_ROOT/pid/nginx.pid" ]; then
    nginx -s quit -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >/dev/null 2>&1 || true
    for n in $(seq 1 30); do [ -f "$NGINX_ROOT/pid/nginx.pid" ] || break; sleep 0.1; done
  fi
  if [ -n "$MANAGER_PID" ] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill -TERM -- "-$MANAGER_PID" 2>/dev/null || true
    sleep 1
    kill -KILL -- "-$MANAGER_PID" 2>/dev/null || true
  fi
  rm -f "$COOKIE_JAR" 2>/dev/null || true
  rm -f "$SCENARIO"/*.hdr "$SCENARIO"/*.body 2>/dev/null || true
  rm -rf "$SCENARIO/workspace-plugin" 2>/dev/null || true
  if [ -f "$SCENARIO/manager.log" ]; then san < "$SCENARIO/manager.log" > "$EVIDENCE_DIR/m3-acceptance-$RUN_STAMP-manager.log.txt" 2>/dev/null || true; fi
  if [ -f "$NGINX_ROOT/logs/access.log" ]; then san < "$NGINX_ROOT/logs/access.log" > "$EVIDENCE_DIR/m3-acceptance-$RUN_STAMP-nginx-access.log.txt" 2>/dev/null || true; fi
  if [ -f "$NGINX_ROOT/logs/error.log" ]; then san < "$NGINX_ROOT/logs/error.log" > "$EVIDENCE_DIR/m3-acceptance-$RUN_STAMP-nginx-error.log.txt" 2>/dev/null || true; fi
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

build_manager() {
  local go_bin win_go cmd bat win_bat size sha
  if command -v go.exe >/dev/null 2>&1; then go_bin="$(command -v go.exe)"
  elif command -v go >/dev/null 2>&1; then go_bin="$(command -v go)"; else fatal "no Go toolchain"; fi
  mkdir -p "$REPO_ROOT/bin"
  case "$go_bin" in
    *.exe)
      win_go="$(winpath "$go_bin")"; cmd="$(command -v cmd.exe)"; bat="$REPO_ROOT/bin/m3-build.bat"; win_bat="$(winpath "$bat")"
      { printf '@echo off\r\n'; printf 'set GOOS=linux\r\nset GOARCH=amd64\r\nset CGO_ENABLED=0\r\n'; printf '"%s" build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=0.3.0-m3" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go\r\n' "$win_go"; printf 'exit /b %%%%ERRORLEVEL%%%%\r\n'; } > "$bat"
      ( cd "$REPO_ROOT" && "$cmd" /c "$win_bat" ) >> "$LOG" 2>&1 || { rm -f "$bat"; fatal "manager build failed"; }
      rm -f "$bat";;
    *) ( cd "$REPO_ROOT" && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$go_bin" build -trimpath -buildvcs=false -ldflags '-s -w -X main.version=0.3.0-m3' -o bin/pcl-dsh-go ./cmd/pcl-dsh-go ) >> "$LOG" 2>&1 || fatal "manager build failed";;
  esac
  chmod +x "$MANAGER_BIN"
  size=$(stat -c %s "$MANAGER_BIN"); sha=$(sha256sum "$MANAGER_BIN" | awk '{print $1}')
  log "manager built from current tree (M3): size=$size B sha256=$sha"
  check "manager linux/amd64 build from current tree" 1 "size=$size sha256=$sha"
}
start_manager() {
  setsid "$MANAGER_BIN" --pcl.data-root="$DATA_ROOT" --pcl.tool-root="$TOOLS_ROOT" \
    --server.address=127.0.0.1 --server.port="$MANAGER_PORT" > "$SCENARIO/manager.log" 2>&1 &
  MANAGER_PID=$!
  log "manager start pid=$MANAGER_PID data-root=$DATA_ROOT port=$MANAGER_PORT"
  local deadline=$(( $(now_ms) + 30000 ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    if curl -sf --max-time 2 --noproxy '*' "http://127.0.0.1:$MANAGER_PORT/api/health" > "$SCENARIO/health.json" 2>/dev/null; then
      log "manager health: $(san < "$SCENARIO/health.json")"
      check "manager /api/health" 1
      return 0
    fi
    kill -0 "$MANAGER_PID" 2>/dev/null || fatal "manager exited during startup"
    sleep 0.3
  done
  fatal "manager not ready in 30s"
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
prepare_nginx() {
  mkdir -p "$NGINX_ROOT"/{conf,logs,static/assets,body,proxy,tmp,pid}
  printf '<!doctype html><html><head><title>PCL-DSH-Go M3 fixture</title></head><body>m3 fixture</body></html>\n' > "$NGINX_ROOT/static/index.html"
  printf 'window.__M3_FIXTURE__ = true;\n' > "$NGINX_ROOT/static/assets/app.js"
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
  check "nginx -t (M2 production template adapted)" 1
  nginx -c "$NGINX_ROOT/conf/nginx.conf" -p "$NGINX_ROOT" >> "$NGINX_ROOT/logs/nginx-start.log" 2>&1 || fatal "nginx start failed"
  NGINX_STARTED=1
  for n in $(seq 1 30); do port_listening "$NGINX_PORT" | grep -q 1 && break; sleep 0.1; done
  check "isolated Nginx :$NGINX_PORT up (system :80 untouched)" "$(port_listening "$NGINX_PORT")"
}
req() { # req <manager|nginx> <METHOD> <PATH> [BODY]
  local target="$1" method="$2" path="$3" body="$4"
  local base="$MNGR"; [ "$target" = "nginx" ] && base="$BASE"
  local hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body"
  : > "$hdr"; : > "$bodyf"
  local args=(-sS --noproxy '*' --max-time 300 -X "$method" -D "$hdr" -o "$bodyf" -w '%{http_code}')
  args+=(-b "$COOKIE_JAR" -c "$COOKIE_JAR")
  [ -n "$CSRF_TOKEN" ] && args+=(-H "X-XSRF-TOKEN: $CSRF_TOKEN")
  args+=("$base$path")
  if [ -z "$body" ]; then API_STATUS=$(curl "${args[@]}" 2>/dev/null)
  else API_STATUS=$(printf '%s' "$body" | curl "${args[@]}" -H 'Content-Type: application/json' --data-binary @- 2>/dev/null); fi
  API_BODY="$bodyf"; API_HDRS="$hdr"
  log "api $target $method $path -> $API_STATUS"
}
upload() { # upload <manager|nginx> <path> <file>
  local target="$1" path="$2" file="$3"
  local base="$MNGR"; [ "$target" = "nginx" ] && base="$BASE"
  local hdr="$SCENARIO/up.hdr" bodyf="$SCENARIO/up.body"
  : > "$hdr"; : > "$bodyf"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 300 -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
    -H "X-XSRF-TOKEN: $CSRF_TOKEN" -F "file=@$file" \
    -D "$hdr" -o "$bodyf" -w '%{http_code}' "$base$path" 2>/dev/null)
  API_BODY="$bodyf"; API_HDRS="$hdr"
  log "api $target upload $path ($(basename "$file")) -> $API_STATUS"
}
hdr_get() { grep -i "^$1:" "$API_HDRS" 2>/dev/null | head -1 | tr -d '\r' | sed -E "s/^[^:]+:[[:space:]]*//"; }
jget() { # jget <file> <dotted>
  "$NODE" -e '
const fs=require("fs");const v=(function(o,p){for(const k of p.split(".")){if(o==null)process.exit(2);o=o[k];}return o;})(JSON.parse(fs.readFileSync(process.argv[1],"utf8")),process.argv[2]);
if(v===null||v===undefined)process.exit(2);process.stdout.write(typeof v==="object"?JSON.stringify(v):String(v));' "$1" "$2" 2>/dev/null || true
}
err_ok() { # err_ok <name> <want-status> <want-code>
  local name="$1" want="$2" code="$3" c m d t good=1
  [ "$API_STATUS" = "$want" ] || good=0
  c=$(jget "$API_BODY" code); m=$(jget "$API_BODY" message); d=$(jget "$API_BODY" details); t=$(jget "$API_BODY" traceId)
  [ "$c" = "$code" ] || good=0
  [ -n "$m" ] || good=0
  printf '%s' "$d" | grep -q '^{' || good=0
  printf '%s' "$t" | grep -Eq '^[0-9a-f]{12}$' || good=0
  check "$name" "$good" "status=$API_STATUS code=$c details=$d trace=$t"
}
refresh_me() { req manager GET /api/auth/me ""; CSRF_TOKEN=$(jget "$API_BODY" csrfToken); }
do_login() {
  local pw="$1" body
  body=$(printf '{"password":"%s"}' "$pw")
  req manager POST /api/auth/login "$body"
}
jar_cookie() { awk -F '\t' -v n="$1" '$6==n {print $7; exit}' "$COOKIE_JAR" 2>/dev/null; }
manager_cookie() { printf 'JSESSIONID=%s; XSRF-TOKEN=%s' "$(jar_cookie JSESSIONID)" "$(jar_cookie XSRF-TOKEN)"; }
job_poll() { # job_poll <id> [timeout-ms]
  local id="$1" timeout="${2:-300000}" deadline state
  deadline=$(( $(now_ms) + timeout ))
  while [ "$(now_ms)" -lt "$deadline" ]; do
    req manager GET "/api/jobs/$id" ""
    state=$(jget "$API_BODY" state)
    case "$state" in
      SUCCEEDED|FAILED|CANCELLED)
        JOB_STATE="$state"; JOB_JSON="$API_BODY"
        [ "$state" = "FAILED" ] && log "job $id FAILED error=$(jget "$JOB_JSON" error.code): $(jget "$JOB_JSON" error.message | head -c 160)"
        return 0;;
    esac
    sleep 0.5
  done
  JOB_STATE="TIMEOUT"; JOB_JSON="$API_BODY"; return 1
}
start_job() { # start_job <type-label> <method> <path> <body>
  req manager "$2" "$3" "$4"
  check "$1 accepted (202 JobView)" "$([ "$API_STATUS" = "202" ] && echo 1 || echo 0)" "status=$API_STATUS"
  JOB_ID=$(jget "$API_BODY" id)
  [ -n "$JOB_ID" ] || fatal "$1 returned no job id"
  log "job $1 id=$JOB_ID type=$(jget "$API_BODY" type)"
}
wb_check() { # wb_check <instanceId> <label>
  local iid="$1" label="$2" loc token_url bs dsh_cookie page combined
  : > "$SCENARIO/wb-anon.hdr"; : > "$SCENARIO/wb-anon.body"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 30 -D "$SCENARIO/wb-anon.hdr" -o "$SCENARIO/wb-anon.body" -w '%{http_code}' "$MNGR/workbench/$iid/" 2>/dev/null)
  API_BODY="$SCENARIO/wb-anon.body"; API_HDRS="$SCENARIO/wb-anon.hdr"
  err_ok "$label workbench anonymous 401 four-field" 401 UNAUTHORIZED
  req manager GET "/workbench/$iid/" ""
  check "$label workbench logged-in 302 token" "$([ "$API_STATUS" = "302" ] && echo 1 || echo 0)" "status=$API_STATUS"
  loc=$(hdr_get Location)
  check "$label 302 Location carries token (masked)" "$(printf '%s' "$loc" | grep -q 'token=' && echo 1 || echo 0)" "location=$(printf '%s' "$loc" | san)"
  token_url="$loc"; case "$token_url" in http*) ;; *) token_url="$MNGR$token_url";; esac
  bs="$SCENARIO/wb-$label.hdr"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 30 -D "$bs" -o /dev/null -w '%{http_code}' -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$token_url" 2>/dev/null)
  check "$label DSH bootstrap 303" "$([ "$API_STATUS" = "303" ] && echo 1 || echo 0)" "status=$API_STATUS"
  dsh_cookie=$(grep -i '^set-cookie:' "$bs" | head -1 | sed -E 's/^[Ss]et-[Cc]ookie: ([^;]+);.*/\1/')
  combined="$(manager_cookie); $dsh_cookie"
  page="$SCENARIO/wb-page-$label.html"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 60 -o "$page" -w '%{http_code}' -H "Cookie: $combined" "$MNGR/workbench/$iid/")
  check "$label workbench page 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "status=$API_STATUS"
  check "$label page title DeepSeek Harness" "$(grep -qi '<title>DeepSeek Harness</title>' "$page" && echo 1 || echo 0)"
  check "$label page base href=./" "$(grep -qi '<base[^>]*href="./"' "$page" && echo 1 || echo 0)"
}

run() {
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO"
  : > "$LOG"
  log "=== PCL-DSH-Go M3-K acceptance E2E $RUN_STAMP ==="
  log "repo=$REPO_ROOT data_root=$DATA_ROOT manager=127.0.0.1:$MANAGER_PORT nginx=127.0.0.1:$NGINX_PORT runtime=$VERSION"
  local etc_before ref_before ref_path="/mnt/d/Projects/DSHL/PCL-DSH-Linux" uploads_before instances_before trash_before
  etc_before=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  ref_before=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  win_dsh_baseline_capture
  check "system :80 untouched before test" "$(port_listening 80)"

  build_manager
  init_data_root
  uploads_before=$(find "$DATA_ROOT/cache/uploads" -type f 2>/dev/null | wc -l)
  instances_before=$(find "$DATA_ROOT/instances" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l)
  trash_before=$(find "$DATA_ROOT/trash" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
  start_manager
  prepare_nginx
  req nginx GET /api/health ""
  check "via Nginx /api/health 200 + X-Trace-Id" "$([ "$API_STATUS" = "200" ] && hdr_get X-Trace-Id | grep -Eq '^[0-9a-f]{12}$' && echo 1 || echo 0)" "trace=$(hdr_get X-Trace-Id)"

  # ---- auth / CSRF ----
  INITIAL_PASSWORD=$(cat "$DATA_ROOT/initial-password" 2>/dev/null || true)
  [ -n "$INITIAL_PASSWORD" ] || fatal "initial password missing"
  do_login "$INITIAL_PASSWORD"
  check "login initial via API 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  NEW_PASSWORD="M3qa$(head -c 12 /dev/urandom | base64 | tr -d '/+=' | head -c 12)"
  req manager PUT /api/auth/password "$(printf '{"oldPassword":"%s","newPassword":"%s"}' "$INITIAL_PASSWORD" "$NEW_PASSWORD")"
  check "change password 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  rm -f "$COOKIE_JAR"
  do_login "$NEW_PASSWORD"
  check "login new password 204" "$([ "$API_STATUS" = "204" ] && echo 1 || echo 0)"
  refresh_me
  : > "$SCENARIO/anon.hdr"; : > "$SCENARIO/anon.body"
  API_STATUS=$(curl -sS --noproxy '*' -o "$SCENARIO/anon.body" -D "$SCENARIO/anon.hdr" -w '%{http_code}' \
    -F "file=@$REPO_ROOT/testdata/pack/valid/dspack-v2-profile.dspack" "$MNGR/api/packs/preview" 2>/dev/null)
  API_BODY="$SCENARIO/anon.body"; API_HDRS="$SCENARIO/anon.hdr"
  err_ok "unauth preview -> 401 four-field" 401 UNAUTHORIZED
  req manager POST /api/packs/preview ""
  check "authenticated preview without multipart -> 400" "$([ "$API_STATUS" = "400" ] && echo 1 || echo 0)" "status=$API_STATUS code=$(jget "$API_BODY" code)"

  # ---- G6: malicious samples all rejected ----
  local f total=0 rejected=0 failed_list="" status code msg
  for f in "$REPO_ROOT"/testdata/pack/malicious/*.dspack; do
    total=$((total + 1))
    upload manager /api/packs/preview "$f"
    status="$API_STATUS"; code=$(jget "$API_BODY" code); msg=$(jget "$API_BODY" message)
    if [ "$status" = "400" ] && [ "$code" = "VALIDATION" ] && [ -n "$msg" ]; then
      rejected=$((rejected + 1))
    else
      failed_list="$failed_list$(basename "$f") status=$status code=$code; "
    fi
  done
  check "malicious samples >= 25" "$([ "$total" -ge 25 ] && echo 1 || echo 0)" "count=$total"
  check "all malicious samples rejected 400 VALIDATION (no disk write)" "$([ "$rejected" = "$total" ] && echo 1 || echo 0)" "rejected=$rejected/$total failed=[$failed_list]"
  local uploads_after instances_after
  uploads_after=$(find "$DATA_ROOT/cache/uploads" -type f 2>/dev/null | wc -l)
  instances_after=$(find "$DATA_ROOT/instances" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l)
  check "malicious previews leave no upload residue" "$([ "$uploads_after" = "$uploads_before" ] && echo 1 || echo 0)" "uploads_before=$uploads_before after=$uploads_after"
  check "malicious previews create no instance" "$([ "$instances_after" = "$instances_before" ] && echo 1 || echo 0)" "instances_before=$instances_before after=$instances_after"

  # ---- G6: valid previews ----
  preview_valid() { # preview_valid <name> <file> <exp-format>
    local name="$1" file="$2" exp_fmt="$3"
    upload manager /api/packs/preview "$file"
    local pid fmt ver dsh profiles unpacked good=1
    pid=$(jget "$API_BODY" previewId); fmt=$(jget "$API_BODY" format); ver=$(jget "$API_BODY" version)
    dsh=$(jget "$API_BODY" dshVersion); profiles=$(jget "$API_BODY" profiles); unpacked=$(jget "$API_BODY" unpackedBytes)
    [ "$API_STATUS" = "200" ] || good=0
    [ "$fmt" = "$exp_fmt" ] || good=0
    [ -n "$pid" ] && printf '%s' "$dsh" | grep -q "$VERSION" || good=0
    [ -n "$unpacked" ] && [ "$unpacked" -gt 0 ] || good=0
    check "$name preview 200 + fields (format/version/dshVersion/profiles/unpackedBytes)" "$good" "status=$API_STATUS format=$fmt version=$ver dshVersion=$dsh profiles=$profiles unpacked=$unpacked"
    LAST_PREVIEW_ID="$pid"
  }
  preview_valid "dspack v2 profile" "$REPO_ROOT/testdata/pack/valid/dspack-v2-profile.dspack" "dspack"; PREVIEW_V2="$LAST_PREVIEW_ID"
  preview_valid "dspack v3 profile" "$REPO_ROOT/testdata/pack/valid/dspack-v3-profile.dspack" "dspack"; PREVIEW_V3="$LAST_PREVIEW_ID"
  preview_valid "dspack v3 dshhome" "$REPO_ROOT/testdata/pack/valid/dspack-v3-dshhome.dspack" "dspack"; PREVIEW_HOME="$LAST_PREVIEW_ID"
  preview_valid "Overture v1" "$REPO_ROOT/testdata/pack/valid/overture-v1-minimal.dspack" "overture"; PREVIEW_OVERTURE="$LAST_PREVIEW_ID"

  # ---- import: missing preview -> Job FAILED NOT_FOUND (backend semantics) ----
  req manager POST /api/packs/import '{"previewId":"0123456789ab"}'
  check "import unknown preview -> 202 JobView" "$([ "$API_STATUS" = "202" ] && echo 1 || echo 0)"
  local badjob; badjob=$(jget "$API_BODY" id)
  job_poll "$badjob" 120000
  check "import unknown preview Job FAILED NOT_FOUND" "$([ "$JOB_STATE" = "FAILED" ] && [ "$(jget "$JOB_JSON" error.code)" = "NOT_FOUND" ] && echo 1 || echo 0)" "state=$JOB_STATE code=$(jget "$JOB_JSON" error.code)"

  # ---- synthetic minimal pack: import creates instance only (no launch assertion) ----
  start_job "import synthetic v3 preview" POST /api/packs/import "$(printf '{"previewId":"%s"}' "$PREVIEW_V3")"
  local syn_job="$JOB_ID"
  job_poll "$syn_job" 300000
  check "synthetic import Job SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  local syn_iid; syn_iid=$(jget "$JOB_JSON" result.instanceId)
  check "synthetic import result {instanceId,name}" "$([ -n "$syn_iid" ] && [ "$(jget "$JOB_JSON" result.name)" = "sample-v3" ] && echo 1 || echo 0)" "instance=$syn_iid"
  req manager GET "/api/instances/$syn_iid" ""
  check "synthetic imported instance exists" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "status=$(jget "$API_BODY" status)"
  req manager DELETE "/api/instances/$syn_iid" ""
  check "synthetic imported instance deleted (no launch asserted)" "$([ "$API_STATUS" = "200" ] && [ -n "$(jget "$API_BODY" trashedPath)" ] && echo 1 || echo 0)"

  # ---- baseline: create normal instance -> launch (proves runtime) -> stop ----
  req manager POST /api/instances "$(printf '{"name":"m3-baseline","version":"%s","profile":"web","port":0}' "$VERSION")"
  check "create baseline instance 201" "$([ "$API_STATUS" = "201" ] && echo 1 || echo 0)"
  BASE_IID=$(jget "$API_BODY" id)
  check "baseline instance id" "$([ -n "$BASE_IID" ] && echo 1 || echo 0)" "id=$BASE_IID"
  start_job "baseline launch" POST "/api/instances/$BASE_IID/launch" ""
  job_poll "$JOB_ID" 180000
  check "baseline launch SUCCEEDED (real runtime)" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  req manager GET "/api/instances/$BASE_IID/web-url" ""
  check "baseline web-url 200" "$([ "$API_STATUS" = "200" ] && [ -n "$(jget "$API_BODY" url)" ] && echo 1 || echo 0)" "url=$(jget "$API_BODY" url)"
  req manager POST "/api/instances/$BASE_IID/stop" ""
  check "baseline stop 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"

  # ---- export profile (real instance) -> artifact download -> preview -> import roundtrip ----
  start_job "export profile" POST /api/packs/export "$(printf '{"instanceId":"%s","mode":"profile"}' "$BASE_IID")"
  local export_job="$JOB_ID"
  job_poll "$export_job" 180000
  check "export profile Job SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  ARTIFACT_ID=$(jget "$JOB_JSON" result.artifactId)
  local exp_name exp_size exp_file
  exp_name=$(jget "$JOB_JSON" result.filename); exp_size=$(jget "$JOB_JSON" result.size)
  check "export result {artifactId,filename,size}" "$([ -n "$ARTIFACT_ID" ] && [ -n "$exp_name" ] && [ -n "$exp_size" ] && echo 1 || echo 0)" "artifact=$ARTIFACT_ID filename=$exp_name size=$exp_size"
  exp_file="$SCENARIO/export-profile.dspack"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 60 -b "$COOKIE_JAR" -D "$SCENARIO/art.hdr" -o "$exp_file" -w '%{http_code}' "$MNGR/api/artifacts/$ARTIFACT_ID" 2>/dev/null)
  API_HDRS="$SCENARIO/art.hdr"
  check "artifact download 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "status=$API_STATUS"
  check "artifact Content-Disposition attachment" "$(hdr_get Content-Disposition | grep -qi attachment && echo 1 || echo 0)" "value=$(hdr_get Content-Disposition)"
  check "artifact Cache-Control no-store" "$(hdr_get Cache-Control | grep -qi no-store && echo 1 || echo 0)" "value=$(hdr_get Cache-Control)"
  check "artifact size matches Job result" "$([ "$(stat -c %s "$exp_file")" = "$exp_size" ] && echo 1 || echo 0)" "file=$(stat -c %s "$exp_file") job=$exp_size"
  local art_count art_bad art_marker
  art_count=$(python3 -c 'import sys,zipfile;print(len(zipfile.ZipFile(sys.argv[1]).namelist()))' "$exp_file" 2>/dev/null || echo "")
  art_bad=$(python3 -c 'import sys,zipfile,re;z=zipfile.ZipFile(sys.argv[1]);print(sum(1 for n in z.namelist() if re.search(r"(^|/)(\.credentials|credentials|settings\.yaml|sessions?|logs?|cache|trash)(/|$)", n)))' "$exp_file" 2>/dev/null || echo "?")
  art_marker=$(python3 -c 'import sys,zipfile;print(1 if "dspack.json" in zipfile.ZipFile(sys.argv[1]).namelist() else 0)' "$exp_file" 2>/dev/null || echo 0)
  check "exported artifact is valid ZIP, no credentials/sessions/logs/cache" "$([ -n "$art_count" ] && [ "$art_count" -gt 0 ] && [ "$art_bad" = "0" ] && echo 1 || echo 0)" "entries=$art_count sensitive=$art_bad"
  check "exported artifact contains dspack marker" "$([ "$art_marker" = "1" ] && echo 1 || echo 0)"
  local reprev; preview_valid "exported profile re-preview" "$exp_file" "dspack"; reprev="$LAST_PREVIEW_ID"
  check "exported profile re-preview 200" "$([ -n "$reprev" ] && echo 1 || echo 0)"
  if [ -n "$reprev" ]; then
    start_job "roundtrip import exported profile" POST /api/packs/import "$(printf '{"previewId":"%s"}' "$reprev")"
    local rt_job="$JOB_ID"
    job_poll "$rt_job" 300000
    check "roundtrip import SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
    local rt_iid; rt_iid=$(jget "$JOB_JSON" result.instanceId)
    check "roundtrip instance created" "$([ -n "$rt_iid" ] && echo 1 || echo 0)" "instance=$rt_iid"
    if [ -n "$rt_iid" ]; then
      start_job "roundtrip launch" POST "/api/instances/$rt_iid/launch" ""
      job_poll "$JOB_ID" 180000
      check "roundtrip launch SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
      wb_check "$rt_iid" "roundtrip"
      req manager POST "/api/instances/$rt_iid/stop" ""
      req manager DELETE "/api/instances/$rt_iid" ""
      check "roundtrip instance deleted (trash)" "$([ "$API_STATUS" = "200" ] && [ -n "$(jget "$API_BODY" trashedPath)" ] && echo 1 || echo 0)"
    fi
  fi
  # ---- export home: web-only instance must FAIL VALIDATION (reference behaviour) ----
  start_job "export home on web-only instance (negative)" POST /api/packs/export "$(printf '{"instanceId":"%s","mode":"home"}' "$BASE_IID")"
  job_poll "$JOB_ID" 180000
  check "web-only instance home export FAILED VALIDATION (no exportable profile)" "$([ "$JOB_STATE" = "FAILED" ] && [ "$(jget "$JOB_JSON" error.code)" = "VALIDATION" ] && echo 1 || echo 0)" "state=$JOB_STATE code=$(jget "$JOB_JSON" error.code) message=$(jget "$JOB_JSON" error.message | head -c 140)"

  # ---- export home success: import legal dshhome (non-web profile) then export home ----
  start_job "import synthetic dshhome" POST /api/packs/import "$(printf '{"previewId":"%s"}' "$PREVIEW_HOME")"
  job_poll "$JOB_ID" 300000
  check "dshhome import Job SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE error=$(jget "$JOB_JSON" error.code): $(jget "$JOB_JSON" error.message | head -c 140)"
  HOME_IID=$(jget "$JOB_JSON" result.instanceId)
  check "dshhome imported instance created" "$([ -n "$HOME_IID" ] && echo 1 || echo 0)" "instance=$HOME_IID"
  if [ -n "$HOME_IID" ]; then
    req manager POST "/api/instances/$HOME_IID/stop" "" >/dev/null 2>&1 || true
    start_job "export home from dshhome instance" POST /api/packs/export "$(printf '{"instanceId":"%s","mode":"home"}' "$HOME_IID")"
    job_poll "$JOB_ID" 180000
    check "dshhome instance home export SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE error=$(jget "$JOB_JSON" error.code): $(jget "$JOB_JSON" error.message | head -c 140)"
    if [ "$JOB_STATE" = "SUCCEEDED" ]; then
      local home_aid; home_aid=$(jget "$JOB_JSON" result.artifactId)
      API_STATUS=$(curl -sS --noproxy '*' --max-time 60 -b "$COOKIE_JAR" -o "$SCENARIO/export-home.dspack" -w '%{http_code}' "$MNGR/api/artifacts/$home_aid" 2>/dev/null)
      check "home artifact download 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
      local hprev; preview_valid "exported home re-preview" "$SCENARIO/export-home.dspack" "dspack"; hprev="$LAST_PREVIEW_ID"
      check "exported home re-preview 200" "$([ -n "$hprev" ] && echo 1 || echo 0)"
    else
      log "home export failure context (manager log tail):"
      tail -8 "$DATA_ROOT/logs"/manager-*.log 2>/dev/null | san | tee -a "$LOG" || true
      info "dsh-home tree: $(find "$DATA_ROOT/instances/$HOME_IID/dsh-home" -maxdepth 3 2>/dev/null | head -10 | tr '\n' ' ')"
    fi
    req manager DELETE "/api/instances/$HOME_IID" "" >/dev/null 2>&1 || true
    check "dshhome imported instance deleted" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
  fi

  local errs_happy
  errs_happy=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs ERROR count 0 on happy path (before negative tests)" "$([ "$errs_happy" = "0" ] && echo 1 || echo 0)" "errors=$errs_happy"

  # ---- import failure rollback (dependency restore fails: registry -> closed port) ----
  req manager GET /api/settings ""
  check "GET settings 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
  local old_proxy old_registry old_ssh
  old_proxy=$(jget "$API_BODY" proxy); old_registry=$(jget "$API_BODY" registry); old_ssh=$(jget "$API_BODY" directAccessSshUser)
  local settings_body
  settings_body=$(printf '{"proxy":"%s","registry":"http://127.0.0.1:9/","directAccessSshUser":"%s"}' "$old_proxy" "$old_ssh")
  req manager PUT /api/settings "$settings_body"
  check "set registry to closed port for rollback test" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "status=$API_STATUS"
  local failing_preview; preview_valid "failing-import pack preview" "$REPO_ROOT/testdata/pack/import-failure/dspack-failing-deps.dspack" "dspack"; failing_preview="$LAST_PREVIEW_ID"
  check "failing-import pack previews 200 (valid archive)" "$([ -n "$failing_preview" ] && echo 1 || echo 0)"
  instances_before=$(find "$DATA_ROOT/instances" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l)
  trash_before=$(find "$DATA_ROOT/trash" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
  if [ -n "$failing_preview" ]; then
    start_job "failing import" POST /api/packs/import "$(printf '{"previewId":"%s"}' "$failing_preview")"
    local fail_job="$JOB_ID"
    job_poll "$fail_job" 300000
    check "failing import Job FAILED (rollback path)" "$([ "$JOB_STATE" = "FAILED" ] && echo 1 || echo 0)" "state=$JOB_STATE code=$(jget "$JOB_JSON" error.code) message=$(jget "$JOB_JSON" error.message | head -c 120)"
  fi
  instances_after=$(find "$DATA_ROOT/instances" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l)
  local trash_after
  trash_after=$(find "$DATA_ROOT/trash" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
  check "failed import leaves no instance in store" "$([ "$instances_after" = "$instances_before" ] && echo 1 || echo 0)" "instances_before=$instances_before after=$instances_after"
  check "failed import cleaned to trash (rollback)" "$([ "$trash_after" -gt "$trash_before" ] && echo 1 || echo 0)" "trash_before=$trash_before after=$trash_after"
  settings_body=$(printf '{"proxy":"%s","registry":"%s","directAccessSshUser":"%s"}' "$old_proxy" "$old_registry" "$old_ssh")
  req manager PUT /api/settings "$settings_body"
  check "restore settings registry 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"

  # ---- G7 plugins (independent normal instance; negative specs run last on throwaway) ----
  req manager POST /api/instances "$(printf '{"name":"m3-plugin","version":"%s","profile":"web","port":0}' "$VERSION")"
  check "create plugin instance 201" "$([ "$API_STATUS" = "201" ] && echo 1 || echo 0)"
  PLUGIN_ID=$(jget "$API_BODY" id)
  check "plugin instance id" "$([ -n "$PLUGIN_ID" ] && echo 1 || echo 0)" "id=$PLUGIN_ID"
  start_job "plugin instance launch baseline" POST "/api/instances/$PLUGIN_ID/launch" ""
  job_poll "$JOB_ID" 180000
  check "plugin instance launch baseline SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  req manager POST "/api/instances/$PLUGIN_ID/stop" ""
  check "plugin instance stop baseline" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
  req manager POST "/api/instances/$PLUGIN_ID/stop" "" >/dev/null 2>&1 || true
  req manager GET "/api/instances/$PLUGIN_ID" ""
  local inst_path; inst_path=$(jget "$API_BODY" path)
  check "instance path available for plugin file:" "$([ -n "$inst_path" ] && [ -d "$inst_path" ] && echo 1 || echo 0)" "path=$inst_path"
  rm -rf "$inst_path/workspace/local-test-plugin" 2>/dev/null || true
  cp -r "$REPO_ROOT/testdata/plugin/local-test-plugin" "$inst_path/workspace/local-test-plugin" 2>/dev/null || true
  check "local test plugin copied into workspace" "$([ -f "$inst_path/workspace/local-test-plugin/package.json" ] && echo 1 || echo 0)"
  req manager GET "/api/instances/$PLUGIN_ID/plugins?profile=web" ""
  check "plugins list 200 baseline" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "count=$("$NODE" -e 'const a=JSON.parse(require("fs").readFileSync(process.argv[1]));console.log(a.length)' "$API_BODY" 2>/dev/null || echo '?')"
  start_job "plugin add file:./local-test-plugin" POST "/api/instances/$PLUGIN_ID/plugins" '{"spec":"file:./local-test-plugin"}'
  job_poll "$JOB_ID" 300000
  check "plugin add Job SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  req manager GET "/api/instances/$PLUGIN_ID/plugins?profile=web" ""
  check "plugins list contains @pcl-dsh/m3-test-plugin" "$(grep -q '@pcl-dsh/m3-test-plugin' "$API_BODY" && echo 1 || echo 0)" "body=$(san < "$API_BODY" | head -c 300)"
  start_job "launch for plugin activation" POST "/api/instances/$PLUGIN_ID/launch" ""
  job_poll "$JOB_ID" 180000
  check "launch for plugin activation SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  req manager POST "/api/instances/$PLUGIN_ID/stop" ""
  req manager GET "/api/instances/$PLUGIN_ID/plugins?profile=web" ""
  check "plugin still listed after stop/start cycle" "$(grep -q '@pcl-dsh/m3-test-plugin' "$API_BODY" && echo 1 || echo 0)"
  local plugin_path_name="%40pcl-dsh%2Fm3-test-plugin"
  start_job "plugin update" PATCH "/api/instances/$PLUGIN_ID/plugins/$plugin_path_name" ""
  job_poll "$JOB_ID" 300000
  check "plugin update Job SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  req manager GET "/api/instances/$PLUGIN_ID/plugins?profile=web" ""
  check "plugin still listed after update" "$(grep -q '@pcl-dsh/m3-test-plugin' "$API_BODY" && echo 1 || echo 0)"
  start_job "plugin remove" DELETE "/api/instances/$PLUGIN_ID/plugins/$plugin_path_name" ""
  job_poll "$JOB_ID" 300000
  check "plugin remove Job SUCCEEDED" "$([ "$JOB_STATE" = "SUCCEEDED" ] && echo 1 || echo 0)" "state=$JOB_STATE"
  req manager GET "/api/instances/$PLUGIN_ID/plugins?profile=web" ""
  check "plugin removed from list" "$(grep -q '@pcl-dsh/m3-test-plugin' "$API_BODY" && echo 0 || echo 1)"

  # invalid specs: 8 sync 400 VALIDATION (no Job) + 2 instance-dependent -> Job FAILED VALIDATION
  local spec_case spec status2 job2 jobcode jobs_before jobs_after_sync sync_ok sync_total
  sync_ok=0; sync_total=0
  req manager GET /api/jobs ""
  jobs_before=$("$NODE" -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1])).length)' "$API_BODY" 2>/dev/null || echo 0)
  while IFS='|' read -r spec_case action spec; do
    [ -n "$spec_case" ] || continue
    sync_total=$((sync_total + 1))
    local reqbody
    if [ "$spec_case" = "control_char" ]; then
      spec='evil\u0001pkg'
      reqbody='{"spec":"evil\u0001pkg"}'
    else
      reqbody=$(printf '{"spec":"%s"}' "$spec")
    fi
    if [ "$action" = "add" ]; then
      req manager POST "/api/instances/$PLUGIN_ID/plugins" "$reqbody"
    else
      req manager DELETE "/api/instances/$PLUGIN_ID/plugins/$(printf '%s' "$spec" | sed 's|/|%2F|g')" ""
    fi
    status2="$API_STATUS"; jobcode=$(jget "$API_BODY" code)
    [ "$status2" = "400" ] && [ "$jobcode" = "VALIDATION" ] && sync_ok=$((sync_ok + 1))
    log "plugin sync-negative case=$spec_case action=$action status=$status2 code=$jobcode"
  done <<'SPECS'
leading_dash|add|-evil
control_char|add|evil-pkg
spec_too_long|add|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
invalid_npm_name|add|@bad//name
file_escape_workspace|add|file:../../outside
file_absolute|add|file:/etc/passwd
link_escape|add|link:../outside
SPECS
  req manager POST "/api/instances/$PLUGIN_ID/plugins?profile=../evil" '{"spec":"@pcl-dsh/m3-test-plugin"}'
  sync_total=$((sync_total + 1))
  [ "$API_STATUS" = "400" ] && [ "$(jget "$API_BODY" code)" = "VALIDATION" ] && sync_ok=$((sync_ok + 1))
  log "plugin sync-negative case=invalid_profile action=add status=$API_STATUS code=$(jget "$API_BODY" code)"
  check "8 malicious plugin specs/params rejected synchronously 400 VALIDATION" "$([ "$sync_ok" = "$sync_total" ] && [ "$sync_total" -ge 8 ] && echo 1 || echo 0)" "ok=$sync_ok/$sync_total"
  req manager GET /api/jobs ""
  jobs_after_sync=$("$NODE" -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1])).length)' "$API_BODY" 2>/dev/null || echo 0)
  check "sync-rejected plugin specs create no Job" "$([ "$jobs_after_sync" = "$jobs_before" ] && echo 1 || echo 0)" "jobs_before=$jobs_before after=$jobs_after_sync"

  while IFS='|' read -r spec_case action spec; do
    [ -n "$spec_case" ] || continue
    req manager DELETE "/api/instances/$PLUGIN_ID/plugins/$(printf '%s' "$spec" | sed 's|/|%2F|g')" ""
    status2="$API_STATUS"
    if [ "$status2" = "202" ]; then
      job2=$(jget "$API_BODY" id); job_poll "$job2" 120000
      check "instance-dependent plugin negative $spec_case -> Job FAILED VALIDATION" "$([ "$JOB_STATE" = "FAILED" ] && [ "$(jget "$JOB_JSON" error.code)" = "VALIDATION" ] && echo 1 || echo 0)" "state=$JOB_STATE code=$(jget "$JOB_JSON" error.code)"
    else
      check "instance-dependent plugin negative $spec_case -> 202 Job" 0 "status=$status2"
    fi
  done <<'SPECS'
builtin_bundle_remove|remove|@deepseek-ai/dsh-base
remove_not_installed|remove|not-installed-plugin
SPECS
  req manager GET "/api/instances/$PLUGIN_ID/plugins?profile=../evil" ""
  check "invalid plugin profile query -> 400" "$([ "$API_STATUS" = "400" ] && echo 1 || echo 0)" "status=$API_STATUS code=$(jget "$API_BODY" code)"

  # running instance 409 + busy 409
  start_job "launch for running-409" POST "/api/instances/$PLUGIN_ID/launch" ""
  local run_job="$JOB_ID"
  req manager POST "/api/instances/$PLUGIN_ID/plugins" '{"spec":"file:./local-test-plugin"}'
  check "plugin mutate while instance running -> 409" "$([ "$API_STATUS" = "409" ] && echo 1 || echo 0)" "status=$API_STATUS code=$(jget "$API_BODY" code)"
  req manager POST "/api/instances/$PLUGIN_ID/plugins" '{"spec":"file:./local-test-plugin"}'
  check "plugin mutate while Job busy -> 409" "$([ "$API_STATUS" = "409" ] && echo 1 || echo 0)" "status=$API_STATUS code=$(jget "$API_BODY" code)"
  job_poll "$run_job" 180000
  req manager POST "/api/instances/$PLUGIN_ID/stop" ""
  info "pluginManagement=true on real rc.3; 422 capability path covered by server unit tests (not forced here)"
  req manager DELETE "/api/instances/$PLUGIN_ID" ""
  check "plugin instance deleted (cleanup)" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"

  # ---- artifact auth/404 ----
  req manager GET "/api/artifacts/0123456789ab" ""
  err_ok "unknown artifact -> 404 four-field" 404 NOT_FOUND
  local art_saved="$ARTIFACT_ID"
  ARTIFACT_ID=""
  API_STATUS=$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' "$MNGR/api/artifacts/$art_saved" 2>/dev/null)
  check "artifact unauthenticated -> 401" "$([ "$API_STATUS" = "401" ] && echo 1 || echo 0)" "status=$API_STATUS"

  # ---- Nginx chain subset ----
  upload nginx /api/packs/preview "$REPO_ROOT/testdata/pack/valid/dspack-v2-profile.dspack"
  check "via Nginx valid preview 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "status=$API_STATUS"
  upload nginx /api/packs/preview "$REPO_ROOT/testdata/pack/malicious/14_zip_slip_parent.dspack"
  check "via Nginx malicious preview 400" "$([ "$API_STATUS" = "400" ] && echo 1 || echo 0)" "status=$API_STATUS code=$(jget "$API_BODY" code)"
  API_STATUS=$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' -F "file=@$REPO_ROOT/testdata/pack/valid/dspack-v2-profile.dspack" "$BASE/api/packs/preview" 2>/dev/null)
  check "via Nginx unauth preview 401" "$([ "$API_STATUS" = "401" ] && echo 1 || echo 0)" "status=$API_STATUS"
  req nginx GET "/api/instances/$BASE_IID/plugins?profile=web" ""
  check "via Nginx plugins list 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
  API_STATUS=$(curl -sS --noproxy '*' --max-time 60 -b "$COOKIE_JAR" -o "$SCENARIO/art-nginx.dspack" -w '%{http_code}' "$BASE/api/artifacts/$art_saved" 2>/dev/null)
  check "via Nginx artifact download 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)"
  check "Nginx access log has no /api/ lines (access_log off)" "$(grep -q '/api/' "$NGINX_ROOT/logs/access.log" 2>/dev/null && echo 0 || echo 1)"

  # ---- security / platform ----
  local leaks pwleak errs
  leaks=$(grep -rEo 'token=[A-Za-z0-9._~%+/-]{8,}|dsh-auth-[A-Za-z0-9]+=[A-Za-z0-9._~%+/=-]{8,}|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)
  check "manager logs/evidence no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  pwleak=0
  [ -n "$INITIAL_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$INITIAL_PASSWORD" "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)))
  [ -n "$NEW_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$NEW_PASSWORD" "$DATA_ROOT/logs" "$LOG" 2>/dev/null | wc -l)))
  check "passwords never appear in logs/evidence" "$([ "$pwleak" = "0" ] && echo 1 || echo 0)" "matches=$pwleak"
  check "archive contents not echoed into evidence/logs" "$(grep -rEqi 'SYNTHETIC-PRIVATE|root:.*:0:0:' "$DATA_ROOT/logs" "$LOG" 2>/dev/null && echo 0 || echo 1)"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  local errs_unexpected
  errs_unexpected=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | grep -Ev '恢复 profile 依赖失败|整合包导入失败|插件变更失败|job task failed|运行时能力探测失败|rollback' | wc -l)
  check "manager logs have no unexpected ERROR (negative-test failures are expected)" "$([ "$errs_unexpected" = "0" ] && echo 1 || echo 0)" "errors_total=$errs unexpected=$errs_unexpected"
  check "data-root 0700" "$([ "$(file_mode "$DATA_ROOT")" = "700" ] && echo 1 || echo 0)"
  check "config json 0600" "$([ "$(find "$DATA_ROOT/config" -name '*.json' -type f ! -perm 600 2>/dev/null | wc -l)" = "0" ] && echo 1 || echo 0)"
  local etc_after ref_after prog_after
  etc_after=$(find /etc/nginx -type f -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}')
  check "/etc/nginx unchanged" "$([ "$etc_after" = "$etc_before" ] && echo 1 || echo 0)"
  ref_after=$(git -C "$ref_path" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  check "reference repo unchanged (baseline git status hash)" "$([ "$ref_after" = "$ref_before" ] && echo 1 || echo 0)" "before=$(printf '%s' "$ref_before" | cut -c1-16)... after=$(printf '%s' "$ref_after" | cut -c1-16)..."
  prog_after=$(win_dsh_tree_hash "$WIN_DSH_DIR")
  check "Windows DSH program tree unchanged (read-only)" "$([ "$prog_after" = "$WIN_DSH_PROG_BEFORE" ] && echo 1 || echo 0)"

  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS checks_failed=$FAIL runtime_reused=yes scenario=$SCENARIO"
  if [ "$FAIL" -gt 0 ]; then
    printf '%s\n' "$FAILS" | tee -a "$LOG"
    log "RESULT=FAIL"
    return 1
  fi
  log "RESULT=PASS"
  return 0
}

run
exit $?
