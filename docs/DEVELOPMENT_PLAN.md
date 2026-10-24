# PCL-DSH-Go 开发计划（v1.0）

> - **目标项目**：`D:\Projects\DSHL\PCL-DSH-Go`（已初始化；本计划已迁入该仓库 `docs/`）。
> - **依据**：用户 2026-10-24 决策（见 §0）；`docs/GO_PORTING_ANALYSIS.md`、`docs/GO_REWRITE_FEASIBILITY.md`；对 `D:\Projects\DSHL\deepseek-harness-master.zip`（DSH 官方源码快照）的只读检查。
> - **一句话范围**：Go + WebUI 的 DSH 管理器，做“实例/运行时/反代/任务日志/整合包/已装插件/设置/直连登录”，**不做账户系统、社区目录、旧数据兼容，也不做服务器端 clone/编译源码**；Node/pnpm 随发行包内置；**绝不改动 Windows DSH 任何文件**。
> - **状态**：计划定稿 v1.0（未开工）。许可选择 GPL 路线；WSL 环境已就绪；仓库已初始化；下一步进入 M0。
> - **估算口径**：[估算] AI 辅助人日（1 名开发 + AI）；纯人力约 ×3–4；误差 ±25%，随 PoC 校准。

---

## 0. 决策记录

| 问题 | 用户决定（2026-10-24） | 对计划的影响 |
| --- | --- | --- |
| Q1 功能范围 | **暂时不追求全量对等** | 只做本计划 §2 的 P0/P1；P2 与“不做”明确列出 |
| Q2 UI 形态 | **仍选 WebUI；复用现有内容优先** | Go 管理器 + Vue WebUI；复用现有前端并瘦身，不重做视觉 |
| Q3 旧数据 | **不再在意上一版管理器的 data-root** | 全新 data-root；不读取/不迁移/不删除旧管理器数据（§2.2） |
| 账户系统 | **砍掉** | 删除账户 CRUD、AES-GCM/secret.key、Provider 预设、实例绑定、启动 overlay/校验、账户 API/UI/测试（§2.3） |
| 整合包 | **暂时保留** | 预览/导入/导出、安全归档、artifact 下载进入 P1 |
| 已装插件 | **暂时保留** | 插件列表 + 安装/更新/移除（经 DSH CLI/Node 桥）；社区目录不做 |
| 直连登录 | **一定要保留**（用户截图：SSH 隧道 + 一次性 token URL）；**SSH 用户可配置** | 恢复 `direct-access` API/UI；设置项 `directAccessSshUser`（默认 root）（§3.4/§3.6/§3.7/§4.7） |
| 社区插件目录 | **暂时不做** | 仅保留已装插件管理 |
| 新项目位置 | **`D:\Projects\DSHL\PCL-DSH-Go`，一切都是新的** | 代码/文档/构建全新；本计划迁入新仓库 |
| 运行时来源 | **默认 npm registry + pnpm；暂不做 GitHub clone/编译**（服务器资源太紧） | 未来可选“预构建 runtime 离线包/发布资产”（§3.8、§3.9），是否启用按服务器性能取舍 |
| DSH 基线 | **源码快照 0.2.1-rc.3** | 以 rc.3 为验收基线；与 PCL 已验证的 rc.2 有差异，需测试 |
| 发行包依赖 | Node 24.22.1 + **单一 pnpm 11.28.5**（rc.3 源码仓库同款；若验证不兼容再回退 10.34.0） | 部署无需系统 Node/npm；WSL 已安装并验证 11.28.5 |
| WSL | **已安装完成**；Node 24.22.1 + pnpm 11.28.5 + Nginx 1.28.3 已配好 | 验证阶段可在 WSL 进行；Windows DSH 仍不碰 |
| 部署入口 | **继续 HTTP + Nginx** | 应用层 HTTP；HTTPS 由 Nginx 终止（可选模板） |
| 许可 | **复用现有内容，继续 GPL-3.0-or-later + HMCL §7** | 保留 LICENSE/LICENSES/版权声明；Go 代码与 UI 均按 GPL 发布 |
| Windows DSH | **禁止变动任何文件** | §1 硬约束；测试用 WSL 副本/临时 data-root，绝不触碰 Windows DSH |

---

## 1. 硬约束：Windows DSH 只读

> 第一条红线。上一轮环境被搞坏的教训之后，任何操作先过这一节。

- **禁止写入/删除/重命名/安装/升级**：
  - `D:\DevelopSoftware\DSH\**`（DSH 程序、`resources/app.asar`、内置运行时）；
  - `C:\Users\<当前用户>\.dsh\**`（DSH 用户数据：会话、附件、插件、配置、缓存、凭据）；
  - 任何由 Windows 上 DSH 管理的实例/工作区/运行时目录；以及本对话当前运行的 DSH。
- **允许**：只读查看/读取用户主动提供的截图、日志、配置副本；读取工作区内的 `deepseek-harness-master.zip` 等参考资料。
- **开发/测试写入范围**：仅 `D:\Projects\DSHL\**`（含新项目 `PCL-DSH-Go`）与 WSL 的 `/tmp/**`；**WSL 已就绪，可在验证阶段使用**（但仍不碰 Windows DSH）。
- **真实 DSH 测试（将来）**：用 WSL 中 runtime 的副本与独立 data-root；不修改/不重启 `/opt/pcl-dsh-linux` 原服务，不碰 Windows DSH。
- **不在 Windows 上安装/运行 Go 管理器**：目标平台 Linux；Windows 只写代码，测试在 WSL 进行。
- **脚本/CI 强制检查**：涉及 DSH 路径的脚本先做白名单断言；禁止把 `D:\DevelopSoftware\DSH`、`%USERPROFILE%\.dsh` 当可写目录。
- **测试完成清理**：删除临时目录/进程；日志保留到工作区 `reports/`。

