# PCL-DSH-Go M5 接口冻结（发行/部署）

> 状态：M5 启动冻结（2026-10-24）。范围：release 打包、install/uninstall/reset、systemd、Nginx、干净环境验收（G9/G10）、CI/文档收尾。
> 部署目标：用户指定的 **WSL 实际部署**（Nginx 监听 `:80`，Windows 浏览器 http://localhost/ 可访问）。

## 1. Release 布局（版本 1.1.0）

```
pcl-dsh-go-1.0.0-linux-amd64/
  bin/pcl-dsh-go                 # linux/amd64，version=1.0.0，trimpath/-buildvcs=false
  static/                        # web/dist（index.html + assets/ + licenses/ + favicon.webp）
  tools/node/                    # 内置 Node 24.22.1（含 bin/node）
  tools/pnpm/                    # 内置 pnpm 11.28.5（tools.Locate 识别的目录布局）
  packaging/install.sh uninstall.sh reset.sh README.md
  packaging/systemd/pcl-dsh-go.service   # unit 模板（@PREFIX@/@DATA_DIR@/@PORT@）
  packaging/nginx/pcl-dsh-go-site.conf   # site 模板（@PREFIX@/@PORT@/@LISTEN_PORT@）
  packaging/nginx/pcl-dsh-go.conf        # M2 手工运维模板（历史参考）
  docs/{README.md,M5_DEPLOY.md} LICENSE LICENSES/ THIRD_PARTY_NOTICES.md VERSION BUILD_INFO
SHA256SUMS                       # 包文件校验（tar.gz 与目录清单）
```

## 2. 安装契约

- 安装前缀 `/opt/pcl-dsh-go`：`releases/1.0.0/` + `current -> releases/1.0.0` 软链；`install.sh` 支持 `--prefix`（默认 /opt/pcl-dsh-go）与 `--data-dir`（默认 /var/lib/pcl-dsh-go）。
- 系统用户/组 `pcl-dsh`（无登录 shell），数据目录 `/var/lib/pcl-dsh-go`（0700，属主 pcl-dsh），日志 `<data>/logs`。
- `install.sh`：解包/复制 release → 创建用户/目录 → 写 systemd unit → 写 Nginx site（`/etc/nginx/sites-available/pcl-dsh-go` + enabled 软链，默认站冲突时备份并禁用）→ `nginx -t` → enable/start manager → 首次启动生成 initial-password（0600）→ 打印访问地址与初始密码路径。
- `systemd` unit：`User=pcl-dsh`、`WorkingDirectory=/opt/pcl-dsh-go/current`、`ExecStart=/opt/pcl-dsh-go/current/bin/pcl-dsh-go --server.address=127.0.0.1 --server.port=8080 --pcl.data-root=/var/lib/pcl-dsh-go --pcl.tool-root=/opt/pcl-dsh-go/current/tools --server.static-dir=/opt/pcl-dsh-go/current/static`；`Restart=on-failure`；沙箱：`NoNewPrivileges=yes`、`PrivateTmp=yes`、`ProtectSystem=strict`、`ProtectHome=yes`、`ReadWritePaths=/var/lib/pcl-dsh-go`、`LimitNOFILE=65536`；停机 `TimeoutStopSec=45`（覆盖 30s 实例停止）。
- `uninstall.sh` 保留数据目录（默认）；`reset.sh` 停止服务并要求显式 `--yes` 后删除数据。
- `SHA256SUMS` 供 `sha256sum -c` 校验；安装脚本拒绝非 root（或提示 sudo）。

## 3. Nginx 契约

- 监听 80（WSL 实际部署；生产由运维按需加 TLS；本里程碑不做应用层 HTTPS）。
- `root <prefix>/current/static`；SPA fallback `try_files $uri $uri/ /index.html`；`index.html`/SPA no-store，`/assets/` 1y immutable（与 Go 静态服务一致）。
- `location ^~ /api/` 与 `^~ /workbench/` 反代 `127.0.0.1:8080`：HTTP/1.1、Host 保留端口（`map $http_host`）、`proxy_buffering off`（SSE）、WebSocket Upgrade、`client_max_body_size 2g`、`proxy_read_timeout 3600s`、`access_log off`（token 不入日志）。
- 安全头与错误体：`server_tokens off`；代理错误返回 JSON 四字段。

## 4. G9/G10 验收矩阵（M5）

