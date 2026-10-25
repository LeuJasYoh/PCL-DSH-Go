# AGENTS.md — PCL-DSH-Go 项目standing rules

本仓库由 AI 实现代理维护；所有改动必须遵守以下规则，并与 docs/DEVELOPMENT_PLAN.md、docs/IMPLEMENTATION_SPEC.md、docs/DSH_INTEGRATION.md、docs/UI_MIGRATION.md、docs/ACCEPTANCE.md 保持一致。

## 必须做

- 先读文档再改代码；实现与文档冲突时先更新文档（或记录决策）再改代码。
- 版本号以仓库根 `VERSION` 为唯一来源；构建、CI、发布脚本从这里读取，`bin/pcl-dsh-go --version` 必须与之一致；不要在其他文件硬编码项目版本。
- 使用 Go 1.27+；gofmt/go vet/go test 全绿；前端 npm run build / vue-tsc 通过。
- 安全优先：认证/会话/CSRF/登录限流/首登改密/日志脱敏/SSRF/路径穿越/上传限制/原子写/flock 按规范实现。
- 真实 DSH 测试在 WSL 中，用 runtime 副本与独立 data-root（/tmp/pcl-dsh-go-test）；测试后清理。
- 发布包必须内置 Node 24.22.1 + pnpm 11.28.5，目标机不依赖系统 Node/npm/JDK。
- 许可：GPL-3.0-or-later + HMCL §7；保留 LICENSE/LICENSES/THIRD_PARTY_NOTICES；复制参考前端/资产时保留版权声明。
- 小步提交；提交信息说明做了什么/验证了什么；不提交构建产物、数据、密钥、日志。

## 绝对禁止

- 修改 Windows DSH 任何文件：D:\DevelopSoftware\DSH\**、C:\Users\<用户>\.dsh\**、Windows DSH 的配置/插件/会话/缓存/实例/进程。
- 修改参考仓库 D:\Projects\DSHL\PCL-DSH-Linux。
- 在 Windows 上运行/安装本管理器；所有运行验证在 WSL/Linux。
- 实现账户系统（API Key/Provider/加密/绑定/overlay/--patch）、社区插件目录、背景图上传/切换、旧数据兼容。
- 服务器端 clone/编译 DSH；只走 npm + 随包 pnpm。
- 伪造验收、mock 真实 DSH 测试、提交占位接口/未实现但返回成功。
- 移除或替换 GPL/HMCL 许可声明。

## Agent Teams

- Lead 必须使用共享任务板（team_task_*）拆分/跟踪工作；同一文件/目录同一时间只有一个 writer。
- 子代理只能改任务 write scope 内文件；跨模块修改由 Lead 协调。
- 接口（数据 schema/API/错误码/SSE/DSH 命令/反代语义）以 docs/IMPLEMENTATION_SPEC.md 与 docs/DSH_INTEGRATION.md 为准；变更需更新文档并通知。
- 每个任务完成前跑验收命令并留下证据；Lead 在里程碑结束做集成验收。

## 快速入口

- 开发计划：docs/DEVELOPMENT_PLAN.md
- 实现规格：docs/IMPLEMENTATION_SPEC.md
- DSH 集成：docs/DSH_INTEGRATION.md
- 前端迁移：docs/UI_MIGRATION.md
- 验收：docs/ACCEPTANCE.md
- 任务拆解：docs/TASK_BREAKDOWN.md
- 给实现 AI 的主提示词：docs/AGENT_PROMPT.md