---

## 2. 产品范围（新范围，非全量对等）

### 2.1 范围总表

| 优先级 | 功能 | 说明/验收要点 |
| --- | --- | --- |
| **P0** | 登录/会话/CSRF/改密 | 单管理员；初始密码文件；WebUI 登录；首登强制改密 |
| **P0** | 首页/实例管理 | 列表、创建、编辑（名称/版本/profile/端口）、删除（trash）、选择当前实例、启动/停止、状态 |
| **P0** | 运行时 | npm 版本查询、安装/删除（引用保护）、能力探测 |
| **P0** | 工作台反代 | `/workbench/{id}/` HTTP/SSE/WS：token 引导、Cookie/Location/Origin 重写、SSRF 白名单 |
| **P0** | **直连登录** | SSH 隧道命令 + 一次性 token URL；供远程完成 DeepSeek 账号网页登录 |
| **P0** | 任务与日志 | 单任务 409、Job/SSE（state/log/result/error）、取消、日志 tail |
| **P0** | 设置 | npm registry、下载代理 |
| **P0** | 健康检查/版本 | `/api/health`、版本展示 |
| **P1** | 整合包 | 预览、导入、导出（profile/home）、artifact 下载、安全归档、失败进 trash |
| **P1** | 已装插件 | 列表（profile package.json）、安装/更新/移除（Job，经 DSH CLI/Node 桥） |
| **P1** | 发行包内置工具链 | Node 24.22.1 + pnpm（单一版本）+ 校验；部署无需系统 Node/npm |
| **P2**（可选） | 预构建 DSH runtime 离线包 | 在性能更好的机器/CI 上构建、发 Release 资产、安装脚本导入；作为“按服务器性能取舍”的可选部署模式（§3.8/§3.9） |
| **P2**（未来） | 社区插件目录 | GitHub 检索/安装目标核验 |
| **P2**（未来） | 背景图/外观 | 自定义背景上传/切换 |
| **P2**（未来） | 多实例并行/ARM64/HTTPS | 当前保持单实例、HTTP+Nginx |
| **删除/不做** | 账户系统 | 整套删除 |
| **删除/不做** | 旧数据兼容/迁移 | 不读取、不迁移、不删除旧 data-root |
| **删除/不做** | 服务器端源码 clone/编译 | 资源不适合，至少当前不做 |
| **删除/不做** | 桌面版遗产/Windows 分支 | 不迁移 |
| **删除/不做** | 社区插件目录（本期）/背景上传（本期） | 后续 P2 再议 |
| **删除/不做** | **Windows DSH 任何操作** | 见 §1 |

### 2.2 “旧数据”：不再在意，全新开始

- 上一版管理器自己的 data-root（`config/launcher-v2.json`、`accounts.json`、`server.json`、`secret.key`、缓存/日志等）**我们不再关心**：新项目用全新 data-root，不读取、不迁移、不删除旧目录。
- 旧 data-root 里的 `instances/<id>/dsh-home` 等属于 DSH 实例数据；本期也不自动接管（未来如需“接管已有实例”，作为 P2 的 adopt-existing-instance 单独做）。
- **Windows DSH 数据**与上述无关：永远不读不写（§1）。旧管理器数据与 Windows DSH 数据是两码事。

### 2.3 删除“账户系统”的具体影响

**删除内容**：账户页面/表单/预设/导入、Provider 列表、实例账户绑定、启动 `--patch`、`HDSL_SELECTED_ACCOUNT_API_KEY`、账户 overlay/verifier、`accounts.json`/`secret.key`、AES-GCM 账户加密、账户 API/测试/降级逻辑。

**收益**：省约 4–6 AI 人日；去掉最脆弱的 DSH 内部 JS 源码探测；去掉主密钥管理/备份/权限风险；启动流程更短、失败点更少。

**前提**：用户在 DSH 工作台里自行配置模型/API Key；管理器不提供“统一账户”。直连登录与账户系统无关，仍保留。

### 2.4 保留功能的高层验收

| 功能 | 验收要点 |
| --- | --- |
| 实例 | 创建/编辑/删除/选择/启动/停止；端口分配/冲突；最多 1 个运行实例 |
| 运行时 | 按精确版本安装；失败可清理；探测失败给原因；被引用删除 409 |
| 反代 | 真实 DSH：token→Cookie→页面/静态/SSE/WS 全可用；未就绪 409；上游 502/504 |
| 直连登录 | 运行中/就绪实例返回 SSH 隧道命令与带 token 的回环 URL；复制可用；未就绪给提示 |
| 任务/日志 | 202+Job；SSE 事件/取消/超时；日志 tail |
| 整合包 | 安全校验（穿越/链接/大小写/压缩率等）；导入可启动；导出可再导入；失败 trash |
| 已装插件 | 列表与 profile 一致；安装/更新/移除成功；失败给 CLI/pnpm 摘要 |
| 设置 | registry/代理保存后生效；非法值 400 |

---

## 3. 目标架构

### 3.1 形态与部署

