#!/usr/bin/env bash
# PCL-DSH-Go M0-5 recon: real DSH 0.2.1-rc.3 in WSL.
#
# Writes ONLY under /tmp/pcl-dsh-go-test/** (runtime/temp data) plus, in the report
# phase, sanitized evidence into <repo>/docs/evidence/m0/. Windows DSH and /opt are
# never modified. Tokens/cookies are masked before any log/evidence write.
#
# Usage (in WSL):
#   bash scripts/wsl/recon-rc3.sh [env|registry|install|probe|launch|report|all]
# Env overrides: PCL_DSH_TEST_ROOT, DSH_VERSION, DSH_REGISTRY, DSH_PORT,
#                PCL_DSH_NODE, PCL_DSH_PNPM
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT="${PCL_DSH_TEST_ROOT:-/tmp/pcl-dsh-go-test}"
VERSION="${DSH_VERSION:-0.2.1-rc.3}"
REGISTRY="${DSH_REGISTRY:-https://registry.npmjs.org}"
NODE="${PCL_DSH_NODE:-/opt/pcl-dsh-go/tools/node/bin/node}"
PNPM="${PCL_DSH_PNPM:-/opt/pcl-dsh-go/tools/pnpm/node_modules/pnpm/bin/pnpm.cjs}"
PORT="${DSH_PORT:-3080}"
LOG_DIR="$ROOT/logs"
META_DIR="$ROOT/meta"
RUNTIME_DIR="$ROOT/runtimes/$VERSION"
LIB_DIR="$SCRIPT_DIR/lib"
EVIDENCE_DIR="$REPO_ROOT/docs/evidence/m0"
PHASE="${1:-all}"

mkdir -p "$LOG_DIR" "$META_DIR" "$ROOT/runtimes" "$ROOT/cache/pnpm-store" "$ROOT/cache/npm" \
         "$ROOT/cache/probes" "$ROOT/tools/shims" "$ROOT/work"

log() { printf '%s\n' "$*"; }
now_ms() { local t="${EPOCHREALTIME/./}"; printf '%d' "$(( 10#$t / 1000 ))"; }

# Mask every secret-bearing value before it can reach a log/evidence file.
sanitize() {
  sed -E \
    -e 's/token=[^&"[:space:]]+/token=***/g' \
    -e 's/dsh-auth-[A-Za-z0-9]+=[^;,"[:space:]]*/dsh-auth-***=***/g' \
    -e 's/(set-cookie:[[:space:]]*)[^[:space:]]+/\1***/gi' \
    -e 's/(cookie:[[:space:]]*)[^[:space:]]+/\1***/gi' \
    -e 's/(authorization:[[:space:]]*)[^[:space:]]+/\1***/gi' \
    -e 's/([?&](access_token|api_key|apikey|password|secret)=)[^&"[:space:]]+/\1***/g'
}

# capture <outfile> <command...> : run command, sanitize combined output into outfile.
capture() {
  local outfile="$1"; shift
  { "$@" 2>&1; } | sanitize > "$outfile"
  local rc=${PIPESTATUS[0]}
  return "$rc"
}

phase_env() {
  local out="$LOG_DIR/01-env.txt"
  {
    echo "# PCL-DSH-Go M0-5 environment record"
    echo "captured_at=$(date -Is)"
    echo "repo_root=$REPO_ROOT"
    echo "test_root=$ROOT"
    echo "--- os-release ---"
    sed -n '1,12p' /etc/os-release 2>/dev/null || true
    echo "--- uname ---"
    uname -a
    echo "--- cpu ---"
    nproc
    echo "--- memory ---"
    free -h
    echo "--- disk /tmp (tmpfs?) ---"
    df -h /tmp "$ROOT" 2>/dev/null || df -h /tmp
    echo "--- mount /tmp ---"
    mount | grep -E ' /tmp ' || true
    echo "--- node/pnpm (packaged toolchain) ---"
    echo "node_path=$NODE"
    "$NODE" -v
    echo "node_sha256=$(sha256sum "$NODE" | awk '{print $1}')"
    echo "pnpm_path=$PNPM"
    "$NODE" "$PNPM" -v
    echo "pnpm_cjs_sha256=$(sha256sum "$PNPM" | awk '{print $1}')"
    echo "--- tools available ---"
    for tool in curl git ss setsid nohup python3 timeout jq node go; do
      if command -v "$tool" >/dev/null 2>&1; then echo "$tool=$(command -v "$tool") $("$tool" --version 2>&1 | head -1)"; else echo "$tool=MISSING"; fi
    done
    echo "--- nginx ---"
    (nginx -v 2>&1 || echo "nginx=MISSING")
    echo "--- /opt read-only probe ---"
    test -w /opt/pcl-dsh-go/tools/node/bin/node && echo "writable_node_binary=YES" || echo "writable_node_binary=NO"
    test -w /opt/pcl-dsh-go/tools/pnpm/node_modules/pnpm/bin/pnpm.cjs && echo "writable_pnpm_binary=YES" || echo "writable_pnpm_binary=NO"
    echo "--- registry reachability ---"
    curl -sS -m 30 -o /dev/null -w 'registry_http=%{http_code} registry_time=%{time_total}s\n' "$REGISTRY/@deepseek-ai%2fdsh" || echo "registry_curl=FAILED"
    echo "--- port $PORT before recon ---"
    ss -ltnp 2>/dev/null | grep -E ":$PORT\\b" || echo "port_$PORT=FREE"
    echo "--- test root ---"
    ls -la "$ROOT"
  } | sanitize > "$out"
  log "env -> $out"
}

