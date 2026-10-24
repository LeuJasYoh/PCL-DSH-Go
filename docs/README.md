# PCL-DSH-Go 文档

> 适用于 PCL-DSH-Go 0.2.1。

本目录包含 PCL-DSH-Go 的**用户文档**；开发期的设计/验收资料已归档到 [dev/](dev/README.md)，仅作历史参考。

## 用户文档

| 文档 | 内容 |
| --- | --- |
| [用户指南](USER_GUIDE.md) | 登录、运行时安装、实例创建/启动、工作台、直连登录、整合包、插件、任务日志、设置与常见问题 |
| [部署与运维](DEPLOYMENT.md) | 发布包校验、安装/升级/回滚/卸载/重置、systemd、Nginx、权限、备份、排障 |
| [配置参考](CONFIGURATION.md) | 启动参数、环境变量、数据目录布局、端口、运行实例上限与资源建议、WebUI 设置 |
| [架构说明](ARCHITECTURE.md) | 组件、Go 包结构、请求/任务流程、安全模型、数据 schema 与 DSH 集成 |
| [开发与发布](DEVELOPMENT.md) | 构建、测试、前端、CI、release 打包、发布流程与项目约束 |
| [更新日志](CHANGELOG.md) | 版本功能与修复记录 |
| [贡献指南](../CONTRIBUTING.md) | 环境、PR 流程、测试与 GPL 贡献声明 |
| [安全策略](../SECURITY.md) | 漏洞报告、支持版本、安全模型与加固建议 |

项目入口与快速开始见仓库根目录 [README](../README.md)。

## 图片

[images/](images/) 存放 README/用户指南引用的界面截图（来源为实际运行的 WebUI）：

| 文件 | 页面 |
| --- | --- |
| `login.png` | 登录页 |
| `home.png` | 启动页 |
| `instances.png` | 实例列表 |
| `multi-instance.png` | 多实例同时运行 |
| `runtimes.png` | 运行时下载 |
| `version-picker.png` | 实例版本选择器 |
| `packs.png` | 整合包 |
| `plugins.png` | 已装插件 |
| `logs.png` | 任务与日志 |
| `settings.png` | 设置 |
| `workbench.png` | DSH 工作台（经反代） |

## 历史归档

- [dev/](dev/README.md)：开发期计划、实现规格、DSH 集成契约、前端迁移指南、验收标准、任务拆解、各里程碑接口/结果与旧版部署指南。**已不再更新**，其中命令/版本/API 可能过时；当前信息以本目录用户文档为准。
- [evidence/](evidence/)：开发期真实 DSH 验收证据（日志/截图，可能包含测试环境的临时路径与版本信息，仅作审计参考）。