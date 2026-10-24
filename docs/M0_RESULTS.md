# PCL-DSH-Go M0 验收报告（真实 DSH 0.2.1-rc.3，WSL）

> 状态：**定稿（qa-release 产出、Lead 复核通过，2026-10-24）**。
> 日期：2026-10-24。里程碑：M0（垂直切片：Go 管理器 + 内置 Node/pnpm + 真实 DSH 安装/启动/就绪/工作台反代/停止）。
> 边界：真实 DSH 只在 WSL /tmp/pcl-dsh-go-test 下用独立 data-root；未修改 Windows DSH、参考仓库、/opt 既有部署；token/cookie 全流程只在内存/临时文件，证据脱敏为 ***。
> 主要证据：`docs/evidence/m0/`（recon、logs、e2e）。复现命令见 §8。

## 1. 结论摘要

- **M0 垂直切片端到端通过**：在 WSL Ubuntu 26.04.1 上用发布形态的内置 Node 24.22.1 + pnpm 11.28.5 安装真实 **DSH 0.2.1-rc.3**，经 Go 管理器（dev-mode，127.0.0.1:18080，fresh data-root）完成：
  **health → 真实 Go 安装 runtime → 创建实例 → 启动/就绪 → /workbench 302(token)→303(./)→Cookie 重写→200 页面 → 静态资源/插件 bundle → SSE 分块 → WebSocket 101+ready+close 1000 → 真实浏览器（Windows Edge headless）打开工作台 → 停止/端口释放 → 二次启动 → 清理**，全程 **58/58 检查 PASS**（`docs/evidence/m0/e2e/e2e-20261024-140806.log.txt`）。
- **真实 Go 安装链路已证明**：POST /api/m0/runtimes/install 返回 200、`reused=false`、耗时 **78.1s**，runtime **508 MB / 34,434 文件 / 3,859 目录**；entryPath 校验通过；发行批次 **295 个 `@deepseek-ai/dsh-*` 全部 0.2.1-rc.3**（mismatched=0）。
- M0 期间修复 **1 个真实缺陷**（D1：runtime install 子进程 PATH 缺主机 PATH 导致 pnpm 254），并完成 **1 项上游迁移适配决策**（D2：pnpm 11 把 overrides 迁到 pnpm-workspace.yaml，我方双写并更新契约；不算代码缺陷）。详见 §11，修复 commit `42a558c`，修复后真实安装通过。
- **真实浏览器验证补齐 M0 退出条件**：Windows Edge 155.0.4283.62 **headless 真实渲染** WSL 管理器中的 `/workbench/{id}/`（302 token→303→cookie→200 自动完成），完整工作台 UI 可见、`<title>DeepSeek Harness</title>`、DOM 含 `__DSH_BOOT__` 与 rc.3 release 信息；截图/DOM 已入库（§4.9）。Windows 仅作浏览器客户端，管理器与 DSH 全在 WSL，未碰 Windows DSH。Playwright 自动化因 WSL Chromium 依赖安装超预算终止（脚本保留），不影响 M0 结论。
- 与上游 rc.3 的若干差异已记录（§5），对实现影响最大的是：**cookie 名绑定 Host authority**（反代必须固定上游 Host=127.0.0.1:<port>）、**token 仅 GET / 单值有效**、**工作台事件走 WS `/api/remote.mux`，HTTP SSE 路由是 `/plugins/events`**。
- 未覆盖项见 §6；M0 验收边界是“能装、能起、能代理、能停”，**不含**登录会话/CSRF/Job/整合包/插件/直连/WebUI/发行包（M1–M5）。

## 2. 环境与基线

| 项 | 值 |
| --- | --- |
| 发行版 | Ubuntu 26.04.1 LTS（WSL2） |
| 内核 | Linux 6.18.33.2-microsoft-standard-WSL2 x86_64 |
| CPU / 内存 | 16 vCPU / 17.8 GiB |
| /tmp | tmpfs 8.8G（本次侦察期间 WSL 曾重启清空一次，runtime 按脚本重装；E2E 会检测/重建） |
| Node | v24.22.1，`/opt/pcl-dsh-go/tools/node/bin/node`，sha256 `e32e2d16081cf644f88392934288086e7662529d0d43277c34f7aad2207cfafb` |
| pnpm | 11.28.5，`/opt/pcl-dsh-go/tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`，sha256 `67b035e322203961795e8e34ca63a08c37a4386eda94107fb3d28f3246d882ad` |
| 交叉编译 | Windows go1.27.3（WSL 内经 `go.exe`，脚本用 .bat 注入 GOOS/GOARCH 与带空格的 ldflags） |
| Nginx | 1.28.3（已装，M0 未启用；生产链路 M4） |
| 浏览器（仅客户端） | Windows Edge 155.0.4283.62 headless（M0 用于打开 WSL 中管理器页面；Windows 不运行管理器/DSH，未触碰 Windows DSH） |
| DSH 基线 | `@deepseek-ai/dsh@0.2.1-rc.3`（npm 已发布 2026-10-23；dist-tags：latest=0.2.1-rc.2，next=0.2.1-rc.3；共 37 个版本） |
| registry | `https://registry.npmjs.org` 可达（HTTPS 200，包元数据约 268 KB） |

