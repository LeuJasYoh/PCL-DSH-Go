# M1 HTTP 契约矩阵（端点 × 鉴权/CSRF × 请求 × 成功/失败码）

> 状态：**task-21 产出（qa-release），供 task-19 逐项驱动 E2E；如与实现冲突先报 Lead 并更新 docs/M1_INTERFACES.md**。
> 权威来源：docs/IMPLEMENTATION_SPEC.md §4.1–§4.5 + §5、docs/M1_INTERFACES.md §10、docs/ACCEPTANCE.md G1/G2/G3/G5、docs/DSH_INTEGRATION.md §6.4（直连）。
> 范围：M1 后端契约（登录/会话/CSRF、实例、运行时、Job/SSE、日志、设置、直连、工作台反代）。整合包/插件/artifact 属 M2/M3，§6 列为“M1 不实现”。
> 约定：HTTP 状态 + JSON 错误体 `{code,message,details?,traceId}`；成功响应 Content-Type 为 `application/json; charset=utf-8`；SSE 为 `text/event-stream`；时间戳毫秒。
> **冻结口径（2026-10-24，Lead）**：§7 的全部歧义已关闭（标 RESOLVED），backend-server 与 qa-release 一致执行；task-19 按冻结口径断言，实现偏离需记录到 M1_RESULTS 并报 Lead。
> 证据脚本：scripts/wsl/m1-acceptance.sh（task-19 填充各 phase 的实际断言）。

## 1. 通用约定与中间件决策顺序

