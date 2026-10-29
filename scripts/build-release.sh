#!/usr/bin/env bash
# PCL-DSH-Go | release builder (M5)
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Assembles a self-contained Linux amd64 release from an already built
# bin/pcl-dsh-go, web/dist and the bundled Node/pnpm toolchain, then writes
# SHA256SUMS for both the package tree and the tarball.
#
# Run inside WSL/Linux (tools are Linux binaries); the repository may live on
# /mnt/d (Windows) during development.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION=""
TOOLS_DIR="${PCL_DSH_TOOLS_DIR:-/opt/pcl-dsh-go/tools}"
OUT_DIR="${PCL_DSH_RELEASE_DIR:-$REPO_ROOT/release}"

usage() {
  cat <<'EOF'
Usage: scripts/build-release.sh [options]

  --version <ver>   Release version (default: repo VERSION)
  --tools <dir>     Bundled toolchain root containing node/ and pnpm/ (default: /opt/pcl-dsh-go/tools)
  --out <dir>       Output directory (default: <repo>/release)
  -h, --help        Show this help

Requires: bin/pcl-dsh-go (linux/amd64), web/dist (built by scripts/frontend-build.ps1),
          tools/node/bin/node and tools/pnpm (Node 24.22.1 + pnpm 11.28.5).
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?}"; shift 2;;
    --tools) TOOLS_DIR="${2:?}"; shift 2;;
    --out) OUT_DIR="${2:?}"; shift 2;;
    -h|--help) usage; exit 0;;
    *) echo "未知参数: $1" >&2; usage >&2; exit 2;;
  esac
done

fail() { echo "build-release: $*" >&2; exit 1; }
if [ -z "$VERSION" ]; then
  VERSION_FILE="$REPO_ROOT/VERSION"
  [ -f "$VERSION_FILE" ] || fail "缺少 $VERSION_FILE"
  VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
fi
[ -n "$VERSION" ] || fail "版本不能为空"
NAME="pcl-dsh-go-$VERSION-linux-amd64"
PKG="$OUT_DIR/$NAME"
BIN="$REPO_ROOT/bin/pcl-dsh-go"
DIST="$REPO_ROOT/web/dist"

[ -f "$BIN" ] || fail "缺少 $BIN（先构建 linux/amd64 二进制）"
[ -f "$DIST/index.html" ] || fail "缺少 $DIST/index.html（先运行 scripts/frontend-build.ps1）"
[ -f "$TOOLS_DIR/node/bin/node" ] || fail "缺少内置 Node: $TOOLS_DIR/node/bin/node"
[ -d "$TOOLS_DIR/pnpm" ] || fail "缺少内置 pnpm: $TOOLS_DIR/pnpm"
if [ "${PCL_DSH_CROSS_BUILD:-0}" != "1" ]; then
  [ -x "$BIN" ] || chmod 0755 "$BIN" 2>/dev/null || fail "bin/pcl-dsh-go 不可执行"
  [ -x "$TOOLS_DIR/node/bin/node" ] || fail "内置 Node 不可执行: $TOOLS_DIR/node/bin/node"
fi
if command -v file >/dev/null 2>&1; then
  file "$BIN" | grep -q 'ELF 64-bit LSB.*x86-64' || fail "bin/pcl-dsh-go 不是 Linux amd64 ELF"
fi
BUILT_VERSION="$("$BIN" --version 2>/dev/null || true)"
if [ -z "$BUILT_VERSION" ] && [ "${PCL_DSH_CROSS_BUILD:-0}" = "1" ]; then
  # Windows/MSYS cannot execute an ELF manager. Cross-build callers must build
  # the Linux binary with -X main.version=$VERSION; a Windows check binary can
  # verify the same ldflags separately.
  echo "WARN: cross-build mode; skipping ELF --version execution check (VERSION=$VERSION)" >&2
  BUILT_VERSION="$VERSION"
fi
[ -n "$BUILT_VERSION" ] || fail "无法读取 bin/pcl-dsh-go --version；请用 -X main.version 注入版本后重新构建"
[ "$BUILT_VERSION" = "$VERSION" ] || fail "bin/pcl-dsh-go 版本 ($BUILT_VERSION) 与 VERSION ($VERSION) 不一致；请用 -X main.version=$VERSION 重新构建"

echo "==> release: $NAME"
rm -rf "$PKG"
mkdir -p "$PKG"/{bin,packaging,docs}
cp -a "$BIN" "$PKG/bin/pcl-dsh-go"
chmod 0755 "$PKG/bin/pcl-dsh-go"
cp -a "$DIST/." "$PKG/static/"
if [ "${PCL_DSH_CROSS_BUILD:-0}" = "1" ]; then
  # NTFS cannot create Unix symlinks (npm/npx/corepack); dereference to real files.
  cp -aL "$TOOLS_DIR/node" "$PKG/tools-node"
  mkdir -p "$PKG/tools"
  mv "$PKG/tools-node" "$PKG/tools/node"
  cp -aL "$TOOLS_DIR/pnpm" "$PKG/tools/pnpm"
else
  cp -a "$TOOLS_DIR/node" "$PKG/tools-node"
  mkdir -p "$PKG/tools"
  mv "$PKG/tools-node" "$PKG/tools/node"
  cp -a "$TOOLS_DIR/pnpm" "$PKG/tools/pnpm"
fi
cp -a "$REPO_ROOT/packaging/." "$PKG/packaging/"
find "$PKG/packaging" -name '*.sh' -exec chmod 0755 {} + 2>/dev/null || true
cp -a "$REPO_ROOT/README.md" "$PKG/README.md" 2>/dev/null || true
cp -a "$REPO_ROOT/CONTRIBUTING.md" "$REPO_ROOT/SECURITY.md" "$PKG/" 2>/dev/null || true
cp -a "$REPO_ROOT/docs/"*.md "$PKG/docs/" 2>/dev/null || true
[ -d "$REPO_ROOT/docs/images" ] && cp -a "$REPO_ROOT/docs/images" "$PKG/docs/"
cp -a "$REPO_ROOT/LICENSE" "$PKG/LICENSE"
cp -a "$REPO_ROOT/LICENSES" "$PKG/LICENSES"
cp -a "$REPO_ROOT/THIRD_PARTY_NOTICES.md" "$PKG/THIRD_PARTY_NOTICES.md"
printf '%s\n' "$VERSION" > "$PKG/VERSION"
printf 'built_at=%s\ncommit=%s\n' "$(date -Is)" "$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)" > "$PKG/BUILD_INFO"

# Inner checksum manifest (paths relative to the package root).
( cd "$PKG" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS )
( cd "$PKG" && sha256sum -c SHA256SUMS >/dev/null )

TAR="$OUT_DIR/$NAME.tar.gz"
rm -f "$TAR" "$TAR.sha256"
TAR_MODE_OPT=()
if [ "${PCL_DSH_CROSS_BUILD:-0}" = "1" ]; then
  # NTFS checkouts lose Unix mode bits; force a readable/executable archive.
  TAR_MODE_OPT=(--mode='u+rwx,go+rx')
fi
tar "${TAR_MODE_OPT[@]}" -C "$OUT_DIR" -czf "$TAR" "$NAME"
( cd "$OUT_DIR" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256" )

SIZE=$(du -sh "$PKG" | cut -f1)
TSIZE=$(du -sh "$TAR" | cut -f1)
echo "==> OK"
echo "package_dir=$PKG ($SIZE)"
echo "tarball=$TAR ($TSIZE)"
echo "sha256=$(cut -d' ' -f1 "$TAR.sha256")"
echo "files=$(find "$PKG" -type f | wc -l)"