# PCL-DSH-Go 验收标准（v1.0）

> 本文件定义“完成”的判据。所有门禁必须有可复现命令、日志或测试报告；“通过”不允许仅凭口头描述。
> 环境：WSL2 Ubuntu 26.04.1（Node 24.22.1、pnpm 11.28.5、Nginx 1.28.3）；Windows 只做开发，Windows DSH 只读。
> 基线：DSH 0.2.1-rc.3（若 npm 未发布，回退最近版本并在验收记录写明）。

---

## 0. 通用门槛（每个里程碑都要过）

- gofmt 无差异；go vet ./... 通过；go test ./... 通过（Linux/WSL 运行）。
- 前端 npm run build / vue-tsc --noEmit 通过。
- 新增/修改文件有对应测试；不以“手工试过”替代自动化（真实 DSH E2E 除外）。
- 不修改参考仓库/Windows DSH；git 提交干净、无构建产物/数据文件。
- 所有 API/行为变更同步更新 docs/IMPLEMENTATION_SPEC.md/UI_MIGRATION.md/本文件。
- 无 TODO/FIXME 遗留在发布路径；无未实现但返回“成功”的接口。

---

## 1. G1 认证/会话/CSRF

| 检查 | 期望 |
| --- | --- |
| 未登录 GET /api/state、/api/instances、/api/jobs | 401 JSON {code:"UNAUTHORIZED"}，不是重定向/HTML |
| 登录错误密码 5 次/60s | 第 5 次后 423 LOGIN_LOCKED + details.retryAfterSeconds |
| 正确密码登录 | 204 + JSESSIONID(HttpOnly/SameSite=Lax) + XSRF-TOKEN |
| 写接口不带 X-XSRF-TOKEN | 403 CSRF_INVALID |
| 带 Cookie+Header | 通过 |
| 首次启动 initialPasswordPending | 除 auth/health 外 403 PASSWORD_CHANGE_REQUIRED |
| 改密后旧会话/旧密码 | 旧密码失效；新密码可登录；initial-password 删除 |
| 登出 | 会话失效；再次访问 401 |
| 日志/SSE/审计 | 不含密码/token/API key/cookie 原文 |

---

## 2. G2 实例/运行时

- 创建：正常返回 201；空名/非法版本/非法端口 400；重复端口 409 CONFLICT_PORT_BUSY；忙时 409 CONFLICT_BUSY。
- 编辑：名称/端口可改；改 version/profile 返回 409 CONFLICT_UNSUPPORTED_CHANGE；运行中改端口/删除拒绝。
- 删除：停止状态 → 200 trashedPath 且目录进入 trash；运行中 → 409。
- 选择：PUT selection 持久化，重启后 /api/state currentInstanceId 正确。
- 启动：202 Job；成功 /api/state 状态“运行中”；重复启动/多实例 409；失败有中文原因和任务日志。
- 停止：停止后进程树无残留、端口释放（ss -ltnp 无监听）、状态“未启动”。
- 运行时：available 查询成功；安装 202+Job；安装后 /api/runtimes installed=true；删除被实例引用 409 CONFLICT_IN_USE；删除未被引用 → trash；探测失败给原因。
- 单实例：已有实例运行中，启动另一个必须 409 CONFLICT_RUNNING_INSTANCE。

---

## 3. G3 Job/SSE

- 202 返回 JobView，字段完整（id/type/state/phase/createdAt/lastSeq/result/error）。
- SSE 收到 state → log → result（或 error）；事件 id 单调；data JSON 字段与 §4.5 一致。
- Last-Event-ID 重连补发后续事件；无重复/丢失（对比 seq）。
- 15s 心跳注释 ping 可见；SSE 连接断开不崩溃。
- 取消：运行中任务 cancel → 202，最终 CANCELLED；已结束任务 cancel → 409 CONFLICT_JOB_FINISHED。
- 超时：可配置短超时任务（测试）→ 终态 FAILED + TIMEOUT/INTERRUPTED 语义。
- 忙：任务运行中调用其他写接口 → 409 CONFLICT_BUSY。
- 最近 20 个 Job 列表；重启后历史为空（符合本期内存历史设计）。

---

## 4. G4 工作台反代（真实 DSH）

前置：WSL 内独立 data-root；DSH rc.3 已安装；启动实例成功。

- GET /workbench/{id}/ 未登录 → 401/跳登录；登录且实例运行就绪 → 302 带 token 或直接页面。
- 无 dsh-auth Cookie、无 token → 302 到 /workbench/{id}/?token=...；随后 DSH 303 + Set-Cookie 被重写（Path=/workbench/{id}、去 Domain/Secure、SameSite=Lax）。
- 页面 HTML 200；base href="./"；静态资源/插件模块经反代 200（不再回环绝对地址）。
- 前端 API（相对 /api/ 解析到 /workbench/{id}/api/...）可用。
- SSE：工作台事件流逐块到达（curl -N 能看到，不缓冲）。
- WebSocket：浏览器/测试客户端连接 /workbench/{id}/api/remote.mux（以真实页面为准），收发消息成功；关闭正常。
- 错误：实例未运行/未就绪 → 409 INSTANCE_NOT_READY；上游断开 → 502/504；请求路径含 .. → 400。
- 日志：/workbench 的 access log 不记录 query/token；应用日志无 token。
- 停止后端口释放，再次启动可访问。

---

## 5. G5 直连登录