phase_registry() {
  local out="$LOG_DIR/02-registry.txt"
  capture "$out" "$NODE" "$LIB_DIR/registry-report.mjs" --registry "$REGISTRY" --version "$VERSION" --outdir "$META_DIR"
  local rc=$?
  log "registry report (rc=$rc):"
  sed -n '1,80p' "$out"
  return $rc
}

phase_install() {
  local out="$LOG_DIR/03-install.log"
  local summary="$LOG_DIR/03-install-summary.txt"
  : > "$out"
  local started; started=$(now_ms)
  local cohort_ms=0 pnpm_ms=0 validate_ms=0 already_installed=no
  if "$NODE" "$LIB_DIR/check-runtime.mjs" "$RUNTIME_DIR" "$VERSION" > "$META_DIR/runtime-check.txt" 2>&1; then
    already_installed=yes
    log "runtime already installed: $RUNTIME_DIR"
  else
    log "installing $VERSION into $ROOT/runtimes (pnpm $("$NODE" "$PNPM" -v))"
    local staging="$ROOT/runtimes/.install-$VERSION-$(date +%s)-$$"
    mkdir -p "$staging/.hdsl-install-home"
    local cohort_started; cohort_started=$(now_ms)
    if ! "$NODE" "$LIB_DIR/cohort.mjs" --registry "$REGISTRY" --version "$VERSION" \
         --data-root "$ROOT" --staging "$staging" 2>&1 | tee -a "$out" | sanitize; then
      log "cohort resolution FAILED; see $out"
      return 1
    fi
    cohort_ms=$(( $(now_ms) - cohort_started ))
    log "running pnpm install (this takes 4-8 min; log: $out)"
    local install_started; install_started=$(now_ms)
    ( cd "$staging" && env -i HOME="$staging/.hdsl-install-home" PATH="$ROOT/tools/shims:/usr/local/bin:/usr/bin:/bin" \
        timeout 1500 "$NODE" "$PNPM" install --prod --no-frozen-lockfile \
          --registry="$REGISTRY" --store-dir="$ROOT/cache/pnpm-store" --reporter=append-only ) \
        >> "$out" 2>&1
    local rc=$?
    pnpm_ms=$(( $(now_ms) - install_started ))
    if [ "$rc" -ne 0 ]; then
      log "pnpm install FAILED rc=$rc; last log lines:"
      tail -40 "$out" | sanitize
      echo "RESULT=install_failed rc=$rc pnpm_seconds=$((pnpm_ms / 1000)) staging=$staging" >> "$summary"
      return 1
    fi
    local validate_started; validate_started=$(now_ms)
    if ! "$NODE" "$LIB_DIR/check-runtime.mjs" "$staging" "$VERSION" > "$META_DIR/runtime-check.txt" 2>&1; then
      log "runtime validation FAILED after install:"
      sanitize < "$META_DIR/runtime-check.txt"
      echo "RESULT=validation_failed staging=$staging" >> "$summary"
      return 1
    fi
    validate_ms=$(( $(now_ms) - validate_started ))
    if [ -e "$RUNTIME_DIR" ]; then
      log "target runtime dir unexpectedly exists: $RUNTIME_DIR"
      echo "RESULT=target_exists" >> "$summary"
      return 1
    fi
    mv "$staging" "$RUNTIME_DIR"
    "$NODE" "$LIB_DIR/check-runtime.mjs" "$RUNTIME_DIR" "$VERSION" > "$META_DIR/runtime-check.txt" 2>&1 || true
  fi
  local total_ms=$(( $(now_ms) - started ))
  local size_bytes; size_bytes=$(du -sb "$RUNTIME_DIR" | awk '{print $1}')
  local dirs=0 files=0
  dirs=$(find "$RUNTIME_DIR" -type d | wc -l); files=$(find "$RUNTIME_DIR" -type f | wc -l)
  local cohort_cache
  cohort_cache=$(find "$ROOT/cache/registry-metadata" -name "cohort-$VERSION.json" 2>/dev/null | head -1)
  local native_total native_elf native_sample
  native_total=$(find "$RUNTIME_DIR/node_modules" -name '*.node' -type f 2>/dev/null | wc -l)
  native_elf=$(find "$RUNTIME_DIR/node_modules" -name '*.node' -type f -exec sh -c 'head -c4 "$1" | od -An -tx1 | tr -d " \n" | grep -q "^7f454c46$" && echo "$1"' _ {} \; 2>/dev/null | wc -l)
  native_sample=$(find "$RUNTIME_DIR/node_modules" -name '*.node' -type f 2>/dev/null | sed "s|$RUNTIME_DIR/||" | head -8 | tr '\n' ' ')
  {
    echo "runtime_dir=$RUNTIME_DIR"
    echo "already_installed=$already_installed"
    echo "install_total_seconds=$(( total_ms / 1000 ))"
    echo "cohort_seconds=$(( cohort_ms / 1000 ))"
    echo "pnpm_install_seconds=$(( pnpm_ms / 1000 ))"
    echo "validate_seconds=$(( validate_ms / 1000 ))"
    echo "runtime_size_bytes=$size_bytes"
    echo "runtime_size=$(du -sh "$RUNTIME_DIR" | awk '{print $1}')"
    echo "dirs=$dirs files=$files"
    cat "$META_DIR/runtime-check.txt"
    echo "lockfile=$(test -f "$RUNTIME_DIR/pnpm-lock.yaml" && echo yes || echo no)"
    echo "node_modules_top=$(du -sh "$RUNTIME_DIR/node_modules" 2>/dev/null | awk '{print $1}')"
    echo "native_modules_total=$native_total"
    echo "native_modules_elf=$native_elf"
    echo "native_modules_sample=$native_sample"
    echo "cohort_cache=$cohort_cache"
    echo "cohort_cache_sha256=$(test -f "$cohort_cache" && sha256sum "$cohort_cache" | awk '{print $1}')"
    echo "cohort_overrides_count=$(test -f "$cohort_cache" && "$NODE" -e "process.stdout.write(String(Object.keys(require(process.argv[1]).overrides).length))" "$cohort_cache" 2>/dev/null)"
    echo "RESULT=installed"
  } > "$summary"
  cat "$summary"
}

