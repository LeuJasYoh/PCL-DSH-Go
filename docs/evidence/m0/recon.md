# M0-5 侦察报告：真实 DSH 0.2.1-rc.3（WSL）

> 任务：M0-5（qa-release）。日期：2026-10-24。状态：**完成**。
> 目的：在 WSL 内用发布形态的内置 Node/pnpm 安装并运行真实 DSH rc.3，取得 M0 反代/进程/就绪/E2E 所需的一手行为事实，并留下可复现证据。
> 边界：只写 WSL `/tmp/pcl-dsh-go-test/` 与仓库 `scripts/`、`docs/evidence/m0/`；未修改 Windows DSH、参考仓库、`/opt` 既有部署；证据中 token/cookie 全部脱敏为 `***`。
>
> 复现命令（WSL）：
> `bash scripts/wsl/recon-rc3.sh all`（阶段：env/registry/install/probe/launch/report）
> 证据：`docs/evidence/m0/logs/` 与 `docs/evidence/m0/logs/launch/`。

> 注意（环境事实）：WSL 的 `/tmp` 是 tmpfs；本次侦察期间 WSL 曾重启导致 `/tmp` 清空，runtime 已按同样命令重装（clean install 104s）。M0-7/E2E 脚本必须能容忍 runtime 缺失并重建，不能假设 `/tmp` 持久。
> 注意（git）：仓库 `.gitignore` 忽略 `*.log`，因此 `report` 阶段把日志复制为 `*.log.txt`，保证证据可提交；重跑 `recon-rc3.sh report` 即可刷新。

## 0. 结论（给实现队友的 TL;DR）

1. **pnpm 11.28.5 + Node 24.22.1 可成功安装 rc.3**，无需回退 pnpm 10.34.0。runtime 552 MB，入口 `node_modules/@deepseek-ai/dsh/lib/bin.js`（bin.dsh）。
2. **启动命令/环境白名单成立**：`node <entry> --profile web --port 3080 --host 127.0.0.1 --no-open`；无 HOME 也能运行；stdout 打印 `dsh web: http://127.0.0.1:3080/?token=<43 字符>`。就绪 1.6–6.5s（90s 预算充足）；停止 0.12–0.27s，端口立即释放；可重复启动。
3. **认证契约成立**：无 cookie → 401；token（仅 GET `/`、单值）→ 303 `Location: ./` + Set-Cookie `dsh-auth-<43>=<171>`; `Max-Age=2592000; Path=/; HttpOnly; SameSite=Strict`；带 cookie → 200，`base href="./"`，无回环绝对地址、无 CSP/XFO。
4. **关键新事实（与文档假设不同，务必按实际实现）**：
   - cookie 名 = `dsh-auth-` + base64url(sha256(**Host authority**))；值 171 字符（`v1.<payload>.<hmac>`）。→ **反代转发上游的 Host 必须恒为 `127.0.0.1:<实例端口>`**（引导与后续请求一致），否则 cookie 名不匹配 → 401。
   - token 只在 `GET /`（pathname 恰为 `/`、query 只有一个 token）生效；`/index.html?token=...` → 401。→ 反代 bootstrap 必须把 `/workbench/{id}/` 去前缀为 `/` 并原样保留 query。
   - **工作台事件不走 HTTP SSE，走 WebSocket `/api/remote.mux`**；HTTP SSE 实际路由是 `/plugins/events`（client-hmr 模块）。G4 的“SSE 逐块到达”用 `/plugins/events` 验收（无 cookie 也返回 200，管理器仍须先登录鉴权）。
   - WebSocket 协议为 JSON 文本帧（open/item/end/cancel + item/error/end），`/api/remote.mux` 无 cookie 401，带 cookie 101；无子协议；正常 open `$events` 收到 `ready`；错误载荷被 1008 关闭。代理只需透传帧，E2E 用正确 open 帧证明“消息收发”。
