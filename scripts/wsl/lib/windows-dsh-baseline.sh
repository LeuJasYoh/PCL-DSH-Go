#!/usr/bin/env bash
# PCL-DSH-Go M1 | read-only Windows DSH baseline / verification helpers.
#
# The M1 acceptance scripts source this file to prove that the real-DSH tests in WSL
# never modified Windows DSH. Only read-only 'find -printf' traversal is used; this
# library never opens a Windows DSH file for writing.
#
# Usage (after sourcing):
#   win_dsh_baseline_capture            # sets WIN_DSH_PROG_BEFORE / WIN_DSH_USER_BEFORE
#   win_dsh_baseline_verify <expected_prog_hash> <log_function>
#   win_dsh_baseline_write <outfile>    # writes a readable evidence block
#
# Env overrides: WIN_DSH_DIR (default /mnt/d/DevelopSoftware/DSH),
#                WIN_USER_DSH (default /mnt/c/Users/23500/.dsh),
#                WIN_DSH_HASH_TIMEOUT (default 240s per tree).
set -o pipefail

WIN_DSH_DIR="${WIN_DSH_DIR:-/mnt/d/DevelopSoftware/DSH}"
WIN_USER_DSH="${WIN_USER_DSH:-/mnt/c/Users/23500/.dsh}"
WIN_DSH_HASH_TIMEOUT="${WIN_DSH_HASH_TIMEOUT:-240}"

# Hash a tree by path|size|mtime metadata (read-only). Prints the sha256 hex digest,
# or "missing" when the tree does not exist.
win_dsh_tree_hash() {
  local tree="$1"
  if [ ! -d "$tree" ]; then printf 'missing'; return 0; fi
  timeout "$WIN_DSH_HASH_TIMEOUT" find "$tree" -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum | awk '{print $1}'
}

win_dsh_baseline_capture() {
  WIN_DSH_PROG_BEFORE="$(win_dsh_tree_hash "$WIN_DSH_DIR")"
  WIN_DSH_USER_BEFORE="$(win_dsh_tree_hash "$WIN_USER_DSH")"
  export WIN_DSH_PROG_BEFORE WIN_DSH_USER_BEFORE
}

# Verify the Windows DSH program tree is unchanged (strict). The user-data tree is
# logged informationally: the already-running Windows DSH writes its own sessions/logs.
# <log_function> receives (status, name, detail), e.g. the acceptance 'check' helper.
win_dsh_baseline_verify() {
  local expected_prog="$1" logf="${2:-echo}" prog_after user_after
  prog_after="$(win_dsh_tree_hash "$WIN_DSH_DIR")"
  user_after="$(win_dsh_tree_hash "$WIN_USER_DSH")"
  if [ "$prog_after" = "$expected_prog" ]; then
    "$logf" "PASS" "Windows DSH program tree unchanged" "before=$(printf '%s' "$expected_prog" | cut -c1-16)... after=$(printf '%s' "$prog_after" | cut -c1-16)..."
  else
    "$logf" "FAIL" "Windows DSH program tree changed" "before=$expected_prog after=$prog_after"
    return 1
  fi
  if [ "$user_after" = "$WIN_DSH_USER_BEFORE" ]; then
    "$logf" "INFO" "Windows user .dsh tree unchanged (read-only traversal)" "hash=$(printf '%s' "$user_after" | cut -c1-16)..."
  else
    "$logf" "INFO" "Windows user .dsh differs (ambient writes by the running Windows DSH; acceptance only reads it)" "before=$(printf '%s' "$WIN_DSH_USER_BEFORE" | cut -c1-16)... after=$(printf '%s' "$user_after" | cut -c1-16)..."
  fi
  return 0
}

# Write a readable baseline evidence block to <outfile> (paths/hashes only, no secrets).
win_dsh_baseline_write() {
  local out="$1" prog user
  prog="$(win_dsh_tree_hash "$WIN_DSH_DIR")"
  user="$(win_dsh_tree_hash "$WIN_USER_DSH")"
  {
    echo "# Windows DSH read-only baseline (find -printf path|size|mtime | sha256sum)"
    echo "captured_at=$(date -Is)"
    echo "windows_dsh_dir=$WIN_DSH_DIR"
    echo "windows_dsh_dir_exists=$(test -d "$WIN_DSH_DIR" && echo yes || echo no)"
    echo "windows_dsh_program_hash=$prog"
    echo "windows_user_dsh_dir=$WIN_USER_DSH"
    echo "windows_user_dsh_exists=$(test -d "$WIN_USER_DSH" && echo yes || echo no)"
    echo "windows_user_dsh_hash=$user"
    echo "note=read-only traversal only; the running Windows DSH may write its own user data (session/cache), so the user-data hash is informational while the program tree is compared strictly."
  } > "$out"
}