```text
浏览器（桌面）
  │ HTTP :80（可选来源 IP 白名单）
  ▼
Nginx（宿主机）
  ├─ /              → Vue WebUI 静态文件（release/static）
  ├─ ^~ /api/       → 127.0.0.1:8080（不缓冲；SSE）
  └─ ^~ /workbench/ → 127.0.0.1:8080（HTTP/SSE/WS，access_log off）
        ▼
pcl-dsh-go（Go 单二进制，127.0.0.1:8080）
  ├─ 会话/CSRF/登录、REST、Job/SSE、日志
  ├─ /workbench 反代（HTTP/SSE/WS）
  ├─ 直连登录（SSH 隧道命令 + 一次性 token URL）
  ├─ 运行时安装/探测、实例/进程管理
  ├─ 整合包、已装插件
  └─ release/tools/node + release/tools/pnpm（固定版本）
        ▼ spawn（最多 1 个）
Node DSH 实例：127.0.0.1:3080+（--profile web --port ... --host 127.0.0.1 --no-open）
```
- 运行期不需要 JDK，也不需要系统 Node/npm/pnpm；发行包自带。
- 安装/升级/回滚：`releases/<version>` + `current` 软链 + data 独立；install.sh 去 Java 检查、换 Go 二进制。
- HTTP + Nginx 继续；HTTPS 由 Nginx 终止（可选模板）；systemd 沙箱/反代模板沿用 Java 版。

### 3.2 代码组织（建议）

```text
cmd/pcl-dsh-go/main.go            ~120   启动/信号/优雅停机
internal/config                     ~250   flags/env/路径/版本
internal/logging                    ~200   日志/审计/脱敏
internal/store                      ~450   实例/设置（新 schema，JSON v1）
internal/tools                      ~500   Node/pnpm 定位/解压/校验/PATH/env
internal/runtime                    ~900   npm 元数据、pnpm install、probe、launch cmd/env
internal/process                    ~450   spawn/进程组/stdout token/就绪/停止
internal/server                     ~2,400  api/security/job-sse/proxy/upload/static/direct-access
internal/pack                       ~1,300  安全归档/预览/导入/导出/artifact
internal/plugin                     ~400    已装插件列表/变更（Node 桥）
internal/directaccess               ~150    SSH 隧道命令/token URL 生成
web/（Vue 构建产物）                  —     由 release/static 提供
```
- 预计生产代码 **4,800–6,300 行 Go**（不含测试）；原型 1,243 行已覆盖安全/SSE/反代/WS/进程/上传骨架。
- DSH 集成逻辑集中在 `runtime`/`process`/`tools`/`server/proxy` 四个包，便于上游漂移时集中修改。

### 3.3 Node/pnpm 工具链（版本与打包说明）

**pnpm 是什么**：它是 Node 的包管理器（类似 npm），DSH 官方 README 的运行方式是 npm，但从源码安装/构建时用 pnpm；DSH 的安装也适合用 pnpm 处理大量 `@deepseek-ai/dsh-*` 包和 peer/overrides。不同 pnpm 大版本的锁文件/peer 行为有差异，所以版本要固定并测试。

**最终安排（简化）**：
- **发行包只带一个 pnpm 版本：11.28.5**（与 DSH 源码快照 0.2.1-rc.3 的根 `packageManager` 一致；WSL 已安装并验证可运行）。
- 若 rc.3 实测安装不兼容，再回退 10.34.0（PCL v0.3.2 已验证 rc.2），最终仍只带一个版本。
- WSL 环境已就绪（Node 24.22.1 + pnpm 11.28.5 + Nginx 1.28.3），可直接进入 rc.3 安装验证。
- pnpm 以 vendored 目录随发行包分发（`tools/pnpm/<ver>/node_modules`），运行时用内置 Node 执行 `pnpm.cjs`；目标机不需要 npm/pnpm 命令。
- Node 固定 24.22.1（来自 `node-v24.22.1-linux-x64.tar.xz`，满足 DSH `>=24.9.0`）；构建时校验 SHA-256。
- 查找顺序：`release/tools/` → `data/tools/` → `PATH`（沿用 Java 版策略）。
- 环境注入：`PATH`、`DSH_HOME`、`DSH_AGENTS_HOME`、registry/代理、`NODE_OPTIONS=--max-old-space-size=<cap>`。
- 官方平台原生包已预编译；管理器不编译 native 模块，只检测缺失并给原因。

### 3.4 数据与状态（新 schema，不保留旧数据）

```text
<data-root>/
  config/manager.json     { schemaVersion:1, selectedInstanceId, settings:{proxy,registry,directAccessSshUser} }
  config/instances.json   { schemaVersion:1, instances:[
                             {id,name,version,profile,port,createdAt,updatedAt} ] }
  config/admin.json       { schemaVersion:1, passwordHash(BCrypt), initialPasswordPending, createdAt }
  initial-password        首次启动生成；改密后删除
  instances/<id>/{dsh-home,workspace,agents-home}
  runtimes/<semver>/                      pnpm 安装/预置的 DSH
  cache/{uploads,exports,pack-staging}、artifacts.json（TTL 24h）
  logs/{manager,audit}-YYYY-MM-DD.log
  trash/、tools/（可选 data/tools 回退）
```
- **无** `accounts.json`、`secret.key`；无账户字段（`accountId`）。
- 写入：临时文件 + rename 原子写；`flock` 单实例锁；权限 0700/0600；启动校验 schemaVersion。
- 旧 PCL data-root 不再在意：不读取/不迁移/不删除；若被误指向，只提示“不兼容/请用新目录”。
- Job 历史仅内存；日志按日落盘；artifact 元数据持久化（TTL 24h）。
- 预置 runtime：将预构建 runtime 目录放到 `data/runtimes/<ver>/` 即视为已安装（未来离线包用）。

### 3.5 认证与安全