| 项 | 规则 | 来源 |
| --- | --- | --- |
| 认证 | 除 `POST /api/auth/login`、`GET /api/health` 外，全部 `/api/**` 需有效会话；未登录 → **401 UNAUTHORIZED**（JSON，不是 HTML/重定向） | SPEC §4.1 |
| 首登待改密 | `initialPasswordPending=true` 时，除 `/api/auth/**`、`/api/health` 外（含 `/workbench` 与其它 `/api`）→ **403 PASSWORD_CHANGE_REQUIRED**，`details.endpoint="/api/auth/password"` | SPEC §4.1、M1_IF §10 |
| CSRF | 双提交：Cookie `XSRF-TOKEN` + Header `X-XSRF-TOKEN`；除登录外**所有非 GET/HEAD /api/** 必须校验**（job cancel 也要）；**logout：无有效会话 → 204 且不校验 CSRF；有有效会话 → 必须双提交**；`/workbench/**` 豁免 CSRF（仅会话）；失败 → **403 CSRF_INVALID**；常量时间比较 | SPEC §4.1/§5.3、冻结口径 #1 |
| 忙 | 全局同一时间仅 1 个变更任务；其它写 `/api/**`（auth 与 `/api/jobs/{id}/cancel` 除外）→ **409 CONFLICT_BUSY** | SPEC §4.2/§4.5、M1_IF §10 |
| 会话 Cookie | `JSESSIONID`：HttpOnly、SameSite=Lax、Path=/，12h 滑动；`XSRF-TOKEN`：非 HttpOnly、SameSite=Lax；登录 204 与 **me（无论是否登录）** 都下发/刷新 XSRF | SPEC §4.1/§5.1、冻结口径 #2 |
| Trace/安全头 | 每个响应 `X-Trace-Id`（12 hex）+ 安全头（nosniff/referrer/no-store 敏感接口）；错误体含 `traceId` | SPEC §5.10 |
| 请求体上限 | JSON 1 MiB；multipart 2 GiB（流式） | SPEC §4.1 |
| 错误模型 | 可预期错误一律 `*apierr.Error`；**错误 JSON 固定 4 字段 `{code,message,details,traceId}`，无 details 时 `details={}`（对象不是 null）**；未知 → 500 INTERNAL（只通用中文消息+traceId，详情只写日志，不回显内部路径） | M1_IF §2、SPEC §5.14、冻结口径 #6 |
| 变更端点返回 | install/probe/launch/pack 等返回 **202 JobView**；stop 200；delete 200 `{trashedPath}` | SPEC §4.2 |

**中间件决策顺序（实现与测试都按此断言）**：
1. 路由匹配 → 2. 公开路由（login/health）直接进入 → 3. 会话校验（失败 401）→ 4. 首登 pending（失败 403 PASSWORD_CHANGE_REQUIRED；`/auth/**` 豁免）→ 5. CSRF（非 GET/HEAD 的 `/api/**`，登录豁免；`/workbench` 豁免）→ 6. Busy（写 `/api/**`，auth 与 jobs cancel 豁免）→ 7. handler。
> `/workbench` 走 3/4（不鉴权 401、pending 403），不做 CSRF/Busy 拦截。
> 补充（2026-10-24 冻结，task-19 已验证）：**未登录访问未知 /api 路径时认证先于路由解析 → 401 UNAUTHORIZED（fail-closed）**；**已登录访问未知 /api 路径 → 404 NOT_FOUND（JSON 四字段）**。

## 2. M1 端点矩阵

图例：鉴权列“登录”= 需会话；“匿名”= 无需。CSRF 列“必需”= 非 GET/HEAD 写接口需双提交；“—”= 只读/豁免。

| # | 方法 / 路径 | 鉴权 | CSRF | 请求 | 成功响应 | 关键失败（HTTP code） |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | POST `/api/auth/login` | 匿名 | 豁免 | `{password:string}` | **204** + JSESSIONID(HttpOnly/SameSite=Lax) + XSRF-TOKEN | 401 LOGIN_FAILED；423 LOGIN_LOCKED（`details.retryAfterSeconds`）；400 BAD_REQUEST（body 非法） |
| 2 | POST `/api/auth/logout` | 匿名/登录 | 仅当有有效会话时必需 | — | **204**（无有效会话 → 204 且不校验 CSRF；有会话 → 204 且必须双提交） | 有会话但缺 CSRF → 403 CSRF_INVALID |
| 3 | GET `/api/auth/me` | 匿名可访问 | — | — | 200 `{authenticated,csrfToken,version,initialPasswordPending}`；**无论是否登录都下发/刷新 XSRF-TOKEN**；登录后 csrfToken 为该会话绑定 token | — |
| 4 | PUT `/api/auth/password` | 登录 | 必需 | `{oldPassword,newPassword}` | **204**（旧会话仍有效或按实现；initial-password 删除） | 400 VALIDATION（旧密码错/新密码不合规）；403 CSRF_INVALID |
| 5 | GET `/api/health` | 匿名 | — | — | 200 `{status:"UP",version,uptimeSeconds}` | — |
| 6 | GET `/api/state` | 登录 | — | — | 200 StateDto（instances/runtimes/status/settings/availableVersions/currentInstanceId/warnings） | 401 UNAUTHORIZED；403 PASSWORD_CHANGE_REQUIRED |
| 7 | GET `/api/instances` | 登录 | — | — | 200 InstanceDto[] | 401 |
| 8 | POST `/api/instances` | 登录 | 必需 | `{name,version,profile,port?}` | **201** InstanceDto | 400 VALIDATION；409 CONFLICT_BUSY；409 CONFLICT_PORT_BUSY |
| 9 | GET `/api/instances/{id}` | 登录 | — | — | 200 InstanceDto | 404 NOT_FOUND |
| 10 | PUT `/api/instances/{id}` | 登录 | 必需 | `{name?,port?}` | 200 InstanceDto | 400 VALIDATION；404；409 CONFLICT_UNSUPPORTED_CHANGE（**version/profile 任何修改**）；409 CONFLICT_INSTANCE_RUNNING（**运行/启动中改 port**）；**name 任何状态可改（不影响进程）** |
| 11 | DELETE `/api/instances/{id}` | 登录 | 必需 | — | 200 `{trashedPath}` | 404；409 CONFLICT_INSTANCE_RUNNING |
| 12 | PUT `/api/instances/{id}/selection` | 登录 | 必需 | — | 200 InstanceDto（持久化 currentInstanceId） | 404 |
| 13 | POST `/api/instances/{id}/launch` | 登录 | 必需 | — | **202** JobView（type=LAUNCH_INSTANCE；result=`{instanceId,webUrl:"/workbench/{id}/"}`，**不得含 token URL**） | **同步预检（创建 Job 前）**：404 NOT_FOUND（实例不存在）；409 CONFLICT_BUSY（Job 忙）；409 CONFLICT_RUNNING_INSTANCE（其它实例运行中）；409 CONFLICT_BUSY（本实例已运行）；409 CONFLICT_PORT_BUSY（端口占用）；409 INSTANCE_NOT_READY（runtime 未安装）。**Job 启动后的失败进 Job error**：探测失败 UNSUPPORTED_RUNTIME；启动/就绪失败/提前退出 INSTANCE_NOT_READY；超时 TIMEOUT；取消 CANCELLED |
| 14 | POST `/api/instances/{id}/stop` | 登录 | 必需 | — | 200 InstanceDto（status=未启动） | 404 |
| 15 | GET `/api/instances/{id}/web-url` | 登录 | — | — | 200 `{url:"/workbench/{id}/"}` | 404；409 INSTANCE_NOT_READY |
| 16 | GET `/api/instances/{id}/direct-access` | 登录 | — | — | 200 DirectAccessDto（`ready` 布尔；未运行/未就绪也 200 且 ready=false，note 中文原因；`Cache-Control:no-store`；**sshHost=请求 Host 去端口，IPv6 字面量保留方括号，拒绝空/控制字符/空白；不使用 X-Forwarded-Host；sshUser=settings.directAccessSshUser**） | 404；未登录 401 |
| 17 | GET `/api/runtimes` | 登录 | — | — | 200 RuntimeDto[]（installed 恒 true 的已装列表 + compatibility verified/unverified） | 500 INTERNAL |
| 18 | GET `/api/runtimes/available` | 登录 | — | — | 200 `{versions:string[]}`（SemVer 降序） | 502 UPSTREAM_ERROR；504 UPSTREAM_TIMEOUT |
| 19 | POST `/api/runtimes` | 登录 | 必需 | `{version:string}` | **202** JobView（INSTALL_RUNTIME；result=`{version,installed:true,compatibility}`） | 400 VALIDATION；409 CONFLICT_BUSY；Job 失败带 UPSTREAM_*/INTERNAL/TIMEOUT |
| 20 | POST `/api/runtimes/{version}/probe` | 登录 | 必需 | — | **202** JobView（PROBE_RUNTIME；result=`{version,compatibility}`） | 400/404；409 CONFLICT_BUSY |
| 21 | DELETE `/api/runtimes/{version}` | 登录 | 必需 | — | 200 `{trashedPath}` | 404；409 CONFLICT_IN_USE（任一实例引用） |
| 22 | GET `/api/jobs` | 登录 | — | — | 200 JobView[]（最近 20，按创建时间倒序） | 401 |
| 23 | GET `/api/jobs/{id}` | 登录 | — | — | 200 JobView | 404 |
| 24 | GET `/api/jobs/{id}/events` | 登录 | — | `Last-Event-ID` 可选 | **SSE** `text/event-stream`（state/log/result/error；见 §3） | 404（未知 job）；401 |
| 25 | POST `/api/jobs/{id}/cancel` | 登录 | 必需 | — | **202** JobView（最终 CANCELLED） | 404；409 CONFLICT_JOB_FINISHED（已终态） |
| 26 | GET `/api/logs/tail?lines=800` | 登录 | — | lines **缺省 800**；**非整数 / <1 / >2000 → 400 VALIDATION（不静默 clamp）** | 200 `{lines:string[]}`（脱敏） | 400 VALIDATION；401 |
| 27 | GET `/api/settings` | 登录 | — | — | 200 `{proxy,registry,directAccessSshUser}` | 401 |
| 28 | PUT `/api/settings` | 登录 | 必需 | 同 GET | 200 同上 | 400 VALIDATION；409 CONFLICT_BUSY |
| 29 | ANY `/workbench/{id}/**` | 登录 | 豁免 | 原始 HTTP 请求 | 反代流（302 token→303→cookie→200 页面/静态/SSE） | 401（未登录）；403 PASSWORD_CHANGE_REQUIRED；404（实例不存在）；409 INSTANCE_NOT_READY；502/504（上游） |
| 30 | WS `/workbench/{id}/**` | 登录 | 豁免 | Upgrade + Cookie + 子协议 | 101 双向（/api/remote.mux 等） | 401；404；409；502 |
| 31 | ANY `/api/<未知路径>` | 登录（匿名先 401） | 写方法同通用规则（但匿名已先 401） | — | 已登录：**404 NOT_FOUND** JSON | 匿名：**401 UNAUTHORIZED**（fail-closed） |

