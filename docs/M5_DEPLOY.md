# M5 部署与运维指南（PCL-DSH-Go 1.0.0）

> owner：backend-core（M5-D）。适用：**PCL-DSH-Go release 1.0.0（linux/amd64）**；部署目标为 Linux（本里程碑按用户要求已在 **WSL + Nginx :80** 实际部署，Windows 浏览器访问 `http://localhost/`）。
> 关联契约：`docs/M5_INTERFACES.md`（发行布局/安装契约/Nginx 契约/G9-G10）、`docs/IMPLEMENTATION_SPEC.md` 第 5/8 节、`docs/ACCEPTANCE.md`、`docs/M4_BUILD.md`（前端构建）。
> 安装/升级/卸载脚本以 **发行包内 `packaging/` 目录** 为准；CI 说明见第 13 节，构建发布包见第 14 节。

## 1. 架构与部署拓扑

```text
Windows 浏览器 / 局域网客户端
        │  http://localhost/  （WSL2 时 Windows localhost 转发到 WSL :80）
        ▼
Nginx :80（唯一公网入口；TLS 可选，由运维在 Nginx 终止）
  ├─ /、/instances、/packs…   → SPA 静态：/opt/pcl-dsh-go/current/static（try_files → index.html）
  ├─ ^~ /api/                → 反代 127.0.0.1:8080（JSON/Job SSE，不缓冲）
  └─ ^~ /workbench/          → 反代 127.0.0.1:8080（HTTP/SSE/WebSocket）
        ▼
pcl-dsh-go 管理器 127.0.0.1:8080（systemd: pcl-dsh-go.service，用户 pcl-dsh）
  ├─ 数据：/var/lib/pcl-dsh-go（config/instances/runtimes/cache/logs/trash/initial-password）
  ├─ 版本化发行：/opt/pcl-dsh-go/releases/<version> + current 软链
  ├─ 内置工具链：/opt/pcl-dsh-go/current/tools/{node,pnpm}
  └─ spawn（最多 1 个运行实例）：DSH 127.0.0.1:3080+
```

关键设计：

- **release 不可变、数据独立**：升级 = 新增 `releases/<newver>` 并切换 `current` 软链；`data-root` 不在 release 内，升级/回滚不动数据。
- **管理器只监听回环**：`127.0.0.1:8080`；公网只暴露 Nginx :80。不要改 `--server.address` 为 0.0.0.0，也不要对公网开放 8080。
- **单实例**：data-root 下 `.lock` flock 保证同一 data-root 只有一个管理器进程；产品层面同时最多 1 个运行中的 DSH 实例。
- **无系统依赖**：运行期只用随包 Node 24.22.1 + pnpm 11.28.5，不需要系统 node/npm/pnpm/JDK。
- WSL 部署仅用于本机演示/验收；生产建议独立 Linux 主机，做法相同。

## 2. 发布包内容与校验

发布物：

```text
pcl-dsh-go-1.0.0-linux-amd64.tar.gz
pcl-dsh-go-1.0.0-linux-amd64.tar.gz.sha256
```

解包后目录（`VERSION` 为实际版本）：

```text
pcl-dsh-go-1.0.0-linux-amd64/
  bin/pcl-dsh-go                 # linux/amd64 ELF（-trimpath，version=1.0.0）
  static/                        # M4 WebUI 构建产物（index.html、assets/、licenses/、favicon.webp）
  tools/node/                    # 内置 Node 24.22.1（tools/node/bin/node）
  tools/pnpm/                    # 内置 pnpm 11.28.5（tools/pnpm/node_modules/pnpm/bin/pnpm.cjs）
  packaging/install.sh           # 安装（root）
  packaging/uninstall.sh         # 卸载（默认保留数据）
  packaging/reset.sh             # 重置数据（需 --yes）
  packaging/README.md            # packaging 目录说明
  packaging/systemd/pcl-dsh-go.service   # systemd unit 模板（@PREFIX@/@DATA_DIR@/@PORT@）
  packaging/nginx/pcl-dsh-go-site.conf   # Nginx site 模板（@PREFIX@/@PORT@/@LISTEN_PORT@，install.sh 使用）
  packaging/nginx/pcl-dsh-go.conf        # M2 手工运维模板（历史参考，install.sh 不使用）
  docs/M5_DEPLOY.md              # 本文档
  docs/ACCEPTANCE.md
  README.md
  LICENSE / LICENSES/ / THIRD_PARTY_NOTICES.md
  VERSION
  BUILD_INFO                     # built_at / commit
  SHA256SUMS                     # 包内所有文件（相对包根）的 sha256
```

校验（下载后先做，解包后再做一次）：