## 3. 验证方法与脚本

| 脚本 | 作用 | 证据 |
| --- | --- | --- |
| `scripts/wsl/recon-rc3.sh` | 真实 DSH 侦察（env/registry/install/probe/launch/report）：独立 runtime、token/cookie 内存采集、SSE/WS 行为取证 | `docs/evidence/m0/logs/`、`docs/evidence/m0/recon.md` |
| `scripts/wsl/run-linux-tests.sh` | Windows 交叉编译各 internal 包 Linux 测试二进制→WSL 执行（自动 GOOS/GOARCH/ELF 校验） | `docs/evidence/m0/logs/05-linux-tests.txt` |
| `scripts/wsl/m0-acceptance.sh` | 正式 M0 E2E（fresh data-root、Go install API、全链路、安全/权限/Windows DSH 检查） | `docs/evidence/m0/e2e/` |
| `scripts/wsl/browser-m0.sh` + `lib/browser-check.mjs` | 浏览器自动化验证脚本（Playwright + headless Chromium，断言 title/#root/截图/console/网络/WS）。本次 WSL 内 Chromium 下载+系统依赖安装超预算被终止并清理；脚本保留供有浏览器依赖的环境复用，**不是 M0 证据** | —（M0 证据用 Edge headless，见 §4.9） |
| `scripts/wsl/lib/{ws-e2e,sse-e2e}.mjs` | 可复用 WS/SSE 客户端（stdin 传 cookie、输出脱敏、断言 101/帧/关闭码与 `: connected`/data 分块） | 同上 |
| `scripts/wsl/lib/{cohort,check-runtime,registry-report,recon-launch}.mjs` | 侦察辅助（cohort 解析、runtime 入口校验、registry 报告、启动/HTTP/SSE/WS 侦察） | 同上 |

- 单测基线（最终）：Windows `go test ./...` **10 个 Go 包全绿**（含 `cmd/pcl-dsh-go`；Windows-only 测试由 build tag 隔离）；Linux `scripts/wsl/run-linux-tests.sh ./internal/...` **9 个包：built=9 build_failed=0 run_failed=0 RESULT=PASS**（249 个 PASS；差异是 Windows `./...` 含 cmd main 包，Linux 运行器按设计限定 internal；证据 `docs/evidence/m0/logs/05-linux-tests.txt`）。Windows 交叉编译→WSL 执行链经该脚本验证可用。

## 4. 验收结果（真实 DSH rc.3）