5. **rc.3 CLI 仍有 `--patch`**（上游账户特性，契约要求不注入，符合）；web app 新增 `--public-url/--trusted-host/--tls-cert/--tls-key`（本期不用，能力探测应容忍）。
6. manifest **无 `engines`、无 `packageManager`**：不能用 manifest 校验 Node 版本；按发布包固定 Node 24.22.1。
7. 无子进程：DSH 是单个 node 进程（RSS 就绪 283–321 MB，SSE/WS 后 377–379 MB）；停止用进程组 SIGTERM 即可（无需逐个后代 kill，但仍按契约先子孙后根实现）。

## 1. 环境（logs/01-env.txt）

| 项 | 值 |
| --- | --- |
| 发行版 | Ubuntu 26.04.1 LTS（Resolute Raccoon），WSL2 |
| 内核 | Linux 6.18.33.2-microsoft-standard-WSL2 x86_64 |
| CPU / 内存 | 16 vCPU / 17.8 GiB（free 总 17.875G） |
| /tmp | tmpfs 8.8G（本次侦察后占用 ~1.2G；重启会清空） |
| Node | v24.22.1，`/opt/pcl-dsh-go/tools/node/bin/node`，sha256 `e32e2d16081cf644f88392934288086e7662529d0d43277c34f7aad2207cfafb` |
| pnpm | 11.28.5，`/opt/pcl-dsh-go/tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`，sha256 `67b035e322203961795e8e34ca63a08c37a4386eda94107fb3d28f3246d882ad` |
| Nginx | 1.28.3（已装，未启用） |
| 其他 | curl 8.18.0、git 2.53.0、ss、setsid、timeout、python3 3.14.4；WSL 内无 go、无 jq |
| npm registry | HTTPS 200，包元数据 267,842 B，约 1.5–5.2s |
| 交叉编译 | Windows go1.27.3，WSL 通过 `go.exe` 交叉编译 linux/amd64（脚本已处理 WSL→Windows 不传 env 的问题） |

## 2. npm 版本与 manifest（logs/02-registry.txt）

- 版本总数 **37**；dist-tags：`latest=0.2.1-rc.2`、`next=0.2.1-rc.3`、`alpha=0.2.1-alpha.5`。
- **0.2.1-rc.3 已发布**：2026-10-23T07:54:30.267Z；基线可用，无需回退 rc.2。
- rc.3 manifest：`name=@deepseek-ai/dsh`、`version=0.2.1-rc.3`、`engines={}`、`packageManager` 缺失、`bin={"dsh":"lib/bin.js"}`、dependencies 86、optional/peer 0；tarball `https://registry.npmjs.org/@deepseek-ai/dsh/-/dsh-0.2.1-rc.3.tgz`，integrity `sha512-H+E9HLnHf7fJqzen84Z9Bq8ggb5xbrcbyhZjXTJLE9NabHr3zteGvTRvdvGkxPl4ov80is4WLglFmSo+/olJjg==`，shasum `ccc527065b9f736ea299a1f78150a21143d961aa`。

## 3. 安装（pnpm 11.28.5，logs/03-install*、cohort-0.2.1-rc.3.json）