```bash
# 1) 校验压缩包（.sha256 文件由发布方提供）
sha256sum -c pcl-dsh-go-1.0.0-linux-amd64.tar.gz.sha256

# 2) 解包并校验包内清单
tar -xzf pcl-dsh-go-1.0.0-linux-amd64.tar.gz
cd pcl-dsh-go-1.0.0-linux-amd64
sha256sum -c SHA256SUMS
cat VERSION BUILD_INFO
file bin/pcl-dsh-go          # 期望 ELF 64-bit LSB ... x86-64
```

检查内置工具链可用（不依赖系统 node/npm）：

```bash
./tools/node/bin/node -v
./tools/node/bin/node ./tools/pnpm/node_modules/pnpm/bin/pnpm.cjs -v
# 期望：v24.22.1 / 11.28.5
```

> 发布包内只包含程序、静态资源、内置工具链、安装/许可文档，**不含**数据/日志/密钥/构建缓存。许可证为 GPL-3.0-or-later + HMCL 附加条款 §7，见 `LICENSE`/`LICENSES/`/`THIRD_PARTY_NOTICES.md`。

## 3. 前置条件

| 项 | 要求 |
| --- | --- |
| 系统 | Linux x86_64（Ubuntu 22.04/24.04/26.04 等）；WSL2 需启用 systemd |
| 权限 | root 或 sudo（install/uninstall/reset 都要求 root） |
| systemd | `systemctl` 可用（WSL: `/etc/wsl.conf` 中 `[boot] systemd=true` 后 `wsl --shutdown` 重启） |
| Nginx | `nginx` ≥ 1.18（`map`/HTTP1.1/Upgrade）；默认站点会由安装脚本备份并让位 |
| 网络 | 安装机需要能访问 npm registry（后续在 WebUI 安装 DSH runtime 时；可用设置里的 registry/代理） |
| 磁盘 | 发布包（含 Node/pnpm）解压后约数百 MB；每个 DSH runtime 约 500 MB；再预留数据/日志/cache |
| 端口 | `:80` 对外（Nginx）；`127.0.0.1:8080` 管理器回环；`127.0.0.1:3080+` DSH 实例回环 |
| 依赖 | 仅需 `tar`/`curl`/`sha256sum` 等基础工具；**不需要**系统 Node/npm/pnpm/JDK |

WSL2 额外检查：

```bash
# WSL 内
systemctl is-system-running --quiet && echo systemd-ok
ss -ltnp | grep ':80 ' || echo 'port 80 free'
# Windows 侧
curl.exe -I http://localhost/     # 安装完成后应 200
```

若 Windows `localhost` 无法访问 WSL 的 :80，见第 15 节 FAQ（WSL localhost 转发/端口占用）。


## 4. 首次安装

```bash
# 以 Ubuntu/WSL 为例；在解包后的 release 目录内
cd pcl-dsh-go-1.0.0-linux-amd64
sha256sum -c SHA256SUMS
sudo ./packaging/install.sh
```

可选参数（`sudo ./packaging/install.sh --help` 可查看）：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--prefix <dir>` | `/opt/pcl-dsh-go` | 安装前缀（releases/ 与 current 软链所在） |
| `--data-dir <dir>` | `/var/lib/pcl-dsh-go` | 数据根（持久化，权限 0700，属主 pcl-dsh） |
| `--port <port>` | `8080` | 管理器回环端口（Nginx 反代到该端口） |
| `--listen-port <port>` | `80` | Nginx 对外监听端口 |
| `--no-service` | 关 | 仅安装 release 树 + 数据目录，不写 systemd/Nginx、不启动也不等待 health（隔离安装/测试用；仍要求 root 以创建用户与目录） |

安装脚本执行的动作（与 `packaging/install.sh` 一一对应）：

1. 校验 root（始终）、systemd/nginx（仅非 `--no-service`）；校验 release 内容（VERSION/bin/static/tools）。
2. 创建系统用户/组 `pcl-dsh`（无登录 shell；已存在则复用）。
3. 创建 `<prefix>`、`<prefix>/releases`、`<data-dir>`（0700，属主 pcl-dsh）。
4. 复制 release 到 `<prefix>/releases/<version>`，把 `<prefix>/current` 软链指过去。
5. 渲染 systemd unit（替换 @PREFIX@/@DATA_DIR@/@PORT@）到 `/etc/systemd/system/pcl-dsh-go.service`，`daemon-reload` + `enable` + `restart`。
6. 渲染 Nginx site 到 `/etc/nginx/sites-available/pcl-dsh-go.conf`，启用到 `/etc/nginx/sites-enabled/pcl-dsh-go.conf`；若存在默认站 `default`，先备份为 `default.pcl-dsh-go-disabled` 再启用本 site。
7. `nginx -t` 失败时移除新 site 并恢复默认站，安装中止（不会留下坏配置）；成功则 `systemctl reload nginx`。
8. 轮询 `http://127.0.0.1:<port>/api/health`（约 20s）；未就绪时打印 journal 并失败。
9. 打印访问地址与初始密码文件路径。

