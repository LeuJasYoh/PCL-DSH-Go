# packaging/

M5 发行/部署资产（由 `scripts/build-release.sh` 打进 release，`packaging/install.sh` 安装）：

| 路径 | 内容 |
| --- | --- |
| `install.sh` | 安装 release：/opt/pcl-dsh-go + releases/current、pcl-dsh 用户、/var/lib/pcl-dsh-go、systemd unit、Nginx site(:80)、启动并打印 URL/初始密码路径 |
| `uninstall.sh` | 停止/禁用服务、移除 unit/Nginx site（恢复默认站）、删除安装目录；**默认保留数据**（`--purge-data` 全删） |
| `reset.sh` | 需 `--yes` 才删除数据目录并重启服务（重新生成初始密码） |
| `systemd/pcl-dsh-go.service` | systemd unit 模板（`@PREFIX@`/`@DATA_DIR@`/`@PORT@` 占位，沙箱/保护目录/45s 停机） |
| `nginx/pcl-dsh-go-site.conf` | Nginx site 模板（`@PREFIX@`/`@PORT@`/`@LISTEN_PORT@` 占位；API/工作台反代、SSE/WS、2G body、access_log off、SPA） |
| `nginx/pcl-dsh-go.conf` | M2 手工运维版 Nginx 模板（历史参考，可被 site 模板替代） |
| `nginx/README.md` | Nginx 模板安装/运维说明 |

安装（在解包后的 release 目录内）：

```bash
sudo ./packaging/install.sh                 # 默认 /opt/pcl-dsh-go + /var/lib/pcl-dsh-go + :80
sudo ./packaging/install.sh --prefix /tmp/pcl --data-dir /tmp/pcl-data --listen-port 18089  # 隔离验证
```

首次启动生成 `/var/lib/pcl-dsh-go/initial-password`（0600），登录后必须改密。升级：安装新 release 后 `current` 软链切换，数据不动；回滚：把 `current` 指回旧 `releases/<ver>` 并重启服务。
