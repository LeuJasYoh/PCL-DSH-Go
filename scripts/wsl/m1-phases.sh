#!/usr/bin/env bash
# PCL-DSH-Go M1 | E2E phase implementations for m1-acceptance.sh --full (task-19).
# Sourced by m1-acceptance.sh; uses its globals (LOG/SCENARIO/COOKIE_JAR/api helpers)
# and the Windows-DSH baseline library. Everything is written under
# /tmp/pcl-dsh-go-test/m1-data + docs/evidence/m1; token/cookie/password values are
# never written to evidence (sanitize on every log path).

VERSION_M1="${DSH_VERSION:-0.2.1-rc.3}"
INITIAL_PASSWORD=""
NEW_PASSWORD=""
CSRF_TOKEN=""
MAIN_ID=""
MAIN_PORT=""
OTHER_ID=""
REF_ID=""
PERSIST_ID=""
REF_REPO_BASELINE=""

# ---------- http helpers with header capture ----------
req() { # req <anon|auth> <METHOD> <PATH> [BODY] [nosrf]
  local mode="$1" method="$2" path="$3" body="${4:-}" noc="${5:-}" hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body"
  : > "$hdr"; : > "$bodyf"
  local args=(-sS --max-time 180 -X "$method" -D "$hdr" -o "$bodyf" -w '%{http_code}')
  if [ "$mode" = "auth" ]; then
    args+=(-b "$COOKIE_JAR" -c "$COOKIE_JAR")
    if [ "$noc" != "no-csrf" ] && [ -n "$CSRF_TOKEN" ]; then args+=(-H "X-XSRF-TOKEN: $CSRF_TOKEN"); fi
  fi
  args+=("http://127.0.0.1:$MANAGER_PORT$path")
  if [ -z "$body" ]; then
    API_STATUS=$(curl "${args[@]}" 2>/dev/null)
  else
    API_STATUS=$(printf '%s' "$body" | curl "${args[@]}" -H 'Content-Type: application/json' --data-binary @- 2>/dev/null)
  fi
  API_BODY="$bodyf"; API_HDRS="$hdr"
  log "api $mode $method $path -> $API_STATUS"
}
anon() { req anon "$@"; }
auth() { req auth "$@"; }
authed() { req auth "$@"; }

