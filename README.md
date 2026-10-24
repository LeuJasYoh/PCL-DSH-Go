# PCL-DSH-Go

PCL-DSH-Go 是 [PCL-DSH-Linux](https://github.com/LeuJasYoh/pcl-dsh-linux) 的 Go 重写版：一个在 Linux 服务器上以 WebUI 管理 DeepSeek Harness（DSH）实例的管理器。

> 状态：开发计划阶段（M0 之前）。仓库当前包含开发计划、许可材料与项目骨架；尚未开始编码。

## 目标

- 按精确版本安装 DSH 运行时，创建/启停 DSH 实例；
- 通过 /workbench/{id}/ 反代 DSH 工作台（HTTP / SSE / WebSocket）；
- 任务与日志、整合包导入导出、已装插件管理、直连登录（SSH 隧道 + 一次性 token URL）；
- 保留 WebUI；不含账户系统（API Key 统一管理）、社区插件目录、旧数据兼容；
- Node/pnpm 随发行包内置，服务器无需系统 Node/npm。

## 文档

- [开发计划](docs/DEVELOPMENT_PLAN.md)（v1.0，目标、范围、架构、里程碑、测试与风险）

## 开发环境（参考）

- Go 1.27+
- Node 24.22.1 + pnpm 11.28.5（随发行包内置；开发机也可用）
- 验证目标：Linux x86_64 / WSL Ubuntu；不修改 Windows 上的 DSH 任何文件
- 部署形态：HTTP + Nginx + systemd；单用户、同时最多 1 个 DSH 实例

## 许可

GPL-3.0-or-later + HMCL 第 7 条附加条款（见 [LICENSE](LICENSE)、[LICENSES](LICENSES/)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)）。
本项目复用 PCL-DSH-Linux / HDSL 的界面与设计，相关版权与许可声明保留。