### 4.1 Go 管理器构建（要求 1）

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=0.1.0-m0" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
```

- size=**8,474,784 B**；sha256=`2d0159a005c2899095b400a8ed241273e41b48c7b36a26eee3244d186c53d483`（本次构建；不同构建机/时间可能不同，证据日志记录当次值）。
- `GET /api/health` → `{"status":"UP","uptimeSeconds":0,"version":"0.1.0-m0"}`（版本化二进制生效）。

### 4.2 运行时安装（真实 Go 路径，要求 2/3）

- fresh data-root `/tmp/pcl-dsh-go-test/e2e-data`（删除重建，0700）；管理器 127.0.0.1:18080，`--pcl.tool-root=/opt/pcl-dsh-go/tools --pcl.dev-mode=true`。
- `POST /api/m0/runtimes/install {"version":"0.2.1-rc.3"}` → **200**，`{"version":"0.2.1-rc.3","entryPath":".../runtimes/0.2.1-rc.3/node_modules/@deepseek-ai/dsh/lib/bin.js","reused":false}`，**78.1s**。
- runtime：**508 MB / 34,434 文件 / 3,859 目录**；入口 `node_modules/@deepseek-ai/dsh/lib/bin.js` 校验通过；`pnpm-lock.yaml` 存在。
- cohort overrides：`pnpm-workspace.yaml` 保留契约基础块（autoInstallPeers/nodeLinker/resolutionMode/strictPeerDependencies/dangerouslyAllowAllBuilds）+ **overrides 295 项**（单引号排序）；295 个 `@deepseek-ai/dsh-*` 安装版本全部 **0.2.1-rc.3**（mismatched=0）；lockfile 含 overrides 段。
- 二次 install：`reused=true`、32ms（已安装快速返回与入口校验）。
- 上游正常行为（已记录、不断言逐字）：pnpm 11 会向 `pnpm-workspace.yaml` 追加 `minimumReleaseAgeExclude`，并打印 package.json `pnpm` 字段被忽略的警告（overrides 已在 workspace 生效）。
- 缺陷历史：修复前同命令失败（pnpm 254，依赖构建脚本失败），证据保留在 `docs/evidence/m0/e2e/e2e-20261024-135618*`，根因/修复见 §11。

### 4.3 实例创建 / 启动 / 就绪（要求 4）

- `POST /api/m0/instances` → **201**（{name:"m0-e2e",version:"0.2.1-rc.3",profile:"web",port:3080}）。
- `POST /api/m0/instances/{id}/launch` → **200**，状态“运行中”，**3,182 ms**（含能力探测/进程启动/就绪轮询）；二次启动 **1,860 ms**。
- 就绪判定与直连侦察一致：spawn 后先 ECONNREFUSED，首个可用 HTTP 状态为 **401**（无 token 页面）；契约判定 401+已捕获 token 即就绪（90s 预算，实测 1.6–6.5s 波动）。
- 启动命令/环境（侦察证据）：`node <entry> --profile web --port 3080 --host 127.0.0.1 --no-open`；环境白名单 13 键（DSH_HOME/DSH_AGENTS_HOME/registry/PNPM_*/XDG_CACHE_HOME/DSH_TELEMETRY_MODE/PATH，PATH 末尾含主机 PATH）；无 HOME 也可运行。
- 进程/内存（直连侦察）：单 node 进程、无子进程；RSS 就绪 283–321 MB，SSE/WS 后 377–379 MB（1.6 GiB 预算下单实例可行）。

### 4.4 /workbench 反向代理：HTTP/静态（要求 4）

| 步骤 | 期望 | 实测 |
| --- | --- | --- |
| GET /workbench/{id}/（无 cookie） | 302 到带一次性 token 的地址 | 302，Location=`/workbench/{id}/?token=***`（证据脱敏） |
| GET token URL（无 cookie） | DSH 303 + Set-Cookie 重写 | 303，`Location: ./`；Set-Cookie `dsh-auth-***=***; Max-Age=2592000; Expires=...; HttpOnly; SameSite=Strict; Path=/workbench/{id}`（去 Domain/Secure，保留 Strict） |
| GET /workbench/{id}/（带 DSH cookie） | 200 页面 | 200，title=`DeepSeek Harness`，`base href="./"` |
| 静态资源 | 200 | `assets/index-BXV-oxvX.js` 200/941,436 B；`plugins/??<模块列表>&rev=...` 200/9,146,303 B（HTML 中为 `&amp;`，脚本解码后请求；反代 RawQuery 原样透传） |
| 未知实例 /workbench/i-000000000000/ | 404 | 404 |
| 实例已停止 | 4xx | 409（INSTANCE_NOT_READY 类错误） |

资源均为相对路径（base href="./"），HTML 无回环绝对地址、无 CSP/X-Frame-Options；因此前缀反代无需 HTML/JS 重写。

### 4.5 SSE（要求 5）

- 端点：`/workbench/{id}/plugins/events`（上游真实路由 `/plugins/events`，由 `@deepseek-ai/dsh-client-hmr` 注册；工作台业务事件走 WS，见 §5）。
- `curl -N`（经代理、带 cookie、python 打点）：HTTP 200 `text/event-stream`，首个块 `": connected"` 在 **0.0 ms** 到达，`data: {"type":"graph",...}` 在 **0.1 ms** 到达，共 4 行（连接保持、逐块刷新，不缓冲）。
- 上游该端点不鉴权（无 cookie 也 200）；管理器在 /workbench 前置登录后代理，安全边界在管理器（M1 登录接入后生效）。

### 4.6 WebSocket（要求 6）

- 路径：`/workbench/{id}/api/remote.mux`（上游 `/api/remote.mux`）。无 cookie 握手 **401**；带 dsh-auth cookie → **101 Switching Protocols**；未提供/接受子协议。
- 客户端发送正确 open 帧 `{"type":"open","streamId":"e2e-1","endpoint":"$events","payload":{"args":{}}}` → 收到 `{"type":"item",...,"value":{"type":"ready",...}}`；帧数 **4**（open→pong→item ready→close），**close code 1000**，客户端 exit 0。
- 协议细节：JSON 文本帧 open/item/end/cancel（客户端）与 item/error/end（服务端）；错误载荷会被服务端 1008 "invalid Remote stream request" 关闭（代理只透传帧、不改写）。

### 4.7 停止 / 端口释放 / 重复启动（要求 7）

- `POST /api/m0/instances/{id}/stop` → 200，状态“已停止”；3080 无监听；无 `dsh/lib/bin.js` 残留进程。
- 二次启动 1,860 ms 成功、/workbench 再次 302；二次停止成功；管理器 SIGTERM 优雅退出、18080 释放。
- 直连侦察补充：进程组 SIGTERM 后 118–269 ms 退出、端口 <2s 释放（含二次启动验证）。

### 4.8 安全 / 权限 / 日志 / 未越界（要求 8）

| 检查 | 结果 |
| --- | --- |
| data-root 权限 | `700` |
| config/*.json 权限 | `600`（instances.json、manager.json，2 个文件均合规） |
| manager/证据日志 token 泄漏扫描（token= / dsh-auth-* / v1.* 值） | **0 匹配** |
| manager.log ERROR 行 | **0** 条 |
| Windows DSH 程序目录 D:\DevelopSoftware\DSH | 只读 find 清单（路径、大小、mtime）拼接后 sha256sum，前后**相同**（`d21dbbc5855f4bfd...`） |
| Windows 用户数据 C:\Users\23500\.dsh | 仅只读 `find` 遍历；测试期间 hash 变化（运行中的 Windows DSH 自身写会话/缓存），已作为 INFO 记录，未写入/未重启任何 Windows DSH |
| 参考仓库 PCL-DSH-Linux | git status 基线 hash 前后相同（`88b665dc9e72fcd2...`）；其 3 处本地改动为测试前既存，非本次所致 |
| 真实 DSH 写入范围 | 仅 WSL `/tmp/pcl-dsh-go-test/`；/opt 仅只读执行 |
| token/cookie 生命周期 | 仅内存/临时文件，证据全量脱敏；测试后进程与端口清理 |

### 4.9 真实浏览器渲染验证（M0 退出条件）

- 目标：验证“浏览器打开工作台”这一 M0 退出条件（HTTP/静态/SSE/WS 已在 §4.4–4.6 用 curl/Node 客户端验证）。
- 环境/方法：WSL 内 Go 管理器（独立 data-root `/tmp/pcl-dsh-go-browser`，runtime 复用 `e2e-data` 已安装的 rc.3，不重复下载）→ 创建/启动实例 → **Windows Edge 155.0.4283.62 以 headless 方式作为浏览器客户端**打开 `http://127.0.0.1:18080/workbench/{id}/`（WSL localhost forwarding 使 Windows 可访问）；302(token)→303(./)→cookie→200 自动完成；证据 URL 脱敏为 `/workbench/{id}/`。

