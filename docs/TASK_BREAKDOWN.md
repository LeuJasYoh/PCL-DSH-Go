# PCL-DSH-Go 任务拆解与 Agent Teams 工作方式（v1.0）

> 本文件是 Lead 创建共享任务板、拆分给子 agent 的基线。任务 ID 可直接用 team_task_create 创建；write_scopes 为工作目录相对路径前缀。
> 原则：**同一文件同一时间只有一个 writer**；跨模块接口先冻结（IMPLEMENTATION_SPEC / DSH_INTEGRATION）；每个任务必须有测试与证据；Lead 负责最终集成与验收。

---

## 1. 建议团队结构

| 角色 | 职责 | 主要 write scope |
| --- | --- | --- |
| Lead（实现 AI 主代理） | 架构、接口冻结、任务板、集成、验收、最终提交 | 全局；优先修接口/集成问题 |
| backend-core | config/logging/store/tools/runtime/process/cmd | internal/config、internal/logging、internal/store、internal/tools、internal/runtime、internal/process、cmd/ |
| backend-server | auth/session/csrf/rest/jobs/logs/settings/state/static | internal/server（除 proxy/directaccess 子目录） |
| proxy | /workbench HTTP/SSE/WS + direct-access | internal/server/proxy、internal/directaccess |
| pack | 安全归档/dspack/Overture/预览/导入/导出/artifact | internal/pack、internal/artifact（或 pack 内） |
| plugin | 已装插件列表/变更/Node 桥 | internal/plugin、internal/runtime 的 bridge 相关（与 core 协调） |
| frontend | 复用 Vue 并瘦身、直连对话框、SSH 用户设置、E2E | web/ |
| qa-release | 测试门禁/发布包/install/systemd/Nginx/验收脚本/文档核对 | packaging/、scripts/、testdata/、docs/ACCEPTANCE.md（与 Lead 协调） |

说明：人数可压缩（例如 core+server 合并、pack+plugin 合并），但**接口/write scope 不变**；最多 8 个成员（含 Lead 的队友上限按运行环境）。

---

## 2. 团队协作规则（必须执行）

1. Lead 先读取 docs/DEVELOPMENT_PLAN.md、IMPLEMENTATION_SPEC.md、DSH_INTEGRATION.md、UI_MIGRATION.md、ACCEPTANCE.md，再创建任务板。
2. 每个任务必须写清：目标、输入/接口、write scope、验收命令、依赖任务、负责人；Lead 用 team_task_create 创建并 set_dependencies。
3. 子 agent 认领前 team_task_get 查看最新 revision，claim 后开始；完成时跑验收命令并在 complete 前把证据写进任务/报告。
4. 同一文件/目录不允许多个 agent 同时写；需要跨界修改时由 Lead 协调（先完成任务/加依赖/拆任务）。
5. 共享接口（data schema/API DTO/错误码/SSE 事件/DSH 命令）以 IMPLEMENTATION_SPEC/DSH_INTEGRATION 为准；子 agent 不得擅自改接口。
6. 每个里程碑结束 Lead 做集成：go test ./...、前端 build、WSL 冒烟；不通过不得进入下一里程碑。
7. 禁止修改 Windows DSH/参考仓库；真实 DSH 测试用 WSL 副本；所有临时数据放 /tmp 且清理。
8. 结束时 Lead 必须提交 git（分阶段 commit）、更新 README/文档、给出验收证据与遗留项。

---

## 3. 里程碑与任务清单

### M0：骨架 + 垂直切片（5–8 AI 人日）

| ID | 任务 | 建议 owner | write scope | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| M0-1 | 仓库骨架：go.mod、cmd/main、config/logging、flags/env、data-root 初始化、flock、优雅停机 | backend-core | cmd/、internal/config/、internal/logging/、internal/store/ | — | go build/test；启动打印版本；data-root 0700；无 JDK |
| M0-2 | Node/pnpm 工具层：定位/解压/校验/调用 pnpm、工具目录、版本清单 | backend-core | internal/tools/ | M0-1 | 单测：查找顺序/校验；WSL 内 node -v/pnpm -v 通过管理器调用 |
| M0-3 | 实例 store + 进程管理：instances.json 原子写、spawn/进程组/stdout token/就绪/停止 | backend-core | internal/store/、internal/process/ | M0-1/M0-2 | 单测 + mock 进程；端口释放断言 |
| M0-4 | 最小 runtime/probe/launch：npm 版本查询、安装命令、能力探测、启动命令/环境 | backend-core | internal/runtime/ | M0-2/M0-3 | 单测；WSL 真实 rc.3 安装/启动（只用手动数据目录） |
| M0-5 | /workbench 反代最小闭环：HTTP/SSE/WS + token/Cookie/Location 重写 | proxy | internal/server/proxy/ | M0-3/M0-4 | mock 上游单测 + WSL 真实 DSH 冒烟 |
| M0-6 | M0 验收记录与演示脚本 | qa-release | docs/、scripts/（验收脚本） | 全部 | 按 ACCEPTANCE §11 记录；Lead 复核 |

**M0 退出条件**：真实 DSH rc.3：安装→启动→就绪→浏览器打开工作台（HTTP/静态/SSE/WS）→停止/端口释放；无账户注入；Windows DSH 未改。

### M1：后端核心 API（7–9 AI 人日）