hdr_get() { # hdr_get <Header-Name> (case-insensitive, first value)
  grep -i "^$1:" "$API_HDRS" 2>/dev/null | head -1 | tr -d '\r' | sed -E "s/^[^:]+:[[:space:]]*//"
}
hdr_all() { grep -i "^$1:" "$API_HDRS" 2>/dev/null | tr -d '\r' | sed -E "s/^[^:]+:[[:space:]]*//" | paste -sd '|' -; }
trace12() { hdr_get X-Trace-Id | grep -Eq '^[0-9a-f]{12}$' && echo 1 || echo 0; }
json_str() { json_get "$API_BODY" "$1" 2>/dev/null || true; }
json_num() { json_str "$1"; }
json_arr_len() { "$NODE" -e 'const fs=require("fs");let v;try{v=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));}catch(e){process.exit(3);}for(const k of process.argv[2].split(".")){if(v==null){process.exit(2);}v=v[k];}process.stdout.write(String(Array.isArray(v)?v.length:0));' "$API_BODY" "$1" 2>/dev/null || echo 0; }
err_ok() { # err_ok <expected-code> [expected-details-endpoint]
  local code="$1" epl="${2:-}" c m d t good=1
  c=$(json_str code); m=$(json_str message); d=$(json_str details); t=$(json_str traceId)
  [ "$c" = "$code" ] || good=0
  [ -n "$m" ] || good=0
  printf '%s' "$d" | grep -q '^{' || good=0
  printf '%s' "$t" | grep -Eq '^[0-9a-f]{12}$' || good=0
  if [ -n "$epl" ]; then printf '%s' "$(json_get "$API_BODY" details.endpoint 2>/dev/null)" | grep -qx "$epl" || good=0; fi
  echo "$good"
}
expect_http() { # expect_http <name> <expected-status> [detail]
  local name="$1" want="$2" detail="${3:-}"
  check "$name" "$([ "$API_STATUS" = "$want" ] && echo 1 || echo 0)" "status=$API_STATUS want=$want $detail"
}
expect_err() { # expect_err <name> <status> <code> [endpoint]
  local name="$1" want="$2" code="$3" ep="${4:-}"
  check "$name" "$([ "$API_STATUS" = "$want" ] && [ "$(err_ok "$code" "$ep")" = "1" ] && echo 1 || echo 0)" "status=$API_STATUS want=$want code=$(json_str code) details=$(json_str details) trace=$(json_str traceId)"
}
jar_cookie() { # jar_cookie <name>
  awk -F '\t' -v n="$1" '$6==n {print $7; exit}' "$COOKIE_JAR" 2>/dev/null
}
session_cookie_header() {
  local s x out
  s="$(jar_cookie JSESSIONID)"; x="$(jar_cookie XSRF-TOKEN)"
  out="JSESSIONID=$s"
  [ -n "$x" ] && out="$out; XSRF-TOKEN=$x"
  printf '%s' "$out"
}
do_login() { # do_login <password> ; logs in with the cookie jar (session fixation defence may drop an older session)
  local pw="$1" body
  body=$(printf '{"password":"%s"}' "$pw")
  API_STATUS=$(printf '%s' "$body" | curl -sS --max-time 30 -D "$SCENARIO/login.hdr" -o "$SCENARIO/login.body" -w '%{http_code}' \
    -b "$COOKIE_JAR" -c "$COOKIE_JAR" -H 'Content-Type: application/json' --data-binary @- \
    "http://127.0.0.1:$MANAGER_PORT/api/auth/login" 2>/dev/null)
  API_BODY="$SCENARIO/login.body"; API_HDRS="$SCENARIO/login.hdr"
  log "api login -> $API_STATUS"
}
anon_login() { # anon_login <password> ; no cookie jar, used for per-IP lockout tests
  local pw="$1" body
  body=$(printf '{"password":"%s"}' "$pw")
  API_STATUS=$(printf '%s' "$body" | curl -sS --max-time 30 -D "$SCENARIO/anon-login.hdr" -o "$SCENARIO/anon-login.body" -w '%{http_code}' \
    -H 'Content-Type: application/json' --data-binary @- \
    "http://127.0.0.1:$MANAGER_PORT/api/auth/login" 2>/dev/null)
  API_BODY="$SCENARIO/anon-login.body"; API_HDRS="$SCENARIO/anon-login.hdr"
  log "api anon-login -> $API_STATUS"
}
refresh_me() {
  auth GET /api/auth/me
  CSRF_TOKEN=$(json_str csrfToken)
}
job_sse() { # job_sse <jobid> <outfile> [last-event-id]
  local jid="$1" out="$2" last="${3:-}" sse_url="http://127.0.0.1:$MANAGER_PORT/api/jobs/$1/events" cookie rc
  cookie="$(session_cookie_header)"
  local args=()
  [ -n "$last" ] && args+=(--last-event-id "$last")
  printf '%s' "$cookie" | "$NODE" "$SCRIPT_DIR/lib/job-sse.mjs" --url "$sse_url" --json-out "$out" "${args[@]}" > "$SCENARIO/job-sse.out" 2>&1
  rc=$?
  log "job-sse $jid rc=$rc: $(san < "$SCENARIO/job-sse.out" | head -6)"
  JOB_SSE_RC="$rc"
  return 0
}
job_field() { # job_field <jsonfile> <dot.path>
  json_get "$1" "$2" 2>/dev/null || true
}
job_field_num() { job_field "$1" "$2"; }
check_job_view() { # check_job_view <label> <jsonfile> <expected-type>
  local label="$1" f="$2" wtype="${3:-}" id type state
  id=$(job_field "$f" id); type=$(job_field "$f" type); state=$(job_field "$f" state)
  check "$label job view fields" "$([ -n "$id" ] && [ -n "$type" ] && [ -n "$state" ] && echo 1 || echo 0)" "id=$id type=$type state=$state"
  if [ -n "$wtype" ]; then check "$label job type=$wtype" "$([ "$type" = "$wtype" ] && echo 1 || echo 0)" "type=$type"; fi
}
sse_ok() { # sse_ok <label> <jsonfile> <expected-terminal-state>
  local label="$1" f="$2" want="${3:-SUCCEEDED}" rc terminal names ping mono
  rc="$JOB_SSE_RC"; terminal=$(job_field "$f" terminalState); names=$(job_field "$f" eventNames); ping=$(job_field "$f" pingCount); mono=$(job_field "$f" idsMonotonic)
  check "$label SSE client ok" "$([ "$rc" = "0" ] && echo 1 || echo 0)" "rc=$rc terminal=$terminal"
  check "$label SSE terminal state=$want" "$([ "$terminal" = "$want" ] && echo 1 || echo 0)" "terminal=$terminal fails=$(job_field "$f" failures)"
  check "$label SSE ids monotonic" "$([ "$mono" = "true" ] && echo 1 || echo 0)" "monotonic=$mono"
  check "$label SSE event names include state" "$(printf '%s' "$names" | grep -q 'state' && echo 1 || echo 0)" "names=$(printf '%s' "$names" | head -c 200)"
  SSE_PING="$ping"; SSE_NAMES="$names"
}
instance_field() { json_get "$API_BODY" "$1" 2>/dev/null || true; }
create_instance() { # create_instance <name> <port> ; sets INSTANCE_ID
  local name="$1" port="$2" body
  body=$(printf '{"name":"%s","version":"%s","profile":"web","port":%s}' "$name" "$VERSION_M1" "$port")
  auth POST /api/instances "$body"
  INSTANCE_ID=$(json_str id)
  INSTANCE_PORT=$(json_str port)
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
port_listening() { ss -ltn 2>/dev/null | grep -q ":$1\b" && echo 1 || echo 0; }
file_mode() { stat -c '%a' "$1" 2>/dev/null || echo ""; }

# ---------- phase 1: anonymous + health + headers ----------
phase_anon() {
  log "--- phase: anonymous/health/headers ---"
  anon GET /api/health
  expect_http "health anonymous 200" 200
  check "health body status=UP" "$(json_str status | grep -qx 'UP' && echo 1 || echo 0)" "body=$(san < "$API_BODY" | head -c 200)"
  check "health version 0.2.0-m1" "$(json_str version | grep -qx '0.2.0-m1' && echo 1 || echo 0)" "version=$(json_str version)"
  check "X-Trace-Id is 12 hex" "$(trace12)" "trace=$(hdr_get X-Trace-Id)"
  check "X-Content-Type-Options nosniff" "$(hdr_get X-Content-Type-Options | grep -qx nosniff && echo 1 || echo 0)"
  check "Referrer-Policy no-referrer" "$(hdr_get Referrer-Policy | grep -qx no-referrer && echo 1 || echo 0)"
  check "API Cache-Control no-store" "$(hdr_get Cache-Control | grep -q 'no-store' && echo 1 || echo 0)" "value=$(hdr_get Cache-Control)"
  anon GET /api/state
  expect_err "anonymous /api/state 401" 401 UNAUTHORIZED
  anon GET /api/instances
  expect_err "anonymous /api/instances 401" 401 UNAUTHORIZED
  anon GET /api/jobs
  expect_err "anonymous /api/jobs 401" 401 UNAUTHORIZED
  anon GET /workbench/i-000000000000/
  expect_err "anonymous /workbench 401" 401 UNAUTHORIZED
  anon POST /api/auth/logout
  expect_http "anonymous logout 204 (no CSRF required)" 204
  anon GET /api/auth/me
  expect_http "anonymous me 200" 200
  check "anonymous me authenticated=false" "$(json_str authenticated | grep -qx false && echo 1 || echo 0)" "authenticated=$(json_str authenticated)"
  check "anonymous me csrfToken 32hex" "$(json_str csrfToken | grep -Eq '^[0-9a-f]{32}$' && echo 1 || echo 0)" "len=$(printf '%s' "$(json_str csrfToken)" | wc -c)"
  check "anonymous me sets XSRF-TOKEN cookie" "$(hdr_all Set-Cookie | grep -q 'XSRF-TOKEN=' && echo 1 || echo 0)" "set-cookie=$(hdr_all Set-Cookie | sed -E 's/=[^;]+/***/g' | head -c 200)"
  anon GET /api/does-not-exist
  expect_err "anonymous unknown API route 401 (fail-closed auth-first)" 401 UNAUTHORIZED
}

# ---------- phase 2: pending password + login + change password ----------
phase_pending_password() {
  log "--- phase: initial password / pending / change password ---"
  INITIAL_PASSWORD=$(cat "$DATA_ROOT/initial-password" 2>/dev/null || true)
  check "initial-password file exists (0600)" "$([ -n "$INITIAL_PASSWORD" ] && [ "$(file_mode "$DATA_ROOT/initial-password")" = "600" ] && echo 1 || echo 0)" "mode=$(file_mode "$DATA_ROOT/initial-password")"
  [ -n "$INITIAL_PASSWORD" ] || fatal "initial password file missing under $DATA_ROOT"
  do_login "$INITIAL_PASSWORD"
  expect_http "login with initial password 204" 204
  local sc
  sc=$(hdr_all Set-Cookie)
  check "login sets JSESSIONID (HttpOnly/SameSite=Lax/Path=/, no Secure)" "$(printf '%s' "$sc" | grep -q 'JSESSIONID=' && printf '%s' "$sc" | grep -qi 'HttpOnly' && printf '%s' "$sc" | grep -qi 'SameSite=Lax' && printf '%s' "$sc" | grep -qi 'Path=/' && ! printf '%s' "$sc" | grep -qi 'Secure' && echo 1 || echo 0)" "cookie=$(printf '%s' "$sc" | sed -E 's/=[^;]+/***/g' | head -c 240)"
  check "login sets XSRF-TOKEN (readable)" "$(printf '%s' "$sc" | grep -q 'XSRF-TOKEN=' && echo 1 || echo 0)" ""
  refresh_me
  check "me authenticated=true after login" "$(json_str authenticated | grep -qx true && echo 1 || echo 0)"
  check "me initialPasswordPending=true" "$(json_str initialPasswordPending | grep -qx true && echo 1 || echo 0)"
  check "me csrfToken matches XSRF cookie" "$([ -n "$CSRF_TOKEN" ] && [ "$CSRF_TOKEN" = "$(jar_cookie XSRF-TOKEN)" ] && echo 1 || echo 0)" "csrf_len=$(printf '%s' "$CSRF_TOKEN" | wc -c)"
  auth GET /api/state
  expect_err "pending: /api/state 403 PASSWORD_CHANGE_REQUIRED" 403 PASSWORD_CHANGE_REQUIRED /api/auth/password
  auth GET /workbench/i-000000000000/
  expect_err "pending: /workbench 403 PASSWORD_CHANGE_REQUIRED" 403 PASSWORD_CHANGE_REQUIRED
  NEW_PASSWORD="M1qa$(head -c 16 /dev/urandom | base64 | tr -d '/+=' | head -c 16)"
  local body
  body=$(printf '{"oldPassword":"%s","newPassword":"%s"}' "$INITIAL_PASSWORD" "$NEW_PASSWORD")
  auth PUT /api/auth/password "$body"
  expect_http "change password 204" 204
  check "initial-password file removed after change" "$([ ! -f "$DATA_ROOT/initial-password" ] && echo 1 || echo 0)"
  refresh_me
  check "me initialPasswordPending=false after change" "$(json_str initialPasswordPending | grep -qx false && echo 1 || echo 0)"
  auth GET /api/state
  expect_http "state 200 after password change" 200
  local state_keys state_missing
  state_keys=$("$NODE" -e 'const o=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"));console.log(Object.keys(o).join(","));' "$API_BODY" 2>/dev/null)
  state_missing=$("$NODE" -e 'const o=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"));const ks=["version","currentInstanceId","instances","runtimes","status","settings","availableVersions","warnings"];console.log(ks.filter(k=>!(k in o)).join(","));' "$API_BODY" 2>/dev/null)
  check "state JSON has required keys" "$([ -z "$state_missing" ] && echo 1 || echo 0)" "keys=$state_keys missing=$state_missing"
}

# ---------- phase 3: settings/csrf/sessions/logout/lockout ----------
phase_security() {
  log "--- phase: settings / CSRF / logout / login lockout ---"
  auth GET /api/settings
  expect_http "settings GET 200" 200
  check "settings defaults (registry/sshUser)" "$(json_str registry | grep -q '^https://registry.npmjs.org' && json_str directAccessSshUser | grep -qx root && echo 1 || echo 0)" "registry=$(json_str registry) sshUser=$(json_str directAccessSshUser)"
  local body
  body=$(printf '{"proxy":"","registry":"%s","directAccessSshUser":"bad user!"}' "$(json_str registry)")
  auth PUT /api/settings "$body"
  expect_err "settings invalid sshUser 400 VALIDATION" 400 VALIDATION
  body=$(printf '{"proxy":"","registry":"%s","directAccessSshUser":"qauser"}' "$(json_str registry)")
  auth PUT /api/settings "$body"
  expect_http "settings valid sshUser 200" 200
  check "settings reflects qauser" "$(json_str directAccessSshUser | grep -qx qauser && echo 1 || echo 0)" "sshUser=$(json_str directAccessSshUser)"
  body=$(printf '{"proxy":"","registry":"%s","directAccessSshUser":"root"}' "$(json_str registry)")
  auth PUT /api/settings "$body"
  expect_http "settings restore root 200" 200
  # CSRF double submit on a write endpoint
  body=$(printf '{"name":"csrf-probe","version":"%s","profile":"web","port":0}' "$VERSION_M1")
  auth POST /api/instances "$body" no-csrf
  expect_err "write without X-XSRF-TOKEN 403 CSRF_INVALID" 403 CSRF_INVALID
  auth POST /api/instances "$body"
  expect_http "write with CSRF 201" 201
  CSRF_PROBE_ID=$(json_str id)
  [ -n "$CSRF_PROBE_ID" ] || fatal "csrf probe instance returned no id"
  # logout semantics
  auth POST /api/auth/logout "" no-csrf
  expect_err "logout with session but no CSRF 403 CSRF_INVALID" 403 CSRF_INVALID
  auth POST /api/auth/logout ""
  expect_http "logout with CSRF 204" 204
  auth GET /api/state
  expect_err "old session invalid after logout 401" 401 UNAUTHORIZED
  do_login "$NEW_PASSWORD"
  expect_http "login with new password 204" 204
  refresh_me
  auth GET /api/does-not-exist
  expect_err "authenticated unknown API route 404" 404 NOT_FOUND
}

# ---------- phase 4: instances CRUD/validation ----------
phase_instances_crud() {
  log "--- phase: instances CRUD / validation / selection ---"
  create_instance "m1-main" 0
  expect_http "create instance 201" 201
  MAIN_ID="$INSTANCE_ID"; MAIN_PORT="$INSTANCE_PORT"
  [ -n "$MAIN_ID" ] || fatal "create main instance returned no id"
  check "created instance fields" "$([ -n "$MAIN_ID" ] && [ "$(json_str version)" = "$VERSION_M1" ] && [ "$(json_str profile)" = "web" ] && [ "$(json_str status)" = "未启动" ] && [ -n "$(json_str path)" ] && echo 1 || echo 0)" "id=$MAIN_ID port=$MAIN_PORT status=$(json_str status)"
  check "instance profiles includes web" "$(json_str profiles | grep -q web && echo 1 || echo 0)" "profiles=$(json_str profiles)"
  auth GET "/api/instances/$MAIN_ID"
  expect_http "get instance 200" 200
  auth POST /api/instances '{"name":"","version":"'"$VERSION_M1"'","profile":"web","port":0}'
  expect_err "empty name 400 VALIDATION" 400 VALIDATION
  auth POST /api/instances '{"name":"bad-version","version":"not-semver","profile":"web","port":0}'
  expect_err "bad version 400 VALIDATION" 400 VALIDATION
  auth POST /api/instances '{"name":"bad-port","version":"'"$VERSION_M1"'","profile":"web","port":99999}'
  expect_err "bad port 400 VALIDATION" 400 VALIDATION
  auth POST /api/instances '{"name":"dup-port","version":"'"$VERSION_M1"'","profile":"web","port":'"$MAIN_PORT"'}'
  expect_err "duplicate port 409 CONFLICT_PORT_BUSY" 409 CONFLICT_PORT_BUSY
  auth PUT "/api/instances/$MAIN_ID" '{"name":"m1-main-renamed"}'
  expect_http "update name 200" 200
  check "name updated" "$(json_str name | grep -qx 'm1-main-renamed' && echo 1 || echo 0)" "name=$(json_str name)"
  auth PUT "/api/instances/$MAIN_ID" '{"version":"0.2.1-rc.2"}'
  expect_err "update version 409 CONFLICT_UNSUPPORTED_CHANGE" 409 CONFLICT_UNSUPPORTED_CHANGE
  auth PUT "/api/instances/$MAIN_ID" '{"profile":"tui"}'
  expect_err "update profile 409 CONFLICT_UNSUPPORTED_CHANGE" 409 CONFLICT_UNSUPPORTED_CHANGE
  auth PUT "/api/instances/$MAIN_ID/selection" ""
  expect_http "select instance 200" 200
  auth GET /api/state
  check "state currentInstanceId persisted" "$([ "$(json_str currentInstanceId)" = "$MAIN_ID" ] && echo 1 || echo 0)" "current=$(json_str currentInstanceId)"
  check "state instances includes main" "$(json_arr_len instances | grep -Eq '^[1-9]' && echo 1 || echo 0)" "instances_len=$(json_arr_len instances)"
  check "state status READY" "$(json_str status.code | grep -qx READY && echo 1 || echo 0)" "status=$(json_str status)"
  create_instance "m1-other" 0
  expect_http "create second instance 201" 201
  OTHER_ID="$INSTANCE_ID"; OTHER_PORT="$INSTANCE_PORT"
  [ -n "$OTHER_ID" ] || fatal "create other instance returned no id"
}

# ---------- phase 5: launch prechecks (before runtime install) ----------
phase_prelaunch() {
  log "--- phase: launch prechecks ---"
  auth POST "/api/instances/$MAIN_ID/launch" ""
  expect_err "launch without runtime 409 INSTANCE_NOT_READY" 409 INSTANCE_NOT_READY
  auth POST "/api/instances/i-000000000000/launch" ""
  expect_err "launch unknown instance 404 NOT_FOUND" 404 NOT_FOUND
  # port busy precheck: allocate instance with port 0, occupy that port, launch.
  create_instance "m1-portbusy" 0
  expect_http "create portbusy instance 201" 201
  PORTBUSY_ID="$INSTANCE_ID"; PORTBUSY_PORT="$INSTANCE_PORT"
  [ -n "$PORTBUSY_ID" ] || fatal "create portbusy instance returned no id"
  "$NODE" -e 'const net=require("net");const s=net.createServer();s.listen(Number(process.argv[1]),"127.0.0.1");setTimeout(function(){process.exit(0);},90000);' "$PORTBUSY_PORT" >/dev/null 2>&1 &
  LISTENER_PID=$!
  sleep 0.5
  check "port listener started for busy test" "$(port_listening "$PORTBUSY_PORT")" "port=$PORTBUSY_PORT"
  auth POST "/api/instances/$PORTBUSY_ID/launch" ""
  expect_err "launch port busy 409 CONFLICT_PORT_BUSY" 409 CONFLICT_PORT_BUSY
  kill "$LISTENER_PID" 2>/dev/null || true
  sleep 0.3
}

# ---------- phase 6: runtime cancel + real install + probe (Jobs/SSE) ----------
phase_runtime_jobs() {
  log "--- phase: runtime install cancel / real install / probe (Jobs+SSE) ---"
  local body jobid cancel_status
  body=$(printf '{"version":"%s"}' "$VERSION_M1")
  auth POST /api/runtimes "$body"
  expect_http "install runtime 202" 202
  jobid=$(json_str id)
  check_job_view "install(cancel)" "$API_BODY" INSTALL_RUNTIME
  check "job id format j-12hex" "$(printf '%s' "$jobid" | grep -Eq '^j-[0-9a-f]{12}$' && echo 1 || echo 0)" "id=$jobid"
  local job_missing
  job_missing=$(for k in id type state phase message createdAt lastSeq; do
    case "$k" in
      createdAt|lastSeq) json_get "$API_BODY" "$k" >/dev/null 2>&1 || echo "$k";;
      *) [ -n "$(json_str "$k")" ] || echo "$k";;
    esac
  done)
  check "job view fields complete" "$([ -z "$job_missing" ] && echo 1 || echo 0)" "state=$(json_str state) missing=$job_missing"
  auth POST "/api/jobs/$jobid/cancel" ""
  cancel_status="$API_STATUS"
  check "cancel running install 202" "$([ "$cancel_status" = "202" ] && echo 1 || echo 0)" "status=$cancel_status"
  job_sse "$jobid" "$SCENARIO/job-cancel.json"
  sse_ok "install cancelled" "$SCENARIO/job-cancel.json" CANCELLED
  auth GET "/api/jobs/$jobid"
  expect_http "cancelled job GET 200" 200
  check "job terminal CANCELLED" "$(json_str state | grep -qx CANCELLED && echo 1 || echo 0)" "state=$(json_str state)"
  auth GET /api/runtimes
  expect_http "runtimes list after cancel 200" 200
  check "cancelled install left no runtime" "$(json_arr_len "" | grep -qx 0 && echo 1 || echo 0)" "len=$(json_arr_len "")"
  local staging
  staging=$(find "$DATA_ROOT/runtimes" -maxdepth 1 -name '.install-*' -type d 2>/dev/null | wc -l)
  info "cancelled install staging dirs kept for diagnosis: $staging (design allows)"

  # real install
  body=$(printf '{"version":"%s"}' "$VERSION_M1")
  auth POST /api/runtimes "$body"
  expect_http "real install runtime 202" 202
  jobid=$(json_str id)
  check_job_view "install(real)" "$API_BODY" INSTALL_RUNTIME
  body=$(printf '{"proxy":"","registry":"https://registry.npmjs.org","directAccessSshUser":"root"}')
  auth PUT /api/settings "$body"
  expect_err "busy: settings write during install 409 CONFLICT_BUSY" 409 CONFLICT_BUSY
  auth GET /api/state
  check "state status RUNNING during job" "$(json_str status.code | grep -qx RUNNING && echo 1 || echo 0)" "status=$(json_str status)"
  job_sse "$jobid" "$SCENARIO/job-install.json"
  sse_ok "install job" "$SCENARIO/job-install.json" SUCCEEDED
  check "install SSE has log events" "$(printf '%s' "$SSE_NAMES" | grep -q 'log' && echo 1 || echo 0)" "names=$(printf '%s' "$SSE_NAMES" | head -c 260)"
  check "install SSE heartbeat ping observed" "$([ -n "$SSE_PING" ] && [ "$SSE_PING" -ge 1 ] && echo 1 || echo 0)" "pings=$SSE_PING"
  check "install result version+installed" "$([ "$(job_field "$SCENARIO/job-install.json" resultPayload.result.version)" = "$VERSION_M1" ] && [ "$(job_field "$SCENARIO/job-install.json" resultPayload.result.installed)" = "true" ] && echo 1 || echo 0)" "result=$(san < "$SCENARIO/job-install.json" | grep -o '\"result\":{.*}' | head -c 200)"
  check "install result compatibility=unverified" "$(job_field "$SCENARIO/job-install.json" resultPayload.result.compatibility | grep -qx unverified && echo 1 || echo 0)" "compat=$(job_field "$SCENARIO/job-install.json" resultPayload.result.compatibility)"
  auth GET /api/runtimes
  expect_http "runtimes list after install 200" 200
  check "runtime installed unverified before probe" "$(json_arr_len "" | grep -qx 1 && json_str 0.compatibility | grep -qx unverified && echo 1 || echo 0)" "body=$(san < "$API_BODY" | head -c 220)"
  check "runtime entry file exists" "$([ -f "$DATA_ROOT/runtimes/$VERSION_M1/node_modules/@deepseek-ai/dsh/lib/bin.js" ] && echo 1 || echo 0)"
  info "runtime size: $(du -sh "$DATA_ROOT/runtimes/$VERSION_M1" 2>/dev/null | awk '{print $1}')"

  # probe job -> verified
  auth POST "/api/runtimes/$VERSION_M1/probe" ""
  expect_http "probe runtime 202" 202
  jobid=$(json_str id)
  check_job_view "probe" "$API_BODY" PROBE_RUNTIME
  job_sse "$jobid" "$SCENARIO/job-probe.json"
  sse_ok "probe job" "$SCENARIO/job-probe.json" SUCCEEDED
  check "probe result compatibility=verified" "$(job_field "$SCENARIO/job-probe.json" resultPayload.result.compatibility | grep -qx verified && echo 1 || echo 0)" "compat=$(job_field "$SCENARIO/job-probe.json" resultPayload.result.compatibility)"
  auth GET /api/runtimes
  check "runtimes verified after probe" "$(json_str 0.compatibility | grep -qx verified && echo 1 || echo 0)" "body=$(san < "$API_BODY" | head -c 200)"
  auth GET /api/jobs
  expect_http "jobs list 200" 200
  check "jobs list has recent jobs" "$(json_arr_len "" | grep -Eq '^[3-9]|^[1-9][0-9]' && echo 1 || echo 0)" "len=$(json_arr_len "")"
  # Last-Event-ID replay on the finished install job
  job_sse "$jobid" "$SCENARIO/job-replay.json" 0
  check "SSE Last-Event-ID replay on finished job" "$([ "$JOB_SSE_RC" = "0" ] && [ "$(job_field "$SCENARIO/job-replay.json" terminalState)" = "SUCCEEDED" ] && echo 1 || echo 0)" "events=$(job_field "$SCENARIO/job-replay.json" events | head -c 80)"
  # 405 / 413 / error shapes
  auth DELETE /api/state
  expect_err "method not allowed 405 METHOD_NOT_ALLOWED" 405 METHOD_NOT_ALLOWED
  local big="$SCENARIO/big.json"
  ( printf '{"name":"'; head -c 1100000 /dev/zero | tr '\0' 'a'; printf '"}') > "$big"
  req_file auth POST /api/instances "$big"
  expect_err "oversized JSON body 413 PAYLOAD_TOO_LARGE" 413 PAYLOAD_TOO_LARGE
}