使用 `--no-service` 时跳过第 5–8 步（不写 unit/Nginx、不启动/不等待 health），只安装 release 树与数据目录，并打印手动启动命令（隔离验收/CI smoke 用）：

```bash
sudo ./packaging/install.sh --no-service --prefix /tmp/pcl-dsh-test --data-dir /tmp/pcl-dsh-test/data
# 输出 mode=isolated ...；随后可手动启动：
# /tmp/pcl-dsh-test/current/bin/pcl-dsh-go --server.port=18080 ...
```

安装成功输出示例：

```text
==> 安装 PCL-DSH-Go 1.0.0
    prefix=/opt/pcl-dsh-go data=/var/lib/pcl-dsh-go manager=127.0.0.1:8080 public=:80
==> 安装完成
service=active version=1.0.0
webui=http://localhost/
initial_password_file=/var/lib/pcl-dsh-go/initial-password
首次登录后请立即修改管理员密码。
health=http://127.0.0.1:8080/api/health
```

安装后自检：

```bash
systemctl is-active pcl-dsh-go
curl -sf http://127.0.0.1:8080/api/health; echo
curl -sI http://localhost/ | head -5
sudo nginx -t
sudo ls -l /var/lib/pcl-dsh-go/initial-password   # 期望 -rw------- pcl-dsh pcl-dsh
```

> `install.sh` 可重复执行：同一版本会重建 `releases/<version>` 并重启服务，**不会删除数据**（升级到新版本请用新版本发行包，见第 8 节）。

## 5. 初始管理员密码与首次登录

- 位置：`/var/lib/pcl-dsh-go/initial-password`（0600，属主 pcl-dsh；root 可读）。
- 内容：24 位随机字母数字（外加一个换行），只在首次启动时生成一次；**不会写入日志/审计**。
- 查看（注意不要粘贴到聊天/工单）：

```bash
sudo cat /var/lib/pcl-dsh-go/initial-password
```

- 浏览器打开 `http://localhost/`，用初始密码登录；首次登录后必须修改密码（新密码 8–64 字符），改密成功后该文件被删除、`initialPasswordPending=false`。
- 若升级/重装前已改过密码，不会重新生成 initial-password（安装输出会提示“管理员密码已初始化（非首次安装）”）。
- 忘记密码：M5 不提供“直接改密”脚本；唯一受支持的恢复方式是 **重置数据**（会清空实例/运行时/日志，见第 11 节），或按第 11.3 节恢复 `config/admin.json` 备份。

## 6. 日常运维（systemd / 数据 / 日志）

服务名：`pcl-dsh-go.service`；unit：`/etc/systemd/system/pcl-dsh-go.service`。

```bash
sudo systemctl status pcl-dsh-go
sudo systemctl restart pcl-dsh-go
sudo systemctl stop pcl-dsh-go
sudo systemctl start pcl-dsh-go
sudo systemctl is-enabled pcl-dsh-go

# 日志（stdout/stderr + 崩溃/boot 信息）
sudo journalctl -u pcl-dsh-go -n 200 --no-pager
sudo journalctl -u pcl-dsh-go -f

# 应用日志（按日、已脱敏）：<data>/logs/manager-YYYY-MM-DD.log 与 audit-YYYY-MM-DD.log
sudo tail -n 100 /var/lib/pcl-dsh-go/logs/manager-$(date +%F).log
# 也可登录后经 GET /api/logs?source=manager&tail=800 查看
```

- **restart/stop 会停止该管理器启动的 DSH 实例**（管理器退出时清理自有进程树）：维护前请在 WebUI 停止实例或确认可以中断。
- unit 关键内容：`User=pcl-dsh`、`WorkingDirectory=<prefix>/current`、ExecStart 携带 `--server.address=127.0.0.1 --server.port=8080 --pcl.data-root=<data> --pcl.tool-root=<prefix>/current/tools --server.static-dir=<prefix>/current/static`；沙箱 `NoNewPrivileges=yes/PrivateTmp=yes/ProtectSystem=strict/ProtectHome=yes/ReadWritePaths=<data>`；`TimeoutStopSec=45`（覆盖实例停止的 30s）。
- 需要修改启动参数（例如换端口）时，建议重跑 `install.sh --port <port>`（会同步渲染 Nginx 反代端口）；直接改 unit 时注意 Nginx 模板里的 `@PORT@` 也要一致。
- 数据目录（`/var/lib/pcl-dsh-go`，0700）结构：

```text
config/{manager.json,instances.json,admin.json,compatibility.json}
instances/<id>/{dsh-home,workspace,agents-home}
runtimes/<semver>/
cache/{pnpm-store,npm,registry-metadata,probes,uploads,exports,pack-staging}
logs/{manager,audit}-YYYY-MM-DD.log
trash/
initial-password
.lock
```

备份建议：`sudo systemctl stop pcl-dsh-go` 后打包 `/var/lib/pcl-dsh-go`（至少 `config/` 与 `instances/`；`runtimes/` 与 `cache/` 可重建、体积大）。恢复后用 `chown -R pcl-dsh:pcl-dsh` 修正属主再启动。


