# packaging/

M2 起存放部署相关文件（由 M5 的 install.sh / 发行包流程安装）：

| 路径 | 内容 | 里程碑 |
| --- | --- | --- |
| `nginx/pcl-dsh-go.conf` | Nginx 生产反代模板（`/api/`、`/workbench/`、SPA 静态、WS Upgrade） | M2 |
| `nginx/README.md` | Nginx 模板安装/运维说明 | M2 |

后续里程碑将在此加入 systemd unit、install.sh/uninstall.sh、release 打包脚本等（M5）；本目录不修改目标机既有配置，安装动作由安装脚本执行。
