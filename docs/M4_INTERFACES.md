# PCL-DSH-Go M4 接口与协作冻结（WebUI）

> 状态：M4 启动时冻结（2026-10-24）。上游契约：`docs/IMPLEMENTATION_SPEC.md`（API/DTO/错误码/SSE）、`docs/UI_MIGRATION.md`（前端瘦身）、`docs/ACCEPTANCE.md`（G8/G5）。
> 本文件只补充 M4 新增/变更的集成点；API 细节以 IMPLEMENTATION_SPEC 与 `internal/server/api` 实际实现为准。

## 1. 目标与边界

- 复制参考前端 `pcl-dsh-web`（Vue 3 + TS + Vite + Pinia，基线已导入 `web/`），按 UI_MIGRATION 瘦身并接线 Go 后端，交付可用的 8 个页面（登录/首页/实例/运行时/整合包/插件/任务日志/设置）+ 直连对话框。
- 不做：账户/绑定/Provider/API Key、社区插件目录、背景上传/切换、多实例、旧数据兼容；不重做视觉（允许品牌/默认背景差异）。
- 后端 API、Job/SSE、反代、整合包、插件均已在 M1–M3 冻结；M4 只允许**新增** Go 静态资源服务能力（§2）和前端适配，不改既有 API 语义。

## 2. Go 静态资源服务（M4-C）

新增配置项：`--server.static-dir <path>`（空 = 未配置，保留现有占位首页；生产 Nginx 静态根与直接访问 manager 都可用）。

行为（仅在配置了非空目录时生效）：

| 请求 | 行为 |
| --- | --- |
| `GET /`、`GET /instances`、`GET /packs` 等无扩展名路径（非 /api、非 /workbench） | 返回 `index.html`（SPA fallback），`Cache-Control: no-store`；未登录也可返回页面（页面自身跳登录），与 Nginx 生产行为一致 |
| `GET /assets/*`、`/licenses/*`、`/favicon.webp` 等实际存在的文件 | 返回文件，按扩展名 MIME；哈希化 `assets/*` 可 `Cache-Control: public, max-age=31536000, immutable` |
| 不存在且包含扩展名的资源（如 `/assets/missing.js`） | 404（HTML 或纯文本均可，不得落 SPA） |
| 路径逃逸（`..`、绝对路径、NUL/控制字符） | 400，不读目录外文件 |
| `/api/**`、`/workbench/**` | 不进入静态处理，保持现有路由/鉴权 |
| 非 GET/HEAD 静态路径 | 405 |
| 目录本身（`/assets/`） | 404（不列目录） |

实现要求：`http.Dir`/`http.FileServer` + `os.Stat` 防目录穿越；`index.html` 每次读磁盘（不缓存旧 index）；支持相对/绝对路径（启动时 `filepath.Abs` + 目录存在校验，无效目录 manager 启动失败并给中文错误）。测试：SPA fallback、资源/MIME、404、穿越 400、非 GET 405、未配置时占位页不变、`--server.static-dir` 非法值报错。

Nginx 生产模板静态根 = release `static/`（M5 打包时放 `dist` 内容）；M4 期间 E2E 可用 Go 静态服务或现有 M2 隔离 Nginx（root 指 `web/dist`）。

## 3. 前端文件所有权（禁止交叉写）

| owner | write scope | 内容 |
| --- | --- | --- |
| frontend-core（backend-jobs） | `web/package.json`、`web/vite.config.ts`、`web/index.html`、`web/tsconfig*.json`、`web/src/main.ts`、`web/src/App.vue`、`web/src/env.d.ts`、`web/src/api/**`、`web/src/stores/**`、`web/src/router/**`、`web/src/utils/**`、`web/src/styles/**`、`web/src/components/{AppShell,SideNav,AppIcon,AppNotice,AppToast,BaseModal,ConfirmDialog,EmptyState,FooterBar,PageHeader,StatusBadge,TaskProgressStrip,DevPlaceholder,HomeLaunchPane,HomeTaskCard,InstanceDialog}.vue`、`web/src/views/{LoginView,HomeView,InstancesView}.vue`、`web/public/**`、`web/src/assets/**` | 骨架/API/类型/Store/路由/登录首页实例/导航/样式/许可资源 |
| frontend-views（proxy） | `web/src/views/{RuntimesView,PacksView,PluginsView,LogsView,SettingsView}.vue`、`web/src/components/DirectAccessDialog.vue`、`web/src/components/m4/**`（新目录） | 剩余页面、直连对话框、页面级子组件（上传/预览/插件变更）；样式仅写 `<style scoped>`，不改共享 css/api/store |
| qa-release | `web/e2e/**`、`web/playwright.config.ts`、`web/scripts/e2e-stack.*`、`scripts/m4-*`、`docs/evidence/m4/**`、`docs/M4_RESULTS.md` | Playwright 改造/用例/证据/报告 |
| backend-server | `internal/config/**`、`internal/server/**` | `--server.static-dir` + 静态服务 + 测试（含 `cmd` 透传如需） |
| backend-core | `scripts/frontend-build.ps1`、`docs/M4_BUILD.md` | npm ci/build/type-check 工具与文档、Playwright 浏览器可用性调研（Edge channel/WSL），不写 `web/src` |
| Lead | `cmd/`、`README.md`、`docs/M4_INTERFACES.md`、跨模块集成修复 | 集成/验收/提交 |

