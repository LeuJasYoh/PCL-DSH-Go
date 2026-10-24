# PCL-DSH-Go M1 验收报告（后端 v1 API + 真实 DSH 集成）

> 状态：**定稿（qa-release 产出、Lead 复核通过，2026-10-24）**。
> 日期：2026-10-24。里程碑：M1（认证/会话/CSRF、实例/运行时生命周期、Job/SSE、/workbench 登录门槛、直连登录后端）。
> 环境：WSL2 Ubuntu 26.04.1；内置 Node 24.22.1 + pnpm 11.28.5；Windows Go 1.27.3 交叉编译；真实 DSH **0.2.1-rc.3**。
> 边界：真实 DSH 只在 WSL `/tmp/pcl-dsh-go-test` 独立 data-root；未修改 Windows DSH、参考仓库与 `/opt` 既有部署；token/密码/会话 cookie 全流程内存化并在证据中脱敏为 `***`。
> 主要证据：`docs/evidence/m1/`（矩阵、自检、基线、E2E 日志）。

## 1. 结论摘要

- **M1 后端垂直链路端到端通过**：当前工作树交叉编译 manager `0.2.0-m1`（8,798,368 B，sha256 `0da0825ba0bb7733accc1278b7ececb9788db3425c9827091c44945c6fde570c`），在 fresh data-root 上完成：
  **登录/改密 → 实例 CRUD/选择 → 运行时安装（Job+SSE）→ 探测 → 启动（Job+SSE）→ /workbench 登录门槛与 HTTP/静态/SSE/WS 反代 → direct-access → 停止 → 删除/trash/引用保护 → 重启持久化 → 登录限流 → 清理**，正式 E2E **209/209 检查 PASS，EXIT_CODE=0**（`docs/evidence/m1/m1-acceptance-20261024-152121.log.txt`）。
- **G1/G2/G3/G5 后端验收项全部覆盖并通过**；G4 的 M1 部分（登录门槛 + 直连管理器链路反代）通过，完整 G4（Nginx 生产链路/浏览器前端）留 M2/M4。**未声称 G1–G10 全绿**。
- 真实安装 Job：**81.4s**，378 个 SSE 事件、**5 次心跳**、result `{version:"0.2.1-rc.3", installed:true, compatibility:"unverified"}`；probe Job 0.9s → `verified`；launch Job **1.5s** SUCCEEDED；取消 install Job 5ms → CANCELLED；Last-Event-ID 重放成功；忙时写接口 409。
- 工作台反代：未登录 401 → 登录后 302（带一次性 token）→ DSH 303 `Location: ./` + Set-Cookie Path=`/workbench/{id}` → 页面 200（title=DeepSeek Harness，base href=./）→ 静态资源 200 → SSE 分块 → WS 101+ready+close 1000。
- direct-access：运行中 ready=true/port/tunnelCommand/sshHost（仅请求 Host）/sshUser（settings）正确，directUrl 含一次性 token 但证据脱敏、响应 no-store；停止后 ready=false+note；未登录 401、不存在 404。
- 安全/边界：错误 JSON 四字段（details={}）、traceId 12hex、413/405、data 0700/config 0600、manager **0 ERROR**、日志/证据 **0 token/密码泄漏**；Windows DSH 程序树 hash 前后相同；参考仓库 git status 基线相同；18080/3080 已释放、无残留进程。
- M1 期间修复的缺陷：task-22（实例目录改为提前创建，DELETE 从未启动实例返回非空 trashedPath）、c12942b（InstalledVersions 跟随软链运行时目录）。未发现未解决的 P0/P1 契约缺陷。

## 2. 环境与基线

| 项 | 值 |
| --- | --- |
| 验证系统 | WSL2 Ubuntu 26.04.1 LTS（kernel 6.18.33.2-microsoft-standard-WSL2，x86_64） |
| Node / pnpm | v24.22.1（内置）/ 11.28.5（vendored），均通过发布形态工具链路径调用 |
| 交叉编译 | Windows go1.27.3（`go.exe` + cmd .bat 注入 GOOS/GOARCH，`-buildvcs=false`） |
| 目标二进制 | `bin/pcl-dsh-go` linux/amd64，8,798,368 B，sha256 `0da0825b…`，`/api/health` version=`0.2.0-m1` |
| DSH 基线 | `@deepseek-ai/dsh@0.2.1-rc.3`（npm `next`；运行时 508 MB） |
| data-root | `/tmp/pcl-dsh-go-test/m1-data`（每次 E2E 删除重建，0700；manager 127.0.0.1:18080） |
| Windows DSH 基线 | 程序目录 `d21dbbc5855f4bfdf5e2c6542c872930e3546a239e15b699cde37d1eb0d12351`（`find -printf` 清单 sha256；只读） |