phase_probe() {
  local entry; entry=$("$NODE" "$LIB_DIR/check-runtime.mjs" "$RUNTIME_DIR" "$VERSION" | sed -n 's/^entry=//p')
  if [ -z "$entry" ]; then log "runtime entry not found; run install first"; return 1; fi
  local launcher="$LOG_DIR/04-probe-launcher-help.txt"
  local webhelp="$LOG_DIR/04-probe-web-help.txt"
  local t0 t1 rc
  t0=$(now_ms)
  ( cd "$ROOT/work" && timeout 35 "$NODE" "$entry" --help > "$launcher.raw" 2>&1 ); rc=$?
  t1=$(now_ms)
  sanitize < "$launcher.raw" > "$launcher"; rm -f "$launcher.raw"
  {
    echo "entry=$entry"
    echo "launcher_help_rc=$rc launcher_help_ms=$((t1 - t0)) bytes=$(wc -c < "$launcher")"
    echo "launcher_has_profile=$(grep -c -- '--profile' "$launcher" || true)"
    echo "launcher_has_web_alias=$(grep -cE '^[[:space:]]*web([[:space:]]|$)' "$launcher" || true)"
    echo "launcher_has_plugin=$(grep -cE '^[[:space:]]*plugin([[:space:]]|$)' "$launcher" || true)"
    echo "launcher_options=$(grep -oE -- '--[a-zA-Z][a-zA-Z0-9-]*' "$launcher" | sort -u | tr '\n' ' ')"
    echo "--- launcher help ---"
    cat "$launcher"
  } > "$LOG_DIR/04-probe-launcher-summary.txt"
  t0=$(now_ms)
  ( cd "$ROOT/work" && timeout 60 "$NODE" "$entry" --profile web --help > "$webhelp.raw" 2>&1 ); rc=$?
  t1=$(now_ms)
  sanitize < "$webhelp.raw" > "$webhelp"; rm -f "$webhelp.raw"
  {
    echo "web_help_rc=$rc web_help_ms=$((t1 - t0)) bytes=$(wc -c < "$webhelp")"
    echo "web_help_has_usage=$(grep -ci 'usage' "$webhelp" || true)"
    echo "web_help_has_port=$(grep -c -- '--port' "$webhelp" || true)"
    echo "web_help_has_host=$(grep -c -- '--host' "$webhelp" || true)"
    echo "web_help_has_no_open=$(grep -c -- '--no-open' "$webhelp" || true)"
    echo "web_options=$(grep -oE -- '--[a-zA-Z][a-zA-Z0-9-]*' "$webhelp" | sort -u | tr '\n' ' ')"
    echo "--- web help ---"
    cat "$webhelp"
  } > "$LOG_DIR/04-probe-web-summary.txt"
  log "probe -> $LOG_DIR/04-probe-*.txt"
  grep -E '^(launcher_help_rc|launcher_options|web_help_rc|web_options|web_help_has_)' "$LOG_DIR/04-probe-launcher-summary.txt" "$LOG_DIR/04-probe-web-summary.txt" || true
}