- 单管理员密码（BCrypt 存 `admin.json`，0600）；首次启动生成 24 位随机初始密码；首登强制改密。
- 会话：服务端内存 session（随机 ID，HttpOnly+SameSite=Lax，12h 滑动）；CSRF 双提交（`XSRF-TOKEN` Cookie + `X-XSRF-TOKEN` 头）。
- 登录限流（5 次/60s，423）；审计；统一 JSON 错误；traceId。
- 公网建议 Nginx IP 白名单/安全组；HTTPS 由 Nginx 终止（可选）。
- 反代目标只允许回环+实例登记端口；bootstrap token 只在内存；日志脱敏。
- 直连登录：仅登录用户；URL 含一次性 token，不写日志；有效期=本次实例运行；SSH 用户名来自设置 `directAccessSshUser`（默认 root，可配置）。

### 3.6 API 设计（新 API v1，约 32 个端点）

| 分组 | 端点 | 说明 |
| --- | --- | --- |
| 认证 | `POST /api/auth/login`、`POST /api/auth/logout`、`GET /api/auth/me`、`PUT /api/auth/password` | 登录/登出/登录态/改密 |
| 健康 | `GET /api/health` | 匿名；status/version/uptime |
| 聚合 | `GET /api/state` | 实例/运行时/Job/设置/选中（不含账户） |
| 实例 | `GET/POST /api/instances`、`GET/PUT/DELETE /api/instances/{id}`、`POST /api/instances/{id}/launch`、`POST /api/instances/{id}/stop`、`PUT /api/instances/{id}/selection`、`GET /api/instances/{id}/web-url` | 无账户绑定 |
| **直连登录** | `GET /api/instances/{id}/direct-access` | `{ready,port,directUrl,tunnelCommand,sshHost,sshUser,note}`；Host 推导 SSH 主机；token 来自内存 bootstrap URL；`sshUser` 取设置 `directAccessSshUser`（默认 root，可配置） |
| 运行时 | `GET /api/runtimes`、`GET /api/runtimes/available`、`POST /api/runtimes`、`POST /api/runtimes/{version}/probe`、`DELETE /api/runtimes/{version}` | 安装/探测/删除 |
| 任务 | `GET /api/jobs`、`GET /api/jobs/{id}`、`GET /api/jobs/{id}/events(SSE)`、`POST /api/jobs/{id}/cancel` | 单任务/SSE |
| 日志 | `GET /api/logs/tail?lines=` | 最近日志 |
| 设置 | `GET/PUT /api/settings` | proxy/registry |
| 整合包 | `POST /api/packs/preview`、`POST /api/packs/import`、`POST /api/packs/export`、`GET /api/artifacts/{id}` | 预览/导入/导出/下载 |
| 插件 | `GET /api/instances/{id}/plugins?profile=`、`POST /api/instances/{id}/plugins`、`PATCH/DELETE /api/instances/{id}/plugins/{*name}` | 已装插件；无 catalog |
| 反代 | `ANY /workbench/{id}/**`、`WS /workbench/{id}/**` | 登录后访问 |
| 删除 | `/api/accounts*`、`/api/plugin-catalog*`、`/api/settings/background`、`/api/assets/backgrounds/*` | 本期不实现 |

- 错误模型 `{code,message,details,traceId}`；错误码精简（删账户相关）。
- 202+Job、SSE 事件名（state/log/result/error）、单任务忙 409 保持。
- 前端复用现有 API 客户端结构，删账户调用；路径/字段尽量沿用旧版，减少前端改动。

### 3.7 前端（WebUI）策略

**保留 WebUI；复用现有 Vue + 瘦身**；视觉先不重做（后续可另立项）。

| 页面 | 处理 |
| --- | --- |
| 登录/改密 | 保留；适配新 auth |
| 首页/启动 | 保留；去账户绑定状态 |
| 实例 | 保留；表单去账户选择；创建/编辑/删除/启停；复制/改版本可后续 |
| 运行时 | 保留；安装/探测/删除 |
| 整合包 | 保留；预览/导入/导出/artifact |
| 插件 | 保留已装插件；去掉社区目录入口 |
| 任务与日志 | 保留；SSE/取消/日志尾部 |
| 设置 | 保留 registry/代理；新增“直连登录 SSH 用户”（默认 root，可配置）；去背景上传 |
| **直连登录** | **保留现有对话框**（截图）：SSH 隧道命令 + 直连 URL + 复制 + 说明；SSH 用户取设置值；适配新接口 |
| 账户 | **删除页面/路由/Store/API/类型/导航/对话框** |
| 关于/许可 | 保留（按 §3.10 的许可决定处理） |

工作内容：删账户相关代码/测试；实例/首页去绑定；去社区目录/背景入口；适配新 API；更新 Playwright（删账户用例、加直连与“无账户也能启动”用例）。视觉沿用现有（HDSL 派生，许可见 §3.10）。

### 3.8 运行时来源：npm 为主；暂不做源码 clone/编译

**现状**：Java 版（及本计划默认）从 **npm registry** 安装 DSH：查 `@deepseek-ai/dsh` 版本 → 用 pnpm 安装到 `runtimes/<ver>`。**不是从 GitHub 仓库源码构建。**

**为什么暂不做 GitHub clone/编译**：
- 服务器太紧（2 vCPU/1.6 GiB）：DSH 源码快照 0.2.1-rc.3 是数百包的大 monorepo（lockfile 约 1.16 MB），要求 Node ≥24.9、pnpm 11.28.5、Git、构建工具链，构建时间长/内存磁盘开销大，不适合在生产服务器做。
- 源码构建是官方 **开发路径**；官方运行方式是 `npx @deepseek-ai/dsh web`。源码构建产物与 npm 发布包布局不完全一致，升级要重构建，风险高。
- GitHub 的 release 工作流打包的是 **npm tarballs 并发布到 npm**，GitHub Releases 不是现成的 Linux 运行包。

