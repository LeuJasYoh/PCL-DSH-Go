# 架构说明

本文说明 PCL-DSH-Go 当前版本（根目录 `VERSION`）的组件划分、关键流程与安全设计。配置项见 [配置参考](CONFIGURATION.md)，开发构建见 [开发与发布](DEVELOPMENT.md)。

## 1. 总览

```text
┌────────────┐   HTTP/SSE/WS    ┌─────────────────────────────┐
│ 浏览器/WebUI │ ───────────────▶ │ Nginx :80（TLS 可终止于此）   │
└────────────┘                  │  ├─ 静态 SPA（current/static）│
                                │  └─ /api、/workbench 反代     │
                                └──────────────┬──────────────┘
                                               ▼
                          ┌────────────────────────────────────┐
                          │ pcl-dsh-go 管理器（systemd, 回环）    │
                          │  API/会话/CSRF/限流/静态/反代/Job/SSE │
                          │  数据: /var/lib/pcl-dsh-go          │
                          │  工具: current/tools (node+pnpm)    │
                          └───────┬───────────────┬────────────┘
                                  │ spawn         │ pnpm install
                                  ▼               ▼
                        DSH 实例（Node）      runtimes/<ver>/
                        127.0.0.1:3080+      （npm registry）
```

管理器是**单进程 Go 服务**，所有持久状态在 data-root；release（程序/静态/内置工具链）只读；数据与程序分离使升级/回滚不丢数据。

## 2. 设计原则

- **单管理员、单管理器**：flock 保证一个 data-root 只有一个管理器进程；不实现多用户/账户体系。
- **最小暴露面**：管理器只监听 127.0.0.1；公网入口统一由 Nginx 承担；敏感路径不写访问日志。
- **不可信输入 fail-closed**：整合包/上传走严格白名单校验；代理只允许回环上游；错误明确中文提示，不静默降级。
- **任务化长操作**：安装运行时、启动/停止实例、导入/导出整合包、插件变更都通过 Job + SSE 展示进度，避免 HTTP 超时。
- **无系统依赖**：随包 Node/pnpm；目标机不需要 Node/npm/pnpm/JDK。

## 3. Go 包结构

| 包 | 职责 |
| --- | --- |
| internal/config | 启动参数/env、data-root 目录初始化、单实例文件锁 |
| internal/logging | slog JSON（stdout+按日文件）、审计日志、脱敏、日志 tail |
| internal/tools | 内置 Node/pnpm 定位与版本探测、子进程命令/环境 |
| internal/store | manager.json/instances.json/admin.json、原子写、校验、admin 密码（bcrypt-sha256）、trash |
| internal/process | 实例进程 spawn/进程组/就绪探测（stdout token）/停止/状态 |
| internal/runtime | DSH 版本枚举、pnpm 安装、能力探测、启动命令/环境构建、runtime 删除 |
| internal/app | 编排层：实例 CRUD/启停、运行时安装/删除、设置、日志 tail、并发上限 |
| internal/server | HTTP 服务器与路由接线、静态资源、优雅停机 |
| internal/server/api | REST API/中间件（会话、CSRF、首登门槛、限流、busy）、DTO、Job/SSE 路由 |
| internal/server/security | 管理员密码/会话/登录限流/CSRF/改密/审计事件 |
| internal/server/job | Job 管理与 SSE 事件流（LAUNCH/INSTALL/IMPORT/EXPORT/PLUGIN 等） |
| internal/server/proxy | /workbench/{id}/ 反代：HTTP/SSE/WS、token/Cookie/Origin/路径重写、SSRF 白名单 |
| internal/pack | dspack v2/v3、Overture snapshot 识别/预览/导入/导出 |
| internal/plugin | 已装插件列表与经由内置 pnpm/DSH CLI 的变更 |
| internal/safe | ZIP/TAR 安全归档：路径/链接/CRC/压缩比/限值/取消 |
| internal/artifact | 导出文件存储（artifacts.json，TTL 24h）与下载 |
| internal/directaccess | SSH 隧道命令与一次性 token URL 构建 |
| internal/passhash | 管理员密码策略与哈希（bcrypt-sha256，兼容旧 BCrypt） |
| internal/profile | DSH profile 名称校验（支持 Unicode/空格，路径与保留名防护） |
| internal/ports | 实例端口规则（1024–65535） |
| internal/protocol | 会话/CSRF Cookie 与 Header 名 |
| internal/apierr | 统一错误模型（HTTP 状态 + code + 中文 message） |

历史设计文档（里程碑接口/规格）归档在 [dev/README.md](dev/README.md)，不作为当前实现依据；当前实现以代码与用户文档为准。


## 4. 关键流程

### 4.1 WebUI 与 API