```text
msedge.exe --headless=new --disable-gpu --no-sandbox --no-proxy-server \
  --user-data-dir=<temp> --window-size=1440,1000 --screenshot=<png> <workbench-url>
```

- 结果：
  - 截图 `docs/evidence/m0/e2e/browser-workbench.png`：33,603 B，1440x1000，sha256=`3d6cab2f2084d5bdc5fa67e189d936c15d43ed674c6b110482d5d6399b42993a`；可见完整 DSH 工作台 UI（左侧 deepseek HARNESS 侧栏：新会话/插件/自动化任务/更多/工作区；主区“探索未至之境 预览版”与输入区）。
  - DOM dump `docs/evidence/m0/e2e/browser-workbench-dom.html`：543,695 B；含 `<title>DeepSeek Harness</title>`、`__DSH_BOOT__`、`"release":{"version":"0.2.1-rc.3"}`；扫描无 token/cookie 值。
  - 说明：**Windows 仅作为浏览器客户端**，管理器与 DSH 全部在 WSL；未在 Windows 安装/运行管理器，未触碰 Windows DSH；测试后 WSL 管理器/实例已停、18080/3080 释放，Windows 临时 user-data-dir 与仓库临时目录已清理。
- Playwright 自动化（`scripts/wsl/browser-m0.sh` + `lib/browser-check.mjs`）：本次在 WSL 安装 Playwright 与 Chromium，但 Chromium 下载/`install-deps` 系统依赖安装耗时超出预算，按 Lead 决定终止并清理；脚本保留供具备浏览器依赖的环境复用。**M0 浏览器证据以 Edge headless 实测为准。**

## 5. 已知差异：DSH rc.3 与契约/文档（M0 实测）

| # | rc.3 实测 | 对实现的影响 |
| --- | --- | --- |
| 1 | manifest `engines={}`、无 `packageManager` | 不能依赖 manifest 校验 Node；发布包固定 Node 24.22.1 + pnpm 11.28.5 |
| 2 | cookie 名 = `dsh-auth-` + base64url(sha256(**Host authority**))，值 171B 签名 | 反代转发上游 Host 必须恒为 `127.0.0.1:<port>`（引导与后续请求一致），否则 401 |
| 3 | token 仅 `GET /` 单值有效（`/index.html?token=` → 401） | bootstrap 去前缀后路径必须恰为 `/` 且保留 query |
| 4 | 303 `Location: ./`、`Referrer-Policy: no-referrer` | 相对 Location 原样；管理器响应加 `Cache-Control: no-store` |
| 5 | Cookie `Path=/`、`SameSite=Strict`、无 Domain/Secure（HTTP 监听） | Set-Cookie 重写 Path=`/workbench/{id}`、保留 Strict、去 Domain/Secure |
| 6 | 工作台事件走 WS `/api/remote.mux`；HTTP SSE 只有 `/plugins/events`（client-hmr） | G4 的 SSE 验收用 `/plugins/events`；WS 用 remote.mux + 正确 open 帧 |
| 7 | 静态资源/该 SSE 端点上游不鉴权；页面/API/WS 鉴权 | 管理器必须在 /workbench 前置登录（M1 接入），不能依赖上游 200 |
| 8 | 就绪首响应可能直接 401（未见 404 阶段） | 401+token 即就绪；404 等待分支保留 |
| 9 | rc.3 CLI 仍有 `--patch`；web 新增 `--public-url/--trusted-host/--tls-cert/--tls-key` | 管理器不注入 `--patch`；能力探测容忍新增选项，只加 `--port/--host/--no-open` |
| 10 | pnpm 11 忽略 package.json `pnpm.overrides`，并把 overrides 放 `pnpm-workspace.yaml`；会追加 `minimumReleaseAgeExclude` | M0 修复：overrides 同时写 workspace（295 项）+ package.json 兼容保留；文档已记录上游行为 |
| 11 | 安装子进程需要主机 PATH（依赖构建脚本 node-pty/koffi/protobufjs） | 契约 §1.2 的 PATH 末尾 <原 PATH> 必须实现（M0 缺陷 D1 已修复） |
| 12 | rc.3 plugin 管理：`dsh plugin --profile <name> <pnpm args...>` | pluginManagement=true，M1 插件功能可用该 CLI（经 Node 桥） |