- 发行批次 cohort（对齐 `RuntimeService.releaseCohort`）：递归收集 `@deepseek-ai/dsh-*` 且 spec 为 `<ver>/^<ver>/~<ver>` 的依赖，**固定 295 个同版本包**；缓存 `<data>/cache/registry-metadata/<sha256(registry)>/cohort-0.2.1-rc.3.json`（本次路径 `.../036b43b16792c643378e95923ed2f4cc78e800de141b764aeea70d47580396df/cohort-0.2.1-rc.3.json`）。完整 overrides 清单已作为证据 `cohort-0.2.1-rc.3.json`（schemaVersion=1、complete=true、count=295、registry=https://registry.npmjs.org、**canonical sha256=`73781a68e516ddd76c9725f876d242ad0537112b83779c4e5fb4ce10f26d67bf`**）。注意：JSON 的 `checkedAt` 每次解析会更新，故**缓存文件的 sha256 每次不同**（如 clean-root 那次是 `93698901...`），校验用的是文件内 canonical `sha256` 字段（排除 checkedAt）。
- pnpm 命令逐字：`node pnpm.cjs install --prod --no-frozen-lockfile --registry=<registry> --store-dir=<data>/cache/pnpm-store --reporter=append-only`；staging + HOME 指向 staging；成功。
- **clean 安装实测 104s**：cohort 31s + pnpm install 72s + 校验 <1s（另一台 root 全量新装测得；运行机日志 `03-install-summary-cleanroot.txt`）。
- 结果：runtime 目录 **511,037,436 B（552 MB）**，35,056 文件 / 3,893 目录，`node_modules` 507 MB；`pnpm-lock.yaml` 保留。
- 入口校验通过：`node_modules/@deepseek-ai/dsh/lib/bin.js`，11,772 B，sha256 `80112efe368efb1a39720c7ada0af00442e5131fb2744edaaff8a3e8bb499e6f`；manifest 86 dependencies。
- 原生模块：`*.node` 共 15 个，其中 9 个为 ELF（含 `sherpa-onnx-linux-x64/sherpa-onnx.node`、`node-pty/prebuilds/linux-x64/pty.node`）；其余是随包分发的 darwin/win 预编译件，Linux 不使用。
- 网络提示：pnpm 偶发 “Tarball download average speed 18 KiB/s” 警告，安装时长可能波动；20min 超时足够。

## 4. 能力探测（logs/04-probe-*.txt）

- `node <entry> --help`：rc=0，105 ms，1,820 B。launcher options：`--profile --help --version --patch --from-default-profile --dump-config --dump-config-schema --dump-default-config --resume`。
- `node <entry> --profile web --help`：rc=0，1,208 ms，1,803 B（含 usage/--port）。app options：`--host --no-open --port --profile --public-url --tls-cert --tls-key --trusted-host --help`。
- 结论：profileSelection=true、webAlias=true（`dsh web` 等价 `--profile web`）、pluginManagement=true（`dsh plugin --profile <name> <pnpm args...>` 转发 pnpm）、appHelpVerified=true；启动只需 `--profile/--port/--host/--no-open`，**无需 compatibility.json**。
- `--patch` 存在于上游 CLI，但管理器**不注入**（符合 DSH_INTEGRATION §4.2）。

## 5. 启动 / 就绪 / token / 停止（logs/launch/01-startup.log、08-process.txt、10-summary.txt）

- 启动：`node <entry> --profile web --port 3080 --host 127.0.0.1 --no-open`；cwd=workspace；环境仅 13 个白名单键（DSH_HOME/DSH_AGENTS_HOME/registry/PNPM_*/XDG_CACHE_HOME/DSH_TELEMETRY_MODE/PATH），**无 HOME**，实测可运行（home=/root）。
- token：stdout 行 `dsh web: http://127.0.0.1:3080/?token=<43 字符 base64url>`；query 参数名 `token`，path `/`；捕获耗时 1.5–6.4s（首次启动波动）。token 只在内存。
- 就绪：spawn 后先 ECONNREFUSED，随后**第一条 HTTP 响应即 401**（未见 404 阶段）；401+已捕获 token → 就绪，ready 1.6–6.5s。
- 进程：**单 node 进程**（无子进程），pgrp=pid；RSS 就绪 283,500–321,168 KB；SSE/WS 活动后 377,464–379,396 KB。
- 停止：对进程组 SIGTERM → 118–269 ms 退出（graceful），端口 <2s 释放，`ss -ltnp` 无监听；第二次启动就绪 1.6–3.4s、bootstrap 303、页面 200，可重复。

## 6. HTTP / Cookie / HTML / SSE（logs/launch/02*.txt、03-html-analysis.txt、06-sse.txt）