# ---------- phase 7: launch + workbench + direct-access ----------
phase_launch_workbench() {
  log "--- phase: launch / workbench proxy / direct-access ---"
  local jobid dsh_cookie token_url page_status mgr_cookie
  auth POST "/api/instances/$MAIN_ID/launch" ""
  expect_http "launch main 202" 202
  jobid=$(json_str id)
  check_job_view "launch" "$API_BODY" LAUNCH_INSTANCE
  job_sse "$jobid" "$SCENARIO/job-launch.json"
  sse_ok "launch job" "$SCENARIO/job-launch.json" SUCCEEDED
  check "launch result instanceId/webUrl" "$([ "$(job_field "$SCENARIO/job-launch.json" resultPayload.result.instanceId)" = "$MAIN_ID" ] && [ "$(job_field "$SCENARIO/job-launch.json" resultPayload.result.webUrl)" = "/workbench/$MAIN_ID/" ] && echo 1 || echo 0)" "result=$(san < "$SCENARIO/job-launch.json" | grep -o '\"result\":{.*}' | head -c 160)"
  auth GET "/api/instances/$MAIN_ID"
  check "instance status 运行中" "$(json_str status | grep -q '运行中' && echo 1 || echo 0)" "status=$(json_str status)"
  check "DSH port listening" "$(port_listening "$MAIN_PORT")" "port=$MAIN_PORT"
  auth GET "/api/instances/$MAIN_ID/web-url"
  expect_http "web-url 200" 200
  check "web-url returns console path" "$(json_str url | grep -qx "/workbench/$MAIN_ID/" && echo 1 || echo 0)" "url=$(json_str url)"
  auth POST "/api/instances/$MAIN_ID/launch" ""
  expect_err "launch already-running instance 409 CONFLICT_BUSY" 409 CONFLICT_BUSY
  auth POST "/api/instances/$OTHER_ID/launch" ""
  expect_err "launch second instance 409 CONFLICT_RUNNING_INSTANCE" 409 CONFLICT_RUNNING_INSTANCE
  auth PUT "/api/instances/$MAIN_ID" '{"port":40001}'
  expect_err "update running port 409 CONFLICT_INSTANCE_RUNNING" 409 CONFLICT_INSTANCE_RUNNING
  auth PUT "/api/instances/$MAIN_ID" '{"name":"m1-main-running"}'
  expect_http "update name while running 200" 200
  # login gate
  req anon GET "/workbench/$MAIN_ID/"
  expect_err "workbench anonymous 401" 401 UNAUTHORIZED
  auth GET "/workbench/$MAIN_ID/"
  expect_http "workbench logged-in 302" 302
  local loc
  loc=$(hdr_get Location)
  check "workbench 302 carries bootstrap token" "$(printf '%s' "$loc" | grep -q "token=" && echo 1 || echo 0)" "location=$(printf '%s' "$loc" | san)"
  token_url="$loc"
  case "$token_url" in http*) ;; *) token_url="http://127.0.0.1:$MANAGER_PORT$token_url";; esac
  local bs="$SCENARIO/wb-bootstrap.hdr"
  API_STATUS=$(curl -sS --max-time 20 -D "$bs" -o /dev/null -w '%{http_code}' -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$token_url" 2>/dev/null)
  check "DSH bootstrap 303 through proxy" "$([ "$API_STATUS" = "303" ] && echo 1 || echo 0)" "status=$API_STATUS"
  check "DSH Location ./ preserved" "$(grep -qi '^location: \./' "$bs" && echo 1 || echo 0)" "location=$(grep -i '^location:' "$bs" | head -1 | san)"
  check "Set-Cookie rewritten Path=/workbench/id" "$(grep -i '^set-cookie:' "$bs" | grep -qi "Path=/workbench/$MAIN_ID" && echo 1 || echo 0)" "cookie=$(grep -i '^set-cookie:' "$bs" | head -1 | sed -E 's/=[^;]+/***/g' | head -c 200)"
  check "Set-Cookie no Domain/Secure" "$(grep -i '^set-cookie:' "$bs" | grep -qiE 'Domain=|; *Secure' && echo 0 || echo 1)"
  dsh_cookie=$(grep -i '^set-cookie:' "$bs" | head -1 | sed -E 's/^[Ss]et-[Cc]ookie: ([^;]+);.*/\1/')
  mgr_cookie=$(session_cookie_header)
  page_status=$(curl -sS -o "$SCENARIO/wb-page.html" -w '%{http_code}' --max-time 30 -H "Cookie: $mgr_cookie; $dsh_cookie" "http://127.0.0.1:$MANAGER_PORT/workbench/$MAIN_ID/")
  check "workbench page 200" "$([ "$page_status" = "200" ] && echo 1 || echo 0)" "status=$page_status"
  check "workbench title DeepSeek Harness" "$(grep -qi '<title>DeepSeek Harness</title>' "$SCENARIO/wb-page.html" && echo 1 || echo 0)"
  check "workbench base href=./" "$(grep -qi '<base[^>]*href="./"' "$SCENARIO/wb-page.html" && echo 1 || echo 0)"
  local asset
  asset=$(grep -Eo '(src|href)="\./assets/[^"]+\.js"' "$SCENARIO/wb-page.html" | head -1 | sed -E 's/.*="\.\///; s/"$//')
  if [ -n "$asset" ]; then
    API_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 -H "Cookie: $mgr_cookie; $dsh_cookie" "http://127.0.0.1:$MANAGER_PORT/workbench/$MAIN_ID/$asset")
    check "workbench static asset 200" "$([ "$API_STATUS" = "200" ] && echo 1 || echo 0)" "asset=$asset status=$API_STATUS"
  else
    check "workbench static asset referenced" 0
  fi
  # SSE through proxy
  local sse_out
  sse_out=$(printf '%s' "$mgr_cookie; $dsh_cookie" | "$NODE" "$SCRIPT_DIR/lib/sse-e2e.mjs" --url "http://127.0.0.1:$MANAGER_PORT/workbench/$MAIN_ID/plugins/events" --timeout 4000 2>&1)
  log "proxy sse-e2e: $(printf '%s\n' "$sse_out" | san | head -4)"
  check "workbench SSE streaming through proxy" "$(printf '%s' "$sse_out" | grep -q '\"connectedComment\":true' && echo 1 || echo 0)" "$(printf '%s' "$sse_out" | head -2)"
  # WS through proxy
  local ws_out
  ws_out=$(printf '%s' "$mgr_cookie; $dsh_cookie" | "$NODE" "$SCRIPT_DIR/lib/ws-e2e.mjs" --url "ws://127.0.0.1:$MANAGER_PORT/workbench/$MAIN_ID/api/remote.mux" --origin "http://127.0.0.1:$MANAGER_PORT" --timeout 15000 2>&1)
  log "proxy ws-e2e: $(printf '%s\n' "$ws_out" | san | head -5)"
  check "workbench WS 101 through proxy" "$(printf '%s' "$ws_out" | grep -q 'upgraded=true' && echo 1 || echo 0)" "status=$(printf '%s' "$ws_out" | sed -n 's/^status=\([0-9]*\).*/\1/p')"
  check "workbench WS ready frame through proxy" "$(printf '%s' "$ws_out" | grep -q 'item' && echo 1 || echo 0)"
  check "workbench WS close 1000 through proxy" "$(printf '%s' "$ws_out" | grep -q 'close_code=1000' && echo 1 || echo 0)"
  # direct-access while running
  auth GET "/api/instances/$MAIN_ID/direct-access"
  expect_http "direct-access running 200" 200
  check "direct-access ready=true/port" "$(json_str ready | grep -qx true && [ "$(json_str port)" = "$MAIN_PORT" ] && echo 1 || echo 0)" "ready=$(json_str ready) port=$(json_str port)"
  check "direct-access tunnelCommand" "$(json_str tunnelCommand | grep -qx "ssh -L $MAIN_PORT:127.0.0.1:$MAIN_PORT root@127.0.0.1" && echo 1 || echo 0)" "cmd=$(json_str tunnelCommand)"
  check "direct-access sshHost from Host" "$(json_str sshHost | grep -qx 127.0.0.1 && echo 1 || echo 0)" "sshHost=$(json_str sshHost)"
  check "direct-access sshUser from settings" "$(json_str sshUser | grep -qx root && echo 1 || echo 0)" "sshUser=$(json_str sshUser)"
  check "direct-access directUrl has token (masked in log)" "$(json_str directUrl | grep -q 'token=' && echo 1 || echo 0)" "directUrl=$(json_str directUrl | san)"
  check "direct-access Cache-Control no-store" "$(hdr_get Cache-Control | grep -q no-store && echo 1 || echo 0)" "value=$(hdr_get Cache-Control)"
  req anon GET "/api/instances/$MAIN_ID/direct-access"
  expect_err "direct-access anonymous 401" 401 UNAUTHORIZED
  auth GET "/api/instances/i-000000000000/direct-access"
  expect_err "direct-access unknown 404" 404 NOT_FOUND
}