## 6. M0 未覆盖 / 留给后续里程碑

- **认证/登录/会话/CSRF/首登改密/登录限流**（M0 dev-mode 无登录；G1）。
- **Job/SSE 任务流、Last-Event-ID、取消/超时、忙 409**（G3）。
- **整合包预览/导入/导出与安全归档**（G6）、**已装插件增删改**（G7）、**直连登录 direct-access API/前端对话框**（G5）。
- **自定义 profile 自动探测未实现**（M0 fail-closed）：非 web profile 需在 `config/compatibility.json` 的 `versions.<ver>.profiles.<p>.appOptions` 人工声明；`web` profile 已真实验证。
- **运行时的删除 / trash / 被实例引用保护**（409 CONFLICT_IN_USE）未实现（M0 只有安装与复用）。
- **设置 API / 持久化未暴露**（proxy/registry/directAccessSshUser 等；M0 用 flags/默认值）。
- **日志 tail API / Job 日志流**未实现（M0 仅落盘日志）。
- **WebUI 页面与前端 E2E**（G8）；M0 已用 Edge headless 补真实浏览器渲染验证（§4.9），但完整前端交互/登录流程留 M1；Playwright 自动化未完成（WSL Chromium 依赖安装超预算，脚本保留）。
- **发行包/install.sh/systemd/Nginx/升级回滚/离线 runtime**（G9）；Nginx 生产链路（静态直出 + /workbench + SSE/WS）未在 M0 验证。
- 登录后 /workbench 页面完整交互（会话操作/文件/审批等）留 M1/浏览器 E2E；M0 已用 Edge headless 验证页面渲染（§4.9）。

## 7. M1 输入 / 建议

1. 把 M0 E2E 固化为回归：`scripts/wsl/m0-acceptance.sh`（真实 install 约 78–105s，全链路 <4min）可直接在 WSL/CI(Linux runner) 运行；WS/SSE 客户端复用 `scripts/wsl/lib/`。
2. 反代测试必须覆盖：上游 Host 固定、cookie 名 authority 绑定、303 相对 Location、Set-Cookie Path/SameSite 重写、`plugins/??` RawQuery 原样、SSE 不缓冲、WS 帧透传与 close code。
3. M1 登录接入后补：未登录 /workbench 401/跳登录、CSRF/Origin 校验、Manager Cookie 与 DSH Cookie 隔离（只转发非 JSESSIONID/XSRF 的 DSH cookie）。
4. 契约文档建议补：rc.3 无 engines、cookie authority 绑定、token 仅 GET /、SSE 路由 /plugins/events、pnpm 11 overrides/workspace 行为（Lead 已在 M0 决策记录更新）。
5. 内存：Node RSS 283–379 MB（单实例），1.6 GiB 服务器可接受；M1 可加采样/告警。
6. 打包：内置 Node/pnpm 已实测可用；发行包体积/离线 runtime 按计划 G9 处理。
7. **正式 API 冻结**：用认证/会话/CSRF/Job/SSE 的正式 REST 替换 M0 dev API；`/workbench` 必须接入登录门槛（M0 dev-mode 无鉴权）。
8. settings / direct-access 依赖内存中的 bootstrap URL（不得落盘/日志）；保留现有 proxy/process/runtime 语义不动，只做接线与鉴权，并补契约测试。
9. Nginx 生产链路（静态直出 + /workbench + SSE/WS、缓冲/超时/头部）留 M2/M4 验证；M0 只验证管理器直连链路。

## 8. 复现命令