| 请求（回环直连） | 结果 |
| --- | --- |
| GET / 无 cookie | **401** `text/plain; charset=utf-8`，`cache-control: no-store`，body 68B：`dsh web authentication required; reopen the URL printed by dsh web.` |
| GET /?token=<无效> | 401（同上） |
| GET /?token=<有效> | **303**，`Location: ./`，`cache-control: no-store`，`referrer-policy: no-referrer`，Set-Cookie 1 个 |
| GET /?token=<有效>（replay） | 303（同进程内 token 可重复换取 cookie） |
| GET /index.html?token=<有效> | 401（token 仅根路径生效） |
| GET / 带 cookie | **200** `text/html; charset=utf-8`，37,971 B；无 cache-control；无 CSP/X-Frame-Options |
| HEAD / 带 cookie | 200，无 body |
| GET /assets/index-BXV-oxvX.js 无/带 cookie | 200（**静态资源不鉴权**），941,436 B |
| GET /api/recon-unknown 无 cookie | 401 `unauthorized`（12B） |
| GET /api/recon-unknown 带 cookie | 404 `not found`（9B） |

Set-Cookie（脱敏后仍保留属性）：`set-cookie(masked): dsh-auth-***=***; Max-Age=2592000; Path=/; Expires=<30 天后>; HttpOnly; SameSite=Strict`（HTTP 监听无 `Secure`，无 `Domain`）。cookie 名后缀 43 字符 = base64url(sha256(`127.0.0.1:3080`))，已实测匹配；值 171 字符。SameSite=**Strict**（不是 None）。

HTML：`title=DeepSeek Harness`、`<base href="./">`、**无** `127.0.0.1/localhost` 绝对回环地址、无 CSP/XFO；script/link：
- `plugins/??@deepseek-ai/dsh-client-modules/client.js&rev=831d0cb3d012`
- `./assets/index-BXV-oxvX.js`、`./assets/vendor-DcuzkuQH.js`、两个 CSS、`./manifest.webmanifest`、favicon
- 另有两个 `plugins/??<模块列表>&rev=...` 的 `<link>` 资源（client-hmr/api-gateway 等 60+ 客户端模块）。
- 注意：HTML 源文件中 `&` 写作 `&amp;`；浏览器解码后请求 `/plugins/??模块&rev=...`（path=`/plugins/`，query 以 `?` 开头）。**反代必须原样透传 RawQuery，不要重新编码 `??`/逗号**。

SSE（logs/launch/06-sse.txt）：
- `GET /plugins/events` → 200 `text/event-stream; cache-control: no-cache`；第 1 块 `": connected\n\n"`，第 2 块 `data: {"type":"graph","graph":{"rev":"...","release":{"version":"0.2.1-rc.3","commit":"be72761"},...}}`；连接保持（我们读 1.2s 后关闭）。
- 无 cookie 也返回 200（上游未鉴权；管理器反代必须自行登录鉴权）。
- `GET /api/events`、`/api/remote/events` → 404。**rc.3 没有工作台事件用的其它 HTTP SSE 路由。**

## 7. WebSocket（logs/launch/07-ws.txt）

