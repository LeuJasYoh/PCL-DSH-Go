# 更新日志

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/) 风格与语义化版本。**项目版本（如 0.2.1）与 DSH 运行时版本（如 0.2.1-rc.3）是两套独立版本号**，请勿混淆。

## [Unreleased]

暂无。

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