## 7. Nginx 说明

安装脚本写入：

- `/etc/nginx/sites-available/pcl-dsh-go.conf`（由发行包 `packaging/nginx/pcl-dsh-go-site.conf` 渲染，替换 PREFIX/PORT/LISTEN_PORT）
- `/etc/nginx/sites-enabled/pcl-dsh-go.conf` → 指向上面的软链
- 默认站 `/etc/nginx/sites-enabled/default` 若存在，被移动到 `/etc/nginx/sites-available/default.pcl-dsh-go-disabled`（实际是该 default 软链本身被移走；卸载时自动恢复回 `sites-enabled/default`）

关键行为（模板见发行包 `packaging/nginx/pcl-dsh-go-site.conf`）：

| 配置 | 作用/原因 |
| --- | --- |
| `root <prefix>/current/static`；`try_files $uri $uri/ /index.html` | SPA 静态托管；前端路由刷新不 404；静态根走 `current`，升级/回滚自动生效 |
| `location ^~ /api/` → `proxy_pass http://127.0.0.1:<port>` | JSON API + Job SSE；`proxy_buffering off`/`proxy_request_buffering off` 保证流式 |
| `location ^~ /workbench/` + `proxy_set_header Upgrade/Connection` + map | /workbench 的 HTTP/SSE/WebSocket（DSH 工作台） |
| `proxy_read_timeout/proxy_send_timeout 3600s` | 长连接/SSE/WS 不被默认 60s 切断 |
| `proxy_set_header Host $pcl_dsh_host`（map 保留端口） | 管理器 Origin 同源校验需要 Host 含端口；非 80 监听时尤其重要 |
| `access_log off`（/api、/workbench） | query 可能含一次性 token，敏感路径不落访问日志 |
| `client_max_body_size 2048m` | 整合包/上传最大 2 GiB（服务端另有流式限制） |
| `server_tokens off`、SPA no-store、assets 长缓存 | 安全与升级即时生效 |
| `listen <listen-port> default_server` | 作为默认站服务 http://<host>/；如已有业务默认站请先用 `--listen-port` 换端口或人工调整 Nginx |

运维命令：

```bash
sudo nginx -t
sudo systemctl reload nginx       # 配置变更后
sudo tail -n 100 /var/log/nginx/error.log
```

TLS/HTTPS 不在本里程碑应用层实现；公网部署建议由运维在 Nginx 增加 443 server 并 80→443 跳转（证书/套件属部署环境策略）。反向代理必须走回环 `127.0.0.1:<port>`，不要指向远端地址。

## 8. 升级（同 data-root）

1. **准备**：备份数据（见第 6 节）；确认当前版本（`cat /opt/pcl-dsh-go/current/VERSION`）；在 WebUI 停止运行中的实例。
2. **获取新 release**：下载 `pcl-dsh-go-<newver>-linux-amd64.tar.gz` + `.sha256`，按第 2 节校验并解包。
3. **安装**（同一 prefix/data 参数，默认即可）：

```bash
cd pcl-dsh-go-<newver>-linux-amd64
sudo ./packaging/install.sh
```

4. 脚本会：复制到 `<prefix>/releases/<newver>` → 切换 `current` → 重写 systemd unit（同参数）→ 重启服务 → 重渲染 Nginx site → health 检查。**数据目录保持不动**，旧版本目录仍在 `releases/` 下（便于回滚）。
5. **升级后自检**：`systemctl is-active`、`/api/health`、浏览器登录、版本号（页面/`/api/health` 的 version）、实例/设置仍在。

注意：

- 升级/回滚前务必停实例并备份数据；不同版本可能变更 data schema（管理器启动时会校验 `schemaVersion`，不支持的版本会明确报错而不是静默改写）。
- 自定义过 `--prefix/--data-dir/--port` 的环境，升级时必须传相同参数。
- 若新版本启动失败：按第 9 节回滚，并保留 `journalctl`/日志用于排查。

## 9. 回滚

前提：目标旧版本目录仍在 `<prefix>/releases/<oldver>`，并且旧版本能读当前 data schema（跨 schema 变更时需同时恢复升级前数据备份）。

**方式 A：切回软链（最快）**

```bash
sudo ls -1 /opt/pcl-dsh-go/releases
sudo ln -sfn /opt/pcl-dsh-go/releases/<oldver> /opt/pcl-dsh-go/current
sudo systemctl restart pcl-dsh-go
sudo systemctl is-active pcl-dsh-go
curl -sf http://127.0.0.1:8080/api/health; echo
```

Nginx 静态根是 `<prefix>/current/static`（软链），无需改 Nginx；浏览器强刷（index.html no-store，assets 带 hash，通常无需清缓存）。

**方式 B：用旧版本发行包重装**

