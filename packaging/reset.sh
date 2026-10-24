#!/usr/bin/env bash
# PCL-DSH-Go | reset data (explicit --yes required) — M5
set -euo pipefail
SERVICE="pcl-dsh-go"
DATA_DIR="/var/lib/pcl-dsh-go"
YES="0"
while [ $# -gt 0 ]; do
  case "$1" in
    --data-dir) DATA_DIR="${2:?}"; shift 2;;
    --yes) YES="1"; shift;;
    -h|--help) echo "Usage: $0 --yes [--data-dir dir]"; exit 0;;
    *) echo "未知参数: $1" >&2; exit 2;;
  esac
done
[ "$(id -u)" -eq 0 ] || { echo "reset: 需要 root" >&2; exit 1; }
[ "$YES" = "1" ] || { echo "reset: 会删除 $DATA_DIR 全部数据（实例/运行时/日志/凭据）；确认请加 --yes" >&2; exit 1; }
systemctl stop "$SERVICE" >/dev/null 2>&1 || true
echo "==> 删除数据目录 $DATA_DIR"
rm -rf "$DATA_DIR"
systemctl start "$SERVICE" >/dev/null 2>&1 || true
echo "==> 数据已重置（服务已尝试重新启动，将生成新的初始密码）"