```bash
# 0) WSL 内，仓库根目录
cd /mnt/d/Projects/DSHL/PCL-DSH-Go

# 1) 真实 DSH 侦察（安装到 /tmp/pcl-dsh-go-test/runtimes，采 token/cookie/SSE/WS 行为）
bash scripts/wsl/recon-rc3.sh all

# 2) Linux 单测（Windows 交叉编译 + WSL 执行）
bash scripts/wsl/run-linux-tests.sh ./internal/...

# 3) 正式 M0 E2E（fresh data-root，真实 Go install；约 3-4 分钟）
bash scripts/wsl/m0-acceptance.sh
#   调试开关（非正式证据）：--skip-build / --reuse-runtime
```

产物：`docs/evidence/m0/e2e/e2e-<ts>.log.txt`（+ manager 日志）。脚本退出非零即失败并保留现场（`/tmp/pcl-dsh-go-test/e2e-scenario-<ts>/`、manager.log 路径打印）。

## 9. 证据索引（docs/evidence/m0/）

| 路径 | 内容 |
| --- | --- |
| `e2e/e2e-20261024-140806.log.txt`、`e2e/...-140806-manager.log.txt` | **最终正式 E2E**：58/58 PASS，真实 install 78.1s，全链路/WS/权限/Windows DSH |
| `e2e/e2e-20261024-140108.log.txt`、`e2e/...-140108-manager.log.txt` | 修复前 degraded 验证（49 检查，除真实 install 外全链路）——证明缺陷定位准确 |
| `e2e/e2e-20261024-135618.log.txt`、`e2e/...-135618-manager.log.txt` | **缺陷 D1 发现证据**：Go install PATH 缺失 → pnpm 254 / 构建脚本失败 |
| `recon.md` | DSH rc.3 侦察报告：token/cookie/HTML/SSE/WS 细节、契约差异、E2E 请求模板 |
| `logs/01-env.txt` | WSL 环境/版本/sha256/registry 延迟 |
| `logs/02-registry.txt` | npm 版本清单、rc.3 manifest 摘要 |
| `logs/03-install.log.txt`、`logs/03-install-summary.txt` | 侦察直连安装日志/结果 |
| `03-install-summary-cleanroot.txt` | 另一 root 全量 clean install 实测（104s = cohort 31s + pnpm 72s） |
| `cohort-0.2.1-rc.3.json` | 295 个同版本 `@deepseek-ai/dsh-*` overrides（canonical sha256=73781a68...） |
| `logs/04-probe-*.txt` | CLI 能力探测（launcher/web help） |
| `logs/launch/**` | 启动/就绪/token/HTTP/HTML/静态/JS/SSE/WS/进程/二次启动原始证据 |
| `logs/05-linux-tests.txt` | Linux 单测基线：9 个 internal 包 built=9/failed=0，249 个 PASS |
| `e2e/browser-workbench.png` | **真实浏览器渲染证据**（Windows Edge headless）：/workbench 工作台截图，1440x1000，33,603 B，sha256=`3d6cab2f2084d5bdc5fa67e189d936c15d43ed674c6b110482d5d6399b42993a`（无 token） |
| `e2e/browser-workbench-dom.html` | 同一次浏览器会话的 DOM dump：543,695 B，含 title/`__DSH_BOOT__`/release rc.3（无 token/cookie） |
| `recon.md §9` | 可直接复用的 curl/WS/SSE E2E 模板 |

## 10. Windows DSH 未修改声明（含检查命令）

本次 M0 全部真实验证在 WSL 内、使用 /tmp 独立 runtime/data-root 完成；**没有读写 Windows DSH 的安装目录、用户数据、配置、插件、会话、缓存或进程，也没有安装/升级/重启 Windows DSH**。

检查命令（只读；建议每次验收前后各跑一次并比对）：
```bash
find /mnt/d/DevelopSoftware/DSH -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum
find /mnt/c/Users/23500/.dsh       -printf '%p|%s|%T@\n' 2>/dev/null | sha256sum
```

- 程序目录 D:\DevelopSoftware\DSH：本次 E2E 前后 hash **完全相同**（`d21dbbc5855f4bfd...`）。
- 用户数据 C:\Users\23500\.dsh：当前 Windows DSH 正在运行并写会话/缓存，hash 会自然变化；本次仅只读 find 遍历，未写入。该目录不作为“完全不变”断言，程序目录严格比对 + 上述声明为准。
- 参考仓库 `D:\Projects\DSHL\PCL-DSH-Linux` 未修改：git status 基线 hash 前后相同（`88b665dc9e72fcd2...`）。
- WSL 现场说明：`/tmp/pcl-dsh-go-test/` 下保留 `e2e-data`（含真实安装的 rc.3 runtime 508M，供 M1 快速复用）、侦察 runtime、E2E scenario；Playwright/Chromium 临时目录已按 Lead 决定清理；**这些是临时现场，可随时清理**，验收证据已全部入库（`docs/evidence/m0/`）。

## 11. M0 缺陷与上游迁移适配记录