```bash
cd pcl-dsh-go-<oldver>-linux-amd64
sudo ./packaging/install.sh
```

**如果 data schema 已升级**：停止服务 → 用升级前备份恢复 `/var/lib/pcl-dsh-go` → `chown -R pcl-dsh:pcl-dsh` → 切回旧 release → 启动。恢复前建议把当前 data 目录改名保留（如 `*.broken-$(date +%s)`）以便事后分析。

回滚后自检同 `8.5。


## 10. 卸载

在发行的 `packaging/` 目录内（不需要服务在运行）：

```bash
cd pcl-dsh-go-1.0.0-linux-amd64
sudo ./packaging/uninstall.sh              # 删除程序/unit/Nginx site，保留 /var/lib/pcl-dsh-go
sudo ./packaging/uninstall.sh --purge-data # 额外删除数据目录与 pcl-dsh 用户（不可恢复！）
```

行为（与 `packaging/uninstall.sh` 对应）：

- `systemctl disable --now pcl-dsh-go`，删除 unit，`daemon-reload`；
- 删除 `/etc/nginx/sites-enabled|sites-available/pcl-dsh-go.conf`；若存在 `/etc/nginx/sites-available/default.pcl-dsh-go-disabled` 则移回 `/etc/nginx/sites-enabled/default`；`nginx -t` 通过则 reload；
- 删除 `<prefix>`（含 releases/current）；
- 默认**保留** `/var/lib/pcl-dsh-go`；加 `--purge-data` 才删除数据与 `pcl-dsh` 用户。
- 注意：`uninstall.sh` 没有 `--data-dir` 参数；若安装时用了自定义 `--data-dir`，`--purge-data` 只会删除默认 `/var/lib/pcl-dsh-go`，自定义数据目录需要手工删除/备份。

若发行包目录已丢失，可手工执行等价步骤（`systemctl disable --now pcl-dsh-go`、删 unit、删 Nginx site 并恢复默认站、`rm -rf /opt/pcl-dsh-go`；数据目录自行决定是否删除）。卸载前建议先备份数据。

## 11. 重置数据（危险操作）

用途：忘记管理员密码、数据损坏、干净重装验收。**会删除全部实例/runtime/设置/日志/凭据**。

```bash
cd pcl-dsh-go-1.0.0-linux-amd64
sudo ./packaging/reset.sh --yes       # 必须显式 --yes；默认 --data-dir /var/lib/pcl-dsh-go
# 服务会自动尝试重启；首次启动重新生成 initial-password
```

行为：`systemctl stop pcl-dsh-go` → `rm -rf <data-dir>` → `systemctl start pcl-dsh-go` → 输出提示；新初始密码在 `<data-dir>/initial-password`。

### 11.3 恢复备份（替代重置）

```bash
sudo systemctl stop pcl-dsh-go
sudo mv /var/lib/pcl-dsh-go /var/lib/pcl-dsh-go.broken-$(date +%s)
sudo tar -xzf /path/to/pcl-dsh-go-data-backup.tar.gz -C /var/lib
sudo chown -R pcl-dsh:pcl-dsh /var/lib/pcl-dsh-go
sudo systemctl start pcl-dsh-go
```

## 12. 部署验收清单（G9/G10 对应）

| # | 检查 | 命令/期望 |
| --- | --- | --- |
| 1 | 发布包校验 | `sha256sum -c *.tar.gz.sha256`、包内 `sha256sum -c SHA256SUMS` 通过 |
| 2 | 内置工具链 | `current/tools/node/bin/node -v` = v24.22.1；pnpm.cjs -v = 11.28.5；无系统 Node 也能跑 |
| 3 | 服务 | `systemctl is-active pcl-dsh-go` = active；`systemctl is-enabled` = enabled |
| 4 | health | `curl -sf 127.0.0.1:8080/api/health` 返回 200 JSON（status=UP） |
| 5 | :80 首页 | `curl -sI http://localhost/` 返回 200（SPA index.html） |
| 6 | 登录/首登 | initial-password 登录成功；强制改密；改密后新密码可登录、文件已删除 |
| 7 | 持久化 | `systemctl restart pcl-dsh-go` 后设置/实例/initial-password 状态保留（不重新生成） |
| 8 | 权限 | `stat -c '%a %U' /var/lib/pcl-dsh-go` = 700 pcl-dsh；initial-password = 600 |
| 9 | 沙箱 | `systemctl show pcl-dsh-go -p NoNewPrivileges -p ProtectSystem -p ReadWritePaths` 含配置值 |
| 10 | 安全日志 | `grep -R "token=\|password=" data/logs` 无明文敏感值；Nginx /api、/workbench access_log off |
| 11 | 升级/回滚 | 按第 8/9 节切换 version；data 不动；切回旧版可启动（schema 兼容） |
| 12 | 卸载保留数据 | `uninstall.sh` 后 `/var/lib/pcl-dsh-go` 仍在；默认站恢复；`--purge-data` 才删除 |

