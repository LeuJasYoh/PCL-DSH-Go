# 部署与运维

本文面向 Linux x86_64 服务器（含 WSL2 本机部署），介绍 PCL-DSH-Go 0.2.1 的安装、升级、回滚、卸载、重置、systemd/Nginx 与排障。参数细节见 [配置参考](CONFIGURATION.md)。

## 1. 部署拓扑

```text
浏览器（Windows/局域网） → http://<server>/（Nginx :80，可 TLS）
  ├─ /、/instances…      → SPA 静态：/opt/pcl-dsh-go/current/static
  ├─ ^~ /api/            → 反代 127.0.0.1:8080（JSON/Job SSE，不缓冲）
  └─ ^~ /workbench/      → 反代 127.0.0.1:8080（HTTP/SSE/WebSocket）
        ↓
pcl-dsh-go 管理器（systemd，用户 pcl-dsh）127.0.0.1:8080
  ├─ /opt/pcl-dsh-go/releases/<version> + current 软链（程序/静态/内置工具链）
  └─ /var/lib/pcl-dsh-go（配置/实例/运行时/缓存/日志/初始密码；0700）
        ↓ spawn
DSH 实例（默认不限并发；127.0.0.1:3080+）
```

- 管理器默认只监听回环；公网入口只有 Nginx。
- release 版本化（`releases/<version>` + `current` 软链），数据目录独立，升级/回滚不丢数据。
- 内置 Node 24.22.1 + pnpm 11.28.5；服务器**不需要**系统 Node/npm/pnpm/JDK。

## 2. 前置条件

| 项 | 要求 |
| --- | --- |
| 系统 | Linux x86_64（Ubuntu 22.04/24.04/26.04 等）；WSL2 需启用 systemd |
| 权限 | root 或 sudo（安装/卸载/重置需要 root） |
| systemd | `systemctl` 可用；WSL 在 `/etc/wsl.conf` 配置 `[boot] systemd=true` 后 `wsl --shutdown` |
| Nginx | ≥ 1.18（使用 `map`/HTTP 1.1/Upgrade） |
| 网络 | 安装 DSH 运行时/插件需访问 npm registry（可在 WebUI「设置」配置 registry/代理） |
| 磁盘 | release 约数百 MB；每个 DSH 运行时约 500 MB；再预留数据/日志/缓存 |
| 端口 | `:80`（Nginx）；`127.0.0.1:8080`（管理器）；`127.0.0.1:3080+`（实例） |

## 3. 发布包与校验

```text
pcl-dsh-go-0.2.1-linux-amd64/
  bin/pcl-dsh-go                 # linux/amd64（version 0.2.1）
  static/                        # WebUI 构建产物
  tools/node/                    # 内置 Node 24.22.1
  tools/pnpm/                    # 内置 pnpm 11.28.5
  packaging/install.sh           # 安装（root）
  packaging/uninstall.sh         # 卸载（默认保留数据）
  packaging/reset.sh             # 重置数据（需 --yes）
  packaging/systemd/pcl-dsh-go.service
  packaging/nginx/pcl-dsh-go-site.conf
  docs/DEPLOYMENT.md 等文档、LICENSE/LICENSES/THIRD_PARTY_NOTICES.md
  VERSION、BUILD_INFO、SHA256SUMS
```

```bash
sha256sum -c pcl-dsh-go-0.2.1-linux-amd64.tar.gz.sha256   # 压缩包校验
tar -xzf pcl-dsh-go-0.2.1-linux-amd64.tar.gz
cd pcl-dsh-go-0.2.1-linux-amd64
sha256sum -c SHA256SUMS                                    # 包内文件校验
cat VERSION BUILD_INFO
file bin/pcl-dsh-go        # 期望 ELF 64-bit LSB ... x86-64
./tools/node/bin/node -v && ./tools/node/bin/node ./tools/pnpm/node_modules/pnpm/bin/pnpm.cjs -v
```

> 不要跳过校验；不要从非官方渠道获取发布包。

## 4. 安装

```bash
cd pcl-dsh-go-0.2.1-linux-amd64
sudo ./packaging/install.sh
```