| ID | 现象 | 根因 | 修复/状态 | 证据 |
| --- | --- | --- | --- | --- |
| D1 | fresh data-root 经 Go API 安装 rc.3 失败：pnpm exit 254；node-pty/koffi/protobufjs install scripts Failed | runtime 安装子进程 PATH 只含 <node>:<shims>:<pnpm>，缺主机 PATH；pnpm 11 执行依赖构建脚本失败 | commit `42a558c`：install 环境 PATH 追加 manager 的 os.Getenv("PATH")（契约 §1.2）；单测覆盖；真实安装复验通过（78.1s） | `e2e/e2e-20261024-135618*` |
| D2（上游迁移适配，非代码缺陷） | pnpm 11 警告 package.json 的 `pnpm.overrides` 被忽略 → 原契约写法下 295 项发行批次 overrides 不生效 | 上游 pnpm 11 把 overrides 设置迁到 `pnpm-workspace.yaml` | 适配决策 commit `42a558c`：overrides 同时写入 workspace（295 项排序）+ package.json 兼容保留；契约文档记录“安装后 pnpm 追加 minimumReleaseAgeExclude，不断言逐字”。验证：E2E 断言 overrides=295 且 295 个 dsh-* 全为 rc.3 | `e2e/e2e-20261024-140806.log.txt` |
| D3（仅验收脚本） | Windows go.exe 经 WSL 用 cmd /c 传 `-ldflags "-s -w ..."` 时引号被 cmd 拆坏 | WSL interop 不传 env + cmd /c 引号规则 | 验收脚本改为生成临时 .bat 注入 env/ldflags；记录构建 size/sha | 验收脚本源码 |

## 12. 完成项对照

| 要求（task-7 / ACCEPTANCE） | 结果 |
| --- | --- |
| 交叉编译 + 记录 size/sha256 | 完成：8,474,784 B / sha256 见 §4.1/证据 |
| fresh data-root（删除重建 0700）+ 18080 dev-mode | 完成：0700，manager 健康 |
| Go 运行时安装路径（POST install，记录耗时/entryPath/reused，校验入口文件） | 完成：200、78.1s、reused=false、entry 存在且路径匹配 |
| 创建→launch→就绪→/workbench 302/303/cookie 重写/200/至少 2 静态资源/错误映射 | 完成（含 unknown 404、stopped 409） |
| SSE curl -N 分块 + 首块时间戳 | 完成：首块 0.0ms，data 0.1ms，4 行 |
| WS 经代理 open + 正确帧：101/ready/close 1000/帧数 | 完成：101、4 帧、close 1000 |
| stop 端口释放/无残留/二次启动/清理管理器 | 完成 |
| 安全/权限/日志扫描/Windows DSH/参考仓库 | 完成（见 §4.8/§10） |
| 失败非零退出并保留现场 | 完成（脚本 trap 保留 scenario + manager.log 路径） |
| 证据 docs/evidence/m0/（*.log.txt 脱敏） | 完成 |
| 真实浏览器渲染验证（M0 退出条件） | **完成（Edge headless 真实渲染）**：Windows Edge 155.0.4283.62 headless 打开 WSL 管理器 `/workbench/{id}/`，自动完成 302→303→cookie→200；DOM title=DeepSeek Harness、`__DSH_BOOT__`/rc.3 release 存在；截图 `browser-workbench.png`（sha256=3d6cab2f...）。Windows 仅作浏览器客户端，未碰 Windows DSH（§4.9） |
| Playwright 自动化浏览器 E2E | 未完成：WSL 内 Chromium 下载/系统依赖安装耗时超预算，按 Lead 决定终止并清理；`scripts/wsl/browser-m0.sh` + `lib/browser-check.mjs` 保留供后续环境复用（§4.9 说明） |

## 13. M0-1…M0-6 任务完成对照（TASK_BREAKDOWN M0 退出条件）

> TASK_BREAKDOWN §M0 退出条件：真实 DSH rc.3：安装→启动→就绪→浏览器打开工作台（HTTP/静态/SSE/WS）→停止/端口释放；无账户注入；Windows DSH 未改。以下逐项对照。