完整自动化验收由 qa-release 的 `scripts/wsl/m5-acceptance.sh` 与 `docs/evidence/m5/` 提供；本清单用于人工复核/故障基线。

### 12.1 本次 WSL + Nginx :80 实际部署实测（2026-10-24 18:34 +08:00）

做法：发行包 release 1.0.0（tar sha256 `3c33e7ff…9af0c`，69,329,669 B）+ `packaging/install.sh` 安装到默认 prefix/data。只读观测结果：

```text
systemctl is-active pcl-dsh-go      -> active
systemctl is-enabled pcl-dsh-go     -> enabled
GET http://127.0.0.1:8080/api/health -> 200 {"status":"UP","version":"1.0.0","uptimeSeconds":…}
GET http://127.0.0.1/               -> 200（Nginx :80 SPA）
GET http://127.0.0.1/api/health     -> 200（经 Nginx 反代到 manager）
/opt/pcl-dsh-go/current             -> /opt/pcl-dsh-go/releases/1.0.0
/var/lib/pcl-dsh-go                 -> drwx------ pcl-dsh:pcl-dsh（0700）
/var/lib/pcl-dsh-go/initial-password -> -rw------- pcl-dsh:pcl-dsh（25 B = 24 字符 + 换行）
监听                                  -> 127.0.0.1:8080（pcl-dsh-go）+ 0.0.0.0/:: :80（nginx）；8080 不对外
```

登录/首登改密门槛已由 Lead 在浏览器验证通过；完整验收证据见 `docs/M5_RESULTS.md` 与 `docs/evidence/m5/`。

## 13. CI（`.github/workflows/ci.yml`）

触发：push/PR 到 `main`、手动 `workflow_dispatch`；`concurrency` 同 ref 取消旧运行；只读权限。

| job | 内容 | 产物 |
| --- | --- | --- |
| `backend` | Go `gofmt -l .`（非空即失败）、`go vet ./...`、`go test ./... -count=1`、`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=1.0.0-ci"`，校验 ELF | manager 二进制 artifact |
| `frontend` | Node 24 + npm cache；`npm ci --no-audit --no-fund`、`npm run type-check`、`npm run build`；校验 dist 关键文件/JS/CSS/index 引用 | web dist artifact |
| `release-smoke` | 下载后端+前端产物；用**极小假 tools**（仅满足 build-release.sh 的存在性检查）跑 `scripts/build-release.sh`；校验发行包必须含 systemd unit、nginx site、install/uninstall/reset、`docs/M5_DEPLOY.md` 等关键文件；四个 shell 脚本 `bash -n`；`install.sh --help` 含 `--prefix/--no-service/--listen-port`、`uninstall.sh --help` 含 `--purge-data`、`reset.sh --help` 含 `--yes`；ELF 校验、`sha256sum -c SHA256SUMS`、tar.gz 与 `.sha256` 校验 | release tar.gz artifact |

说明：

- CI 不下载 200MB+ 的 Node/pnpm，也不跑真实安装（需要 root/systemd/Nginx）——真实打包/安装由 M5-A/M5-B 在 Linux/WSL 执行并由 qa 验收。
- 真实 DSH / Playwright E2E 需要 WSL + Edge + 真实 DSH，不在 GitHub Hosted Runner 上跑；E2E 见 `docs/M4_BUILD.md` 与 qa 的 M5 脚本。
- 本地等价命令：

```bash
# 后端
gofmt -l .
go vet ./...
go test ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=1.0.0" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go

# 前端（Linux/macOS；Windows 也可用 pwsh -File scripts/frontend-build.ps1）
cd web && npm ci --no-audit --no-fund && npm run type-check && npm run build && cd ..

# release 布局 smoke（与 CI 相同思路；真实打包需真实 tools）
bash -n scripts/build-release.sh
```


## 14. 构建发布包（`scripts/build-release.sh`）

前提：Linux/amd64 二进制（版本已注入）、`web/dist` 已构建、内置 tools 目录（默认 `/opt/pcl-dsh-go/tools`，含 `node/` 与 `pnpm/`）。

```bash
cd /mnt/d/Projects/DSHL/PCL-DSH-Go     # 开发机（WSL）；Linux 服务器同理

# 1) Go 二进制（版本必须注入，否则 build-release 会告警 version dev）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=1.0.0" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go

# 2) 前端 dist（Windows 用 PowerShell 脚本；Linux 可直接 npm）
pwsh -File scripts/frontend-build.ps1   # Windows
# cd web && npm ci --no-audit --no-fund && npm run type-check && npm run build   # Linux

# 3) 打包（必须在 WSL/Linux 运行：tools 是 Linux 二进制）
bash scripts/build-release.sh --version 1.0.0 \
  --tools /opt/pcl-dsh-go/tools \
  --out release
```