可用参数（`sudo ./packaging/install.sh --help`）：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--prefix <dir>` | `/opt/pcl-dsh-go` | 安装前缀（releases/ 与 current） |
| `--data-dir <dir>` | `/var/lib/pcl-dsh-go` | 数据目录（0700，属主 pcl-dsh） |
| `--port <port>` | `8080` | 管理器回环端口 |
| `--listen-port <port>` | `80` | Nginx 对外端口 |
| `--max-running-instances <n>` | `0` | 同时运行实例上限；`0` 不限制。**2C/1.6 GiB 建议显式 `1`** |
| `--no-service` | 关 | 仅安装 release 树与数据目录（不写 systemd/Nginx，不启动；隔离测试用） |

安装脚本会：检查 root/systemd/nginx 与 release 内容 → 创建 `pcl-dsh` 系统用户/数据目录 → 复制 release 到 `<prefix>/releases/<version>` 并切换 `current` → 渲染并启动 systemd unit → 渲染并启用 Nginx site（备份默认站）→ 等待 `/api/health` 就绪 → 打印访问地址与初始密码路径。

安装后自检：

```bash
systemctl is-active pcl-dsh-go
curl -sf http://127.0.0.1:8080/api/health; echo
curl -sI http://localhost/ | head -5
sudo nginx -t
sudo ls -l /var/lib/pcl-dsh-go/initial-password   # -rw------- pcl-dsh
```


## 5. 初始密码与首次登录

- 初始密码：`/var/lib/pcl-dsh-go/initial-password`（0600），24 位随机字母数字，只在首次启动生成，不写入日志。
- 浏览器打开 `http://localhost/` 登录，首次登录强制改密；改密后文件被删除。
- 查看：`sudo cat /var/lib/pcl-dsh-go/initial-password`（不要粘贴到聊天/工单）。
- 忘记密码：使用 `reset.sh` 重置（会清空数据），或从备份恢复。见第 10/11 节。

## 6. 服务管理（systemd）

```bash
sudo systemctl status pcl-dsh-go
sudo systemctl restart pcl-dsh-go
sudo systemctl stop pcl-dsh-go
sudo journalctl -u pcl-dsh-go -n 200 --no-pager
sudo tail -n 100 /var/lib/pcl-dsh-go/logs/manager-$(date +%F).log
```

- 服务用户 `pcl-dsh`、工作目录 `<prefix>/current`；unit 使用 `NoNewPrivileges=yes`、`PrivateTmp=yes`、`ProtectSystem=strict`、`ProtectHome=yes`、`ReadWritePaths=<data-dir>`、`TimeoutStopSec=45` 等沙箱设置。
- **重启/停止管理器会停止其启动的 DSH 实例**（退出时清理自有进程树）；维护前请在 WebUI 停止实例。
- 修改运行实例上限等参数：推荐重跑 `install.sh --max-running-instances N`（会同步 systemd unit）；也可手动编辑 unit 后 `daemon-reload` + `restart`。

## 7. Nginx

安装脚本写入：

- `/etc/nginx/sites-available/pcl-dsh-go.conf`（由发行包模板渲染）
- `/etc/nginx/sites-enabled/pcl-dsh-go.conf` → 软链
- 默认站 `sites-enabled/default` 若存在，被移动到 `sites-available/default.pcl-dsh-go-disabled`（卸载时恢复）

关键行为：SPA fallback、静态资源缓存、`/api/` 与 `/workbench/` 反代、SSE 不缓冲、WebSocket Upgrade、Host 保留端口、2 GiB 上传上限、敏感路径 `access_log off`、隐藏版本号。

```bash
sudo nginx -t && sudo systemctl reload nginx
sudo tail -n 100 /var/log/nginx/error.log
```

- 对外仅需开放 80（或运维自定义端口）；**不要将 8080 暴露到公网**。
- TLS/HTTPS 由运维在 Nginx 或前置代理终止（应用层不处理证书）。
- 反代目标必须是 `127.0.0.1:<port>`，不要指向远端地址。

## 8. 升级

1. 备份数据（见第 12 节），在 WebUI 停止运行中的实例。
2. 下载新 release 并按第 3 节校验、解包。
3. 在同一 prefix/data 参数下执行 `sudo ./packaging/install.sh`：复制到 `releases/<newver>`、切换 `current`、重启服务、重渲染 Nginx；**数据目录不动**，旧版本目录保留用于回滚。
4. 自检 health/登录/实例/设置；如新版本异常按第 9 节回滚。

注意：跨版本可能变更数据 schema（管理器启动时会校验 `schemaVersion` 并明确报错，不会静默改写）；升级前务必备份。


## 9. 回滚

前提：旧版本目录仍在 `<prefix>/releases/<oldver>`，且旧版本能读当前数据 schema。

```bash
sudo ls -1 /opt/pcl-dsh-go/releases
sudo ln -sfn /opt/pcl-dsh-go/releases/<oldver> /opt/pcl-dsh-go/current
sudo systemctl restart pcl-dsh-go
curl -sf http://127.0.0.1:8080/api/health; echo
```

也可用旧版本发行包重新执行 `install.sh`。若 schema 已升级，需同时恢复升级前的数据备份（先停服务，恢复后 `chown -R pcl-dsh:pcl-dsh <data-dir>`）。Nginx 静态根指向 `current/static`，切换软链后无需改 Nginx。

## 10. 卸载