**“固定 DSH runtime / 预构建 runtime”是什么**：指在**性能更好的机器/CI**（未来的 WSL/Linux 构建机）上把某个 DSH 版本安装/构建好，打包成一个 runtime 目录（含 `node_modules`、package.json 等，约 500 MB/版本），再压缩成 release 资产（tar.gz + SHA-256）；服务器部署时由 install.sh 或管理器把它解压到 `data/runtimes/<ver>/`，**无需在服务器上联网安装/编译**。这适合未来解决“服务器资源紧张 + 离线部署”，不是当前必须项。

**计划**：默认 npm；未来 P2 增加“预构建 runtime 离线包/Release 资产 + 安装脚本导入”流程（§3.9）；不做服务器端 git clone/build。

### 3.9 构建、发行包与未来离线 runtime

**发行包内容（标准版）**：

| 组成 | 说明 | 估算大小 |
| --- | --- | ---: |
| `pcl-dsh-go` | Go 静态二进制（linux/amd64） | 10–20 MB |
| `static/` | Vue 构建产物 | ~1 MB |
| `tools/node/` | Node 24.22.1 linux-x64（由 `node-v24.22.1-linux-x64.tar.xz` 解压；校验 SHA-256） | ~150–200 MB |
| `tools/pnpm/<ver>/` | vendored pnpm（单一版本，默认 11.28.5；若 rc.3 不兼容回退 10.34.0） | ~18 MB |
| install/uninstall/reset/systemd/Nginx/许可 | 沿用 Java 版改造 | <1 MB |
| 压缩后 tar.gz | 上传/分发 | 约 80–110 MB |

**install.sh**：root 检查；解压/校验 Node 与 pnpm；安装 Go 二进制/static；创建用户/目录/current 软链；systemd/Nginx；初始密码；健康检查；升级=切 current；不再检查 Java。

**可选离线 runtime（P2，按服务器性能取舍）**：
1. 在性能好的 Linux 机器/CI 上安装/构建指定 DSH 版本到临时目录；
2. 打包 `dsh-runtime-<ver>-linux-x64.tar.gz` + SHA-256，作为 GitHub Release 资产（或私有发布物）；
3. 服务器部署脚本/manifest 下载/校验后解压到 `data/runtimes/<ver>`；
4. 管理器检测到目录完整即视为“已安装”，无需 registry。
（这是你设想的“在我们机器上构建、发 release、部署脚本加入”的路径；作为**可选模式**，不急于实现，等进入验证/发布阶段再定。）

**HTTP + Nginx**：继续；TLS 可选由 Nginx 终止；Nginx 继续负责静态/反代/限流/不缓冲。

### 3.10 许可（已决策：GPL 路线）

- **决策（2026-10-24）**：复用现有 PCL/HDSL 内容，**新项目继续按 GPL-3.0-or-later + HMCL §7 发布**；不采用 clean-room 另许可路线。
- 事实：DSH 本身 MIT；HDSL/PCL 是 GPL-3.0-or-later + HMCL §7；复用现有 Vue UI/文档/设计会保留 GPL 义务，需保留版权与许可声明、发布对应源码。
- 新仓库已包含 `LICENSE`、`LICENSES/`（HDSL MIT、HMCL 附加条款等）与 `THIRD_PARTY_NOTICES.md`；Go/Vue 依赖的声明在开发过程中更新。
- 后续如想改为其他许可证，必须另行 clean-room 替换所有受 GPL 影响的代码/UI/文档并做法律评估；本期不做。

---

## 4. 关键流程

### 4.1 启动实例（无账户注入）

1. 校验实例存在/未运行；检查端口空闲、最多 1 个运行实例。
2. 确保 runtime 已安装（否则走安装 Job）；能力探测（缓存兼容性）。
3. 组命令：`node <dsh-bin> --profile <p> --port <port> --host 127.0.0.1 --no-open`。
4. 清空并注入环境：`DSH_HOME`、`DSH_AGENTS_HOME`、registry/代理、PATH（node/tools）、`NODE_OPTIONS`（可选上限）。
5. spawn（进程组）；抓 stdout 解析 `dsh web: http://127.0.0.1:<port>/?token=...`（仅内存）；就绪轮询（HTTP/1.1、禁代理；2xx/3xx 或 401/403+已捕获 token；404 不算；90 s）。
6. 成功：记录 web 地址（内存）；Job result 返回 `/workbench/{id}/`（不返回带 token URL）；失败：保留进程供查看日志/手动停止。

### 4.2 停止实例

- 先子孙后根：SIGTERM → 3 s → 强制 → 4 s 等待；确认端口释放；只杀自有进程树；systemd cgroup 兜底。
- 无账户 overlay 需清理（已移除）。

### 4.3 运行时安装/探测

- 版本查询：`GET {registry}/@deepseek-ai%2fdsh`（超时/大小上限/SemVer 排序）。
- 安装：用内置 pnpm 执行；参数/workspace/overrides 逐字保留；staging + 原子移动；失败清理。
- 离线：`data/runtimes/<ver>` 已存在且校验通过 → 视为已安装。
- 探测：`--help`（35s/60s）；解析 launcher/app options；`compatibility.json` 覆盖；失败拒绝启动并给原因。

### 4.4 整合包

- 预览：multipart 流式落盘 + SHA-256；安全归档检查；preview 30 min。
- 导入：Job：校验/重算哈希 → 解包暂存 → 安装 runtime（缺失时）→ 恢复 profile/依赖 → 创建实例；失败 trash。
- 导出：profile/home；排除凭据/会话/日志；artifact 24h。
- 无账户后：无账户过滤/绑定校验。