参数（`bash scripts/build-release.sh --help`）：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--version <ver>` | `1.0.0` | 发行版本号（写入 VERSION/目录名/tar 名） |
| `--tools <dir>` | 环境变量 `PCL_DSH_TOOLS_DIR` 或 `/opt/pcl-dsh-go/tools` | 内置工具链根（需含 node/ 与 pnpm/） |
| `--out <dir>` | 环境变量 `PCL_DSH_RELEASE_DIR` 或 `<repo>/release` | 输出目录 |
| `-h, --help` | — | 帮助 |

脚本做什么：检查 `bin/pcl-dsh-go`（ELF/version 告警）、`web/dist/index.html`、`tools/node/bin/node`、`tools/pnpm` → 组装 `bin/static/tools/packaging/docs/README/LICENSE/LICENSES/THIRD_PARTY_NOTICES/VERSION/BUILD_INFO` → 生成包内 `SHA256SUMS` 并自检 → 生成 `pcl-dsh-go-<ver>-linux-amd64.tar.gz` 与 `.sha256` → 打印 `package_dir/tarball/sha256/files`。

注意：

- `release/` 是构建产物，已在 `.gitignore` 中忽略（`/release/`）；不要提交。实际 release 1.0.0 参考：`pcl-dsh-go-1.0.0-linux-amd64.tar.gz` 69,329,669 B（≈66.1 MiB），sha256 `3c33e7ff77e3215b2a91cdbd3191a8480a856ae469c5e0479c3e4cbd2299af0c`。
- 打包脚本会把本文档与 `docs/ACCEPTANCE.md` 复制到发行包 `docs/`，README/LICENSE/LICENSES 也会带上；发布前确认 README 已更新到 M5。
- 真实 CI 版本由 M5-A/Lead 在 Linux 环境构建；GitHub CI 的 release-smoke 仅用假 tools 校验布局与校验和（见第 13 节）。

## 15. 常见问题（FAQ）

### 15.1 Windows 浏览器打不开 `http://localhost/`

1. WSL 内确认 Nginx 在监听：`ss -ltnp | grep ':80 '`、`curl -sI http://127.0.0.1/`。
2. 确认 systemd 正在管理：`systemctl is-active nginx pcl-dsh-go`。
3. Windows 侧用 `curl.exe -I http://localhost/` 自检；WSL2 通常自动做 localhost 转发。若失败：`wsl --shutdown` 后重开（注意会停掉 WSL 内所有服务），或确认 WSL 版本/`.wslconfig` 未关闭 localhost 转发。
4. 兜底：用 `wsl hostname -I` 的 WSL IP 访问 `http://<wsl-ip>/`（仅本机/局域网测试），或按需做 `netsh interface portproxy`（生产不建议）。

### 15.2 `:80` 被占用 / 默认站点冲突

`install.sh` 会把 `sites-enabled/default` 移到 `sites-available/default.pcl-dsh-go-disabled` 再启用本 site；若 80 被**其他进程**（另一个 Nginx 实例、Apache、开发服务器）占用，先停用占用者或改 `--listen-port 8088` 安装。`ss -ltnp | grep ':80 '` 查占用。

### 15.3 页面 502 Bad Gateway

Nginx 到管理器链路失败：`systemctl status pcl-dsh-go`、`curl -sf http://127.0.0.1:8080/api/health`、`journalctl -u pcl-dsh-go -n 100`。常见原因：manager 未启动/崩溃、`--port` 与 Nginx 渲染的 `@PORT@` 不一致、单元沙箱导致启动失败。

### 15.4 管理器启动失败（systemd）

`sudo journalctl -u pcl-dsh-go -n 100 --no-pager` 看第一处错误。常见：data 目录权限/属主不对（应 `pcl-dsh:pcl-dsh 0700`）、端口被占、`--pcl.tool-root` 指向不存在目录、`ProtectSystem=strict` 下写到了数据目录之外。修复后用 `systemctl restart`。

### 15.5 初始密码文件不存在 / 忘记密码

- 非首次安装（已有 `admin.json` 且 `initialPasswordPending=false`）不会再生成，属预期。
- 完全忘记：只能用 `reset.sh --yes` 重置数据（见第 11 节），或用第 11.3 节恢复 `config/admin.json` 备份；M5 不提供不丢数据的直接改密工具。

### 15.6 `8080` 被占用 / 想换端口

安装时用 `--port 8090`（会同时渲染 systemd 与 Nginx 反代端口）；若已安装，重跑 `install.sh --port 8090`。不要把 8080 暴露到公网。

### 15.7 升级后启动报 schemaVersion 不支持

说明当前 data 由更新版本写过、旧/新二进制不兼容该 schema。处理：回滚到写入该数据的版本，或恢复升级前数据备份（第 9/11.3 节）。跨 major/schema 变更前一定先备份。

### 15.8 安装 DSH runtime 失败/很慢