共享文件（api/types/stores/router/components 公共件）只能 frontend-core 改；frontend-views 如需新 API 方法/类型，通过 Lead 转达，不直接改。

## 4. API 契约速查（Go 实现为准）

| 页面/功能 | API |
| --- | --- |
| 登录/首登改密/登出 | `POST /api/auth/login`、`PUT /api/auth/password`、`POST /api/auth/logout`、`GET /api/auth/me`（返回 csrfToken）、`GET /api/auth/session` |
| 全局状态 | `GET /api/state`（instances/settings/currentInstanceId；**无 accounts**）、`GET /api/health`（匿名） |
| 实例 | `GET/POST /api/instances`、`GET/PUT/DELETE /api/instances/{id}`、`POST .../{id}/launch|stop`（202 Job）、`PUT /api/state/selection`、`GET .../{id}/web-url`、`GET .../{id}/direct-access` |
| 运行时 | `GET /api/runtimes`、`GET /api/runtimes/available`、`POST /api/runtimes/{version}/install`（202）、`POST .../delete`、`POST /api/runtimes/{version}/probe`（202） |
| Job/SSE | `GET /api/jobs`（最近 20）、`GET /api/jobs/{id}`、`GET /api/jobs/{id}/events`（SSE，Last-Event-ID）、`POST /api/jobs/{id}/cancel` |
| 日志 | `GET /api/logs?source=manager|instance&tail=N` |
| 设置 | `GET/PUT /api/settings`：`{proxy, registry, directAccessSshUser}` |
| 整合包 | `POST /api/packs/preview`（multipart file，2GiB）、`POST /api/packs/import`（202 IMPORT_PACK）、`POST /api/packs/export`（202 EXPORT_PACK）、`GET /api/artifacts/{id}`（下载） |
| 插件 | `GET /api/instances/{id}/plugins?profile=`、`POST .../plugins {spec}`（202）、`PATCH/DELETE .../plugins/{name...}`（202） |
| 已删除（前端不得调用） | `/api/accounts*`、`/api/instances/{id}/account`、`/api/instances/{id}/copy`、`/api/plugin-catalog*`、`/api/settings/background`、`/api/assets/backgrounds/*` |

类型调整（`web/src/api/types.ts`）：删除 `AccountSummary/ProviderPreset/AccountInput/Catalog*/BackgroundAsset`；`InstanceSummary` 去 `accountId`；`AppSettings` 增 `directAccessSshUser: string`；新增 M3 DTO：`PackPreviewDto {previewId,name,format,version,dshVersion,profiles,unpackedBytes,warnings}`、`InstalledPluginDto {name,version,description,source}`、`JobView.result/error` 按 M3（`{instanceId,name}` / `{artifactId,filename,size}` / `{instanceId,profile,action,name}`）。错误统一 `{code,message,details,traceId}`，前端用中文 message；`409 CONFLICT_BUSY` 与前端忙态一致。

## 5. 构建与 E2E 环境

- 前端构建在 **Windows** 执行（Node v24.22.1 已装）：`cd web && npm ci --no-audit --no-fund && npm run type-check && npm run build`；产物 `web/dist`（约 30 文件 0.94MB，含 licenses）。
- E2E 走生产形态：Windows 构建 dist → Go manager 在 WSL 运行 `--server.static-dir=/mnt/d/Projects/DSHL/PCL-DSH-Go/web/dist` + fresh data-root → Playwright（Windows）`baseURL=http://127.0.0.1:18080`（WSL localhost forwarding；若 8088 隔离 Nginx 同步可用则作为附加链路）。
- 浏览器：优先 Playwright `channel: 'msedge'`（Windows Edge 已装，无需下载浏览器）；`chromium` 下载不可用时以 Edge channel 为准，并在报告注明。Playwright 在 Windows `web/` 下执行（npm ci 已含 @playwright/test）；e2e stack 脚本由参考的 Java/Vite 版本改造为 **Go 二进制 + 构建后 dist**（保留 E2E_EXTERNAL_STACK 复用外部 stack）。
- 认证状态：沿用参考 playwright.config 的 `auth` project + storageState；数据目录 `web/.e2e-data/` 已 gitignore。

## 6. M4 验收映射

- G8：8 页面可用、无账户/目录/背景入口与网络请求、直连对话框字段/复制、SSH 用户保存后 direct-access 回显、Playwright 全绿、无 console error（静态资源 401 噪音需说明）、品牌/许可可读。
- G5：直连对话框展示 `tunnelCommand/directUrl/sshUser/note`，未就绪提示，复制回退。
- G4（UI 侧）：实例页/首页打开 /workbench 链接、SSE/WS 在 iframe/新窗口可用（浏览器验收在 M2 证据基础上补一条 UI 触发）。
- G6/G7（UI 侧）：预览/导入/导出下载、插件列表/安装/更新/移除的进度、错误（同步 400 vs Job VALIDATION/NOT_FOUND）、忙态。