# json_arr_len override: empty path means the root value.
json_arr_len() {
  "$NODE" -e '
const fs=require("fs");let v;
try{v=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));}catch(e){process.exit(3);}
const p=process.argv[2]||"";
if(p!==""){for(const k of p.split(".")){if(v==null){process.exit(2);}v=v[k];}}
process.stdout.write(String(Array.isArray(v)?v.length:0));
' "$API_BODY" "${1:-}" 2>/dev/null || echo 0
}
req_file() { # req_file <anon|auth> <METHOD> <PATH> <file>
  local mode="$1" method="$2" path="$3" file="$4" hdr="$SCENARIO/last.hdr" bodyf="$SCENARIO/last.body"
  : > "$hdr"; : > "$bodyf"
  local args=(-sS --max-time 180 -X "$method" -D "$hdr" -o "$bodyf" -w '%{http_code}' -H 'Content-Type: application/json' --data-binary "@$file")
  if [ "$mode" = "auth" ]; then
    args+=(-b "$COOKIE_JAR" -c "$COOKIE_JAR")
    [ -n "$CSRF_TOKEN" ] && args+=(-H "X-XSRF-TOKEN: $CSRF_TOKEN")
  fi
  args+=("http://127.0.0.1:$MANAGER_PORT$path")
  API_STATUS=$(curl "${args[@]}" 2>/dev/null)
  API_BODY="$bodyf"; API_HDRS="$hdr"
  log "api $mode $method $path(file) -> $API_STATUS"
}