## 3. M1 交付物与验证方法

| 交付物 | 说明 |
| --- | --- |
| `docs/evidence/m1/contract-matrix.md` | 30+ 端点契约矩阵（鉴权/CSRF/请求/成功/失败码）、错误码表、G1–G5 追踪、冻结口径 RESOLVED、task-19 实际结果 |
| `scripts/wsl/m1-acceptance.sh` | M1 验收入口：`--selfcheck`（47 项自检）/ `--baseline`（Windows DSH 基线）/ `--full`（正式 E2E）；fresh data-root、0600 cookie jar 退出必删、脱敏、失败保留现场、脚本自写 `EXIT_CODE` |
| `scripts/wsl/m1-phases.sh` | `--full` 的 E2E phases（G1–G5 + 权限/Windows DSH/参考仓库） |
| `scripts/wsl/lib/job-sse.mjs` | Job SSE 客户端（id/event/data、心跳、Last-Event-ID、终态/monotonic 断言） |
| `scripts/wsl/lib/windows-dsh-baseline.sh` | 只读 Windows DSH 基线/校验（程序目录严格比对，用户 `.dsh` 仅 INFO） |
| `docs/evidence/m1/m1-acceptance-20261024-152121.log.txt`（+ manager.log.txt） | **正式 E2E 证据**：209 PASS / 0 FAIL / EXIT_CODE=0（脱敏） |

模块/单测门禁（Lead 复核）：gofmt 干净、`go vet` 0、Windows `go test ./...` 15 包全绿、`GOOS=linux` 构建成功；Linux 测试二进制经 `scripts/wsl/run-linux-tests.sh` 在 WSL 执行。

## 4. 验收结果（按门禁）

### 4.1 构建与启动