### 4.5 已装插件

- 列表：读 profile `package.json` 的 dependencies/optionalDependencies 与 `dsh.profile.bundles`。
- 变更：经 Node 桥调用 DSH plugin CLI（add/update/remove；`--config.ignore-scripts=true` 等），走 Job/SSE；失败给摘要。
- 不做社区目录检索（本期）。

### 4.6 任务/日志/设置

- Job：同一时间 1 个变更任务；忙 409；事件 state/log/result/error；15 s 心跳；Last-Event-ID；取消/超时。
- 日志：`data/logs/manager-*.log`、`audit-*.log`；tail 接口。
- 设置：proxy/registry；变更后失效运行时/下载客户端缓存。

### 4.7 直连登录（保留）

1. 用户点击实例“直连登录”；前端调 `GET /api/instances/{id}/direct-access`（需登录）。
2. 后端校验实例运行/就绪；从内存 bootstrap URI 取 token；生成：
   - `tunnelCommand = ssh -L <port>:127.0.0.1:<port> <user>@<host>`（host 来自请求 Host，去端口/防伪造；默认 root，可配置）；
   - `directUrl = http://127.0.0.1:<port>/<path>?<bootstrap query>`（含一次性 token，不落日志）；
   - `ready=false` 时给“实例未运行/未就绪”提示，仍返回可复制的隧道命令。
3. 前端对话框展示两步 + 复制 + 说明（token 仅本次实例运行有效；SSH 隧道只把本机 127.0.0.1 端口映射到服务器实例端口）。
4. 用户在浏览器打开 directUrl 完成 DeepSeek 账号网页登录；凭据保存在服务器实例 DSH_HOME；之后可继续用工作台。
5. 安全：仅登录用户；token 不写日志/不持久化；不改变 DSH 只监听回环的事实。

---

## 5. DSH 集成契约（必须保留，与语言无关）

| # | 契约 | 关键点 |
| --- | --- | --- |
| 1 | Node/pnpm 工具链 | 发行包固定 Node 24.22.1 + 单一 pnpm；解压/校验/PATH/env；不依赖系统 Node |
| 2 | npm registry/版本 | 版本查询超时/上限/SemVer；registry 可配置；镜像/代理 |
| 3 | 安装命令 | 内置 pnpm 执行；参数/env/workspace/overrides 逐字保留 |
| 4 | CLI | `--profile/--port/--host/--no-open`；顺序；无 `--patch`（账户已删） |
| 5 | 目录 | `DSH_HOME`/`DSH_AGENTS_HOME`/workspace 隔离 |
| 6 | bootstrap token | stdout `dsh web:` 解析；只回环+端口匹配；仅内存；不落日志 |
| 7 | 就绪 | HTTP/1.1、禁代理；2xx/3xx 或 401/403+token；404 不算；90 s |
| 8 | 进程 | 进程组、先子孙后根、端口释放、只杀自有进程 |
| 9 | 工作台反代 | token 302 引导；Cookie/Location/Origin 重写；SSE 不缓冲；WS 握手/子协议/关闭码；SSRF 白名单；日志不记 token |
| 10 | **直连登录** | 从内存 bootstrap URL 取 token；按请求 Host 生成 SSH 命令；仅登录用户；仅本次实例生命周期 |
| 11 | 任务/SSE | 事件名/顺序/心跳/续传/取消；202+Job |
| 12 | 整合包格式 | .dspack v2/v3、manifest v4/v5、Overture snapshot 的解析与安全规则 |
| 13 | 插件 CLI | DSH plugin 命令、Node 桥、pnpm 参数 |
| 14 | 运行时来源 | 默认 npm；未来可导入预构建 runtime（dir/tarball）；不做服务器端 clone/编译 |

> 账户 overlay、--patch、HDSL_SELECTED_ACCOUNT_API_KEY、account.validation.txt **不再保留**（账户系统已砍）。

---

## 6. 里程碑与任务分解

> 估算：AI 辅助人日（1 人 + AI）；纯人力约 ×3–4。**总计 29–42 AI 人日**（纯人力约 90–150）。WSL 环境已就绪，M0 可开始。

| 里程碑 | 目标 | 任务 | AI 人日 | 验收/回退 |
| --- | --- | --- | ---: | --- |
| **M0 骨架+PoC** | Go 骨架、内置工具链、进程/反代最小链路；真实 DSH 验收 | M0-1 仓库/Go 骨架/config/logging；M0-2 Node/pnpm 解压/校验/调用；M0-3 实例 store/process/token/就绪；M0-4 反代 HTTP+SSE+WS；M0-5 真实 DSH（rc.3）验收 | 5–8 | 单测通过；真实 DSH：启动→浏览器工作台（HTTP/静态/SSE/WS）→停止/端口释放 |
| **M1 后端核心** | 新 API 与核心闭环（含直连登录） | M1-1 认证/会话/CSRF/改密/限流/审计；M1-2 实例 CRUD/选择/trash；M1-3 runtime 安装/探测/删除；M1-4 Job/SSE/日志；M1-5 设置/health/state；M1-6 direct-access API；M1-7 契约测试 | 7–9 | curl/Go 测试；无账户代码/API；202/409/SSE 语义正确 |
| **M2 反代硬化** | 真实 DSH 下 HTTP/SSE/WS 全场景（依赖 WSL） | M2-1 token/Cookie/Location/Origin/SSRF；M2-2 SSE 不缓冲/断线；M2-3 WS 握手/子协议/关闭码/1 MiB；M2-4 Nginx 链路联调 | 4–6 | 真实 DSH+浏览器全可用；未就绪 409、上游 502/504 |
| **M3 整合包/已装插件** | 保留功能落地 | M3-1 安全归档；M3-2 preview/import/export/artifact；M3-3 插件列表/安装/更新/移除；M3-4 失败回滚 trash | 5–7 | 恶意样本 0 绕过；真实 dspack 往返；插件增删改成功 |
| **M4 WebUI 瘦身** | 现有 Vue 适配新 API（含直连对话框） | M4-1 删账户页面/Store/API/路由/对话框；M4-2 实例/首页去绑定；M4-3 去社区目录/背景入口；M4-4 适配 API；M4-5 直连对话框；M4-6 E2E | 4–6 | 页面可用、无账户入口、直连可用、Playwright 通过 |
| **M5 打包/部署/验收** | Linux 发布：内置 Node/pnpm、install 即用 | M5-1 发布包/install/uninstall/reset/systemd/Nginx；M5-2 Node/pnpm vendoring+校验；M5-3 CI；M5-4 干净环境验收/文档/许可 | 4–6 | 干净 Ubuntu 安装；无需系统 Node/npm；升级/回滚；门禁全绿 |
| **合计** | | | **29–42** | |