> JobView 字段：`{id:"j-"+12hex,type,state:RUNNING|SUCCEEDED|FAILED|CANCELLED,phase,message,createdAt,startedAt|null,finishedAt|null,lastSeq,result|null,error:{code,message,details}|null}`（SPEC §4.3）。
> InstanceDto：`{id,name,version,profile,port,status:中文,path,profiles:string[]}`；RuntimeDto：`{version,installed,compatibility}`；DirectAccessDto：`{ready,port,directUrl|null,tunnelCommand,sshHost,sshUser,note}`。

## 3. Job / SSE 契约（G3）

| 项 | 约定 | 验收断言（task-19） |
| --- | --- | --- |
| 创建 | 写接口同步返回 202 + 完整 JobView；同一时间仅 1 个变更任务，忙时 409 CONFLICT_BUSY（不排队） | 202 字段完整；忙时第二写接口 409 |
| 事件命名 | `id: <seq>` / **`event: state` 或 `event: log`/`result`/`error`（严格 SPEC §4.5，不用 data.type 代替）** / `data: <json>`，空行分隔 | 每条事件必须解析出 event 行与对应 JSON；顺序 state→log*→result（或 error） |
| 事件 data | state `{jobId,seq,ts,state,phase,message}`；log `{jobId,seq,ts,level:"info",line}`；result `{jobId,state,result}`；error `{jobId,state,code,message,details}` | JSON 字段逐项校验；id 单调递增 |
| 心跳 | 每 15s 发送 SSE 注释行 ping（形如 `: ping`，不占 seq；测试可注入短心跳） | 长任务期间可见至少 1 次 ping |
| 重连 | `Last-Event-ID` 补发之后事件（环形 4000）；终态补发后关闭；连接最长 30min | 断开重连后 seq 无重复/无丢失 |
| 终态 | SUCCEEDED / FAILED / CANCELLED；FAILED 的 error.code ∈ apierr 集合（超时 TIMEOUT、取消 CANCELLED、未知 INTERNAL） | job 终态与 /api/jobs/{id} 一致 |
| 取消 | 运行中 cancel → 202，最终 CANCELLED；已终态 → 409 CONFLICT_JOB_FINISHED；404 未知 | 两种分支各一次 |
| 历史 | GET /api/jobs 最近 20；仅内存，重启清空 | 数量与重启行为 |
| 失败映射 | `*apierr.Error` → 原 code/message/details；ctx 取消 → CANCELLED；超时 → FAILED+TIMEOUT；其它 → INTERNAL（原错误只进日志） | 至少覆盖一种可预期失败与 INTERNAL |

