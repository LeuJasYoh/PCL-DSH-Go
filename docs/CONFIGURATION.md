# 配置参考

本文列出 PCL-DSH-Go 当前源码的启动参数、环境变量、数据目录、端口与运行实例上限，项目版本见 [`VERSION`](../VERSION)。部署步骤见 [部署与运维](DEPLOYMENT.md)，WebUI 内设置见 [用户指南 · 设置](USER_GUIDE.md#11-设置)。

## 1. 启动参数

命令行参数（也支持单横线形式，如 `-server.port`）：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--server.address` | `127.0.0.1` | HTTP 监听地址。默认仅回环；公网入口应由 Nginx 提供，不建议改为 `0.0.0.0` |
| `--server.port` | `8080` | HTTP 监听端口（1–65535）。需与 Nginx 反代端口一致 |
| `--server.static-dir` | 空 | WebUI 构建产物目录（如 release 的 `static/`）。为空时只提供内置占位页；生产可用 Nginx 或此项由 Go 直接托管 |
| `--pcl.data-root` | `./data` | 数据目录。环境变量 `PCL_DSH_DATA` 可兜底；命令行优先 |
| `--pcl.tool-root` | 空 | 内置 Node/pnpm 的 tools 根目录；为空时依次查找 `<tool-root>` → `<data-root>/tools` → `PATH` |
| `--pcl.max-running-instances` | `0` | 同时运行的 DSH 实例上限：`0` = 不限制；`>0` 为上限；负数启动报错 |
| `--pcl.dev-mode` | `false` | 启用调试接口（仅开发/测试；生产保持关闭） |
| `--version` | — | 打印构建时注入的项目版本并退出 |
| `-h` / `--help` | — | 打印帮助并退出 |

示例：

```bash
/opt/pcl-dsh-go/current/bin/pcl-dsh-go \
  --server.address=127.0.0.1 --server.port=8080 \
  --pcl.data-root=/var/lib/pcl-dsh-go \
  --pcl.tool-root=/opt/pcl-dsh-go/current/tools \
  --server.static-dir=/opt/pcl-dsh-go/current/static \
  --pcl.max-running-instances=1
```

## 2. 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PCL_DSH_DATA` | 空 | data-root 兜底（命令行 `--pcl.data-root` 优先） |
| `PCL_DSH_LOG_LEVEL` | `info` | 应用日志级别：`debug` / `info` / `warn` / `error` |

release 打包脚本另识别 `PCL_DSH_TOOLS_DIR` / `PCL_DSH_RELEASE_DIR`（仅构建期使用，不属于运行时配置）。

优先级：命令行参数 > 环境变量 > 内置默认值。

## 3. 数据目录布局

```text
<data-root>/
  config/
    manager.json        # schemaVersion 1：selectedInstanceId + settings(proxy/registry/directAccessSshUser)
    instances.json      # schemaVersion 1：实例列表（id/name/version/profile/port/createdAt/updatedAt）
    admin.json          # schemaVersion 1：管理员 bcrypt-sha256 密码哈希 + initialPasswordPending
    compatibility.json  # 可选：运行时能力人工声明
  instances/<id>/{dsh-home,workspace,agents-home}
  runtimes/<semver>/    # 由内置 pnpm 安装的 DSH 运行时
  cache/{pnpm-store,npm,registry-metadata,probes,uploads,exports,pack-staging}
  logs/{manager,audit}-YYYY-MM-DD.log
  trash/                # 删除实例/运行时的回收目录
  initial-password      # 首启生成；改密后删除
  .lock                 # 单管理器进程 flock
```

- 目录权限 0700；`initial-password` 与 `config/admin.json` 为 0600。
- DSH 子进程以实例 `workspace` 为 `HOME` 和工作目录；管理器在 `dsh-home/cordis.patch.yml` 配置默认工作区为 `workspace/deepseek-harness/default-workspace`，已有自定义 `documentsDirectory` 不会被覆盖。
- systemd 默认仅放开 data-root 写入；目录选择器可浏览的宿主机路径不等于可写路径，不要把 `/root`、`/home` 或 release 目录当作实例工作区。
- 数据目录与 release 分离，升级/回滚不会改动数据；备份建议见 [DEPLOYMENT.md](DEPLOYMENT.md#12-备份与恢复)。
- 首次启动自动创建；损坏的 JSON 会明确报错而不是静默覆盖。

## 4. 端口

| 端口 | 用途 | 暴露范围 |
| --- | --- | --- |
| `80`（或 `--listen-port`） | Nginx 对外入口（SPA/API/工作台反代） | 公网/内网入口 |
| `8080`（或 `--server.port`） | 管理器 HTTP | 仅回环（Nginx 反代） |
| `3080+`（自动分配） | DSH 实例监听 | 仅回环（由管理器/反代访问） |

不要把 `8080` 暴露到公网；实例端口由创建实例时自动分配（3080 起），也可手动指定 1024–65535 的空闲端口。

## 5. 工具链（Node/pnpm）

- 发布包内置 Node 24.22.1（`tools/node/bin/node`）与 pnpm 11.28.5（`tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`），运行时/插件安装全部使用内置工具链，不依赖系统 Node/npm/pnpm/JDK。
- 查找顺序：`--pcl.tool-root` → `<data-root>/tools` → `PATH`（仅兜底）。生产建议始终显式传 `--pcl.tool-root`（install.sh 已配置）。
- 版本探测：启动时执行 `node -v` 与 `node <pnpm.cjs> -v`（15 秒超时），失败会给出尝试路径。
- 自定义工具链时保持目录结构：`<root>/node/bin/node`、`<root>/pnpm/node_modules/pnpm/bin/pnpm.cjs`（或 `<root>/pnpm/bin/pnpm.cjs`）。

## 6. 运行实例上限与资源建议

`--pcl.max-running-instances` 控制同时运行的 DSH 实例数：

| 值 | 行为 |
| --- | --- |
| `0`（默认） | 不限制（是否够用取决于主机资源） |
| `N > 0` | 最多同时运行 N 个；达到上限时启动返回 `409 CONFLICT_RUNNING_INSTANCE`，提示当前上限 |
| 负数 | 启动时报参数错误 |

每个 DSH 实例为独立 Node 进程，常驻内存约 300–400 MiB（另有工作台会话/SSE 开销），再加上管理器与缓存。建议：

| 主机规格 | 建议上限 |
| --- | --- |
| 2 vCPU / 1.6 GiB | `1`（显式设置，避免内存压力） |
| 4 GiB 左右 | `1–2`（先压测） |
| 8 GiB+ | `2–3` 或按实测 |
| 仅测试/大内存 | `0`（不限制） |

安装时设置：`sudo ./packaging/install.sh --max-running-instances 1`；已安装环境可重跑 install.sh 或编辑 systemd unit 的 `ExecStart`（见 [DEPLOYMENT.md](DEPLOYMENT.md#6-服务管理systemd)）。

## 7. WebUI 设置（持久化）

WebUI「设置」页保存于 `config/manager.json`：

| 设置 | 校验规则 |
| --- | --- |
| npm registry | `http(s)://` 地址，不允许带用户名/密码；默认 `https://registry.npmjs.org` |
| HTTP 代理 | `http://host:port`，不允许带用户信息；空表示直连 |
| 直连登录 SSH 用户 | `[A-Za-z0-9._-]{1,32}`，默认 `root` |

修改后新任务生效；非法值会被拒绝并提示。

## 8. WebUI 静态资源

两种部署方式（二选一或并用）：

1. **Nginx 提供静态资源**（推荐生产）：Nginx `root` 指向 `/opt/pcl-dsh-go/current/static`，`/api/` 与 `/workbench/` 反代到管理器；管理器可不传 `--server.static-dir`。
2. **Go 直接托管**：启动参数传 `--server.static-dir=<release>/static`，管理器对非 API/非工作台路径返回 SPA（含 `index.html` fallback、静态资源 MIME 与缓存头）；适合无 Nginx 或单进程部署。

## 9. 运行方式示例（systemd）

渲染后的 unit 关键行（`@VAR@` 由 install.sh 替换）：

```ini
[Service]
User=pcl-dsh
WorkingDirectory=/opt/pcl-dsh-go/current
Environment=HOME=/var/lib/pcl-dsh-go
ExecStart=/opt/pcl-dsh-go/current/bin/pcl-dsh-go \
  --server.address=127.0.0.1 --server.port=8080 \
  --pcl.data-root=/var/lib/pcl-dsh-go \
  --pcl.tool-root=/opt/pcl-dsh-go/current/tools \
  --server.static-dir=/opt/pcl-dsh-go/current/static \
  --pcl.max-running-instances=0
Restart=on-failure
TimeoutStopSec=45
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/pcl-dsh-go
```

其它 systemd 沙箱项与服务操作见 [DEPLOYMENT.md](DEPLOYMENT.md#6-服务管理systemd)。

## 10. 数据 schema 与兼容性

- 管理器主要配置文件使用 `schemaVersion: 1`；管理器启动时校验，遇到不支持版本会明确报错，不会静默改写。
- `admin.json.passwordHash` 新值使用 `bcrypt-sha256:` 前缀，旧 BCrypt 哈希仍可读取，改密后写入新格式；schemaVersion 未变不代表旧程序能读新哈希。
- 升级前请备份数据；跨 schema 或密码哈希格式回滚需要恢复兼容的旧数据（见 [DEPLOYMENT.md](DEPLOYMENT.md#9-回滚)）。
- 不兼容旧版 PCL-DSH（其他管理器）的 data-root，也不做自动迁移。

## 11. 环境变量与日志

- 应用日志：`<data>/logs/manager-YYYY-MM-DD.log`（stdout 同步），审计日志 `audit-YYYY-MM-DD.log`；按天切换、单行 JSON、自动脱敏。
- 日志级别通过 `PCL_DSH_LOG_LEVEL` 控制（默认 `info`）；也可用 `journalctl -u pcl-dsh-go` 查看服务输出。
- 日志 tail 在 WebUI「任务与日志」页（默认 800 行，最大 2000）或 API `GET /api/logs/tail?lines=800`。
