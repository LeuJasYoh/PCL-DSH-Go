# PCL-DSH-Go

PCL-DSH-Go 是 [PCL-DSH-Linux](https://github.com/LeuJasYoh/pcl-dsh-linux) 的 Go 重写版：在 Linux 服务器上以 WebUI 管理 DeepSeek Harness（DSH）实例的管理器。

> 状态：开发计划已定稿，等待实现（M0 之前）。本仓库包含计划、实现规格、DSH 集成契约、前端迁移指南、验收标准、任务拆解与实现提示词。

## 目标

- 按精确版本安装 DSH 运行时，创建/启停 DSH 实例；
- /workbench/{id}/ 反代 DSH 工作台（HTTP / SSE / WebSocket）；
- 任务与日志、整合包导入导出、已装插件管理、直连登录（SSH 隧道 + 一次性 token URL，SSH 用户可配置）；
- 保留 WebUI；不含账户系统、社区插件目录、背景图管理、旧数据兼容；
- 内置 Node 24.22.1 + pnpm 11.28.5，服务器无需系统 Node/npm/JDK。

## 文档

- [开发计划](docs/DEVELOPMENT_PLAN.md)
- [实现规格](docs/IMPLEMENTATION_SPEC.md)
- [DSH 集成契约](docs/DSH_INTEGRATION.md)
- [前端迁移指南](docs/UI_MIGRATION.md)
- [验收标准](docs/ACCEPTANCE.md)
- [任务拆解（Agent Teams）](docs/TASK_BREAKDOWN.md)
- [给实现 AI 的提示词](docs/AGENT_PROMPT.md)

## 环境与约束

- 开发：Windows + Go 1.27.3；验证：WSL2 Ubuntu 26.04.1（Node 24.22.1、pnpm 11.28.5、Nginx 1.28.3）。
- 部署：Linux x86_64、HTTP + Nginx + systemd、单用户、最多 1 个 DSH 实例。
- **不修改 Windows DSH 任何文件**；真实 DSH 测试只在 WSL 副本/临时数据目录。

## 许可

GPL-3.0-or-later + HMCL 第 7 条附加条款（见 [LICENSE](LICENSE)、[LICENSES](LICENSES/)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)）。
本项目复用 PCL-DSH-Linux / HDSL 的界面与设计，相关版权与许可声明保留。