# ---------- phase 8: logs/tail, stop, delete, runtime protection, restart ----------
phase_stop_delete_restart() {
  log "--- phase: logs/tail / stop / delete / runtime protection / restart ---"
  local lines_len
  auth GET "/api/logs/tail?lines=50"
  expect_http "logs/tail 200" 200
  lines_len=$(json_arr_len lines)
  check "logs/tail returns sanitized lines" "$([ "$lines_len" -gt 0 ] && echo 1 || echo 0)" "lines=$lines_len"
  check "logs/tail has no raw token/cookie" "$(json_str lines | grep -Eo 'token=[^*"[:space:]]|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' | head -1 | grep -q . && echo 0 || echo 1)"
  auth GET "/api/logs/tail?lines=0"
  expect_err "logs/tail lines=0 400 VALIDATION" 400 VALIDATION
  auth GET "/api/logs/tail?lines=abc"
  expect_err "logs/tail lines=abc 400 VALIDATION" 400 VALIDATION
  auth GET "/api/logs/tail?lines=99999"
  expect_err "logs/tail lines=99999 400 VALIDATION" 400 VALIDATION

  auth POST "/api/instances/$MAIN_ID/stop" ""
  expect_http "stop instance 200" 200
  check "stopped status 未启动" "$(json_str status | grep -q '未启动' && echo 1 || echo 0)" "status=$(json_str status)"
  wait_port_free "$MAIN_PORT" 40 && check "DSH port released after stop" 1 || check "DSH port released after stop" 0
  local procs
  procs=$(pgrep -af 'dsh/lib/bin.js' 2>/dev/null | grep -v pgrep | grep -v m1-acceptance | wc -l)
  check "no DSH process left after stop" "$([ "$procs" = "0" ] && echo 1 || echo 0)" "count=$procs"
  auth GET "/api/instances/$MAIN_ID/direct-access"
  expect_http "direct-access stopped 200" 200
  check "direct-access stopped ready=false" "$(json_str ready | grep -qx false && echo 1 || echo 0)" "ready=$(json_str ready) note=$(json_str note | head -c 80)"
  auth GET "/api/instances/$MAIN_ID/web-url"
  expect_err "web-url stopped 409 INSTANCE_NOT_READY" 409 INSTANCE_NOT_READY
  auth GET "/workbench/$MAIN_ID/"
  check "workbench stopped 409 INSTANCE_NOT_READY" "$([ "$API_STATUS" = "409" ] && grep -q 'INSTANCE_NOT_READY' "$API_BODY" && echo 1 || echo 0)" "status=$API_STATUS body=$(san < "$API_BODY" | head -c 160)"

  # delete instances (never-started delete must return non-empty trashedPath: task-22)
  auth DELETE "/api/instances/$MAIN_ID"
  expect_http "delete running-stopped instance 200" 200
  check "delete returns non-empty trashedPath in trash" "$(json_str trashedPath | grep -q '/trash/' && echo 1 || echo 0)" "trashed=$(json_str trashedPath | san)"
  TRASHED_MAIN=$(json_str trashedPath)
  check "trashed instance dir exists" "$([ -d "$TRASHED_MAIN" ] && echo 1 || echo 0)" "path=$TRASHED_MAIN"
  auth GET "/api/instances/$MAIN_ID"
  expect_err "deleted instance GET 404" 404 NOT_FOUND
  auth DELETE "/api/instances/$CSRF_PROBE_ID"
  expect_http "delete never-started instance 200" 200
  check "never-started delete non-empty trashedPath (task-22)" "$([ -n "$(json_str trashedPath)" ] && echo 1 || echo 0)" "trashed=$(json_str trashedPath | san)"
  auth DELETE "/api/instances/$OTHER_ID"
  expect_http "delete other instance 200" 200
  auth DELETE "/api/instances/$PORTBUSY_ID"
  expect_http "delete portbusy instance 200" 200

  # runtime delete: reference protection then trash
  create_instance "m1-ref" 0
  expect_http "create runtime-referencing instance 201" 201
  REF_ID="$INSTANCE_ID"
  [ -n "$REF_ID" ] || fatal "create ref instance returned no id"
  auth DELETE "/api/runtimes/$VERSION_M1"
  expect_err "delete referenced runtime 409 CONFLICT_IN_USE" 409 CONFLICT_IN_USE
  auth DELETE "/api/instances/$REF_ID"
  expect_http "delete reference instance 200" 200
  auth DELETE "/api/runtimes/$VERSION_M1"
  expect_http "delete unreferenced runtime 200" 200
  check "runtime delete returns trash path" "$(json_str trashedPath | grep -q '/trash/' && echo 1 || echo 0)" "trashed=$(json_str trashedPath | san)"
  auth GET /api/runtimes
  check "runtime list empty after delete" "$(json_arr_len "" | grep -qx 0 && echo 1 || echo 0)" "len=$(json_arr_len "")"
  auth DELETE "/api/runtimes/$VERSION_M1"
  expect_err "delete missing runtime 404 NOT_FOUND" 404 NOT_FOUND

  # selection persistence across manager restart
  create_instance "m1-persist" 0
  expect_http "create persistence instance 201" 201
  PERSIST_ID="$INSTANCE_ID"
  [ -n "$PERSIST_ID" ] || fatal "create persistence instance returned no id"
  auth PUT "/api/instances/$PERSIST_ID/selection" ""
  expect_http "select persistence instance 200" 200
  auth GET /api/state
  check "selection before restart" "$([ "$(json_str currentInstanceId)" = "$PERSIST_ID" ] && echo 1 || echo 0)" "current=$(json_str currentInstanceId)"
  stop_manager
  check "manager port released before restart" "$(wait_port_free "$MANAGER_PORT" 30 && echo 1 || echo 0)"
  start_manager
  auth GET /api/state
  expect_err "old session invalid after restart 401" 401 UNAUTHORIZED
  do_login "$NEW_PASSWORD"
  expect_http "login after restart with new password 204" 204
  refresh_me
  auth GET /api/state
  expect_http "state after restart 200" 200
  check "selection persisted across restart" "$([ "$(json_str currentInstanceId)" = "$PERSIST_ID" ] && echo 1 || echo 0)" "current=$(json_str currentInstanceId)"
  auth GET /api/jobs
  check "job history empty after restart (in-memory design)" "$(json_arr_len "" | grep -qx 0 && echo 1 || echo 0)" "len=$(json_arr_len "")"
  auth DELETE "/api/instances/$PERSIST_ID"
  expect_http "delete persistence instance 200" 200
  auth GET "/api/instances/i-000000000000"
  expect_err "error body four fields (404 NOT_FOUND)" 404 NOT_FOUND
}