- 路由：`/api/remote.mux`。无 cookie 握手 → **401**；带 cookie → **101 Switching Protocols**。
- 请求头（E2E 模板）：`GET /api/remote.mux HTTP/1.1`、`Host: 127.0.0.1:3080`、`Upgrade: websocket`、`Connection: Upgrade`、`Sec-WebSocket-Key: <16B base64>`、`Sec-WebSocket-Version: 13`、`Origin: http://127.0.0.1:3080`、`Cookie: dsh-auth-***=***`；**未提供/未接受子协议**（响应无 `Sec-WebSocket-Protocol`）。
- 响应头：`Upgrade: websocket`、`Connection: Upgrade`、`Sec-WebSocket-Accept: <base64>`。
- 帧协议（来自 `@deepseek-ai/dsh-api-gateway/lib/types/stream-protocol*`）：客户端文本 JSON `{"type":"open","streamId","endpoint","payload"}` / `{"type":"item",...}` / `{"type":"end","streamId"}` / `{"type":"cancel","streamId"}`；服务端 `{"type":"item","streamId","value"}` / `{"type":"error",...}` / `{"type":"end",...}`。
- 实测收发：发 `open{streamId:"recon-1",endpoint:"$events",payload:{args:{}}}` → 收 135B `{"type":"item","streamId":"recon-1","value":{"type":"ready","clientId":"<uuid>","host":{"home":"/root"}}}`；双向 ping/pong 各 1 次；发 `end` + close(1000) → 收 close(1000)。早期发送非协议文本被服务端 **1008 "invalid Remote stream request"** 关闭（说明代理必须原样透传帧，不可改写）。
- 代理实现提示：HTTP/1.1 Upgrade 需转发 `Sec-WebSocket-Key/Version/Protocol` 与 `Cookie`，改写 `Origin` 为回环；Host 恒为 `127.0.0.1:<port>`。浏览器侧 Origin 为管理器地址时，管理器必须做同源校验并改写上游 Origin（契约 §6.3）。

## 8. 与文档/契约的差异及实现影响

| # | 事实（rc.3 实测） | 影响 / 行动 |
| --- | --- | --- |
| 1 | manifest `engines={}`、无 `packageManager` | 不能依赖 manifest 校验 Node；发布包固定 Node 24.22.1 + pnpm 11.28.5（已实测兼容） |
| 2 | cookie 名绑定 Host authority，值 171B 签名 | 反代上游 Host 必须恒为 `127.0.0.1:<port>`；引导与后续请求一致；不可透传浏览器 Host |
| 3 | token 仅 `GET /` 单值有效 | bootstrap 去前缀后路径必须恰为 `/`，保留 query；302 由管理器发出，token 只出现在 302 Location（不落日志） |
| 4 | 303 `Location: ./`、`Referrer-Policy: no-referrer`、无缓存头 | 相对 Location 原样；管理器响应仍应加 `Cache-Control: no-store`（契约） |
| 5 | SameSite=Strict、无 Secure/Domain、Path=/ | Set-Cookie 重写保留 Strict；Path 改 `/workbench/{id}`；去 Domain/Secure（本机本就没有）；None→Lax 的规则本次未触发 |
| 6 | 静态资源不鉴权；页面/API/WS 鉴权；SSE `/plugins/events` 也不鉴权 | 反代必须在 /workbench 前置登录（管理器自己的鉴权），不能依赖上游 |
| 7 | 工作台事件走 WS；HTTP SSE 只有 `/plugins/events` | G4 的 SSE 验收用 `/plugins/events`（curl -N 看 `: connected` + `data:` 分块）；不要把 WS 当 SSE 验收 |
| 8 | 就绪首个响应可能直接 401（未见 404） | 契约判定正确；另外 404 继续等待的分支保留 |
| 9 | 单 node 进程、无子进程；RSS 283–379 MB | 内存预算 1.6 GiB 下单实例可行；停止按进程组 |
| 10 | rc.3 CLI 有 `--patch`；web 新增 `--public-url/--trusted-host/--tls-*` | 保持“能力裁剪”：只加 `--port/--host/--no-open`；探测解析应容忍新增选项（不要 fail-closed 在未知选项上） |
| 11 | plugin 管理：`dsh plugin --profile <name> <pnpm args...>` 转发 pnpm | pluginManagement=true；M1 插件功能可用该 CLI（经 Node 桥） |
| 12 | HTML `base href="./"`、相对资源、无绝对回环地址 | 前缀反代可行，无需 HTML/JS 重写；资源相对 base 自动落到 `/workbench/{id}/` |

## 9. 可直接复用的 E2E 请求模板

### 9.1 回环直连：token → cookie → 页面（curl，token 只在 shell 变量）

