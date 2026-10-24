#!/usr/bin/env bash
# PCL-DSH-Go | uninstall (keeps data by default) — M5
set -euo pipefail
PREFIX="/opt/pcl-dsh-go"
SERVICE="pcl-dsh-go"
PURGE_DATA="0"
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="${2:?}"; shift 2;;
    --purge-data) PURGE_DATA="1"; shift;;
    -h|--help) echo "Usage: $0 [--prefix dir] [--purge-data]"; exit 0;;
    *) echo "未知参数: $1" >&2; exit 2;;
  esac
done
[ "$(id -u)" -eq 0 ] || { echo "uninstall: 需要 root" >&2; exit 1; }

echo "==> 停止并禁用 $SERVICE"
systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true
rm -f "/etc/systemd/system/$SERVICE.service"
systemctl daemon-reload

echo "==> 移除 Nginx site"
rm -f /etc/nginx/sites-enabled/pcl-dsh-go.conf /etc/nginx/sites-available/pcl-dsh-go.conf
if [ -e /etc/nginx/sites-available/default.pcl-dsh-go-disabled ]; then
  mv /etc/nginx/sites-available/default.pcl-dsh-go-disabled /etc/nginx/sites-enabled/default
fi
nginx -t >/dev/null 2>&1 && systemctl reload nginx || true

echo "==> 移除安装目录 $PREFIX"
rm -rf "$PREFIX"
if [ "$PURGE_DATA" = "1" ]; then
  echo "==> 删除数据目录 /var/lib/pcl-dsh-go"
  rm -rf /var/lib/pcl-dsh-go
  userdel pcl-dsh >/dev/null 2>&1 || true
else
  echo "数据目录 /var/lib/pcl-dsh-go 已保留（如需删除：$0 --purge-data）"
fi
echo "==> 卸载完成"
