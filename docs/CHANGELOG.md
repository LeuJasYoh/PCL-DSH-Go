# 更新日志

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/) 风格与语义化版本。**项目版本（如 0.2.1）与 DSH 运行时版本（如 0.2.1-rc.3）是两套独立版本号**，请勿混淆。

## [Unreleased]

### Changed

- 原创代码与 0.3.0 clean-room 壳层切换为 **MIT**（根 `LICENSE`、Go/脚本 SPDX）。旧 `web/src` 的 HDSL/HMCL 派生视图继续按 **GPL-3.0-or-later + HMCL §7** 保留，详见 `LICENSES/LEGACY-WEB-GPL.md`；包含旧视图的组合发行仍受 GPL 约束，待 clean-room 替换后移除例外。已发布的 0.3.0 包不变，MIT 不追溯。

## [0.3.0] - 2026-10-30

### Added

- 新壳层：56px 命令栏、64px 导航栏、232px 上下文侧栏；实例 / 资源库 / 设置三个入口。
- 顶栏统一实例切换、启动/打开 Web/停止或任务入口，以及 `Ctrl K` 命令面板；主操作不再散落在页面底部。
- 全局活动面板：任务状态、耗时、最近日志、取消与“完整日志”入口；后台任务启动时自动展开，`Ctrl J` 切换。
- 深色 / 浅色主题切换，主题选择保存在浏览器本地；新的玻璃/胶囊/发丝边视觉 tokens。
- 实例概览替代旧启动页：当前实例、运行时、任务、资源与快捷入口集中展示。
- README 增加维护者 `LeuJasYoh` 与仓库链接；版权行、前端路线图和 `web/package.json` 元数据同步更新。

### Changed

- 页面标题调整：首页改为“概览”，下载改为“运行时”，任务与日志改为“活动日志”。
- 移除旧页头、200px 侧栏和浮动启动面板；首页不再使用背景图与绝对定位卡片。
- 旧业务视图接入新主题和新壳层；任务进度条合并到全局活动面板。
- 登录页移除图标与背景图，使用新主题；关于页与登录页的 GPL/HMCL 声明保持不变。
- E2E 布局断言更新到新壳层（56px 命令栏、232px 上下文栏、3 个入口 + 活动日志）。

### Notes

- 本版仍按 **GPL-3.0-or-later + HMCL §7** 发布。新壳层、主题与概览为 clean-room 实现，但旧 `web/src` 业务视图仍包含 HDSL/HMCL 派生代码；完整 clean-room 替换完成后再切换根许可证与 SPDX 头，MIT 不作为本版的许可声明。
- 后端由朋友重写的新版本尚未提交；本发行版使用当前 0.2.4 后端 API，前端接口层保持不变。
- 本机已验证 `go test ./...`、`vue-tsc --noEmit`、`vite build` 与发行包组装；Playwright E2E 因本环境的 WSL 端到端栈无法启动而未执行，建议在 CI/WSL 补跑后再提升为稳定版。

## [0.2.4] - 2026-10-25

### Fixed

- 解除 `/workbench` WebSocket 上下行单条消息 1 MiB 限制，修复大型 DSH 会话历史在代理处以 1009 关闭并反复重连的问题。

## [0.2.3] - 2026-10-25

### Fixed

- 插件命令使用的 pnpm store 改为 pnpm 11 实际读取的 `PNPM_CONFIG_STORE_DIR`，避免写入只读的 `current/tools/pnpm/store`。
- DSH 使用实例 `workspace` 作为 `HOME`，并通过实例级 Cordis 配置固定默认工作区位置；目录选择器不再落到服务账号的数据根目录，已有自定义 Documents 配置保持不变。
- 直连信息复制在非安全上下文先尝试兼容复制；仍需手动复制时自动滚动到文本框并选中内容。

## [0.2.2] - 2026-10-25

### Added

- 仓库根 `VERSION` 作为项目版本唯一来源；新增 `--version`，输出构建时注入的版本号。

### Changed

- 构建、CI 与发布脚本统一从 `VERSION` 读取项目版本；`build-release.sh` 校验二进制 `--version` 与 `VERSION` 一致。
- 实例名称统一限制为 1–160 字符；实例端口统一为 1024–65535（自动分配仍从 3080 起）；前端 DSH 版本校验改为严格 SemVer。
- `GET /api/state` 的 `status` 增加 `phase` 字段；前端改用 `code`/`phase` 判断任务状态，不再解析“进行中：”文案。
- 管理员密码支持 8–256 字符；新哈希使用 SHA-256 预哈希 + BCrypt（`bcrypt-sha256`），兼容旧 BCrypt 哈希并在下次改密时升级。
- Profile 支持中文/空格，前后端统一规则：最多 64 字符、首字符为字母或数字、禁止首尾空格/控制字符/路径分隔符/连续点/结尾点/系统保留名；校验集中到 `internal/profile`。
- 顶部标题栏移除鲸鱼图标、问号、仿桌面窗口按钮与状态旋转图标，保留标题、状态文字和子页返回入口；关于与许可仍可从设置页查看。