- 从当前工作树重建：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=0.2.0-m1"` → 8,798,368 B，sha256 `0da0825b…`（证据第 5 行）。
- `GET /api/health` 匿名 200 `{status:"UP",version:"0.2.0-m1",uptimeSeconds}`；每个响应含 `X-Trace-Id`（12 hex）、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`；`/api/**` 带 `Cache-Control: no-store`。

### 4.2 G1 认证/会话/CSRF/首登改密

| 检查 | 结果 |
| --- | --- |
| 未登录 `/api/state`、`/api/instances`、`/api/jobs`、`/workbench` → 401 JSON `UNAUTHORIZED`（非 HTML/重定向） | PASS |
| 未知 `/api` 路径：匿名 → 401（fail-closed，认证先于路由）；已登录 → 404 JSON | PASS（矩阵 §9/§10 已记录冻结口径） |
| 首登 pending：登录后 `/api/state`、`/workbench` → 403 `PASSWORD_CHANGE_REQUIRED`，`details.endpoint="/api/auth/password"` | PASS |
| 登录：204 + `JSESSIONID`（HttpOnly/SameSite=Lax/Path=/、无 Secure）+ `XSRF-TOKEN`（可读） | PASS |
| `/api/auth/me`：匿名与登录均下发/刷新 XSRF，登录后 csrfToken 与 Cookie 一致 | PASS |
| 改密：204、initial-password 文件删除、pending=false | PASS |
| CSRF 双提交：缺 `X-XSRF-TOKEN` → 403 `CSRF_INVALID`；带 → 成功 | PASS |
| 登出：有会话缺 CSRF → 403；带 CSRF → 204 且旧会话 401；匿名 logout → 204（免 CSRF） | PASS |
| 登录限流：连续失败第 5 次 → 423 `LOGIN_LOCKED`，`details.retryAfterSeconds=60`；锁定期正确密码也 423；已有会话不受影响 | PASS |

### 4.3 G2 实例 / 运行时

| 检查 | 结果 |
| --- | --- |
| 实例创建 201（port=0 自动分配；profiles 含 web；status 未启动）；空名/非法版本/非法端口 400 VALIDATION；重复端口 409 CONFLICT_PORT_BUSY | PASS |
| GET 实例 200；PUT name 200；改 version/profile → 409 CONFLICT_UNSUPPORTED_CHANGE；运行中改 port → 409 CONFLICT_INSTANCE_RUNNING | PASS |
| PUT selection 200；manager 重启后 `/api/state.currentInstanceId` 保持 | PASS |
| 启动前 precheck：runtime 未装 → 409 INSTANCE_NOT_READY；未知实例 → 404；端口被占用 → 409 CONFLICT_PORT_BUSY | PASS |
| 删除：停止实例 → 200 非空 `trashedPath`（目录进 `data/trash`）；**从未启动实例删除也返回非空 trashedPath**（task-22 修复验证）；删除后 GET 404 | PASS |
| 运行时：安装 202 Job → 已安装 unverified → probe Job → verified；DELETE 被实例引用 → 409 CONFLICT_IN_USE；删除未被引用 → 200 trashedPath 进 trash；再删 → 404 | PASS |
| 单实例/忙：已有实例运行时启动第二个 → 409 CONFLICT_RUNNING_INSTANCE；重复启动同一实例 → 409 CONFLICT_BUSY；Job 运行中写 settings → 409 CONFLICT_BUSY | PASS |

### 4.4 G3 Job / SSE

| 检查 | 结果 |
| --- | --- |
| 202 JobView 字段完整（id 格式 `j-<12hex>`/type/state/phase/message/createdAt/lastSeq/result/error） | PASS |
| 真实 install Job：202 → SSE state+log+result，id 单调，**81.4s / 378 事件 / 5 次心跳**，result `{version,installed:true,compatibility:"unverified"}` | PASS |
| probe Job：SUCCEEDED（0.9s，result compatibility=verified） | PASS |
| launch Job：SUCCEEDED（1.5s，result `{instanceId,webUrl:"/workbench/{id}/"}`，不含 token） | PASS |
| 取消：install Job 202 后立即 cancel → 202，最终 **CANCELLED**（本场景 5ms，staging 未产生；设计允许保留 staging 供诊断） | PASS |
| Last-Event-ID 重放已完成 Job：补发历史事件成功、终态 SUCCEEDED | PASS |
| 忙时其它写接口 409 CONFLICT_BUSY；`/api/jobs` 最近 20；manager 重启后历史清空（内存设计） | PASS |

### 4.5 G4 工作台反代（M1 部分）

| 检查 | 结果 |
| --- | --- |
| 未登录 `/workbench/{id}/` → 401 JSON；登录后 → 302（Location 带一次性 token，证据脱敏） | PASS |
| 跟随后 DSH 303 `Location: ./`；Set-Cookie 重写 `Path=/workbench/{id}`、无 Domain/Secure | PASS |
| 页面 200（title=DeepSeek Harness、`base href="./"`）；静态资源 `assets/index-*.js` 200 | PASS |
| SSE（`/plugins/events`）：200 text/event-stream，首块 `": connected"` + data 分块（客户端 6ms headers/2 chunks） | PASS |
| WS（`/api/remote.mux`）：101 升级、ready 帧、close 1000 | PASS |
| 实例停止后：`/workbench` → 409（INSTANCE_NOT_READY 语义） | PASS（响应体为代理错误 JSON，见 §5） |

### 4.6 G5 直连登录（后端）

| 检查 | 结果 |
| --- | --- |
| 运行中：200 ready=true、port、`tunnelCommand="ssh -L <port>:127.0.0.1:<port> root@127.0.0.1"`、sshHost=127.0.0.1（仅请求 Host）、sshUser=settings.directAccessSshUser、directUrl 含 token（证据脱敏）、`Cache-Control: no-store` | PASS |
| 停止后：200 ready=false、note=实例未运行、directUrl=null | PASS |
| 未登录 → 401；不存在实例 → 404 | PASS |
| 设置 sshUser 变更后 direct-access 回显新值（校验非法值 400 VALIDATION） | PASS |

### 4.7 安全 / 权限 / 日志 / 未越界

| 检查 | 结果 |
| --- | --- |
| data-root 0700；config/*.json 0600；initial-password 改密后删除 | PASS |
| manager 日志 ERROR 行 | **0** |
| 日志/证据 token/密码/会话 cookie 泄漏扫描 | **0 命中**（含 E2E 全流程与 manager.log） |
| 错误 JSON 四字段（code/message/details/traceId，details={}）；traceId 12hex | PASS（API 层；/workbench 代理错误见 §5） |
| 请求体超限 → 413 PAYLOAD_TOO_LARGE；不支持方法 → 405 METHOD_NOT_ALLOWED | PASS |
| 环境变量白名单/不注入账户参数 | 代码级（M1_INTERFACES §7）+ 启动检查；无 `--patch`/API Key 注入 |
| Windows DSH 程序目录（`find -printf` 清单 sha256）前后相同 | PASS |
| Windows 用户 `.dsh` 仅只读遍历（运行中的 Windows DSH 自身写会话 → hash 变化，记录 INFO） | PASS |
| 参考仓库 git status 基线 hash 前后相同 | PASS |
| 端口/进程：E2E 结束后 18080/3080 释放、无 DSH/manager 残留 | PASS |

### 4.8 重启 / 持久化 / 限流行为

- manager 重启（同一 data-root）：旧会话 Cookie 401（会话在内存）、`/api/jobs` 历史清空、selection 持久化、登录新密码 204 后继续通过。
- 登录限流按客户端 IP（127.0.0.1 单 IP）：锁定期内所有登录 423；**已建立会话不因限流失效**（E2E 把 lockout 放最后验证，不影响前序用例）。

## 5. 与 spec / 契约的差异与实现决策

1. **未知 `/api` 路径**：冻结口径为“认证先于路由解析”——匿名 401（fail-closed），已登录 404 JSON；矩阵 §1/§2 已按此更新（Lead 2026-10-24 确认）。
2. **`/workbench` 代理错误响应**：实例未运行/未就绪时由 proxy 返回 409，响应体为代理自身错误 JSON（未包含 API 四字段的 `details`/`traceId`）。**Lead 决策：M1 保留该例外**（proxy 属透传层，M2 再统一错误体或文档化 /workbench 例外），不阻塞 M1。
3. **install Job 的 compatibility**：安装完成即 `unverified`，probe 后 `verified`（按设计；不要断言 install 即 verified）。
4. **CANCELLED 终态事件**：Job 取消只发 state 终态事件（无 result/error 事件），SSE 客户端按 state 终态判定；与 IMPLEMENTATION_SPEC §4.5 的“event: state/log/result/error”兼容（cancel 无 result/error 载荷）。
5. **取消 install 的 staging**：设计允许保留 `runtimes/.install-*` 供诊断；E2E 断言容忍（本次 cancel 极快未产生 staging）。
6. **pnpm 11 上游迁移**（M0 已记录并适配）：`pnpm.overrides` 迁移到 `pnpm-workspace.yaml`、`minimumReleaseAgeExclude` 自动追加；M1 E2E 用真实安装验证（295 个 dsh-* pin rc.3）。
7. **登录限流按 IP**（127.0.0.1 单客户端）：会同时影响所有本地登录；已有会话不受影响。生产反代场景的客户端 IP 传递属 M2（TrustedProxy）。
8. **500 INTERNAL 路径**：E2E 无该场景，未触发/未伪造；错误模型代码与单测覆盖，真实触发留 M2 故障注入。

## 6. M1 未覆盖 / 遗留

- **G6 整合包**、**G7 已装插件**、**G8 WebUI**、**G9 发行/部署/离线工具链**、**G10 完整安全攻击面**（M2–M5）。
- **WebUI/前端**：登录页、Job 进度、实例/运行时/设置/直连对话框等页面未实现/未 E2E；M1 只验证后端契约。
- **/workbench 登录后的浏览器完整渲染**：M0 已用 Edge headless 验证工作台页面；M1 登录门槛 + 反代用 curl/Node 客户端验证，未重复浏览器渲染（浏览器全链路留 M2 的 WebUI/Playwright）。
- **Nginx 生产链路**（静态直出、SSE/WS 缓冲、client IP/TrustedProxy、TLS）未验证。
- **直连登录实际 SSH 隧道**：只验证 DTO 与 token 不落盘；未实际建立 SSH 隧道/浏览器直连。
- **G3 边界**：取消 launch Job、超时任务（TIMEOUT）、job 运行中重启、并发任务排队（本期单任务）未覆盖。
- **500 INTERNAL / 上游断连 502/504**：未做故障注入。
- **上传/整合包/插件**相关路径、artifact 下载、preview TTL 未实现。
- 多实例/多用户/长时间运行内存与稳定性：M1 只做单实例短时 E2E（install 期间 Node RSS 未采样）。