# ---------- phase 8.5: login lockout (runs last; per-IP limiter) ----------
phase_lockout() {
  log "--- phase: login lockout (last; per-IP limiter) ---"
  local attempt=0 locked=0
  anon GET /api/state
  expect_err "lockout phase anonymous precheck 401" 401 UNAUTHORIZED
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    anon_login "wrong-password-$attempt"
    if [ "$API_STATUS" = "423" ]; then
      locked=1
      check "login lockout 423 LOGIN_LOCKED" "$([ "$(json_str code)" = "LOGIN_LOCKED" ] && [ -n "$(json_get "$API_BODY" details.retryAfterSeconds 2>/dev/null)" ] && echo 1 || echo 0)" "attempt=$attempt details=$(json_str details)"
      break
    fi
  done
  [ "$locked" = "1" ] || check "login lockout 423 LOGIN_LOCKED" 0 "not reached after 10 attempts"
  anon_login "$NEW_PASSWORD"
  expect_err "correct password while locked 423 LOGIN_LOCKED" 423 LOGIN_LOCKED
  info "note: per-IP limiter intentionally left locked; this phase runs last and cleanup stops the manager"
}

# ---------- phase 9: security / permissions / Windows DSH / final cleanup ----------
phase_cleanup_final() {
  log "--- phase: permissions / secrets / Windows DSH / reference repo ---"
  check "data-root mode 0700" "$([ "$(file_mode "$DATA_ROOT")" = "700" ] && echo 1 || echo 0)" "mode=$(file_mode "$DATA_ROOT")"
  local cfg_total cfg_bad
  cfg_total=$(find "$DATA_ROOT/config" -name '*.json' -type f 2>/dev/null | wc -l)
  cfg_bad=$(find "$DATA_ROOT/config" -name '*.json' -type f ! -perm 600 2>/dev/null | wc -l)
  check "config json files 0600" "$([ "$cfg_total" -gt 0 ] && [ "$cfg_bad" = "0" ] && echo 1 || echo 0)" "files=$cfg_total non600=$cfg_bad"
  check "initial-password absent after change" "$([ ! -f "$DATA_ROOT/initial-password" ] && echo 1 || echo 0)"
  # drop raw request artifacts before scanning: headers/bodies/cookie jar may contain session secrets
  rm -f "$SCENARIO"/*.hdr "$SCENARIO"/*.body "$SCENARIO"/anon-login.* 2>/dev/null || true
  rm -f "$COOKIE_JAR" 2>/dev/null || true
  local leaks pwleak errs
  leaks=$(grep -rEo 'token=[^*"[:space:]]|dsh-auth-[A-Za-z0-9]+=[^*]|JSESSIONID=[A-Za-z0-9-]{10,}|XSRF-TOKEN=[A-Za-z0-9-]{10,}' "$DATA_ROOT/logs" "$LOG" "$LOG.txt" 2>/dev/null | wc -l)
  check "logs/evidence no raw token/cookie" "$([ "$leaks" = "0" ] && echo 1 || echo 0)" "matches=$leaks"
  pwleak=0
  [ -n "$INITIAL_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$INITIAL_PASSWORD" "$DATA_ROOT/logs" "$LOG" "$LOG.txt" 2>/dev/null | wc -l)))
  [ -n "$NEW_PASSWORD" ] && pwleak=$((pwleak + $(grep -rF "$NEW_PASSWORD" "$DATA_ROOT/logs" "$LOG" "$LOG.txt" 2>/dev/null | wc -l)))
  check "passwords never appear in logs/evidence" "$([ "$pwleak" = "0" ] && echo 1 || echo 0)" "matches=$pwleak"
  errs=$(grep -rh '"level":"ERROR"' "$DATA_ROOT/logs" 2>/dev/null | wc -l)
  check "manager logs ERROR count 0" "$([ "$errs" = "0" ] && echo 1 || echo 0)" "errors=$errs"
  local staging
  staging=$(find "$DATA_ROOT/runtimes" -maxdepth 1 -name '.install-*' -type d 2>/dev/null | wc -l)
  info "install staging dirs kept (cancelled install diagnosis allowed): $staging"
  stop_manager
  check "manager port released at end" "$(wait_port_free "$MANAGER_PORT" 30 && echo 1 || echo 0)"
  check "DSH port released at end" "$(wait_port_free "$DSH_PORT" 20 && echo 1 || echo 0)"
  local procs
  procs=$(pgrep -af 'dsh/lib/bin.js' 2>/dev/null | grep -v pgrep | grep -v m1-acceptance | wc -l)
  check "no DSH process at end" "$([ "$procs" = "0" ] && echo 1 || echo 0)" "count=$procs"
  local prog_after
  prog_after=$(win_dsh_tree_hash "$WIN_DSH_DIR")
  check "Windows DSH program tree unchanged (read-only hash)" "$([ "$prog_after" = "$WIN_DSH_PROG_BEFORE" ] && echo 1 || echo 0)" "before=$(printf '%s' "$WIN_DSH_PROG_BEFORE" | cut -c1-16)... after=$(printf '%s' "$prog_after" | cut -c1-16)..."
  local user_after
  user_after=$(win_dsh_tree_hash "$WIN_USER_DSH")
  if [ "$user_after" = "$WIN_DSH_USER_BEFORE" ]; then
    info "Windows user .dsh unchanged (read-only traversal)"
  else
    info "Windows user .dsh differs due to the running Windows DSH's own writes (read-only traversal by acceptance)"
  fi
  local ref_after
  ref_after=$(git -C "$REF_REPO_PATH" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  check "reference repo unchanged (baseline git status hash)" "$([ "$ref_after" = "$REF_REPO_BASELINE" ] && echo 1 || echo 0)" "before=$(printf '%s' "$REF_REPO_BASELINE" | cut -c1-16)... after=$(printf '%s' "$ref_after" | cut -c1-16)..."
}

# ---------- orchestrator ----------
run_m1_full() {
  mkdir -p "$EVIDENCE_DIR" "$SCENARIO"
  log "=== PCL-DSH-Go M1 full acceptance $RUN_STAMP ==="
  log "repo=$REPO_ROOT root=$ROOT data_root=$DATA_ROOT manager_port=$MANAGER_PORT dsh_port=$DSH_PORT version=$VERSION_M1"
  REF_REPO_PATH="${DSH_REF_REPO:-/mnt/d/Projects/DSHL/PCL-DSH-Linux}"
  baseline_capture
  REF_REPO_BASELINE=$(git -C "$REF_REPO_PATH" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
  log "reference repo git status baseline hash=$(printf '%s' "$REF_REPO_BASELINE" | cut -c1-16)..."
  build_linux_manager
  init_data_root
  preflight_ports
  start_manager
  phase_anon
  phase_pending_password
  phase_security
  phase_instances_crud
  phase_prelaunch
  phase_runtime_jobs
  phase_launch_workbench
  phase_stop_delete_restart
  phase_lockout
  phase_cleanup_final
  log ""
  log "=== RESULT ==="
  log "checks_passed=$PASS_COUNT checks_failed=$FAIL_COUNT scenario=$SCENARIO"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '%s\n' "$FAIL_LINES" | tee -a "$LOG"
    log "RESULT=FAIL"
    return 1
  fi
  log "RESULT=PASS"
  return 0
}