1. 浏览器请求 → Nginx（或 Go 静态服务）：SPA 路由回退 index.html，静态资源带 hash 缓存。
2. /api/** 进入管理器：会话 Cookie 校验 → 首登门槛 → CSRF（写请求）→ Origin/JSON 限制 → handler。
3. 未登录/会话失效返回 401；首登未改密除认证/健康检查外返回 403 PASSWORD_CHANGE_REQUIRED；写请求缺 CSRF 返回 403。
4. 长任务写接口创建 Job（202）并通过 /api/jobs 与 SSE 查询进度；管理器同时只执行一个阻塞型 Job，忙时返回 409 CONFLICT_BUSY。
5. `GET /api/state` 的 `status.code` 表示 `READY`/`RUNNING`，运行时附带 `phase`；`message` 仅作展示，前端不再从中文前缀推断任务状态。
6. 管理器 Cookie 与 CSRF Header 名称由 `internal/protocol` 提供，安全层和工作台代理共用；前端对应值位于 `web/src/config.ts`。代理剥离管理器凭据，DSH Cookie 按原反代规则处理，旧 Cookie 自动恢复仍未实现。

### 4.2 运行时安装

- 从 npm registry 查询指定包可用版本；安装时在临时目录生成 package.json，用内置 pnpm 安装到临时目录，校验入口后原子移动到 runtimes/<version>。
- 安装成功记录依赖锁文件；失败保留诊断日志并清理临时状态。
- 探测：调用运行时入口的 --help/能力探测，结果缓存；无法探测时可用 config/compatibility.json 人工声明。

### 4.3 实例启动与工作台

1. 启动前检查实例存在、未运行、运行实例数未超过上限、端口可用、运行时已安装。
2. 构建启动命令/环境（内置 Node 执行 DSH 入口，设置实例 dsh-home/workspace/agents-home 与 profile/port），拉起进程组。
3. 管理器从子进程 stdout 解析一次性 bootstrap token/地址，等待就绪（带超时）；失败停止进程并回传可操作错误。
4. 浏览器打开 /workbench/{id}/：代理校验登录态，向上游发起带 token 的引导请求；重写 DSH 的 302/303、Set-Cookie Path、Location 与 Origin 校验，再透传 HTTP/SSE/WS。
5. 停止实例时按进程组终止并等待端口释放；管理器退出时停止所有自有实例。

### 4.4 整合包与插件

- 预览：上传流先落 cache/uploads，经安全归档（限值/路径/链接/CRC/压缩比）校验并解析 dspack.json/manifest.json/snapshot 元数据；预览只读、不执行归档内容。
- 导入：Job 在 staging 中解包/校验，创建实例目录并写入 profile/home；失败整体回滚到 trash，不影响现有实例。
- 导出：Job 打包实例 profile/home 为 .dspack 或 Overture 结构，排除黑名单文件，登记为 artifact 供下载（TTL 24h）。
- 插件：读取实例 profile 的 package.json 展示列表；变更通过内置 pnpm/DSH CLI 以 Job 执行，内置 bundle 不允许移除。


## 5. 安全模型

| 面 | 措施 |
| --- | --- |
| 认证 | 单管理员 bcrypt-sha256(cost10，兼容旧 BCrypt)，新密码 8–256 字符；初始密码 24 位随机、0600、仅首启生成；首登强制改密 |
| 会话 | 服务端会话 Cookie（HttpOnly、SameSite、12h 滑动过期）；登出废止 |
| CSRF/Origin | 写请求双提交 CSRF；Origin/Referer 与 Host 同源校验；代理仅信任回环上游与 Nginx 注入的 X-Real-IP |
| 暴力破解 | 按客户端 IP 统计失败次数，窗口内锁定并返回 423 |
| 权限 | 管理器进程以专用 pcl-dsh 用户运行；systemd 沙箱（NoNewPrivileges/ProtectSystem=strict/ProtectHome/PrivateTmp/ReadWritePaths 限数据目录） |
| 日志 | 结构化日志写前脱敏（token/password/api_key/authorization/cookie/URL token）；Nginx 敏感路径 access_log off |
| 上传/归档 | 2 GiB 上限、路径穿越/绝对路径/保留名/链接/特殊文件/压缩炸弹/CRC 全校验；解包目标限 data-root 内且父链无 symlink |
| 依赖/插件 | 运行时与插件来自 npm registry，安装会执行第三方代码；仅使用可信 registry/插件 |
| 密钥 | 不保存 DSH 凭据/API Key；管理员密码仅存加盐哈希；一次性 token 不入日志 |

进一步加固与漏洞报告见 [SECURITY.md](../SECURITY.md)。

## 6. 数据与状态

- 数据 schema（schemaVersion 1）：manager.json（选中实例 + 设置）、instances.json（实例列表）、admin.json（管理员哈希/首登状态）；损坏时启动报错，不静默覆盖。
- 实例目录：instances/<id>/{dsh-home,workspace,agents-home}；运行时：runtimes/<semver>；回收：trash/。
- 日志：logs/manager-*（应用）与 audit-*（审计），按日切换。
- 锁：<data-root>/.lock flock 防止同一 data-root 被多个管理器进程使用（与“运行实例上限”是两个不同概念）。

## 7. 端口与网络

| 端口 | 用途 |
| --- | --- |
| :80（可配置） | Nginx 对外入口 |
| 127.0.0.1:8080 | 管理器 API/静态 |
| 127.0.0.1:1024–65535（自动分配从 3080 起） | DSH 实例（仅回环） |

Nginx 反代需保持 HTTP/1.1、关闭代理缓冲（SSE）、透传 Upgrade（WS），并按配置保留 Host 端口；详见发行包 Nginx 模板与 [DEPLOYMENT.md](DEPLOYMENT.md#7-nginx)。

## 8. 兼容性与扩展

- 已验证 DSH 基线为 `0.2.1-rc.3`；版本选择未锁定该版本，其他版本按实际 npm 包能力安装/探测，但不代表均已验收。能力差异通过 compatibility.json 与能力探测处理，不支持的启动请求会明确报错。
- WebUI 与 API 同源部署，静态资源可继续由 Nginx 托管；接入新的前端构建产物只需替换 static/。
- 若需多用户/账户体系，应在反向代理/外部身份层实现，当前版本不内置。