- 实例运行中，登录后 GET /api/instances/{id}/direct-access：
  - 200；ready=true；port=实例端口；tunnelCommand=ssh -L port:127.0.0.1:port sshUser@sshHost；
  - directUrl 为 http://127.0.0.1:port/...token...；sshUser 来自 settings.directAccessSshUser（默认 root，改成其他用户后回显）。
  - 响应 Cache-Control: no-store；token 不写日志/审计。
- 实例未运行/未就绪：200 ready=false，note 中文原因，tunnelCommand 可复制。
- 未登录：401；不存在实例：404。
- 前端对话框可复制两项；说明文案完整。

---

## 6. G6 整合包

- 恶意样本（参考 PackServiceTest 的 25 个）全部被拒绝且无文件落盘/无覆盖；错误中文可操作。
- 预览：合法 dspack v2/v3、Overture snapshot 1 返回正确字段；previewId TTL 30min；过期 404/400。
- 导入：Job 成功创建实例，可启动；失败回滚到 trash，原有实例/文件不变。
- 导出：profile/home 产物可下载（artifact 24h）、命名/大小正确；不含凭据/会话/日志/账户文件；导出文件可再次预览/导入（往返）。
- 路径穿越/符号链接/大小写冲突/超限归档：拒绝。
- 无账户相关字段/逻辑。

---

## 7. G7 已装插件

- 列表：与 profile package.json 的 dependencies/optionalDependencies/bundles 一致；官方/内置/来源标识合理；未安装依赖标记“待恢复”。
- 安装：Job 成功，profile 依赖增加；重启实例后生效；失败给 pnpm/CLI 摘要。
- 更新：Job 成功；移除：Job 成功；移除内置 bundle/非独立依赖 → 409/400 明确错误。
- 运行中实例变更 → 409；忙 → 409。
- 无社区目录请求/页面。

---

## 8. G8 WebUI

- 页面：登录、首页、实例、运行时、整合包、已装插件、任务与日志、设置；无账户页/账户入口/目录页/背景设置。
- 无账户相关网络请求（/api/accounts* 等）；无账户文案/字段。
- 直连登录对话框可用；设置 SSH 用户可保存并影响接口。
- Playwright 全绿；无 console error（允许无害网络 401 噪音需说明）。
- 品牌：PCL-DSH-Go；关于/许可可读；GPL/HMCL 声明保留。

---

## 9. G9 发行/部署/离线工具链

- 构建产生 release 目录：二进制/static/tools/node/tools/pnpm/脚本/许可；tar.gz + SHA256SUMS。
- 干净 Ubuntu（WSL 容器或新目录）安装：
  - 不需要系统 Node/npm/pnpm/JDK；
  - install.sh 创建用户/目录/current/systemd/Nginx/初始密码；
  - /api/health 200；登录/创建实例/启动/工作台可用；
  - 内置 Node/pnpm 路径正确（node -v / pnpm -v 通过管理器进程调用）。
- 升级：新 release 切换 current，data 不动；回滚可切回旧 release（数据新 schema，旧版本可能不兼容——文档说明）。
- systemd 沙箱/权限/启动成功；Nginx 配置 SSE/WS/大小限制正确。
- SHA-256 校验；重启后初始密码/数据不丢；卸载保留 data。

---

## 10. G10 数据/边界/安全

- data-root 0700、config 0600、上传/预览 0600；原子写；flock 防双进程；无效 schema 明确错误。
- 无 accounts.json/secret.key/账户字段残留。
- 反代 SSRF：构造恶意 Host/路径/端口不能访问非白名单目标。
- 日志/审计脱敏用例通过。
- Windows DSH 检查：测试前后 D:\DevelopSoftware\DSH 与 C:\Users\<user>\.dsh 的 mtime/哈希不变（脚本记录）。
- 参考仓库 git status 干净（未修改）。

---

## 11. 真实 DSH 手动验收清单（WSL）

1. 准备独立目录 /tmp/pcl-dsh-go-test/{data,runtimes,tools}；复制 runtime 或在线安装 rc.3。
2. 启动 Go 管理器（127.0.0.1:18080，独立 data-root）。
3. 登录/改密；创建实例（rc.3/web/端口自动）；启动；等待运行中。
4. 浏览器或 curl 走 /workbench/{id}/：token→cookie→页面→静态资源→SSE→WS。
5. 直连登录：获取 tunnel/directUrl；按说明验证（可在同一 WSL 用 ssh 或直接 curl 回环 URL）。
6. 停止实例：无残留进程/端口释放；再次启动正常。
7. 整合包：导出→预览→导入→启动；恶意样本拒绝。
8. 插件：添加/更新/移除（真实 DSH CLI）；列表变化；重启后生效。
9. 安装包冒烟：用 release 目录安装到独立 prefix，确认不需要系统 Node/npm。
10. 记录：命令、版本、时间、结果、截图/日志路径；已知差异写“已知差异”。

> 全程禁止操作 Windows DSH；真实 DSH 只用副本/独立数据目录。

---

## 12. 完成定义（DoD）

- G1–G10 全绿，且证据可复现。
- 文档（README/DEVELOPMENT_PLAN/IMPLEMENTATION_SPEC/DSH_INTEGRATION/UI_MIGRATION/ACCEPTANCE）与实际一致。
- 发布包可在干净 Ubuntu 安装并完成上述手动验收。
- 代码通过 gofmt/vet/test；前端 build/type-check/E2E 通过。
- 无 P0/P1 未解决缺陷；已知限制明确记录。