## 7. M2 输入 / 建议

1. **API/前端契约冻结**：以 M1 矩阵 + 冻结点为基础生成前端 TS 类型（JobView/InstanceDto/StateDto/错误体），WebUI 登录/实例/运行时/Job/设置/直连对话框对接。
2. **/workbench 登录门槛联调**：WebUI 登录后打开工作台（302 token→303→页面/SSE/WS），用 Playwright/E2E 覆盖真实浏览器（含 cookie 双域隔离）。
3. **Nginx 生产链路**：`^~ /api`/`^~ /workbench` 前缀、SSE 不缓冲、WS Upgrade、Host/可信代理、client IP 传递与登录限流、TLS；TrustedProxy/X-Forwarded-Host 设计（M1 明确不用 X-Forwarded-Host）。
4. **错误体统一**：/workbench 代理错误 JSON 与 API 四字段模型统一或文档化例外（§5.2）。
5. **Job/SSE 前端状态机**：Last-Event-ID 重连、心跳、取消、忙 409、终态事件；补 cancel launch/超时/重启中 job 的 E2E。
6. **打包/部署**：release 目录、install.sh/systemd/Nginx、内置 Node/pnpm、SHA256、无系统 Node 验证（G9）。
7. **清理策略**：取消 install 的 staging 保留策略（诊断 vs 磁盘）在 M2 明确 TTL/清理入口。
8. **安全**：上传/整合包/插件路径穿越与限值（G6/G7）、artifact、500/上游故障注入、审计事件补充。