可选/未来（不计入上表）：
- 预构建 runtime 离线包/Release 资产：+2–4 人日（依赖构建机/CI；按服务器性能取舍，验证/发布阶段再定）；
- 社区目录 +3–4；背景/主题 +2–3；多实例/ARM64/HTTPS +3–6。
依赖：M0→M1→M2（M2 可与 M1 后半并行）；M1→M3；M1 API 冻结后 M4 可并行；M5 最后。
裁剪：实例复制/改版本、home 导出、审计 UI 可延后（-2–4 人日）。

---

## 7. 测试与验收计划

### 7.1 分层测试

| 层次 | 内容 | 方式 |
| --- | --- | --- |
| 单元 | SemVer/任务文案/实例 store/BCrypt/安全归档/插件元数据/进程工具 | Go `testing`，表驱动（不需要 WSL） |
| 契约/HTTP | 认证/错误码/202/409/SSE/设置/运行时/实例/直连 | `httptest` + 真实二进制（不需要 WSL） |
| 集成 | 真实 DSH rc.3：安装/启动/就绪/反代 HTTP+SSE+WS/停止；pnpm 11.28.5 验证；插件/整合包往返 | WSL（已就绪）+ runtime 副本；不碰 Windows DSH |
| 发行/离线 | 内置 Node/pnpm 解压可用、无需系统 Node/npm、离线 runtime 预置（可选）、校验和 | 干净 WSL/容器/目录测试 |
| E2E | 登录/实例/运行时/整合包/插件/日志/设置/直连 | Playwright（WSL 已就绪；后端可跑后执行） |
| 发布验收 | 干净 Ubuntu 安装、systemd/Nginx、升级/回滚、内存/启动 | 手工清单（未来服务器/WSL） |

### 7.2 回归门禁（本期，不含账户/旧数据）

| 门禁 | 内容 | 判据 |
| --- | --- | --- |
| G1 认证/安全 | 未登录 401、CSRF 403、首登改密、登录锁定 423、日志脱敏 | 与设计一致 |
| G2 实例/运行时 | CRUD、端口校验、单实例限制、安装/探测/删除引用保护 | curl/Go 测试 |
| G3 Job/SSE | 202+Job、事件顺序、Last-Event-ID、心跳、取消/超时、忙 409 | 前端状态机可消费 |
| G4 反代 | token 302、Cookie/Location/Origin 重写、SSE 不缓冲、真实 WS、409/502/504 | 真实 DSH+浏览器+手工 WS |
| G5 直连登录 | 运行中/就绪返回隧道+URL；未就绪提示；token 不落日志；仅登录可调 | 真实实例 + curl/Playwright |
| G6 整合包 | 恶意样本、预览/导入/导出、artifact、失败 trash | 安全样本 0 绕过 |
| G7 插件 | 列表与 profile 一致、安装/更新/移除、失败摘要 | 真实 DSH CLI |
| G8 WebUI | 页面可用、无账户/目录/背景入口、直连可用、Playwright 通过 | E2E |
| G9 部署/离线 | 干净安装、无需系统 Node/npm、内置 Node/pnpm 可用、升级/回滚 | 验收清单 |
| **不再要求** | 账户/密钥兼容、旧数据双向、旧版 183 测试全等 | 已删除或按新范围重写 |

### 7.3 测试环境与时序

- **WSL 已就绪**：Ubuntu 26.04.1（WSL2）、Node 24.22.1、pnpm 11.28.5、Nginx 1.28.3 已安装（见附录 A）。
- 真实 DSH（0.2.1-rc.3）集成测试在 WSL 内进行；Windows DSH 始终不碰。
- 真实 DSH 测试使用 WSL runtime 副本与独立 data-root；不修改 `/opt/pcl-dsh-linux` 原服务（若存在）；测试后清理。
- 脚本对目标路径做白名单断言；CI 只用 Linux runner。
- 当前阶段：开发计划已定稿、仓库已初始化；等待开始 M0 编码。

---

## 8. 风险与开放问题

### 8.1 风险