runtime 安装在 `data/runtimes`，走 npm registry + 内置 pnpm；检查服务器网络/设置里的 registry/代理；失败后可在 WebUI 运行时页重试。磁盘不足时先清理旧 runtime（UI 删除）与 `data/cache/pnpm-store`。

### 15.9 日志在哪里？为什么 journal 里没有 token？

应用日志：`<data>/logs/manager-*.log`、审计 `audit-*.log`（按日、脱敏）；`journalctl` 只有 stdout/stderr。token/密码/cookie 不会写入日志或 Nginx access log（`access_log off`）。

### 15.10 重启服务后实例没了

管理器退出时会停止自有 DSH 进程（不会自动重启实例）。重启后在 WebUI 重新启动实例；数据/配置保留。

### 15.11 卸载后想彻底清理

`sudo ./packaging/uninstall.sh --purge-data`（会删除数据目录与 `pcl-dsh` 用户；先备份）。仅删程序时用默认 `uninstall.sh`，数据保留在 `/var/lib/pcl-dsh-go`。

### 15.12 `release/` 或 `web/dist` 被误提交

这些是构建产物（应被 gitignore/不应提交）：从提交中移除并保持远端干净；重新打包即可。`web/dist` 与 `release/` 均已在 gitignore 中。

## 16. 附录

### 16.1 命令速查

| 目的 | 命令 |
| --- | --- |
| 安装 | `sudo ./packaging/install.sh` |
| 状态/日志 | `systemctl status pcl-dsh-go`、`journalctl -u pcl-dsh-go -f` |
| 重启 | `sudo systemctl restart pcl-dsh-go` |
| health | `curl -sf http://127.0.0.1:8080/api/health` |
| 首页 | `curl -sI http://localhost/` |
| Nginx 校验/重载 | `sudo nginx -t && sudo systemctl reload nginx` |
| 升级 | 解包新 release → `sudo ./packaging/install.sh` |
| 回滚 | `sudo ln -sfn <prefix>/releases/<old> <prefix>/current && sudo systemctl restart pcl-dsh-go` |
| 卸载（留数据） | `sudo ./packaging/uninstall.sh` |
| 重置数据 | `sudo ./packaging/reset.sh --yes` |
| 校验包 | `sha256sum -c SHA256SUMS` |
| 内置工具版本 | `current/tools/node/bin/node -v`、pnpm.cjs -v |

### 16.2 文件/权限矩阵

| 路径 | 权限/属主 | 说明 |
| --- | --- | --- |
| `/opt/pcl-dsh-go` | 0755 root:root | 安装前缀 |
| `/opt/pcl-dsh-go/releases/<ver>` | 0755 root:root | 只读发行目录 |
| `/opt/pcl-dsh-go/current` | symlink root:root | 指向当前 version（升级/回滚切换点） |
| `/var/lib/pcl-dsh-go` | 0700 pcl-dsh:pcl-dsh | 数据根 |
| `/var/lib/pcl-dsh-go/initial-password` | 0600 pcl-dsh:pcl-dsh | 首次密码（改密后删除） |
| `/var/lib/pcl-dsh-go/config/admin.json` | 0600 | 管理员 BCrypt 凭据 |
| `/var/lib/pcl-dsh-go/logs` | 0700 | manager/audit 日志 |
| `/etc/systemd/system/pcl-dsh-go.service` | 0644 root:root | systemd unit |
| `/etc/nginx/sites-available/pcl-dsh-go.conf` | 0644 root:root | Nginx site |
| `/etc/nginx/sites-enabled/pcl-dsh-go.conf` | symlink | 启用 |

### 16.3 systemd unit 关键字段（渲染后）

```ini
[Unit]
Description=PCL-DSH-Go Manager (DeepSeek Harness)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=pcl-dsh
Group=pcl-dsh
WorkingDirectory=/opt/pcl-dsh-go/current
Environment=HOME=/var/lib/pcl-dsh-go
ExecStart=/opt/pcl-dsh-go/current/bin/pcl-dsh-go \
  --server.address=127.0.0.1 --server.port=8080 \
  --pcl.data-root=/var/lib/pcl-dsh-go \
  --pcl.tool-root=/opt/pcl-dsh-go/current/tools \
  --server.static-dir=/opt/pcl-dsh-go/current/static
Restart=on-failure
RestartSec=3
TimeoutStopSec=45
LimitNOFILE=65536
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/pcl-dsh-go
```

### 16.4 变更记录

| 日期 | 版本 | 说明 |
| --- | --- | --- |
| 2026-10-24 | M5-D 初稿 | 随 release 1.0.0 发布：安装/升级/回滚/卸载/重置、systemd/Nginx、初始密码、CI 与打包说明 |
| 2026-10-24 | M5-D 修订 | 对齐最终脚本：`--no-service` 隔离安装、Nginx default 站备份路径改为 `sites-available/default.pcl-dsh-go-disabled`、release 布局含 `packaging/systemd/`、`release/` 已 gitignore、补充 WSL :80 实际部署证据 |