@`@`bash
# 从管理器日志/内存取 bootstrap（示例占位；真实 E2E 中 token 只放内存，禁止落盘/入库）
TOKEN="<43 字符 token>"
# 1) token 换 cookie（预期 303 Location: ./ + Set-Cookie）
curl -sS -D /tmp/h1 -o /dev/null "http://127.0.0.1:3080/?token=$TOKEN"
# 2) 取 cookie 值（脚本内变量；证据只写脱敏后的属性）
COOKIE=$(grep -i '^set-cookie:' /tmp/h1 | sed -E 's/^[Ss]et-[Cc]ookie: ([^;]+);.*/\1/')
# 3) 页面（预期 200 text/html，base href="./"）
curl -sS -H "Cookie: $COOKIE" http://127.0.0.1:3080/ | head -40
# 4) SSE 分块（预期先 ": connected" 再 "data:"）
curl -sS -N --max-time 2 -H "Cookie: $COOKIE" http://127.0.0.1:3080/plugins/events
@`@`

### 9.2 经管理器反代 `/workbench/{id}/`（M0-7 E2E 断言）

- 无 cookie `GET /workbench/{id}/` → 管理器 **302** 到 `/workbench/{id}/?token=***`（token 来自实例内存 bootstrap；响应 `Cache-Control: no-store`，日志不含 token）。
- 跟随 DSH 的 **303 `Location: ./`**：浏览器解析为 `/workbench/{id}/`。
- 带 DSH cookie `GET /workbench/{id}/` → 200 HTML，`base href="./"`；跟随 `assets/` / `plugins/??...&rev=...` 相对 URL → 经 `/workbench/{id}/...` 200。
- SSE：`curl -sS -N --max-time 2 -H "Cookie: <浏览器侧 dsh-auth cookie>" http://127.0.0.1:18080/workbench/{id}/plugins/events` → 首个块 `: connected`，随后 `data: {"type":"graph"...}`；断言“不缓冲”（headers 与首块在毫秒级到达且连接保持）。
- WS：`ws://127.0.0.1:18080/workbench/{id}/api/remote.mux`，带 DSH cookie、Origin 与 Host 同源（管理器地址）；收到 101 后发 `{"type":"open","streamId":"e2e-1","endpoint":"$events","payload":{"args":{}}}`，期望 `{"type":"item",...,"value":{"type":"ready",...}}`；随后 ping/close(1000) 正常。

### 9.3 Node 客户端模板（可直接抄）

- WS 最小客户端（握手/掩码帧/解析/ping-pong/close）：`scripts/wsl/lib/recon-launch.mjs` 的 `encodeWsFrame/decodeWsFrames/wsProbe`。
- SSE 校验：同文件 `sseProbe`（记录 status/content-type/firstChunk/chunkTimes；首块应以 `: connected` 开头）。
- 注意：Node 全局 `WebSocket` 不能带自定义 Cookie 头，故 E2E 用上述 net+crypto 最小实现（或经 `ws` 依赖，但本期不引入）。

## 10. 证据索引