| 任务 | 内容（TASK_BREAKDOWN） | 完成证据 / 状态 |
| --- | --- | --- |
| M0-1 | 仓库骨架：go.mod、cmd/main、config/logging、flags/env、data-root 初始化、flock、优雅停机 | ✅ commit `74d70e8`/`aaac0fd`；`internal/{config,logging,tools}` + `cmd`；Windows `go test ./...` 10 包全绿；E2E 实测 data 0700、config 0600、SIGTERM 优雅停机、`/api/health` 版本化（§4.1/§4.8） |
| M0-2 | Node/pnpm 工具层：定位/校验/调用 pnpm、工具目录、版本清单 | ✅ commit `aaac0fd`；`internal/tools`（查找顺序/版本探测/子进程 env）；Linux 测试含真实工具链校验（TestVerifyRealToolchain）；真实安装使用内置 node/pnpm（§4.2） |
| M0-3 | 实例 store + 进程管理：instances.json 原子写、spawn/进程组/stdout token/就绪/停止 | ✅ commit `aaac0fd`；`internal/{store,process}`；Linux 测试全绿；E2E launch/就绪/stop/端口释放/二次启动（§4.3/§4.7）；token 只在内存（recon WS/token 证据） |
| M0-4 | 最小 runtime/probe/launch：npm 版本查询、安装命令、能力探测、启动命令/环境 | ✅ commit `aaac0fd`；`internal/runtime`；真实 install 200/reused=false/78.1s、cohort overrides 295；probe 证据 `docs/evidence/m0/logs/04-probe-*`；launch 3,182ms（§4.2/§4.3） |
| M0-5 | /workbench 反代最小闭环：HTTP/SSE/WS + token/Cookie/Location 重写 | ✅ commit `aaac0fd`；`internal/server/proxy`；E2E HTTP/静态/SSE/WS（§4.4–4.6）；真实浏览器打开工作台（§4.9） |
| M0-6 | M0 验收记录与演示脚本（qa-release）；Lead 附加接线 internal/server+app+cmd | ✅ Lead：`74d70e8`（接口/go.mod）、`aaac0fd`（垂直切片+server/app/cmd 接线）、`46a37e1`（脚本+证据）、`1aeeb7c`（Linux 测试加固）；qa-release：`scripts/wsl/*`、`docs/evidence/m0/*`、本报告（§3/§8） |

**M0 退出条件逐项**：安装 ✅ → 启动 ✅ → 就绪 ✅ → 浏览器打开工作台 ✅（HTTP/静态/SSE/WS）→ 停止/端口释放 ✅ → 二次启动 ✅；无账户注入 ✅；Windows DSH 未改 ✅。

## 14. ACCEPTANCE G1–G10 映射（M0 阶段）

> **明确声明：M0 不是 G1–G10 全绿，只是垂直切片。** 下表说明 M0 实际覆盖了哪些门禁的哪些部分，正式门禁在 M1–M5 逐步完成。

| 门禁 | M0 阶段覆盖 | 说明 / 剩余工作 |
| --- | --- | --- |
| G1 认证/会话/CSRF | 未开始 | M0 dev-mode；登录/密码/会话/CSRF/限流在 M1-1/M1-2 |
| G2 实例/运行时 | **部分** | 已覆盖：真实 runtime 安装/复用（200/reused/entryPath/overrides 校验）、实例创建/启动/停止、单实例与端口/忙 409 核心、错误映射；未覆盖：编辑/删除/trash/selection 持久化/完整 409 矩阵 |
| G3 Job/SSE | 未开始 | M0 为同步 API；Job/SSE/取消/续传在 M1-4/M1-8 |
| G4 工作台反代 | **部分** | 已覆盖：302(token)→303(./)→Cookie 重写→200 页面/静态/插件 bundle、SSE 分块、WS 101+ready+close 1000（dev-mode 直连管理器）、真实浏览器验证；剩余：登录门槛（M1）、Nginx 生产链路（M2/M4） |
| G5 直连登录 | 未开始 | M1-7（依赖内存 bootstrap URL，token 不落盘/日志） |
| G6 整合包 | 未开始 | M3 |
| G7 已装插件 | 未开始 | M2/M3（pluginManagement=true 已由 recon 确认） |
| G8 WebUI | 未开始 | M1+；M0 仅验证 DSH 页面渲染（非管理器 WebUI） |
| G9 发行/部署/离线工具链 | **部分** | 已覆盖：内置 Node/pnpm 真实可用、交叉编译；未覆盖：release 打包/install.sh/systemd/Nginx/SHA256/离线 runtime |
| G10 数据/边界/安全 | **部分** | 已覆盖：data 0700/config 0600、日志脱敏扫描 0 泄漏、Windows DSH 未改、参考仓库未改、代理 SSRF 基础设计；未覆盖：完整攻击面/故障注入/双进程 flock 集成测试 |

## 15. 阶段提交

| commit | 内容 |
| --- | --- |
| `74d70e8` | chore(m0): freeze M0 interfaces and add Go module |
| `aaac0fd` | feat(m0): vertical slice - config/logging/store/tools/runtime/process/proxy/server/app |
| `ed19558` | docs(m0): record pnpm 11 overrides migration and install PATH requirement |
| `42a558c` | fix(m0): install PATH must include host PATH and pnpm 11 needs workspace overrides |
| `46a37e1` | test(m0): real DSH 0.2.1-rc.3 acceptance evidence and scripts |
| `c608a8c` | chore: enforce LF line endings for cross-platform WSL development |
| `1aeeb7c` | test(m0): harden portInUse release assertion for WSL2 |
| 本报告提交 | docs(m0): M0 acceptance report + Edge headless browser evidence（`docs/evidence/m0/e2e/browser-workbench*`；qa-release 产出、Lead 复核后入库） |

> 报告结束。M0 验收证据全部位于 `docs/evidence/m0/`；token/cookie 已在所有证据中脱敏为 `***`。