phase_launch() {
  local entry; entry=$("$NODE" "$LIB_DIR/check-runtime.mjs" "$RUNTIME_DIR" "$VERSION" | sed -n 's/^entry=//p')
  if [ -z "$entry" ]; then log "runtime entry not found; run install first"; return 1; fi
  if [ ! -f "$LIB_DIR/recon-launch.mjs" ]; then
    log "recon-launch.mjs not present yet; skipping launch phase"
    return 3
  fi
  local outdir="$LOG_DIR/launch"
  mkdir -p "$outdir"
  DSH_ENTRY="$entry" DSH_VERSION="$VERSION" DSH_PORT="$PORT" DSH_ROOT="$ROOT" DSH_NODE="$NODE" \
    "$NODE" "$LIB_DIR/recon-launch.mjs" --root "$ROOT" --outdir "$outdir" 2>&1 | tee "$LOG_DIR/00-launch-console.txt" | sanitize
  local rc=${PIPESTATUS[0]}
  log "launch phase rc=$rc (logs: $outdir)"
  return "$rc"
}

phase_report() {
  local dest="$EVIDENCE_DIR/logs"
  mkdir -p "$dest"
  local copied=0
  # *.log is git-ignored in this repo; append .txt so evidence is committable.
  for f in "$LOG_DIR"/*.txt "$LOG_DIR"/*.log; do
    [ -f "$f" ] || continue
    local name; name="$(basename "$f")"
    case "$name" in *.log) name="$name.txt";; esac
    sanitize < "$f" > "$dest/$name"
    copied=$((copied + 1))
  done
  if [ -d "$LOG_DIR/launch" ]; then
    mkdir -p "$dest/launch"
    for f in "$LOG_DIR/launch"/*; do
      [ -f "$f" ] || continue
      local name; name="$(basename "$f")"
      case "$name" in *.log) name="$name.txt";; esac
      sanitize < "$f" > "$dest/launch/$name"
      copied=$((copied + 1))
    done
  fi
  local leaks=0
  leaks=$(grep -rEo 'token=[^*"[:space:]]|dsh-auth-[A-Za-z0-9]+=[^*]' "$dest" 2>/dev/null | wc -l)
  echo "evidence_files=$copied evidence_dir=$dest potential_secret_leaks=$leaks"
  if [ "$leaks" -ne 0 ]; then
    echo "REPORT FAILED: possible token/cookie leak in evidence" >&2
    return 1
  fi
  return 0
}

case "$PHASE" in
  env) phase_env ;;
  registry) phase_registry ;;
  install) phase_install ;;
  probe) phase_probe ;;
  launch) phase_launch ;;
  report) phase_report ;;
  all)
    phase_env || exit 1
    phase_registry || exit 1
    phase_install || exit 1
    phase_probe || exit 1
    phase_launch || exit 1
    phase_report || exit 1
    ;;
  *) echo "unknown phase: $PHASE" >&2; exit 2 ;;
esac
