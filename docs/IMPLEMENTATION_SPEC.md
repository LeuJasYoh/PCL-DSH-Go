# PCL-DSH-Go 实现规格（v1.0）

> **权威顺序**：本规格 > `docs/DEVELOPMENT_PLAN.md`（目标/里程碑）> `docs/DSH_INTEGRATION.md`（DSH 契约细节）> `docs/UI_MIGRATION.md`（前端）> `docs/ACCEPTANCE.md`（验收）。
> 当本规格与参考实现（`D:\Projects\DSHL\PCL-DSH-Linux`）冲突时，以**本规格 + 验收测试**为准；参考实现用于理解既有行为，但**不得修改参考仓库**。
> 实现语言：Go 1.27+（Windows 开发机可用 `go1.27.3`）；目标：Linux x86_64。前端：复用参考仓库的 Vue 3 + TS 代码并瘦身。

---

## 0. 任务与边界

### 0.1 要实现什么

一个全新的 Go 编写的 DSH 管理器（WebUI），范围：
- 登录/会话/CSRF/改密；
- 实例管理（列表/创建/编辑名称与端口/删除/选择/启动/停止/状态）；**版本与 profile 创建后不可变**；
- 运行时（DSH npm 版本查询、安装、探测、删除引用保护）；
- `/workbench/{id}/` 反向代理（HTTP/SSE/WebSocket）；
- 直连登录（SSH 隧道命令 + 一次性 token URL，SSH 用户可配置）；
- 任务/SSE/日志；
- 设置（npm registry、代理、直连 SSH 用户）；
- 整合包（预览/导入/导出/artifact）；
- 已装插件（列表/安装/更新/移除）；
- 健康检查、发布包/安装脚本/Nginx/systemd；
- 测试与验收（真实 DSH 0.2.1-rc.3，WSL）。

### 0.2 明确不做（重要）

- **账户系统**：不做 API Key 管理、Provider 预设、AES-GCM 账户加密、secret.key、实例账户绑定、启动 `--patch`/overlay/account.validation.txt；删除参考实现中所有账户相关代码/API/UI/测试。
- **社区插件目录**：不做 GitHub 检索/目录页/安装目标解析。
- **背景图/外观**：不做背景上传/切换/5 张背景管理；保留一个默认视觉背景即可（前端可固定使用现有默认图）。
- **旧数据兼容**：不读取/不迁移/不删除旧 PCL data-root；新 data-root 全新 schema。
- **服务器端源码 clone/编译**：不实现 GitHub clone/build；只走 npm + pnpm；未来预构建 runtime 导入仅为可选设计，本期不实现。
- **桌面版遗产**：桌面安装包下载、Windows 分支、HDSL 1:1 文案依赖。
- 多实例并行、ARM64、应用层 HTTPS、2FA。

### 0.3 不可触碰的红线

1. **禁止修改 Windows DSH 任何文件**：`D:\DevelopSoftware\DSH\**`、`C:\Users\<用户>\.dsh\**`、Windows 上 DSH 管理的实例/配置/缓存/当前运行的 DSH。只读参考，不安装/升级/重启。
2. **禁止修改参考仓库** `D:\Projects\DSHL\PCL-DSH-Linux` 的源码/文档/构建产物；它只是**只读参考**。允许把前端/文档复制到本仓库（GPL 合规）。
3. **禁止在 Windows 上运行/安装本管理器**；所有运行验证在 WSL/Linux。
4. **WSL 使用范围**：可用于构建/运行/真实 DSH 集成测试；真实 DSH 测试必须用 runtime 副本与独立 data-root（`/tmp/pcl-dsh-go-test`），不得修改 `/opt` 下既有部署。
5. **许可**：GPL-3.0-or-later + HMCL §7；保留 `LICENSE`/`LICENSES/`/`THIRD_PARTY_NOTICES.md` 与版权声明。
6. **禁止伪造验收**：所有“通过”必须有命令输出/日志/截图/测试报告；不允许 mock 掉真实 DSH 验收。

---

## 1. 环境与工具链