| 文件 | 内容 |
| --- | --- |
| `logs/01-env.txt` | OS/内核/CPU/内存/磁盘/node+pnpm 版本与 sha256/registry 延迟/端口 |
| `logs/02-registry.txt` | 版本清单、dist-tags、rc.3 manifest 摘要 |
| `logs/03-install.log.txt`、`logs/03-install-summary.txt` | 安装日志与结果（运行机） |
| `03-install-summary-cleanroot.txt`（+ 运行日志 `logs/03-install.log.txt`） | 另一 root 全量 clean install 实测（104s = cohort 31s + pnpm 72s；该 root 的日志在 WSL 重启清空 /tmp 前读取并记录，文件头有说明） |
| `cohort-0.2.1-rc.3.json` | 295 个同版本 `@deepseek-ai/dsh-*` overrides（schema/complete/count/sha256），供 Go 实现对齐 |
| `logs/04-probe-launcher-help.txt`、`04-probe-web-help.txt`（+summary） | CLI/app 能力探测原文与摘要 |
| `logs/launch/01-startup.log.txt` | 启动命令/env 键/带时间戳 stdout（token 已脱敏） |
| `logs/launch/02-http-probe.txt`、`02b-http-extra.txt` | 401/303/200、Set-Cookie 属性、token 边界、静态/API/HEAD 行为 |
| `logs/launch/03-html-analysis.txt` | HTML 特征 + 全文（脱敏） |
| `logs/launch/04-assets.txt` | 静态/插件模块 URL 与状态（含 `&amp;` 解码验证） |
| `logs/launch/05-js-analysis.txt` | 5 个 JS bundle 中 WS/SSE/API 路径与上下文片段 |
| `logs/launch/06-sse.txt` | SSE `/plugins/events` 分块（auth/anon） |
| `logs/launch/07-ws.txt` | WS 握手/帧/关闭码与子协议信息 |
| `logs/launch/08-process.txt`、`09-run2.log.txt` | 进程树/RSS/停止/端口释放、二次启动 |
| `logs/launch/10-summary.txt` | 本轮所有关键字段 key=value |
| `logs/05-linux-tests.txt` | 2026-10-24 Linux 单测基线：`built=8 build_failed=0 run_failed=0 RESULT=PASS`（`scripts/wsl/run-linux-tests.sh ./internal/...`） |

复现：
@`@`bash
wsl -d Ubuntu -- bash /mnt/d/Projects/DSHL/PCL-DSH-Go/scripts/wsl/recon-rc3.sh all
# Go Linux 单测（Windows 交叉编译 + WSL 执行）：
wsl -d Ubuntu -- bash /mnt/d/Projects/DSHL/PCL-DSH-Go/scripts/wsl/run-linux-tests.sh ./internal/...
@`@`

## 11. 边界与合规声明

- 未写/未删/未改 `D:\DevelopSoftware\DSH\**`、`C:\Users\23500\.dsh\**`、Windows DSH 配置/插件/会话/缓存/进程；未安装/重启 Windows DSH。
- 未修改参考仓库 `D:\Projects\DSHL\PCL-DSH-Linux`（只读阅读 Java/文档）。
- 真实 DSH 只使用 WSL `/tmp/pcl-dsh-go-test` 独立 runtime/data；`/opt/pcl-dsh-go/tools` 仅只读执行。
- 未实现/注入账户系统参数（无 `--patch`/API Key/overlay）。
- 未在服务器端 clone/编译 DSH；只用 npm registry + 内置 pnpm。
- token/cookie 只在 WSL 进程内存与临时 shell 变量；所有证据文件脱敏，脚本 `report` 阶段自动扫描 `token=/dsh-auth-` 泄漏。
- 侦察结束：DSH 进程全部停止、3080 端口释放；runtime 保留 `/tmp/pcl-dsh-go-test/runtimes/0.2.1-rc.3` 供 M0-7 E2E 复用（若 WSL /tmp 被清空需重装）。

## 12. 风险与后续

1. WSL `/tmp` 为 tmpfs，本次已遇重启清空：E2E 脚本应先检查 runtime 目录，缺失则用内置 pnpm 重装（或复用离线 runtime 包）。
2. npm 下载偶发限速：安装超时按 20min 设计；E2E 失败先看 pnpm 日志再接续重试。
3. WS 帧协议是 rc.3 私有 JSON 协议；升级 DSH 后需重探（fail-closed，不猜协议）。
4. `/plugins/events`、静态资源上游不鉴权；管理器必须在 `/workbench` 前置登录，且不能把“上游 200”当作安全边界。
5. cookie 名/值绑定 Host authority 与签名 secret（存于 DSH_HOME credentials）；反代固定 Host 后每次实例生命周期内稳定，实例重启（新 secret）需新 bootstrap。
6. 就绪判断不要依赖 404/固定页内容；401+token 已是有效就绪信号，且首个响应可能直接 401。