| ID | 任务 | owner | scope | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| M1-1 | 配置/模型/DTO/错误模型/traceId | backend-server | internal/server/api/、config | M0 | 单测/契约 |
| M1-2 | 认证/会话/CSRF/限流/审计/首登改密 | backend-server | internal/server/security/ | M1-1 | G1 |
| M1-3 | 实例 REST + launch/stop/selection/web-url/delete | backend-server | internal/server/api/、internal/store/、internal/runtime/（只调用） | M1-2/M0-3 | G2 |
| M1-4 | 运行时 REST + 安装/探测 Job | backend-server | internal/server/api/、internal/runtime/ | M1-2/M0-4 | G2/G3 |
| M1-5 | JobManager + SSE + 取消/超时 | backend-server | internal/server/job/ | M1-1 | G3 |
| M1-6 | 日志 tail + 设置 + state + health | backend-server | internal/server/api/、internal/logging/ | M1-1 | G1/G2 |
| M1-7 | direct-access API（SSH 用户可配置） | proxy | internal/directaccess/、internal/server/api/ | M1-3/M0-5 | G5 后端 |
| M1-8 | 契约测试（httptest）与 M1 验证 | qa-release | testdata/、docs/ACCEPTANCE.md | M1-1..7 | 门禁证据 |

### M2：反代硬化（4–6 AI 人日）

| ID | 任务 | owner | scope | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| M2-1 | HTTP 反代完整语义：Cookie/Location/Origin/SSE/超时/错误 | proxy | internal/server/proxy/ | M0-5/M1-3 | G4 部分 |
| M2-2 | WebSocket 反代：握手/同源/子协议/帧/关闭码/1MiB | proxy | internal/server/proxy/ | M2-1 | G4 |
| M2-3 | Nginx 链路联调 + 真实 DSH 浏览器/WS 验证 | qa-release + proxy | packaging/、docs/ | M2-2 | G4 |
| M2-4 | 反代安全/SSRF/日志脱敏测试 | qa-release | testdata/ | M2-2 | G10 相关 |

### M3：整合包 + 已装插件（5–7 AI 人日）

| ID | 任务 | owner | scope | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| M3-1 | 安全归档核心（ZIP/TAR 规则/限值/取消/路径） | pack | internal/pack/safe* | M0-1 | 恶意样本单测 |
| M3-2 | 预览（dspack v2/v3、Overture v1、preview TTL、SHA-256） | pack | internal/pack/ | M3-1 | G6 预览 |
| M3-3 | 导入 Job（runtime 解析/解包/依赖恢复/回滚） | pack | internal/pack/、internal/runtime/（调用） | M3-2/M1-4 | G6 导入 |
| M3-4 | 导出 Job + artifact 下载 | pack | internal/pack/、internal/artifact/ | M3-2/M1-3 | G6 往返 |
| M3-5 | 插件列表 + 变更（Node 桥/CLI） | plugin | internal/plugin/、internal/runtime/ bridge | M1-4 | G7 |
| M3-6 | 整合包/插件真实样本验证 | qa-release | testdata/、docs/ | M3-3/M3-5 | G6/G7 证据 |

### M4：WebUI 复用与瘦身（4–6 AI 人日）

| ID | 任务 | owner | scope | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| M4-1 | 复制 web/ + 删账户相关 + 重建 API 客户端/类型/Store | frontend | web/ | M1 接口冻结 | build/type-check |
| M4-2 | 页面改造：实例/首页/设置/隐藏目录与背景 | frontend | web/ | M4-1 | 页面验收 |
| M4-3 | 直连对话框 + SSH 用户设置 | frontend | web/ | M1-7/M4-1 | G5/G8 |
| M4-4 | Playwright 调整/新增 + 前端验收 | frontend + qa-release | web/e2e/ | M2/M3 后端 | G8 |
| M4-5 | 品牌/关于许可/构建接入 | frontend | web/、packaging/ | M4-1 | 构建产物 |

### M5：打包/部署/验收（4–6 AI 人日）

| ID | 任务 | owner | scope | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| M5-1 | 构建脚本：Go 交叉编译、前端 build、Node/pnpm vendoring、tar.gz/SHA256 | qa-release | scripts/、packaging/ | M0-M4 | 发布包产物 |
| M5-2 | install/uninstall/reset/systemd/Nginx | qa-release | packaging/ | M5-1 | G9 |
| M5-3 | CI（go vet/test/build，前端 build，E2E 可选） | qa-release | .github/、scripts/ | M5-1 | CI 绿 |
| M5-4 | 干净环境安装/升级/回滚验收 + 证据 | qa-release + Lead | docs/ACCEPTANCE | M5-2 | G9/G10 |
| M5-5 | 文档/许可/README 收尾 + 最终提交 | Lead | docs/、README | 全部 | DoD |

---

## 4. 接口冻结点（Lead 负责）

- 冻结 data schema（manager/instances/admin）与 API DTO/错误码/SSE 事件：M1 开始前。
- 冻结 DSH 命令/环境/反代语义：M0 结束时。
- 冻结 UI API 契约：M1 完成时（前端可并行 mock）。
- 任何变更走“spec 更新 + 通知受影响 owner + 重跑相关测试”。

## 5. 每日/里程碑汇报模板

- 今日完成（任务 ID + commit）
- 验证命令与结果（测试/门禁）
- 未解决/风险/需要 Lead/其他人的输入
- 下一步（任务 ID）

## 6. 结束条件

- 按 ACCEPTANCE G1–G10 全绿；发布包可安装；真实 DSH rc.3 验收通过；文档齐全；Windows DSH/参考仓库未改动；最终 commit + tag（由用户决定）。
