#!/usr/bin/env bash
# PCL-DSH-Go | install release to /opt/pcl-dsh-go + systemd + Nginx (M5)
# SPDX-License-Identifier: MIT
set -euo pipefail

PREFIX="/opt/pcl-dsh-go"
DATA_DIR="/var/lib/pcl-dsh-go"
PORT="8080"
LISTEN_PORT="80"
SERVICE="pcl-dsh-go"
SERVICE_USER="pcl-dsh"
NO_SERVICE="0"
MAX_RUNNING="0"

usage() {
  cat <<'EOF'
Usage: sudo ./packaging/install.sh [options]

  --prefix <dir>       Install prefix (default: /opt/pcl-dsh-go)
  --data-dir <dir>     Data root (default: /var/lib/pcl-dsh-go)
  --port <port>        Manager loopback port (default: 8080)
  --listen-port <port> Nginx public port (default: 80)
  --max-running-instances <n>  Max simultaneously running DSH instances (default: 0 = unlimited; set 1 on small servers)
  --no-service         Only install the release tree + data dir (no systemd/Nginx; for isolated tests)
  -h, --help           Show this help

Installs the release this script belongs to, creates the pcl-dsh service user,
writes the systemd unit and the Nginx site, starts the manager and prints the
WebUI URL plus the initial-password path. Existing data is never deleted.
EOF
}

fail() { echo "install: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="${2:?}"; shift 2;;
    --data-dir) DATA_DIR="${2:?}"; shift 2;;
    --port) PORT="${2:?}"; shift 2;;
    --listen-port) LISTEN_PORT="${2:?}"; shift 2;;
    --max-running-instances) MAX_RUNNING="${2:?}"; shift 2;;
    --no-service) NO_SERVICE="1"; shift;;
    -h|--help) usage; exit 0;;
    *) echo "未知参数: $1" >&2; usage >&2; exit 2;;
  esac
done

[ "$(id -u)" -eq 0 ] || fail "请使用 root 运行（sudo）"
case "$MAX_RUNNING" in
  ''|*[!0-9]*) fail "--max-running-instances 必须是非负整数（0 表示不限制）";;
  *) : ;;
esac
if [ "$NO_SERVICE" = "0" ]; then
  command -v systemctl >/dev/null 2>&1 || fail "需要 systemd"
  command -v nginx >/dev/null 2>&1 || fail "需要 Nginx"
fi

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$SRC_DIR/VERSION" ] || fail "未找到 $SRC_DIR/VERSION，请在发行包内运行"
VERSION="$(tr -d '[:space:]' < "$SRC_DIR/VERSION")"
[ -f "$SRC_DIR/bin/pcl-dsh-go" ] || fail "缺少 bin/pcl-dsh-go"
[ -d "$SRC_DIR/static" ] || fail "缺少 static/（WebUI 构建产物）"
[ -x "$SRC_DIR/tools/node/bin/node" ] || fail "缺少内置 Node：tools/node/bin/node"
[ -d "$SRC_DIR/tools/pnpm" ] || fail "缺少内置 pnpm：tools/pnpm"

echo "==> 安装 PCL-DSH-Go $VERSION"
echo "    prefix=$PREFIX data=$DATA_DIR manager=127.0.0.1:$PORT public=:$LISTEN_PORT max_running=$MAX_RUNNING"

# service user
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
fi
install -d -m 0755 "$PREFIX" "$PREFIX/releases"
install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR"

# release tree
TARGET="$PREFIX/releases/$VERSION"
rm -rf "$TARGET"
mkdir -p "$TARGET"
cp -a "$SRC_DIR/." "$TARGET/"
chown -R root:root "$TARGET"
find "$TARGET" -type d -exec chmod 0755 {} +
chmod 0755 "$TARGET/bin/pcl-dsh-go"
ln -sfn "$TARGET" "$PREFIX/current"

if [ "$NO_SERVICE" = "0" ]; then
# systemd unit
sed -e "s|@PREFIX@|$PREFIX|g"     -e "s|@DATA_DIR@|$DATA_DIR|g"     -e "s|@PORT@|$PORT|g"     -e "s|@MAX_RUNNING@|$MAX_RUNNING|g"     "$TARGET/packaging/systemd/pcl-dsh-go.service" > "/etc/systemd/system/$SERVICE.service"
chmod 0644 "/etc/systemd/system/$SERVICE.service"
systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null
if ! systemctl restart "$SERVICE"; then
  journalctl -u "$SERVICE" --no-pager -n 40 || true
  fail "manager 启动失败"
fi

# Nginx site
SITE_AVAILABLE="/etc/nginx/sites-available/pcl-dsh-go.conf"
SITE_ENABLED="/etc/nginx/sites-enabled/pcl-dsh-go.conf"
DEFAULT_LINK="/etc/nginx/sites-enabled/default"
DEFAULT_BACKUP="/etc/nginx/sites-available/default.pcl-dsh-go-disabled"
DISABLED_DEFAULT=""
sed -e "s|@PREFIX@|$PREFIX|g"     -e "s|@PORT@|$PORT|g"     -e "s|@LISTEN_PORT@|$LISTEN_PORT|g"     "$TARGET/packaging/nginx/pcl-dsh-go-site.conf" > "$SITE_AVAILABLE"
if [ -e "$DEFAULT_LINK" ] && [ ! -e "$DEFAULT_BACKUP" ]; then
  mv "$DEFAULT_LINK" "$DEFAULT_BACKUP"
  DISABLED_DEFAULT="1"
fi
ln -sfn "$SITE_AVAILABLE" "$SITE_ENABLED"
if ! nginx -t >/tmp/pcl-dsh-go-nginx-test.log 2>&1; then
  cat /tmp/pcl-dsh-go-nginx-test.log >&2 || true
  rm -f "$SITE_ENABLED"
  if [ -n "$DISABLED_DEFAULT" ]; then
    mv "$DEFAULT_BACKUP" "$DEFAULT_LINK"
  fi
  fail "Nginx 配置测试失败（已恢复默认站）"
fi
systemctl reload nginx
fi

# wait for manager health
HEALTH="http://127.0.0.1:$PORT/api/health"
if [ "$NO_SERVICE" = "0" ]; then
  OK=""
  for _ in $(seq 1 80); do
    if curl -sf --noproxy '*' --max-time 2 "$HEALTH" >/dev/null 2>&1; then OK="1"; break; fi
    sleep 0.25
  done
  [ -n "$OK" ] || { journalctl -u "$SERVICE" --no-pager -n 60 || true; fail "manager 未在 20s 内就绪"; }
fi

PUBLIC_URL="http://localhost/"
if [ "$LISTEN_PORT" != "80" ]; then PUBLIC_URL="http://localhost:$LISTEN_PORT/"; fi
echo "==> 安装完成"
if [ "$NO_SERVICE" = "1" ]; then
  echo "mode=isolated（未安装 systemd/Nginx；手动启动：$PREFIX/current/bin/pcl-dsh-go --server.port=$PORT --pcl.data-root=$DATA_DIR --pcl.tool-root=$PREFIX/current/tools --server.static-dir=$PREFIX/current/static --pcl.max-running-instances=$MAX_RUNNING）"
  echo "version=$VERSION"
  exit 0
fi
echo "service=$(systemctl is-active "$SERVICE") version=$VERSION"
echo "webui=$PUBLIC_URL"
if [ -f "$DATA_DIR/initial-password" ]; then
  echo "initial_password_file=$DATA_DIR/initial-password"
  echo "首次登录后请立即修改管理员密码。"
else
  echo "管理员密码已初始化（非首次安装）。"
fi
echo "health=$HEALTH"