在发行包 `packaging/` 目录内执行：

```bash
sudo ./packaging/uninstall.sh              # 删除程序/unit/Nginx site，保留数据
sudo ./packaging/uninstall.sh --purge-data # 额外删除数据目录与 pcl-dsh 用户（不可恢复！）
```

默认保留 `/var/lib/pcl-dsh-go`；卸载会恢复被备份的默认 Nginx 站。若安装时使用自定义 `--data-dir`，`--purge-data` 只会删除默认数据目录，自定义目录请手工处理。

## 11. 重置数据（危险）

```bash
sudo ./packaging/reset.sh --yes     # 必须显式 --yes
```

会停止服务、删除数据目录（实例/运行时/设置/日志/凭据），然后尝试重启服务并生成新的初始密码。仅在忘记密码、数据损坏或干净重装时使用；执行前务必备份。

## 12. 备份与恢复

```bash
# 备份（先停服务）
sudo systemctl stop pcl-dsh-go
sudo tar -czf /root/pcl-dsh-go-data-$(date +%F).tar.gz -C /var/lib pcl-dsh-go
sudo systemctl start pcl-dsh-go

# 恢复
sudo systemctl stop pcl-dsh-go
sudo mv /var/lib/pcl-dsh-go /var/lib/pcl-dsh-go.broken-$(date +%s)
sudo tar -xzf /root/pcl-dsh-go-data-YYYY-MM-DD.tar.gz -C /var/lib
sudo chown -R pcl-dsh:pcl-dsh /var/lib/pcl-dsh-go
sudo systemctl start pcl-dsh-go
```

至少备份 `config/` 与 `instances/`；`runtimes/` 与 `cache/` 可重建但体积较大。

## 13. 权限、目录与端口

| 路径 | 权限/属主 |
| --- | --- |
| `/opt/pcl-dsh-go`、`releases/<ver>` | 0755 root:root |
| `/opt/pcl-dsh-go/current` | symlink root:root |
| `/var/lib/pcl-dsh-go` | 0700 pcl-dsh:pcl-dsh |
| `<data>/initial-password`、`config/admin.json` | 0600 |
| `<data>/logs/` | 0700 |
| `/etc/systemd/system/pcl-dsh-go.service`、Nginx site | 0644 root:root |

端口：`:80`（Nginx 对外）、`127.0.0.1:8080`（管理器）、`127.0.0.1:3080+`（实例）。运行实例上限默认 `0`（不限制），可用 `--max-running-instances` 控制（见 [配置参考](CONFIGURATION.md#6-运行实例上限与资源建议)）。


## 14. WSL2 本机部署

- 在 WSL 内启用 systemd 并安装 Nginx；安装 release 到 `/opt/pcl-dsh-go`；Windows 浏览器访问 `http://localhost/`（WSL2 localhost 转发），备用 `http://127.0.0.1/`。
- 若 Windows 浏览器无法访问：在 WSL 内确认 `ss -ltnp | grep ':80 '` 与 `curl -sI http://127.0.0.1/`；必要时 `wsl --shutdown` 后重开；仍失败可用 `wsl hostname -I` 的地址直连排查。
- 不要从 Windows 直接运行管理器二进制；Windows 仅用于浏览器与（可选的）前端构建。
- WSL 本机资源有限时，建议显式 `--max-running-instances 1`。

## 15. 排障

| 现象 | 排查/处理 |
| --- | --- |
| 页面 502 | 管理器未启动/崩溃或端口不匹配：`systemctl status pcl-dsh-go`、`curl 127.0.0.1:8080/api/health`、`journalctl -u pcl-dsh-go` |
| 页面打不开/:80 被占 | `ss -ltnp | grep ':80 '`；停用占用进程或安装时改 `--listen-port`；默认站冲突由安装脚本自动备份 |
| 管理器启动失败 | 看 journal 第一处错误：数据目录权限/属主、端口占用、`--pcl.tool-root` 路径、沙箱写权限 |
| 初始密码文件不存在 | 非首次启动（已改密）属正常；忘记密码用 `reset.sh` 或备份恢复 |
| 实例启动失败 | 运行时未安装/端口占用/达到实例上限/工具链缺失；查看任务日志 |
| 工作台空白或 401/403 | 清除旧工作台 Cookie 或使用无痕窗口；确认实例就绪。见 [用户指南](USER_GUIDE.md#6-工作台workbench) |
| 磁盘不足 | 清理旧运行时/缓存（`cache/pnpm-store`）或扩容；DSH 每个版本约 500 MB |
| 升级后启动报 schemaVersion 不支持 | 回滚旧版本或恢复升级前数据备份 |

更多安全加固建议见 [SECURITY.md](../SECURITY.md)，日常使用见 [用户指南](USER_GUIDE.md)。