## 4. 错误码表（JSON `code`）

| code | HTTP | 含义/触发 | 典型端点 |
| --- | --- | --- | --- |
| UNAUTHORIZED | 401 | 无/过期会话 | 所有登录 API、workbench |
| LOGIN_FAILED | 401 | 密码错误 | login |
| LOGIN_LOCKED | 423 | IP 5 次失败/60s 锁定 60s；details.retryAfterSeconds | login |
| PASSWORD_CHANGE_REQUIRED | 403 | 首登未改密 | 除 auth/health 外全部 |
| FORBIDDEN | 403 | 授权不足（预留） | — |
| CSRF_INVALID | 403 | 缺失/不匹配 X-XSRF-TOKEN | 非 GET/HEAD /api/**（登录豁免） |
| BAD_REQUEST | 400 | body 非法/缺字段/超限 | 多数写接口 |
| VALIDATION | 400 | 字段校验失败（名称/端口/版本/profile/settings 等） | 实例/运行时/设置/密码 |
| NOT_FOUND | 404 | 实例/运行时/job/artifact 不存在 | 资源类端点 |
| CONFLICT_BUSY | 409 | 有变更任务运行中 | 除 auth/jobs cancel 外写接口 |
| CONFLICT_INSTANCE_RUNNING | 409 | 实例运行中禁止修改端口/删除 | PUT/DELETE instance |
| CONFLICT_RUNNING_INSTANCE | 409 | 已有实例运行中，不能再启动 | launch |
| CONFLICT_PORT_BUSY | 409 | 端口被占用/重复 | create/launch |
| CONFLICT_IN_USE | 409 | 运行时被实例引用 | DELETE runtime |
| CONFLICT_UNSUPPORTED_CHANGE | 409 | 改 version/profile | PUT instance |
| CONFLICT_JOB_FINISHED | 409 | 取消已终态任务 | jobs cancel |
| INSTANCE_NOT_READY | 409 | 实例未运行/未就绪 | web-url、launch job 失败、workbench |
| PAYLOAD_TOO_LARGE | 413 | body 超限（M1 JSON 1MiB） | 写接口 |
| UNSUPPORTED_RUNTIME | 422 | 运行时能力不满足 | launch/probe（预留） |
| INTERNAL | 500 | 未知内部错误（详情只写日志） | 全部 |
| UPSTREAM_ERROR | 502 | registry/上游失败 | runtimes available/install job |
| UPSTREAM_TARGET_REJECTED | 502 | 上游拒绝（预留） | — |
| UPSTREAM_TIMEOUT | 504 | 上游超时 | runtimes available/install job |
| TIMEOUT / CANCELLED / INTERRUPTED | Job error 内 | 任务超时/取消语义 | Job |

> **错误体固定 4 字段**：`{code,message,details,traceId}`——无 details 时 `details={}`（对象，不是 null）；500 INTERNAL 只给通用中文消息 + traceId，详情仅日志（冻结口径 #6）。

## 5. G1/G2/G3/G5 验收追踪（矩阵行 → 断言）

| 门禁 | 验收点 | 对应矩阵行 | E2E 断言摘要 |
| --- | --- | --- | --- |
| G1 | 未登录 /api/state、/api/instances、/api/jobs → 401 JSON | 6/7/22 | status 401 + code UNAUTHORIZED + JSON（非重定向） |
| G1 | 5 次错误密码/60s → 423 + retryAfterSeconds | 1 | 第 5 次 423 LOGIN_LOCKED，details 数值 >0；之后正确密码仍 423 直到解锁窗口 |
| G1 | 正确登录 → 204 + JSESSIONID(HttpOnly/SameSite=Lax) + XSRF-TOKEN | 1 | 响应头属性逐项校验（Secure 不设） |
| G1 | me 无论是否登录都下发/刷新 XSRF-TOKEN 并返回 csrfToken（登录后为会话绑定 token） | 3 | 匿名与登录两次 GET /api/auth/me，检查 Set-Cookie 与 csrfToken 字段 |
| G1 | 写接口无 X-XSRF-TOKEN → 403 CSRF_INVALID；带 Cookie+Header 通过 | 通用 | 同一写接口两次对照 |
| G1 | 首登 pending → 除 auth/health 外 403 PASSWORD_CHANGE_REQUIRED（含 /workbench） | 通用 | state/workbench 均 403 + details.endpoint |
| G1 | 改密后旧密码失效、新密码可登录、initial-password 删除 | 4 | 旧密码 401；新密码 204；文件不存在 |
| G1 | 登出会话失效（再次访问 401）；无会话 logout 204 不校验 CSRF；有会话必须 CSRF | 2/6 | 无会话 204；有会话缺 CSRF 403、带 CSRF 204；随后 state 401 |
| G1 | 日志/SSE/审计不含密码/token/cookie 原文 | 全部 | 扫描 data/logs 与证据：token=/JSESSIONID=/XSRF-TOKEN=/密码值 0 命中 |
| G2 | 创建 201；非法 400；重复端口 409；忙 409 | 8 | 四类各一次 |
| G2 | PUT：name 任何状态可改；port 运行/启动中 → 409 CONFLICT_INSTANCE_RUNNING；version/profile 修改 → 409 CONFLICT_UNSUPPORTED_CHANGE | 10 | 分别断言 name 成功、运行中改 port 409、改 version 409 |
| G2 | 删除（停止）→ 200 trashedPath 且目录进 trash；运行中 → 409 | 11 | trash 目录存在；运行中删除 409 |
| G2 | selection 持久化；重启后 state.currentInstanceId 正确 | 12/6 | 选择 → 重启 manager → state 校验 |
| G2 | launch 202 Job；成功 state 运行中；**同步预检 404/409 六种（忙/其它运行/本实例已运行/端口/runtime 未装）**；**Job 内失败 UNSUPPORTED_RUNTIME/INSTANCE_NOT_READY/TIMEOUT/CANCELLED** | 13/6/22-24 | job result webUrl=/workbench/{id}/；同步 409 与 Job error 各覆盖一例 |
| G2 | stop 后无残留/端口释放/状态未启动 | 14 | ss 无监听 + process 检查 |
| G2 | runtimes：available 成功；install 202 Job；installed=true；删除引用 409；未被引用 → trash；探测失败给原因 | 17-21 | Job result + GET runtimes + DELETE 两种分支 |
| G3 | 202 JobView 字段完整；SSE 严格 `event:` 行 + 对应 JSON + id 单调；Last-Event-ID 补发；15s `: ping`；取消 202→CANCELLED；已终态 cancel 409；忙 409；jobs 最近 20；重启清空 | 19/22-25 + §3 | task-19 逐项；禁止用 data.type 代替 event 行 |
| G5 | direct-access 200 ready=true、port/tunnelCommand/directUrl/sshUser 正确；Cache-Control no-store；未运行 200 ready=false+note；未登录 401；不存在 404；改 sshUser 回显；**sshHost 仅取请求 Host（去端口/IPv6 保留括号/拒绝空与控制字符），不用 X-Forwarded-Host** | 16 | 两种状态 + settings 变更后回显 + Host 头用例 |
| G4（M1 部分） | /workbench 登录门槛（未登录 401）；登录后代理链路；SSE/WS | 29/30 | M1-I 里与真实 DSH 联合验证（完整 G4 留 M2/M4） |

## 6. M1 明确不实现的端点（M2/M3；不注册路由，访问应为 404）

| 端点族 | 里程碑 |
| --- | --- |
| `/api/packs/preview`、`/api/packs/import`、`/api/packs/export`、`/api/artifacts/{id}` | M3（G6） |
| `/api/instances/{id}/plugins`（GET/POST/PATCH/DELETE） | M2/M3（G7） |
| `/api/accounts*`、`/api/accounts/providers`、`/api/instances/{id}/account`、`/api/instances/{id}/copy`、`/api/plugin-catalog*`、`/api/settings/background`、`/api/assets/backgrounds/*` | 明确删除，永不实现 |
| M0 `/api/m0/**` 调试 API | M1 删除（dev-mode flag 保留但不再暴露） |

## 7. 冻结口径（RESOLVED，2026-10-24 Lead）

> 以下 8 项歧义已由 Lead 冻结；backend-server 与 qa-release 一致执行。task-19 按此断言；若实现偏离，记录到 M1_RESULTS 并报 Lead，不得自行改契约。

| # | 议题 | 冻结口径 |
| --- | --- | --- |
| 1 | logout 的 CSRF/匿名行为 | 无有效会话（无 JSESSIONID 或已过期）→ **204，不校验 CSRF**；有有效会话 → **必须通过 CSRF 双提交**，否则 403 CSRF_INVALID |
| 2 | /api/auth/me 的 XSRF | **无论是否登录都下发/刷新 XSRF-TOKEN Cookie 并返回 csrfToken**；登录后 me 的 token 为该会话绑定 token |
| 3 | launch 同步预检 vs Job 失败边界 | 同步（建 Job 前）：实例不存在 404；Job 忙 409 CONFLICT_BUSY；其它实例运行中 409 CONFLICT_RUNNING_INSTANCE；本实例已运行 409 CONFLICT_BUSY；端口占用 409 CONFLICT_PORT_BUSY；runtime 未安装 409 INSTANCE_NOT_READY。Job 内：探测失败 UNSUPPORTED_RUNTIME（422 语义但 Job error code 用该 code）；启动/就绪失败/提前退出 INSTANCE_NOT_READY；超时 TIMEOUT；取消 CANCELLED |
| 4 | 运行中修改实例 | name 允许（不影响进程）；port 在运行/启动中修改 → 409 CONFLICT_INSTANCE_RUNNING；version/profile 任何修改 → 409 CONFLICT_UNSUPPORTED_CHANGE |
| 5 | logs/tail 参数 | lines 缺省 800；非整数 / 小于 1 / 大于 2000 → **400 VALIDATION（不静默 clamp）** |
| 6 | 错误 JSON 字段 | **始终** code/message/details/traceId 四字段；无 details 时 **details={}（对象，不是 null）**；500 只给通用中文消息+traceId，详情仅日志 |
| 7 | direct-access sshHost | 只取请求 Host 头（去端口；IPv6 字面量保留方括号；拒绝空/控制字符/空白）；**不使用 X-Forwarded-Host**（Nginx 链路 M2 再议）；SSH 用户取 settings.directAccessSshUser |
| 8 | Job SSE 事件格式 | 严格用 **`event: state` / `log` / `result` / `error` 行 + 对应 JSON data**（IMPLEMENTATION_SPEC §4.5）；不得用 data.type 代替 event 行；心跳注释 `: ping` |

（task-21 已据此同步更新 §1/§2/§3/§4/§5；task-19 遇实现差异时在 M1_RESULTS 记录。）

## 8. task-19 使用方式（证据映射）

- 本节已由 §9/§10（实际执行结果）取代，保留标题仅作历史索引；矩阵正文即 task-19 的断言来源。

## 9. task-19 实际验证结果（2026-10-24，M1-I）

- 正式 E2E：`bash scripts/wsl/m1-acceptance.sh --full`（`M1_WIRING_READY=1`，Windows go1.27.3 交叉编译的 manager `0.2.0-m1`（8,798,368 B，sha256 `0da0825ba0bb7733accc1278b7ececb9788db3425c9827091c44945c6fde570c`，当前工作树），fresh data-root）→ **209/209 检查 PASS，EXIT_CODE=0**。
- 证据：`docs/evidence/m1/m1-acceptance-20261024-152121.log.txt`（脱敏）+ `...-manager.log.txt`；失败保留现场 `/tmp/pcl-dsh-go-test/m1-scenario-*`（本次无失败）。
- 覆盖亮点（对应本矩阵）：
  - G1：未登录 401 / CSRF 403 / 首登 403 PASSWORD_CHANGE_REQUIRED / 改密 204 + initial-password 删除 / 登出 + 旧会话 401 / 5 次失败 423 LOGIN_LOCKED(details.retryAfterSeconds=60) / 匿名与登录 me 的 XSRF。
  - G2：创建 201、非法 400、重复端口 409、name 更新、version/profile 409 CONFLICT_UNSUPPORTED_CHANGE、运行中改 port 409 CONFLICT_INSTANCE_RUNNING、selection 重启后持久化、停止后删除 trash（含 never-started 非空 trashedPath）、运行时安装/复用/探测/引用保护/删除/404、jobs 重启清空。
  - G3：install cancel 202→最终 CANCELLED（staging 保留属允许）；真实 install Job≈80s：SSE state+log+result、id 单调、4 次心跳、result `{installed:true,compatibility:"unverified"}`；probe→`verified`；launch Job SUCCEEDED（result 不含 token）；忙时写接口 409 CONFLICT_BUSY；Last-Event-ID 重放成功。
  - G4（M1 部分）：/workbench 未登录 401；登录后 302 token→DSH 303 Location ./ + Set-Cookie Path=/workbench/{id}→页面 200/title/base href=./→静态资源 200→SSE 分块→WS 101+ready+close 1000。
  - G5：direct-access 运行中 ready=true/port/tunnelCommand/sshHost(仅 Host)/sshUser(settings)/directUrl(token 脱敏)/no-store；停止后 ready=false+note；未登录 401；不存在 404。
- 结论：§7 冻结口径全部按预期验证，未发现需上报的产品侧契约差异；错误 JSON 四字段（details={}）已验证；500 INTERNAL 未在 E2E 中触发（无该场景）。

## 10. task-19 使用方式与证据映射（已执行）

- 每个矩阵行 → `scripts/wsl/m1-acceptance.sh` 的 phase 断言；证据写入 `docs/evidence/m1/e2e-*.log.txt`/`m1-acceptance-*.log.txt`（脱敏），失败保留 `/tmp/pcl-dsh-go-test/m1-scenario-*`。
- 必测补充均已覆盖：Cookie/Security 头属性、traceId 12hex、敏感接口 no-store、请求体超限 413、未知路由（匿名 401/已登录 404）、Windows DSH 程序树前后 hash 相同、参考仓库 git status 基线相同、manager 0 ERROR。
- 结果回填：`docs/M1_RESULTS.md`（task-20）。
