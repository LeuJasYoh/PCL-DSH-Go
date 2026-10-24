#!/usr/bin/env bash
# PCL-DSH-Go | linux/amd64 test runner for WSL.
#
# Builds every requested Go package's test binary for linux/amd64 and executes
# those binaries inside WSL/Linux. Windows-only unit tests are excluded by build
# tags, exactly like a real Linux CI run.
#
# Go toolchain resolution:
#   * a native Linux go is used as-is;
#   * a Windows go.exe is driven through cmd.exe with GOOS/GOARCH injected, which
#     is the documented "cross-compile on Windows, run in WSL" flow. WSL interop
#     does not forward WSL environment variables to Windows executables, so the
#     environment is set inside cmd.exe.
#
# Usage (inside WSL):
#   bash scripts/wsl/run-linux-tests.sh                     # ./internal/...
#   bash scripts/wsl/run-linux-tests.sh ./internal/process  # specific packages
#   bash scripts/wsl/run-linux-tests.sh ./...               # everything
#
# Env:
#   GO                go binary (default: go.exe, then go)
#   OUTPUT_DIR        output directory (default: <repo>/bin/tests)
#   TEST_TIMEOUT      per-binary timeout passed to -test.timeout (default 600s)
#   EXTRA_TEST_FLAGS  extra flags, e.g. '-test.run=TestToken'
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/bin/tests}"
TEST_TIMEOUT="${TEST_TIMEOUT:-600s}"
EXTRA_TEST_FLAGS="${EXTRA_TEST_FLAGS:-}"

cd "$REPO_ROOT" || { echo "FAIL: cannot cd to $REPO_ROOT" >&2; exit 1; }

# Convert a /mnt/<drive>/... path to the matching Windows path (no wslpath shim needed).
winpath() {
  local p="$1"
  case "$p" in
    /mnt/[a-zA-Z]/*)
      local drive rest
      drive="$(printf '%s' "$p" | cut -c6)"
      rest="$(printf '%s' "$p" | cut -c8-)"
      printf '%s:\\%s' "$(printf '%s' "$drive" | tr '[:lower:]' '[:upper:]')" "$(printf '%s' "$rest" | sed 's|/|\\|g')"
      ;;
    *) printf '%s' "$p" ;;
  esac
}

GO_BIN="${GO:-}"
if [ -z "$GO_BIN" ]; then
  if command -v go.exe >/dev/null 2>&1; then GO_BIN=go.exe
  elif command -v go >/dev/null 2>&1; then GO_BIN=go
  else echo "FAIL: no Go toolchain found (looked for go.exe and go)" >&2; exit 2; fi
fi
GO_PATH="$(command -v "$GO_BIN" || true)"
[ -z "$GO_PATH" ] && GO_PATH="$GO_BIN"
IS_WINDOWS_GO=0
case "$GO_PATH" in
  *.exe|*.EXE) IS_WINDOWS_GO=1;;
esac
if [ "$IS_WINDOWS_GO" = 1 ]; then
  WIN_GO="$(winpath "$GO_PATH")"
  case "$WIN_GO" in
    *" "*) echo "FAIL: Windows Go path contains spaces ($WIN_GO); set GO=<path> or use a WSL-native Go" >&2; exit 2;;
  esac
  CMD_EXE="$(command -v cmd.exe)"
  cmd_go() { "$CMD_EXE" /s /c "set GOOS=linux&& set GOARCH=amd64&& set CGO_ENABLED=0&& $WIN_GO $*"; }
  GO_VERSION="$(cmd_go version 2>&1)"
else
  cmd_go() { env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" "$@"; }
  GO_VERSION="$(env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" version 2>&1)"
fi
echo "go=$GO_PATH"
echo "go_version=$GO_VERSION"
echo "repo=$REPO_ROOT"
echo "out=$OUTPUT_DIR"

PATTERNS=("$@")
if [ ${#PATTERNS[@]} -eq 0 ]; then PATTERNS=(./internal/...); fi

mkdir -p "$OUTPUT_DIR"
if ! PACKAGE_LIST="$(cmd_go list "${PATTERNS[@]}" 2>&1)"; then
  echo "FAIL: go list failed:" >&2
  echo "$PACKAGE_LIST" >&2
  exit 1
fi
if [ -z "$PACKAGE_LIST" ]; then
  echo "FAIL: no packages matched ${PATTERNS[*]}" >&2
  exit 1
fi

is_elf() {
  [ -f "$1" ] || return 1
  [ "$(head -c 4 "$1" | od -An -tx1 | tr -d ' \n')" = "7f454c46" ]
}

BUILT=()
BUILD_FAILED=()
for PACKAGE in $PACKAGE_LIST; do
  SHORT="$(printf '%s' "$PACKAGE" | sed -e 's|^github.com/LeuJasYoh/PCL-DSH-Go/||' -e 's|^internal/||' -e 's|/|_|g')"
  BINARY="bin/tests/$SHORT.test"
  echo "=== build $PACKAGE -> $BINARY"
  if cmd_go test -c -count=1 -o "$BINARY" "$PACKAGE"; then
    if [ -f "$BINARY" ]; then
      if is_elf "$BINARY"; then
        BUILT+=("$PACKAGE|$BINARY")
      else
        echo "  ERROR: $BINARY is not a linux/amd64 ELF (GOOS injection failed)" >&2
        BUILD_FAILED+=("$PACKAGE")
      fi
    else
      echo "  (no test files)"
    fi
  else
    BUILD_FAILED+=("$PACKAGE")
  fi
done

if [ ${#BUILD_FAILED[@]} -gt 0 ]; then
  echo "" >&2
  echo "FAIL: ${#BUILD_FAILED[@]} package(s) failed to build:" >&2
  printf '  %s\n' "${BUILD_FAILED[@]}" >&2
fi
if [ ${#BUILT[@]} -eq 0 ]; then
  echo "FAIL: no linux test binaries were built" >&2
  exit 1
fi

RUN_FAILED=()
for ENTRY in "${BUILT[@]}"; do
  PACKAGE="${ENTRY%%|*}"
  BINARY="${ENTRY#*|}"
  PACKAGE_DIR="${PACKAGE#github.com/LeuJasYoh/PCL-DSH-Go/}"
  ABS_BINARY="$REPO_ROOT/$BINARY"
  echo ""
  echo "=== run $PACKAGE"
  if ( cd "$REPO_ROOT/$PACKAGE_DIR" && "$ABS_BINARY" -test.v -test.count=1 -test.timeout="$TEST_TIMEOUT" $EXTRA_TEST_FLAGS ); then
    echo "--- PASS $PACKAGE"
  else
    echo "--- FAIL $PACKAGE" >&2
    RUN_FAILED+=("$PACKAGE")
  fi
done

echo ""
echo "=========================="
echo "built=${#BUILT[@]} build_failed=${#BUILD_FAILED[@]} run_failed=${#RUN_FAILED[@]}"
if [ ${#BUILD_FAILED[@]} -gt 0 ] || [ ${#RUN_FAILED[@]} -gt 0 ]; then
  echo "RESULT=FAIL"
  [ ${#BUILD_FAILED[@]} -gt 0 ] && printf 'build_failed: %s\n' "${BUILD_FAILED[@]}"
  [ ${#RUN_FAILED[@]} -gt 0 ] && printf 'run_failed: %s\n' "${RUN_FAILED[@]}"
  exit 1
fi
echo "RESULT=PASS"