| 项 | 值 |
| --- | --- |
| 开发机 | Windows 11 + PowerShell；Go 1.27.3；Git；Node/pnpm 仅用于构建前端，不用于管理器运行 |
| 验证环境 | WSL2 Ubuntu 26.04.1；Node v24.22.1（`/opt/pcl-dsh-go/tools/node`）；pnpm 11.28.5（`/opt/pcl-dsh-go/tools/pnpm`）；Nginx 1.28.3；Go 可在 Windows 交叉编译 |
| 目标部署 | Linux x86_64，HTTP + Nginx + systemd，单用户，最多 1 个 DSH 实例 |
| DSH 基线 | `@deepseek-ai/dsh@0.2.1-rc.3`（若 npm 无此版本，回退最近可用版本并在验收记录中注明） |
| Node | 24.22.1（来自 `D:\Projects\DSHL\node-v24.22.1-linux-x64.tar.xz`） |
| pnpm | 11.28.5（单一版本；若 rc.3 不兼容再回退 10.34.0，不双版本） |
| 前端构建 | Node 24 + npm（构建期）；Vite 7 / Vue 3.5 / TS 5.9 |

**构建命令（示例）**：
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=0.1.0" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
```
Windows 开发时可用 `go test ./...`（Linux 专用文件用 build tags 隔离）；真实集成/Linux 行为在 WSL 验证。

---

## 2. 仓库结构（目标）

```text
cmd/pcl-dsh-go/main.go
internal/config/            # flags/env/路径/版本
internal/logging/           # slog、日志文件、审计、脱敏
internal/store/             # manager.json / instances.json / admin.json，原子写 + flock
internal/tools/             # 定位/解压/校验 Node、pnpm、调用 pnpm
internal/runtime/           # npm 元数据、安装、探测、启动命令/环境
internal/process/           # spawn、进程组、stdout token、就绪、停止
internal/server/            # HTTP server、中间件、auth/csrf、handlers、SSE、proxy、upload、static
internal/proxy/             # /workbench HTTP/SSE/WS（可并入 server）
internal/pack/              # 安全归档、dspack/Overture 预览/导入/导出、artifact
internal/plugin/            # 已装插件列表/变更（Node 桥）
internal/directaccess/      # SSH 隧道命令 + token URL
web/                        # 复用并瘦身后的 Vue 工程（构建产物进 release/static）
docs/                       # 本手册与计划
packaging/                  # install/uninstall/reset/systemd/Nginx 模板
```

**依赖白名单**（尽量少；新增需说明理由）：
- 标准库：`net/http`、`crypto/*`、`encoding/json`、`archive/zip`、`archive/tar`、`compress/gzip`、`os/exec`、`syscall`、`log/slog`、`flag`、`embed` 等。
- `golang.org/x/crypto/bcrypt`（管理员密码）。
- `github.com/gorilla/websocket`（WebSocket 反代/客户端）。
- `gopkg.in/yaml.v3`（Overture snapshot `profile.yaml`）。
- 可选用 `golang.org/x/sys/unix`（flock；也可用 `syscall.Flock`）。
- 不引入 Spring/DI/ORM/模板引擎；不做不必要的框架替换。

---

## 3. 数据模型（新 schema，data-root 全新）

```text
<data-root>/
  config/manager.json
  config/instances.json
  config/admin.json
  initial-password                 # 首次启动生成；改密后删除
  config/compatibility.json        # 可选：DSH 能力人工声明（原样支持）
  instances/<id>/{dsh-home,workspace,agents-home}
  runtimes/<semver>/               # pnpm 安装或预置
  cache/{pnpm-store,npm,registry-metadata,probes,uploads,exports,pack-staging}
  logs/{manager,audit}-YYYY-MM-DD.log
  trash/
```

### 3.1 manager.json
```json
{
  "schemaVersion": 1,
  "selectedInstanceId": "i-xxxxxxxxxxxx",
  "settings": {
    "proxy": "",
    "registry": "https://registry.npmjs.org",
    "directAccessSshUser": "root"
  }
}
```
- 字段默认：`proxy=""`、`registry="https://registry.npmjs.org"`、`directAccessSshUser="root"`。
- 校验：registry 必须 http(s)、无 userinfo；proxy 必须 `http://host:port`、无 userinfo；sshUser 只允许 `[A-Za-z0-9._-]+`。

### 3.2 instances.json
```json
{
  "schemaVersion": 1,
  "instances": [
    {
      "id": "i-xxxxxxxxxxxx",
      "name": "工作实例",
      "version": "0.2.1-rc.3",
      "profile": "web",
      "port": 3080,
      "createdAt": 1750000000000,
      "updatedAt": 1750000000000
    }
  ]
}
```
- id：`i-` + 12 位十六进制（随机）。
- name：1–40 字符，去首尾空白，不允许控制字符/路径分隔符。
- version：精确 SemVer；profile：`[A-Za-z0-9._-]+`；port：1–65535 或 null（自动分配）。
- 版本/profile 创建后不可变；编辑只允许 name/port。
- 原子写：写临时文件 → fsync → rename；启动时校验 schemaVersion；flock 防双进程。

### 3.3 admin.json
```json
{
  "schemaVersion": 1,
  "passwordHash": "$2a$10$...",
  "initialPasswordPending": true,
  "createdAt": 1750000000000
}
```
- BCrypt cost 10；`initial-password` 24 位随机（字母数字），0600；改密后删除；首登强制改密。

### 3.4 其它
- `config/compatibility.json`：与参考实现同结构（`versions.<ver>.{launcherOptions,appOptions,pluginManagement,profiles.<p>.appOptions,webAlias}`），用于 `--help` 探测失败时人工声明。
- `artifacts.json`（cache/exports 下）：`{schemaVersion:1, artifacts:[{id,filename,path,size,contentType,expiresAt}]}`，TTL 24h。
- 日志：应用日志/审计日志按天，单行、脱敏；tail 默认 800 行（1–2000）。
- trash：删除实例/runtime/失败导入的回收目录，保留原路径信息（JSON 或目录名后缀即可，但需可恢复）。
- 无账户文件、无 secret.key。

---

## 4. HTTP 契约（API v1）

### 4.1 通用约定

- Base：`/api`；`/workbench/{id}/**` 为反代（不属于 JSON API）。
- Content-Type：JSON 用 `application/json; charset=utf-8`；SSE 用 `text/event-stream`；上传用 `multipart/form-data`。
- 认证：除 `POST /api/auth/login`、`GET /api/health` 外，所有 `/api/**` 需要有效会话。
- CSRF：Cookie `XSRF-TOKEN`（可读）+ 请求头 `X-XSRF-TOKEN`；所有非 GET/HEAD 的 `/api/**` 必须校验（登录接口豁免；`/workbench` 豁免）。`GET /api/auth/me` 等响应必须刷新/下发 `XSRF-TOKEN`。
- 会话 Cookie：`JSESSIONID`，`HttpOnly`、`SameSite=Lax`、`Path=/`、无 `Secure`（HTTP 部署）；12h 滑动过期。
- 响应头：每个响应带 `X-Trace-Id`（12 位 hex）；错误体含 `traceId`。
- 首次登录未改密：除 `/api/auth/**` 与 `/api/health` 外，全部返回 `403`，错误码 `PASSWORD_CHANGE_REQUIRED`，`details.endpoint` 给前端跳转提示。
- 请求体上限：JSON 1 MiB；multipart 上传 2 GiB（流式，不整读内存）。
- 时间戳：毫秒 Unix 时间；端口为整数。

### 4.2 端点总表

| 方法/路径 | 鉴权/CSRF | 请求 | 成功响应 | 关键失败 |
| --- | --- | --- | --- | --- |
| POST /api/auth/login | 匿名/豁免 | `{password}` | 204 + 会话 Cookie + XSRF Cookie | 401 LOGIN_FAILED；423 LOGIN_LOCKED(details.retryAfterSeconds) |
| POST /api/auth/logout | 登录/必需 | — | 204 | 匿名也 204 |
| GET /api/auth/me | 匿名可访问/— | — | 200 `{authenticated,csrfToken,version,initialPasswordPending}` | — |
| PUT /api/auth/password | 登录/必需 | `{oldPassword,newPassword}` | 204 | 400 VALIDATION |
| GET /api/health | 匿名/— | — | 200 `{status:"UP",version,uptimeSeconds}` | — |
| GET /api/state | 登录/— | — | 200 聚合（见 4.3） | 401 |
| GET /api/instances | 登录/— | — | 200 `InstanceDto[]` | 401 |
| POST /api/instances | 登录/必需 | `{name,version,profile,port?}` | 201 InstanceDto | 400 VALIDATION；409 CONFLICT_BUSY；409 CONFLICT_PORT_BUSY |
| GET /api/instances/{id} | 登录/— | — | 200 InstanceDto | 404 NOT_FOUND |
| PUT /api/instances/{id} | 登录/必需 | `{name?,port?}` | 200 InstanceDto | 400；409 CONFLICT_UNSUPPORTED_CHANGE（改 version/profile）；404 |
| DELETE /api/instances/{id} | 登录/必需 | — | 200 `{trashedPath}` | 404；409 CONFLICT_INSTANCE_RUNNING |
| PUT /api/instances/{id}/selection | 登录/必需 | — | 200 InstanceDto | 404 |
| POST /api/instances/{id}/launch | 登录/必需 | — | 202 JobView（LAUNCH_INSTANCE） | 404；409 CONFLICT_BUSY / CONFLICT_RUNNING_INSTANCE / CONFLICT_PORT_BUSY / INSTANCE_NOT_READY（job 失败/同步校验） |
| POST /api/instances/{id}/stop | 登录/必需 | — | 200 InstanceDto | 404 |
| GET /api/instances/{id}/web-url | 登录/— | — | 200 `{url:"/workbench/{id}/"}` | 404；409 INSTANCE_NOT_READY |
| GET /api/instances/{id}/direct-access | 登录/— | — | 200 `{ready,port,directUrl,tunnelCommand,sshHost,sshUser,note}` | 404；未运行/未就绪也返回 200 且 ready=false |
| GET /api/runtimes | 登录/— | — | 200 RuntimeDto[] | 500 INTERNAL |
| GET /api/runtimes/available | 登录/— | — | 200 `{versions:string[]}` | 502 UPSTREAM_ERROR；504 UPSTREAM_TIMEOUT |
| POST /api/runtimes | 登录/必需 | `{version}` | 202 JobView（INSTALL_RUNTIME） | 400；409 CONFLICT_BUSY |
| POST /api/runtimes/{version}/probe | 登录/必需 | — | 202 JobView（PROBE_RUNTIME） | 400/404；409 CONFLICT_BUSY |
| DELETE /api/runtimes/{version} | 登录/必需 | — | 200 `{trashedPath}` | 409 CONFLICT_IN_USE；404 |
| GET /api/jobs | 登录/— | — | 200 JobView[]（最近 20） | 401 |
| GET /api/jobs/{id} | 登录/— | — | 200 JobView | 404 |
| GET /api/jobs/{id}/events | 登录/— | Last-Event-ID 可选 | SSE | 404 |
| POST /api/jobs/{id}/cancel | 登录/必需 | — | 202 JobView | 404；409 CONFLICT_JOB_FINISHED |
| GET /api/logs/tail?lines=800 | 登录/— | — | 200 `{lines:string[]}` | 401 |
| GET /api/settings | 登录/— | — | 200 `{proxy,registry,directAccessSshUser}` | 401 |
| PUT /api/settings | 登录/必需 | 同上 | 200 同上 | 400 VALIDATION；409 CONFLICT_BUSY |
| POST /api/packs/preview | 登录/必需 | multipart `file` | 200 PackPreviewDto | 400 VALIDATION；413 PAYLOAD_TOO_LARGE |
| POST /api/packs/import | 登录/必需 | `{previewId,name?,version?}` | 202 JobView（IMPORT_PACK） | 400；409 CONFLICT_BUSY |
| POST /api/packs/export | 登录/必需 | `{instanceId,mode:"profile"\|"home"}` | 202 JobView（EXPORT_PACK） | 400；404；409 CONFLICT_BUSY |
| GET /api/artifacts/{id} | 登录/— | — | 文件流 | 404（过期/不存在） |
| GET /api/instances/{id}/plugins?profile= | 登录/— | — | 200 PluginDto[] | 404 |
| POST /api/instances/{id}/plugins | 登录/必需 | `{spec}` | 202 JobView（PLUGIN_MUTATE） | 400；409 CONFLICT_BUSY/INSTANCE_RUNNING |
| PATCH /api/instances/{id}/plugins/{*name} | 登录/必需 | — | 202 JobView | 同上 |
| DELETE /api/instances/{id}/plugins/{*name} | 登录/必需 | — | 202 JobView | 同上 |
| ANY /workbench/{id}/** | 登录/— | 原始请求 | 反代流 | 404/409/502/504 |
| WS /workbench/{id}/** | 登录/— | Upgrade + Cookie + 子协议 | 双向 | 401/404/409/502 |

**明确不实现**：`/api/accounts*`、`/api/accounts/providers`、`/api/instances/{id}/account`、`/api/instances/{id}/copy`、`/api/plugin-catalog*`、`/api/settings/background`、`/api/assets/backgrounds/*`。前端必须删除相应入口。

### 4.3 DTO 字段

```ts
// GET /api/state
{
  version: string,
  currentInstanceId: string | null,
  instances: InstanceDto[],
  runtimes: RuntimeDto[],
  status: { code: "READY"|"RUNNING", message: string },  // 空闲 "就绪"；忙 "进行中：<phase>"
  settings: { proxy: string, registry: string, directAccessSshUser: string },
  availableVersions: string[],
  warnings: string[]
}

// InstanceDto
{
  id: string, name: string, version: string, profile: string,
  port: number, status: "未启动"|"启动中"|"运行中"|"停止中"|"已停止"| ... 中文状态,
  path: string,             // 实例目录绝对路径（仅登录可见）
  profiles: string[]        // 当前实例已有 profile；至少包含 profile 本身
}

// RuntimeDto
{ version: string, installed: boolean, compatibility: string }

// PluginDto
{ name: string, version: string, description: string, source: string }

// JobView
{
  id: string,               // "j-" + 12 hex
  type: "LAUNCH_INSTANCE"|"INSTALL_RUNTIME"|"PROBE_RUNTIME"|"PLUGIN_MUTATE"|"IMPORT_PACK"|"EXPORT_PACK",
  state: "RUNNING"|"SUCCEEDED"|"FAILED"|"CANCELLED",
  phase: string, message: string,
  createdAt: number, startedAt: number|null, finishedAt: number|null,
  lastSeq: number,
  result: unknown|null,
  error: { code: string, message: string, details: object } | null
}

// PackPreviewDto
{ previewId, name, format, version, dshVersion, profiles: string[], unpackedBytes: number, warnings: string[] }

// DirectAccessDto
{ ready: boolean, port: number, directUrl: string|null, tunnelCommand: string,
  sshHost: string, sshUser: string, note: string }
```

**Job result 约定**：
- LAUNCH_INSTANCE：`{instanceId, webUrl:"/workbench/{id}/"}`（**不得返回带 token 的 URL**）。
- INSTALL_RUNTIME：`{version, installed:true, compatibility}`。
- PROBE_RUNTIME：`{version, compatibility}`。
- PLUGIN_MUTATE：`{instanceId, profile, action, name}`。
- IMPORT_PACK：`{instanceId, name}`。
- EXPORT_PACK：`{artifactId, filename, size}`。

### 4.4 错误模型

统一错误体：
```json
{ "code": "CONFLICT_BUSY", "message": "中文可操作提示。", "details": {}, "traceId": "abc123def456" }
```
错误码（至少以下；不要发明新 code 而不记录）：
- 401 `UNAUTHORIZED` / `LOGIN_FAILED`
- 403 `FORBIDDEN` / `CSRF_INVALID` / `PASSWORD_CHANGE_REQUIRED`
- 400 `BAD_REQUEST` / `VALIDATION` / `METHOD_NOT_ALLOWED`（405）
- 404 `NOT_FOUND`
- 409 `CONFLICT_BUSY` / `CONFLICT_INSTANCE_RUNNING` / `CONFLICT_RUNNING_INSTANCE` / `CONFLICT_PORT_BUSY` / `CONFLICT_IN_USE` / `CONFLICT_UNSUPPORTED_CHANGE` / `CONFLICT_JOB_FINISHED` / `INSTANCE_NOT_READY`
- 413 `PAYLOAD_TOO_LARGE`
- 422 `UNSUPPORTED_RUNTIME`（或按参考实现）
- 423 `LOGIN_LOCKED`（details.retryAfterSeconds）
- 500 `INTERNAL`
- 502 `UPSTREAM_ERROR` / `UPSTREAM_TARGET_REJECTED`；504 `UPSTREAM_TIMEOUT`
- 未知任务/过期 artifact：`NOT_FOUND`。

### 4.5 SSE（Job 事件）

- 路径：`GET /api/jobs/{id}/events`，`Content-Type: text/event-stream`。
- 每事件：`id: <seq>\n event: <name>\n data: <json>\n\n`。
- 事件名与 data 字段（与前端 `stores/jobs.ts` 对齐）：
  - `state`：`{jobId,seq,ts,state,phase,message}`
  - `log`：`{jobId,seq,ts,level:"info",line}`
  - `result`：`{jobId,state,result}`
  - `error`：`{jobId,state,code,message,details}`
- 心跳：每 15s 发送注释行 `": ping"\n\n`（不占 seq）。
- 断线重连：支持 `Last-Event-ID`，补发该 seq 之后的事件（保留最近 4000 条；任务终态后可补发后关闭）。
- 终止：终态事件后发完即关闭；SSE 连接最长 30min 服务端超时。
- 任务 ID：`j-` + 12 hex；最近 20 个在 `GET /api/jobs`。
- Job 并发：**全局同一时间仅 1 个变更任务**；忙时同步写返回 409，不排队；任务超时默认 20min（安装/导入按参考实现 20/30min 可更宽）。

---

## 5. 安全要求

1. **认证/会话**：单管理员、BCrypt hash、登录成功废弃旧会话（防固定攻击）、12h 滑动、登出清 Cookie。
2. **登录限流**：按客户端 IP，5 次失败/60s 锁定 60s，423 + retryAfterSeconds；审计记录成功/失败/锁定。
3. **CSRF**：双提交；登录响应、me 响应刷新 XSRF；所有写 API 校验；常量时间比较；登录豁免；`/workbench` 豁免。
4. **首次改密**：initialPasswordPending 时，除 auth/health 外 403 PASSWORD_CHANGE_REQUIRED；前端跳转改密。
5. **授权**：所有 API 仅管理员；/workbench 需要登录且实例存在/运行/就绪；直连登录仅登录用户。
6. **路径安全**：静态文件/artifact/上传/解包均防路径穿越；`..`、绝对路径、符号链接、控制字符拒绝；解包规则见 DSH_INTEGRATION §7。
7. **反代安全**：目标只允许 `127.0.0.1` + 实例登记端口（SSRF 白名单）；不接受请求 Host 作为上游；过滤 hop-by-hop 头；token 只内存、不写日志；Cookie/Location 重写按 DSH_INTEGRATION §5。
8. **上传**：流式落盘（multipart 不整读），大小上限 2 GiB；文件名不复用请求值；预览 TTL 30min；临时文件 0600/0700；失败清理。
9. **日志脱敏**：token、API key、password、cookie、authorization、查询串中的敏感值；不记录请求体/请求头原文；审计记录事件+IP+摘要。
10. **HTTP 头**：`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、禁止缓存敏感接口（`Cache-Control: no-store`）；前端静态可缓存。
11. **进程安全**：只杀自己启动的进程树；`Setpgid`；PID starttime 校验；不执行用户提供的 customCommand；环境变量白名单，不继承无关变量。
12. **Node/pnpm**：使用随包二进制；不执行用户提供的 npm install 脚本以外的任意命令；插件操作经 DSH CLI/Node 桥；registry/proxy 只允许合法 URL。
13. **数据文件权限**：data-root 0700、config 0600；写入原子化；flock 防并写。
14. **错误不回显内部路径/环境/堆栈**；500 仅简短消息 + traceId，详情写日志。
15. **前端的 token**：直连 URL 只在登录后返回；不得写入日志/审计/Job result（Job result 用 `/workbench/{id}/`）。

---

## 6. DSH 集成（摘要；细节见 `docs/DSH_INTEGRATION.md`）

必须保留的能力（按参考实现语义重写，不照搬 Java 代码）：
- Node/pnpm 工具链定位（随包 `tools/node` + `tools/pnpm`，回退 data/tools/PATH）；启动子进程时注入 PATH/env。
- npm 版本查询：`@deepseek-ai/dsh`，60s 超时、32MB 上限、SemVer 排序。
- 安装：与参考实现相同的 pnpm 参数/workspace/overrides，staging + 原子移动。
- 能力探测：`--help` 35s / `--profile web --help` 60s；解析 launcher/app options；`compatibility.json` 覆盖。
- 启动：`node <dsh> --profile <p> --port <port> --host 127.0.0.1 --no-open`（按能力检测裁剪）；环境变量白名单 + registry/proxy/NODE_OPTIONS。
- 就绪：解析 stdout `dsh web: http://127.0.0.1:<port>/?token=...`（仅内存）；HTTP/1.1、禁代理；2xx/3xx 或 401/403+已捕获 token；404 不算；90s。
- 停止：先子孙后根，SIGTERM → 3s → SIGKILL → 4s；端口释放确认；只杀自有进程树。
- `/workbench` 反代：token 302 引导、Cookie/Location/Origin 重写、SSE 不缓冲、WS 握手/子协议/关闭码、SSRF 白名单；详见 §5。
- 直连登录：从内存 bootstrap URL 取 token；Host 推导 SSH 主机；SSH 用户取 `directAccessSshUser`（默认 root）；返回隧道命令 + directUrl；token 不落日志。
- 整合包：dspack v2/v3 + manifest v4/v5 + Overture snapshot v1 的预览/导入/导出/安全归档；语义以参考实现 `PackService`/`SafeArchive`/`TarSafety` + 25 个测试为准。
- 已装插件：读 profile package.json；经 DSH CLI/Node 桥安装/更新/移除；不做社区目录。
- 明确不做：账户 overlay/--patch/account 环境变量；旧数据兼容。

---

## 7. 前端（WebUI）

**策略**：复用参考仓库 `pcl-dsh-web` 的 Vue 3 代码并瘦身；保留 GPL 版权声明；不重做视觉。
- 必须删除：账户页面/路由/Store/API/类型/对话框/SideNav 账户区；账户绑定 UI；社区目录（在 RuntimesView 中）；背景上传/切换（SettingsView/背景工具/资源可保留一张默认图）。
- 必须保留/适配：登录/改密、首页/启动、实例、运行时、整合包、已装插件、任务与日志、设置、直连登录对话框。
- 实例编辑：版本/profile 不可变（UI 只读展示），可编辑名称/端口；删除复制/改版本入口。
- 设置：新增“直连登录 SSH 用户”字段（默认 root）；保留 proxy/registry；删除 background 字段与相关 UI。
- API 客户端：删除 accounts/catalog/background 方法；`state`/instance 类型去掉 accountId；settings 增加 `directAccessSshUser`；其余尽量沿用旧路径/字段。
- E2E：删除账户/目录/背景用例；保留/调整登录、实例、运行时、整合包、插件、日志、设置；新增“无账户也能启动/直连对话框”用例。
- 详细文件级映射见 `docs/UI_MIGRATION.md`。

---

## 8. 打包、部署与运行

### 8.1 发布包（由构建脚本生成）

```text
pcl-dsh-go-<ver>-linux-x64/
  release/pcl-dsh-go                 # Go 二进制
  release/static/                    # Vue dist（Nginx 直出；Go 也可 fallback）
  release/tools/node/                # Node 24.22.1（来自 node-v24.22.1-linux-x64.tar.xz）
  release/tools/pnpm/                # pnpm 11.28.5（vendored，node_modules/pnpm）
  install.sh / uninstall.sh / reset-password.sh
  pcl-dsh-go.service / nginx-pcl-dsh-go.conf
  LICENSE / LICENSES/ / THIRD_PARTY_NOTICES.md / VERSION
  SHA256SUMS
```
- install.sh：root 检查；解压/校验 Node+pnpm；安装二进制/static；创建 `pcl-dsh` 用户与 `/opt/pcl-dsh-go/{releases,current,data}`；systemd/Nginx；初始密码；health 等待；升级=切 current；卸载保留 data。
- systemd：`ExecStart=/opt/pcl-dsh-go/current/release/pcl-dsh-go --server.address=127.0.0.1 --server.port=8080 --pcl.data-root=/opt/pcl-dsh-go/data --pcl.tool-root=/opt/pcl-dsh-go/current/release/tools`；沙箱参数沿用参考实现。
- Nginx：继续静态直出 + `^~ /api/` + `^~ /workbench/`（SSE/WS 不缓冲、access_log off、client_max_body_size 2048m）。
- **服务器不需要**系统 Node/npm/pnpm/JDK；运行时只用 `release/tools/node` + `release/tools/pnpm`。
- 未来可选的“预构建 DSH runtime”发布资产：本期不实现；设计上允许把 runtime 目录预置到 `data/runtimes/<ver>`，管理器检测即视为已安装。

### 8.2 本地构建/运行

```bash
# 前端（Windows 或 WSL 都可）
cd web && npm ci && npm run build
# 后端交叉编译 Linux 二进制（Windows）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
# 本地运行（WSL，临时数据目录）
wsl -d Ubuntu -- bash -lc '/path/to/bin/pcl-dsh-go --server.address=127.0.0.1 --server.port=18080 --pcl.data-root=/tmp/pcl-dsh-go-dev/data --pcl.tool-root=/tmp/pcl-dsh-go-dev/tools'
# 初始密码：/tmp/pcl-dsh-go-dev/data/initial-password
```

### 8.3 开发约定

- 配置：flags/env（`--server.address/--server.port/--pcl.data-root/--pcl.tool-root`；env `PCL_DSH_DATA` 可选）——与参考实现命名保持一致，systemd/Nginx 模板复用。
- 日志：`log/slog` JSON 或文本，写 data/logs/manager-YYYY-MM-DD.log；审计单独文件；单行、脱敏。
- 代码风格：`gofmt`、`go vet`、内部包不导出无关符号；错误包装 `%w`；context 贯穿 I/O/进程；不滥用全局状态。
- 测试：每个包至少单元测试；HTTP 契约用 `httptest`；进程/反代/SSE/WS 做集成测试；真实 DSH 用 WSL。
- 提交：小步提交、信息清晰；不提交构建产物/数据/密钥。
- 文档：改动 API/配置/行为时同步更新本规格/README/验收。

---

## 9. 验收（摘要；细节见 `docs/ACCEPTANCE.md`）

必须全绿的门禁：
- **G1 认证/安全**：未登录 401、CSRF 403、首登 403、登录锁定 423、日志脱敏。
- **G2 实例/运行时**：CRUD、端口/单实例限制、安装/探测/删除引用保护。
- **G3 Job/SSE**：202+Job、事件顺序、Last-Event-ID、心跳、取消/超时、忙 409。
- **G4 反代**：真实 DSH rc.3 下 token 302、Cookie/Location/Origin、SSE、WS、409/502/504。
- **G5 直连登录**：运行中/就绪返回隧道命令+URL；sshUser 来自设置；token 不入日志；未登录不可调。
- **G6 整合包**：恶意样本 0 绕过；真实 .dspack 预览/导入/导出往返。
- **G7 插件**：真实 DSH CLI 安装/更新/移除；列表一致。
- **G8 WebUI**：无账户/目录/背景入口；页面可用；Playwright。
- **G9 部署/离线**：干净 Ubuntu 安装；无需系统 Node/npm；内置 Node/pnpm 可用；升级/回滚。
- **G10 数据/安全**：data-root 权限、原子写、flock、无账户文件；日志不泄露 token。
- **不再要求**：账户/旧数据兼容、旧版 183 测试全等。

---

## 10. 参考实现索引（只读，不修改）

| 主题 | 参考路径 |
| --- | --- |
| 实例编排 | `pcl-dsh-server/src/main/java/com/hdsl/server/instance/InstanceService.java` |
| 进程/就绪 | `pcl-dsh-core/src/main/java/com/hdsl/core/ProcessService.java` |
| 运行时/安装/探测/启动 | `pcl-dsh-core/src/main/java/com/hdsl/runtime/RuntimeService.java` |
| 能力探测 | `pcl-dsh-core/src/main/java/com/hdsl/runtime/Capabilities.java` |
| 工作台反代 | `pcl-dsh-server/src/main/java/com/hdsl/server/proxy/{WorkbenchProxyController,ProxyTargets,WorkbenchHandshakeInterceptor,WorkbenchWebSocketHandler}.java` |
| Job/SSE | `pcl-dsh-server/src/main/java/com/hdsl/server/job/JobManager.java` |
| 安全/认证 | `pcl-dsh-server/src/main/java/com/hdsl/server/security/*`、`api/AuthController.java`、`GlobalExceptionHandler.java` |
| 实例/运行时/插件/整合包 Controller/DTO | `pcl-dsh-server/src/main/java/com/hdsl/server/api/*` |
| 整合包/安全归档 | `pcl-dsh-core/src/main/java/com/hdsl/pack/{PackService,SafeArchive,TarSafety,PackInspection}.java` |
| 插件 CLI/桥 | `pcl-dsh-core/src/main/java/com/hdsl/runtime/PluginService.java`、`RuntimeService.java` 的 shim/bridge 部分 |
| 数据格式/旧 schema | `pcl-dsh-core/src/main/java/com/hdsl/core/InstanceStore.java`、`account/*`（账户部分仅参考，不实现） |
| 前端 | `pcl-dsh-web/src/{api,views,components,stores,router,utils,styles}` |
| 验收/行为说明 | 参考仓库 `README.md`、`docs/IMPLEMENTATION_PLAN.md`、`docs/M0_RESULTS.md`、`docs/DEPLOYMENT_VALIDATION.md`、`docs/DSH_WORKBENCH_PROXY.md`、`docs/REMOTE_ACCOUNT_LOGIN.md` |

> 参考实现是 GPL 衍生作品；复制前端/资产到本仓库时保留版权/许可声明。不要修改参考仓库。