| 项 | 判据 |
| --- | --- |
| 无系统依赖 | 安装/登录/建实例/启动不依赖系统 node/npm/pnpm/JDK（仅内置 tools）；`node -v`/`pnpm -v` 由管理器进程调用成功 |
| 安装 | `install.sh` 成功、systemd active、`/api/health` 200、`:80` 首页 200 |
| 登录/首登 | initial-password 登录 → 首登改密 → 新密码登录；文件删除 |
| 实例 | 创建/启动（内置 pnpm 安装 runtime 或复用已装）→ `/workbench/{id}/` 200；停止后端口释放 |
| 升级/回滚 | 安装同版本新 release 切换 current（data 不动）；切回旧 release 可启动（文档说明 schema 兼容性） |
| 持久化 | 重启 systemd/服务后数据/实例/设置保留；initial-password 不重复生成 |
| 权限 | data 0700、initial-password/artifacts 0600、systemd 沙箱生效 |
| 校验 | `sha256sum -c SHA256SUMS` 通过；包不含构建产物外的数据/密钥 |
| 安全 | 日志/审计无 token/密码；access_log off；Windows DSH/参考仓库未改 |

## 5. WSL 实际部署（用户访问）

- 安装到 `/opt/pcl-dsh-go`，服务名 `pcl-dsh-go.service`，Nginx site 监听 `:80`；Windows 浏览器访问 **http://localhost/**（WSL 已确认 :80 可从 Windows 访问，默认站返回 200）。
- 初始管理员密码在 `/var/lib/pcl-dsh-go/initial-password`（root 可读）；首次登录后必须改密。
- 首次启动后可经 WebUI「下载」页安装 DSH `0.2.1-rc.3`（内置 pnpm 联网）+ 创建/启动实例；工作台经 `/workbench/{id}/`。

## 6. 任务与所有权

| 任务 | owner | scope |
| --- | --- | --- |
| M5-A release 构建 + 打包 | Lead | scripts/build-release.sh、packaging/ |
| M5-B WSL :80 实际部署 | Lead | WSL /opt、systemd、/etc/nginx |
| M5-C 独立验收脚本 + 干净重装验证 | qa-release | scripts/wsl/m5-acceptance.sh、docs/evidence/m5/ |
| M5-D CI + 发布文档 | backend-core | .github/workflows/、docs/M5_DEPLOY.md、README M5 |
| M5-E M5 报告 | qa-release | docs/M5_RESULTS.md |
| M5-F Lead 最终验收/提交/汇报 | Lead | docs/、README、commit |
## 7. WSL 实际部署记录（2026-10-24，Lead）

- 安装：release 1.0.0（tar.gz 69,322,728 B，sha256 `abce7d56dd414e67f49deab1c5cb03fa27f858ffac268e881ea09d6bf7107326`）→ `install.sh` 默认路径；`/opt/pcl-dsh-go/current -> releases/1.0.0`，数据 `/var/lib/pcl-dsh-go`（0700，`pcl-dsh`），初始密码文件 0600。
- 服务：`pcl-dsh-go.service` active/enabled（NoNewPrivileges/ProtectSystem=strict/ReadWritePaths=/var/lib/pcl-dsh-go）；manager 仅 `127.0.0.1:8080`；Nginx `:80`（IPv4+IPv6）为唯一入口；旧 `sites-enabled/default` 已备份到 `/etc/nginx/sites-available/default.pcl-dsh-go-disabled`。
- 访问：Windows Edge `http://localhost/` 与 `http://127.0.0.1/` 均 200（登录页 title=登录 · PCL-DSH-Go、0 console error）；`/api/health` version=1.0.0；匿名 `/api/state` 401；登录（初始密码）204 且 `/api/state` 403 PASSWORD_CHANGE_REQUIRED（首登门槛）；未代用户改密。
- 持久化：`systemctl restart pcl-dsh-go` 后 active、health/首页 200、initial-password 哈希不变（数据保留）。
- 已知环境差异：Windows .NET `Invoke-WebRequest http://localhost/` 因 IPv6 首选可能超时；Edge/curl 正常，备用 `http://127.0.0.1/`。
## 8. M5.1 增补契约（2026-10-24）

- 配置：`--pcl.max-running-instances int`，默认 **0（不限制）**；正数为显式上限；负数启动失败；其他不变。
- launch：同步预检与 app 共用上限；达到上限时 409 `CONFLICT_RUNNING_INSTANCE`，message 包含当前上限与参数名；同实例已运行仍 `CONFLICT_BUSY`，端口冲突/未安装 runtime 语义不变。
- 安装：`install.sh --max-running-instances N` 渲染 systemd `ExecStart --pcl.max-running-instances=N`；默认 0（不限制）；修改后 `systemctl daemon-reload && systemctl restart pcl-dsh-go` 生效。
- 错误码/事件/其它 API 不变；实例数据 schema 不变；单管理器进程锁（flock）不变。