## 8. 复现命令与证据索引

````bash
cd /mnt/d/Projects/DSHL/PCL-DSH-Go
bash scripts/wsl/m1-acceptance.sh --selfcheck        # 47 项自检（不启动 manager）
bash scripts/wsl/m1-acceptance.sh --baseline         # Windows DSH 只读基线
M1_WIRING_READY=1 bash scripts/wsl/m1-acceptance.sh --full   # 正式 E2E（约 4-5 分钟）
````

| 证据 | 内容 |
| --- | --- |
| `m1-acceptance-20261024-152121.log.txt` | 正式 E2E：209 PASS / 0 FAIL / EXIT_CODE=0（脱敏） |
| `m1-acceptance-20261024-152121-manager.log.txt` | 本次 manager 日志（0 ERROR / 0 secret） |
| `contract-matrix.md` | 契约矩阵 + 冻结口径 + task-19 结果 |
| `m1-acceptance-selfcheck.txt` | 脚本自检 47 项 PASS |
| `m1-acceptance-full-refusal.txt` | wiring 未就绪时 --full 安全拒绝 evidence（exit 3） |
| `windows-dsh-baseline-before.txt` | Windows DSH 程序目录只读基线 hash |

## 9. 阶段提交（M1）

| commit | 内容 |
| --- | --- |
| `7f0a4a1` | chore(m1): freeze M1 interfaces and add shared API error model |
| `47cdad9` | feat(m1): v1 REST API, auth/CSRF/jobs/SSE, runtime+instance lifecycle, direct-access |
| `c12942b` | fix(m1): follow symlinked runtime directories in InstalledVersions |
| `47cdad9`（含） | task-22：实例目录提前创建，DELETE 从未启动实例返回非空 trashedPath（随 M1 垂直切片提交） |
| 本报告提交 | M1 验收脚本/证据（scripts/wsl/m1-acceptance.sh、m1-phases.sh、lib/job-sse.mjs、docs/evidence/m1/**）+ 本报告 |

> M0 基线提交序列见 docs/M0_RESULTS.md §15。

## 10. 完成项对照与已知限制

| 项 | 状态 |
| --- | --- |
| G1 认证/会话/CSRF/首登改密/登录限流 | ✅ 覆盖（§4.2） |
| G2 实例/运行时 CRUD/删除/trash/引用保护/单实例/忙 | ✅ 覆盖（§4.3） |
| G3 Job/SSE/取消/Last-Event-ID/心跳/忙/jobs 历史 | ✅ 覆盖（§4.4）；取消 launch/超时留 M2 |
| G4 工作台反代（M1 部分） | ✅ 登录门槛 + 直连管理器链路；Nginx/浏览器全链路留 M2/M4 |
| G5 直连登录后端 | ✅ DTO/权限/no-store/token 脱敏；实际 SSH 隧道留 M2 |
| 安全/权限/日志/Windows DSH/参考仓库 | ✅ 覆盖（§4.7） |
| 500 INTERNAL / 502/504 故障注入 | ⏳ 未触发（M2） |
| WebUI / 整合包 / 插件 / 发行包 | ⏳ M2–M5 |

## 11. M1 任务完成对照（docs/TASK_BREAKDOWN.md M1-1…M1-8）

| 任务 | 完成内容 | 证据 |
| --- | --- | --- |
| M1-1 配置/模型/DTO/错误模型/traceId | `internal/apierr` 统一错误模型 + API DTO/错误体/12hex traceId | `internal/server/api/{respond,dto}.go`；§4.2/§4.7 |
| M1-2 认证/会话/CSRF/限流/审计/首登改密 | `internal/server/security` 17 测试；登录 423/CSRF/首登 403/改密/审计 | §4.2；E2E 209 检查 |
| M1-3 实例 REST + launch/stop/selection/web-url/delete | 实例完整 CRUD/自动端口/校验/409 矩阵/trash/选择持久化 | §4.3 |
| M1-4 运行时 REST + 安装/探测 Job | 安装/探测 202 Job、引用保护删除、registry available、软链运行时兼容 | §4.3/§4.4；c12942b |
| M1-5 JobManager + SSE + 取消/超时 | 单任务、事件环形缓存/心跳/Last-Event-ID/取消/历史 20 | `internal/server/job`；§4.4 |
| M1-6 日志 tail + 设置 + state + health | `/api/logs/tail`、settings 校验/持久化、state 聚合、health | §4.2/§4.3/§4.7 |
| M1-7 direct-access API（SSH 用户可配置） | `internal/directaccess` DTO + API 接线，token 仅内存/no-store | §4.6 |
| M1-8 契约测试（httptest）与 M1 验证 | 45 API 契约测试 + server 接线集成测试 + 209 项黑盒 E2E | §3/§4；`docs/evidence/m1/` |

## 12. Lead 复核结论

- **M1 达到退出条件**：后端 v1 API（G1/G2/G3/G5）与 /workbench 登录门槛（G4 的 M1 部分）经真实 DSH rc.3 黑盒验收 **209/209 PASS**；单测/契约测试全绿；无 P0/P1 未解决缺陷。
- **已接受差异**（§5）：/workbench 代理错误体例外、install compatibility=unverified→probe verified、CANCELLED 仅 state 终态事件、install 取消 staging 保留、登录限流按 IP（TrustedProxy 留 M2）。
- **未验证项如实保留**（§6）：Nginx 生产链路、WebUI/Playwright、500/502/504 故障注入、取消 launch/超时任务、直连 SSH 隧道实连、G6/G7/G8/G9/G10 完整门禁。
- **M2 优先级建议**：① /workbench 反代硬化 + Nginx 生产链路（G4）；② WebUI 契约冻结与前端实现（G8，可并行）；③ 错误体/TrustedProxy/故障注入补强；④ 整合包/插件（G6/G7）。
- **红线复核**：Windows DSH 程序目录 hash 前后相同（只读）、参考仓库未改、真实 DSH 仅 WSL `/tmp`、无账户系统/无源码 clone 构建、GPL 声明保留；token/密码/会话 0 泄漏。

> 报告结束。M1 验收证据全部位于 `docs/evidence/m1/`；token/密码/会话 cookie 已在所有证据中脱敏为 `***`。