### Security

- Cookie/CSRF 名称统一到 `internal/protocol`，安全层与工作台反代使用同一来源，避免反代剥离错 Cookie。

### Fixed

- 前端移除历史版本回退（`0.3.2`）与未使用的 `APP_VERSION`（`0.1.0`）；项目版本只显示后端返回的实际值。
- 插件端口探测测试在 WSL 中有时限地等待监听释放，避免关闭瞬间仍可连接导致误报；生产探测逻辑不变。

### Documentation

- README/用户指南同步输入规则、顶部栏和 0.2.2 变更；保留旧版截图并注明来源，区分项目版本与 DSH 验收基线。
- 开发文档说明版本注入、打包覆盖参数及 npm 元数据同步；部署文档补充新密码哈希、实例规则的回滚限制和卸载自定义目录警告。
- MIT 仍是待审计与授权确认的目标，现行 GPL/HMCL 许可未变；其他尚未批准的整理事项继续列入待办。
- 按实际发行内容重写 `THIRD_PARTY_NOTICES.md`，补齐 Go/Vue 依赖许可文本，记录 Node/pnpm 许可位置及 pnpm 内 OpenPGP.js 的 LGPL 声明与上游源码地址；移除对未随本发行物提供的 Java/HDSL 依赖的错误引用，并将 HDSL/HMCL 上游声明保留在独立章节。
- 修正关于页对 PCL-DSH-Go 整体版权的表述，区分 PCL-DSH-Go 贡献者与 HDSL/HMCL 上游版权。

## [0.2.1] - 2026-10-24

首个正式开源版本。

### Added

- WebUI 管理界面：登录/首登强制改密、启动页、实例、运行时下载、整合包、已装插件、任务与日志、设置。
- 实例管理：创建/编辑/选择/删除、启动/停止、自动端口分配、工作台入口、直连登录；支持同时运行多个实例。
- DSH 运行时管理：可用版本查询、精确版本安装（内置 pnpm）、能力探测、删除引用保护。
- 工作台反代 `/workbench/{id}/`：HTTP、SSE、WebSocket，一次性 token 引导与 Cookie/Origin/路径重写，登录门槛。
- 直连登录：SSH 隧道命令 + 一次性 token URL，SSH 用户可配置。
- 整合包：`.dspack` v2/v3 与 Overture snapshot 预览、导入（生成实例）、导出与 artifact 下载；安全归档校验。
- 已装插件：列表、安装/更新/移除（内置 bundle 保护），以任务进度展示。
- 任务与 SSE：长操作 Job 化，进度/日志/取消；管理器日志 tail。
- 部署：Linux release（内置 Node 24.22.1 + pnpm 11.28.5）、systemd unit、Nginx site 模板、install/uninstall/reset 脚本、SHA256SUMS 校验。
- 文档：用户指南、部署运维、配置参考、架构说明、开发与发布、安全策略、贡献指南。

### Changed

- 运行实例上限可通过 `--pcl.max-running-instances` 配置：默认 **0（不限制）**；资源有限的机器（如 2 vCPU / 1.6 GiB）建议显式设为 `1`；负数启动报错。达到上限时启动接口返回 `409 CONFLICT_RUNNING_INSTANCE` 并提示当前上限。
- 安装脚本支持 `--max-running-instances` 并写入 systemd unit；`--no-service` 支持隔离安装。
- 项目版本统一为 **0.2.1**，作为独立开源项目发布；文档结构重写并归档历史开发资料。

### Fixed

- 创建实例的 Harness 版本选择由原生 `datalist`（部分浏览器单击不展开）改为显式下拉，支持键盘/点选/手写精确版本（含预发布版本）。
- Windows 动态端口区间导致的个别测试不稳定问题（测试改为不依赖固定端口区间）。

### Known issues

- **工作台旧 Cookie**：升级管理器或切换实例后，浏览器可能残留上一个工作台的 DSH Cookie，导致工作台要求重新登录、空白或 `401/403`。处理方式：清除该站点 Cookie 或使用无痕窗口后重新打开；后续版本将按实例隔离工作台 Cookie。
- 应用层仅提供 HTTP，HTTPS 需由 Nginx/前置代理终止。
- 单管理员模式，不提供多用户、MFA 或账户体系。