| # | 风险 | 影响 | 应对 |
| --- | --- | --- | --- |
| R1 | DSH 上游漂移（Node/pnpm、CLI/token/包布局） | 高 | capability probe + compatibility.json；固定验收版本 rc.3；跟踪上游 |
| R2 | /workbench 反代/WS 细节 | 高 | 真实 DSH + 浏览器 + 手工 WS；逐项对照 Java 行为 |
| R3 | 直连登录 token 泄漏/Host 伪造 | 高 | 仅登录；token 只内存/不落日志；Host 校验；同源/CSRF；审计 |
| R4 | 整合包安全归档回归 | 高 | 恶意样本+对拍+fuzz+安全评审 |
| R5 | 已装插件 CLI/Node 桥/pnpm 差异 | 中 | 逐字参数/env；失败摘要；真实插件抽样 |
| R6 | 移除账户后用户不会在 DSH 配 Key | 中 | 文档/UI 提示“在 DSH 工作台配置模型/Key” |
| R7 | 许可：复用现有 UI/代码 → 项目按 GPL/HMCL 发布；日后不能单方面转许可 | 高 | 已选 A：继续 GPL-3.0-or-later + HMCL §7；保留许可/版权/源码义务 |
| R8 | WSL/验证环境异常 | 低 | WSL 已装好 Node/pnpm/Nginx；验证前跑一次冒烟；Windows DSH 不动 |
| R9 | 误改 Windows DSH 文件 | 高（不可接受） | §1 白名单/断言/只读；测试只用 WSL 副本 |
| R10 | 发行包体积/未来离线 runtime 过大 | 中 | 标准包只带 Node/pnpm；runtime 独立 Release 资产/离线包 |
| R11 | rc.3 npm 包/依赖兼容性（相对 rc.2） | 中 | WSL 就绪后先做 rc.3 安装/启动冒烟；pnpm 版本以实测为准 |

### 8.2 已解决 / 待验证（不阻塞 M0 启动）

已解决：
- 许可：路线 A，GPL-3.0-or-later + HMCL §7（§3.10）。
- 直连登录 SSH 用户：允许配置，设置项 `directAccessSshUser`（默认 root）。
- pnpm：默认 11.28.5（单一版本），WSL 已安装验证；失败再回退 10.34.0。
- WSL：已安装并完成 Node/pnpm/Nginx 基础环境（附录 A）。
- 新仓库：`D:\Projects\DSHL\PCL-DSH-Go` 已初始化，本计划已迁入。
- 预构建 runtime：作为可选模式（按服务器性能取舍），不在当前阶段实现。

仍待验证（进入 M0 前/中）：
1. **DSH rc.3 在 npm registry 的可用性**：确认 0.2.1-rc.3 是否已发布；若未发布，基线回退到最近可用版本（首选 rc.2）。
2. **pnpm 11.28.5 安装 rc.3 的兼容性**：WSL 内实测；失败回退 10.34.0。
3. **仓库远端**：GitHub 仓库名 `PCL-DSH-Go` 与可见性（当前本地初始化；gh 登录账号 LeuJasYoh，可创建远端）。
4. **Go module 路径**：确认使用 `github.com/LeuJasYoh/PCL-DSH-Go`（如需全小写可调整）。
5. **预构建 runtime 的启用时机**：验证/发布阶段按服务器性能决定；当前不实现。

---

## 9. 下一步

1. **计划 v1.0 已定稿**，新仓库 `PCL-DSH-Go` 已初始化（README/计划/许可/gitignore）；本计划已迁入 `docs/DEVELOPMENT_PLAN.md`。
2. **WSL 环境已就绪**（附录 A）：进入验证阶段后先做 rc.3 + pnpm 11.28.5 的安装/启动冒烟，确认基线。
3. 你确认开始后进入 **M0**：Go 骨架 → 内置 Node/pnpm → 实例/进程/就绪 → 反代 HTTP/SSE/WS → 真实 DSH 验收。
4. M0 通过后按 M1→M5 推进；每阶段可回滚。
5. 重申：**Windows DSH 只读**；真实 DSH 测试只用 WSL 副本/临时目录，测完清理。

---

## 附录 A：WSL 验证环境（已就绪，2026-10-24）

| 项 | 值 |
| --- | --- |
| 发行版 | Ubuntu 26.04.1 LTS（WSL2，kernel 6.18） |
| Node | v24.22.1，路径 `/opt/pcl-dsh-go/tools/node`；`/usr/local/bin/node` 软链 |
| npm | 11.19.1（随 Node） |
| pnpm | 11.28.5，路径 `/opt/pcl-dsh-go/tools/pnpm`；`/usr/local/bin/pnpm` 包装脚本 |
| Nginx | 1.28.3（Ubuntu 包，已安装；仅安装，未启用生产站点） |
| 用途 | 验证阶段跑真实 DSH rc.3、反代/WS 集成、打包安装冒烟 |
| 约束 | 不修改 Windows DSH；不修改 `/opt/pcl-dsh-linux` 原部署（若存在）；测试用副本与独立 data-root |

> 环境准备命令（已执行）：解压 `node-v24.22.1-linux-x64.tar.xz` 到 `/opt/pcl-dsh-go/tools/node`；用其 npm 安装 pnpm 11.28.5 到 `/opt/pcl-dsh-go/tools/pnpm`；apt 安装 nginx。

## 附录 B：仓库与许可

- 本地仓库：`D:\Projects\DSHL\PCL-DSH-Go`
- 计划：`docs/DEVELOPMENT_PLAN.md`（本文）
- 许可：`LICENSE`（GPL-3.0-or-later）、`LICENSES/HDSL-MIT.txt`、`LICENSES/HMCL-ADDITIONAL-TERMS.md`、`THIRD_PARTY_NOTICES.md`
- 复用 PCL-DSH-Linux / HDSL 的界面与设计，版权/许可声明保留。

> 你后续的问题可以继续问；本计划会随决策更新。
