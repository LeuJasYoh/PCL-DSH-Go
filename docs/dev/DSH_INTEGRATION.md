# DSH 集成契约（PCL-DSH-Go v1.0）

> 本文件是实现 PCL-DSH-Go 时与 DeepSeek Harness（DSH）交互的**行为契约**。参考实现：`D:\Projects\DSHL\PCL-DSH-Linux`（只读，不修改）。
> 基线：**DSH 0.2.1-rc.3**；Node **24.22.1**；pnpm **11.28.5**；目标 Linux x86_64。
> 与参考实现冲突时，以本文件 + `docs/ACCEPTANCE.md` 为准；参考实现用于理解行为。

---

## 1. Node/pnpm 工具链

### 1.1 版本与布局
- Node：`release/tools/node/bin/node`（来自 `node-v24.22.1-linux-x64.tar.xz`，构建时 SHA-256 校验）。
- pnpm：`release/tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`（vendored；用内置 Node 执行）。
- 不依赖系统 node/npm/pnpm；目标机只调用内置二进制。
- 查找顺序（参考 `RuntimeService.toolRoots/nodeExecutable/pnpmScript`）：
  1. `--pcl.tool-root` 指定目录（发布包 `current/tools`）；
  2. data-root 的 `tools/`；
  3. `PATH`（仅兜底；发布包存在时不应使用）。
- Node 查找：`tools/node/bin/node`（Linux）或 `tools/node/node`；pnpm 查找：`tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`、`tools/pnpm/bin/pnpm.cjs` 等。
- 注意：Node 目录可能包含 npm；pnpm 由我们随包提供，不要求系统 npm。

### 1.2 子进程环境（DSH 进程）
参考 `RuntimeService.configureEnvironment` 的键值，至少注入：
```text
HOME=<instance>/workspace
DSH_HOME=<instance>/dsh-home
DSH_AGENTS_HOME=<instance>/agents-home
HDSL_NPM_REGISTRY=<registry>
NPM_CONFIG_REGISTRY=<registry>
NPM_CONFIG_CACHE=<data>/cache/npm
PNPM_HOME=<release>/tools/pnpm
PNPM_CONFIG_STORE_DIR=<data>/cache/pnpm-store
NPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS=false
NPM_CONFIG_PACKAGE_MANAGER_STRICT=false
PNPM_CONFIG_AUTO_INSTALL_PEERS=true
XDG_CACHE_HOME=<data>/cache
DSH_TELEMETRY_MODE=DISABLED
PATH=<node-bin>:<data>/tools/shims:<tools>/pnpm:<原 PATH>
```
- 有代理时：`HTTP_PROXY`、`HTTPS_PROXY`、`NPM_CONFIG_PROXY`、`NPM_CONFIG_HTTPS_PROXY`、`NO_PROXY=localhost,127.0.0.1,::1`。
- **环境从空白开始**（不继承用户无关环境），只注入白名单；不得注入账户/API Key（本期无账户）。
- `NODE_OPTIONS` 默认不强制；如启用内存上限，使用 `--max-old-space-size=<cap>`（默认不设置，避免影响 DSH 行为；可在后续版本加设置项）。

---

## 2. 版本查询

- 请求：`GET {registry}/@deepseek-ai%2fdsh`（registry 默认 `https://registry.npmjs.org`）。
- 超时 60s；响应体上限 32 MiB；跟随重定向**关闭**或严格限制；只接受 HTTPS/HTTP 合法 registry。
- 解析 `versions` 对象的键，按**SemVer 降序**返回（使用自己的 SemVer 实现；npm 的版本可能含 prerelease/build，按 SemVer 规则排序）。
- 上游错误映射：超时/连接 → 504 `UPSTREAM_TIMEOUT`；HTTP 非 2xx/解析失败 → 502 `UPSTREAM_ERROR`，中文可操作提示。
- 该接口不占用全局变更任务锁（参考实现：发现类请求）。

---

## 3. 运行时安装

### 3.1 安装准备（staging）
- 校验 version 精确 SemVer；目标目录 `<data>/runtimes/<version>`。
- 已安装（目录 + 入口校验通过）→ 直接返回“已安装，保留现有运行时”。
- staging：`<data>/runtimes/.install-<version>-<uuid>`。
- 写入 `package.json`：
```json
{
  "name": "hdsl-runtime",
  "private": true,
  "dependencies": { "@deepseek-ai/dsh": "<version>" },
  "pnpm": { "overrides": { "<@deepseek-ai/dsh-* 同批次包>": "<version>" } }
}
```
- 写入 `pnpm-workspace.yaml`（基础内容逐字；pnpm 11 下 overrides 适配见下）：
```yaml
autoInstallPeers: true
nodeLinker: hoisted
resolutionMode: time-based
strictPeerDependencies: false
dangerouslyAllowAllBuilds: true
```
  - **pnpm 11 适配（M0 决策，2026-10-24）**：pnpm 11.28.5 **不再读取** `package.json` 的 `pnpm.overrides`（会打印警告并忽略，导致发行批次 pin 不生效）。为让 overrides 真正生效，安装时把同一份 overrides 同时写入 `pnpm-workspace.yaml` 的 `overrides:` 字段（键名 YAML 安全引用、顺序稳定）；`package.json` 的 `pnpm.overrides` 继续保留（兼容 pnpm 10）。安装后 pnpm 可能向该文件追加 `minimumReleaseAgeExclude` 等字段，这是上游行为：**逐字要求仅适用于写入前的基础内容 + overrides 段**，不要断言安装后文件逐字一致。
- **发行批次 overrides**（参考 `RuntimeService.releaseCohort`）：
  - 请求 registry 上 `@deepseek-ai/dsh` 该版本 manifest；
  - 递归读取 dependencies/peerDependencies/optionalDependencies，仅收集名以 `@deepseek-ai/dsh-` 开头且 spec 恰好是 `<version>`、`^<version>`、`~<version>` 的包；
  - 最多 2048 个；全部 pin 到同一 version；结果缓存 `<data>/cache/registry-metadata/<sha256(registry)>/cohort-<version>.json`（schemaVersion 1、complete true、package/version/registry/checkedAt/count/overrides/sha256；TTL 30 天；校验失败自动重建）；
  - 目的：避免 rc 主程序混装不同 rc 组件。

### 3.2 安装命令（逐字保留语义）
```text
<node> <pnpm.cjs> install --prod --no-frozen-lockfile   --registry=<registry>   --store-dir=<data>/cache/pnpm-store   --reporter=append-only
```
- 工作目录 = staging；环境 = DSH 环境 + HOME 指向 staging 临时 home（避免污染用户目录）。
  - **PATH 必须包含宿主机原 PATH**（§1.2 的 `<原 PATH>` 后缀）：pnpm 执行 node-pty/koffi/protobufjs 等生命周期脚本时需要 `/usr/bin`、`/bin` 等宿主工具；实测缺失时 pnpm 退出 254、install scripts Failed。DSH 启动环境同样按 §1.2 处理。
- 超时 20 分钟；日志实时上报 Job。
- 安装脚本正常运行（`dangerouslyAllowAllBuilds: true`）；保留 pnpm-lock.yaml。
- 校验 `staging/node_modules/@deepseek-ai/dsh/package.json`：name/version 匹配；读取 `bin`（字符串或 `bin.dsh`）得到入口（如 `lib/bin.js`），必须位于包目录内、存在、realpath 不越界。
- 原子移动 staging → target；失败保留 staging 便于诊断；目标已存在则拒绝覆盖。
- 成功后 invalidate 能力探测缓存。
- 删除运行时：被实例引用 → 409 `CONFLICT_IN_USE`；否则移动到 `data/trash/`；返回被移动路径。

---

## 4. 能力探测与启动命令

### 4.1 探测（参考 `RuntimeService.inspect` / `Capabilities.fromHelp`）
- 临时 probe 目录：`<data>/cache/probes/<version>/<uuid>/{workspace,dsh-home}`；不复制用户目录/凭据。
- `node <dsh-entry> --help`：超时 35s；解析 launcher options（`--profile`、`--help` 等）与 `webAlias`、`pluginManagement`。
- 若 launcher 支持 `--profile`：`node <entry> --profile web --help`；否则若支持 web 别名：`node <entry> web --help`；超时 60s。
- app help 验证：输出包含 `usage`/`options`/`--port` 之一；否则 `appHelpVerified=false`。
- 人工覆盖：`<data>/config/compatibility.json` 的
```json
{ "versions": { "<ver>": {
  "launcherOptions": ["--profile", "--help"],
  "appOptions": ["--port", "--host", "--no-open"],
  "pluginManagement": true,
  "webAlias": false,
  "profiles": { "<profile>": { "appOptions": [...] } }
} } }
```
- 缓存：按入口文件 mtime/size + compatibility.json mtime 作为指纹；重启服务可重建。
- 探测失败必须 fail-closed：例如无法确认 `--port` 时拒绝启动实例，提示刷新检测或补 compatibility.json。

### 4.2 启动命令（无账户注入）
```text
<node> <dsh-entry> [--profile <profile> | web]   --port <port> [--host 127.0.0.1] [--no-open]
```
- 参数顺序：profile 选择先于 app 参数；`--port` 必须支持，否则拒绝启动；`--host`/`--no-open` 仅在能力声明支持时添加。
- 自定义 profile（非 web）：
  - 优先使用 `compatibility.json` 的 `profiles.<profile>.appOptions`；
  - 否则按参考实现：临时 probe home 复制 profile 的 package.json 依赖与 bundles，junction/symlink node_modules，运行 `node <entry> --profile <profile> --help`（60s），解析 app options；不复制用户 patch/凭据；
  - 失败则拒绝启动，提示补充 compatibility.json。
- 不添加 `--patch`（账户系统已删）；不添加任何用户自定义命令参数。

---

## 5. 启动、就绪、停止

### 5.1 启动前检查
1. 实例存在、未运行；端口空闲（尝试连接 127.0.0.1:端口，能连接即视为占用）。
2. 运行实例上限：`--pcl.max-running-instances`（默认 0=不限制；2C/1.6GiB 基线建议显式设为 1）；达到上限返回 409 CONFLICT_RUNNING_INSTANCE（M5.1 起可配置，M5.2 起默认不限制）。
3. 运行时已安装且入口校验通过；能力探测已缓存；端口在 1–65535。
4. staging 写入启动锁定（内存状态），失败清理。

### 5.2 spawn
- 命令：§4.2；工作目录 = instances/<id>/workspace；设置进程组（Unix Setpgid: true）。
- 环境：§1.2 白名单；不继承无关变量；启动前确保 dsh-home/agents-home/workspace 存在。
- 进程启动后：
  - goroutine A：每 100ms 抓 descendants 快照（记录 pid + /proc/<pid>/stat starttime 或等价唯一标识），用于只杀自有进程与 PID 复用防护；
  - goroutine B：逐行读 stdout/stderr（合并），ANSI 去色，解析 bootstrap URI；每行写入实例日志前脱敏（token/key/password/cookie/authorization）；
  - goroutine C：wait 退出并记录退出码；更新状态。
- 只允许本管理器启动的进程被停止；不属于自有进程的一律不动（端口冲突只报错，不杀）。

### 5.3 bootstrap token 解析（安全关键）
- 扫描每行 stdout（去 ANSI），找 "dsh web:" 标记；取其后第一个空格分隔 token 作为 URI。
- 只接受：scheme=http、host ∈ 集合 {127.0.0.1, localhost, [::1]}、port == 实例端口、无 userinfo；否则忽略。
- token/URI 只存内存（进程生命周期），不写盘、不写日志/Job result；日志需把 token=... 脱敏。
- 停止/退出时清除。

### 5.4 就绪判定（90s 超时）
- HTTP/1.1 客户端，connectTimeout=1s、请求超时 2s、显式禁用代理/NO_PROXY（防止环境代理劫持回环）。
- 轮询 http://127.0.0.1:端口/，间隔 400ms，上限 90s。
- 判定：
  - 2xx/3xx → 就绪；
  - 401/403 且已捕获 bootstrap URI → 就绪（新版 DSH 先 401，带 token 后才可访问）；
  - 404/其他 → 继续等待；
  - 进程提前退出 → 失败，提示查看任务日志；
  - 超时 → 进程可能仍在运行，状态置“进程运行中，服务未就绪”，任务失败 INSTANCE_NOT_READY；允许用户手动停止/重试。
- 就绪后状态“运行中”，记录 web 地址（内存，含 token query）；GET /api/instances/{id}/web-url 返回 /workbench/{id}/（不暴露 token）。

### 5.5 停止（先子孙后根）
1. 抓当前 descendants（自有 pid + starttime 校验）。
2. 对子孙发 SIGTERM（反序），再对根 SIGTERM。
3. 等待根退出最多 3s；未退出 → 对存活的子孙/根 SIGKILL；最多再等 4s。
4. 仍有存活 → 报错“进程尚未退出，暂不能修改该实例”。
5. 清理状态/内存 web 地址；轮询端口释放最多 2s；记录日志。
- 服务退出时必须停止所有自有实例（systemd cgroup 兜底，但不能依赖）。
- 只杀自有进程；不得使用 pkill/killall 按名字杀 Node。
- 取消/超时：停止操作可被中断；中断后仍要尽力清理，返回明确错误。

### 5.6 状态字符串（前端依赖）
- 未启动、启动中、运行中、已停止（或参考实现的“已退出 (code)”）、进程运行中，服务未就绪、端口被占用。
- status 返回中文；前端按字符串显示/分支；发生改变需同步前端代码与测试。

---

## 6. /workbench 反向代理

### 6.1 路由与前置条件
- 路径：/workbench/{instanceId}/**（含空 remainder、/、深层路径）；HTTP 方法仅 GET/POST/PUT/PATCH/DELETE/HEAD。
- 登录会话必需；实例存在、运行、状态“运行中”；未就绪 → 409 INSTANCE_NOT_READY；不存在 → 404。
- 代理目标永远由 http://127.0.0.1:<instance.port> 构造，不接受请求 Host/URI 中的 host；端口为实例登记端口；请求路径含 .. 或控制字符 → 400；构造后再校验 scheme/host/port/userinfo（SSRF 白名单）。
- 空 remainder（/workbench/{id}）→ 302 到 /workbench/{id}/（保留 query），no-store，避免 base href 解析错误。

### 6.2 token 引导
- 无 dsh-auth-* Cookie 且查询串无 token → 302 到 /workbench/{id}/?<bootstrap query>（token 来自内存 bootstrap URI；仅回环+端口匹配时带 query，否则裸路径）。
- 其他情况正常转发；token 只在首次引导使用，不写日志。
- 不转发本应用 Cookie（JSESSIONID/XSRF-TOKEN）；只转发 DSH 自身 Cookie（dsh-auth-* 及其他非本应用 Cookie）。
- 请求头过滤：hop-by-hop（connection/keep-alive/proxy-*/te/trailer/transfer-encoding/upgrade）、Host、Content-Length、Cookie、Origin、Referer、CSRF 头、Forwarded/X-Forwarded-* 不转发；浏览器 Origin 改写为 http://127.0.0.1:<port>。
- 响应头过滤：hop-by-hop 不转发；Set-Cookie 重写；Location 重写；Cache-Control/Expires/Pragma 丢弃，响应设 Cache-Control: no-store。
- Set-Cookie 重写：去掉 Domain/Secure/Path，Path 改为 /workbench/{id}；SameSite=None → Lax；其余属性保留。
- Location 重写：
  - 相对地址（./、../、相对路径）原样；
  - 绝对路径（/...）加前缀 /workbench/{id}；
  - 指向回环实例端口的绝对 URL → 前缀 + path + query；
  - 外部绝对 URL 原样。
- 流式转发：SSE/其他响应逐块 flush；普通请求超时 60s → 504 UPSTREAM_TIMEOUT；连接失败 → 502 UPSTREAM_ERROR；header 先发后流中断只关闭连接并 debug 日志；HEAD 不写 body。
- 日志：不记录 query/token/Cookie/请求头原文/上游 body；仅实例 id、方法、状态、耗时（可选）。
- 方法/body：POST/PUT/PATCH 流式转发请求体；GET/HEAD/DELETE 无 body。

### 6.3 WebSocket 反向代理
- 握手：仅登录会话 + 实例存在/运行/状态运行中；Origin 与 Host 同源（默认端口归一化：http→80、https→443；Host 无端口时使用 Origin 的 scheme 默认端口）。
- 上游 URI：ws://127.0.0.1:<port><path>?<query>（path 来自去掉前缀后的 remainder；同 SSRF 规则）。
- 透传：Cookie（DSH 自身 Cookie）、Sec-WebSocket-Protocol（子协议）、Origin 改写为回环；最大消息 1 MiB；文本/二进制帧双向；ping/pong；关闭码/原因归一化转发。
- 错误：未就绪 409；上游连接失败 502；握手拒绝 401/403/404/409 与请求一致。
- 测试：真实 DSH 页面建立 /workbench/{id}/api/remote.mux（具体路径以 DSH 页面为准），至少验证连接、消息收发、关闭。

### 6.4 直连登录（SSH 隧道 + 一次性 token）
- GET /api/instances/{id}/direct-access（登录会话；CSRF 不适用）：
  - 实例存在；无论是否运行都返回 200；
  - port = 实例端口；
  - sshHost：从请求 Host 推导（去掉端口；拒绝空/控制字符；Nginx 传递的 Host 公网名/IP）；
  - sshUser：取设置 directAccessSshUser（默认 root；允许配置）；
  - tunnelCommand = ssh -L <port>:127.0.0.1:<port> <sshUser>@<sshHost>；
  - ready：运行且状态“运行中”且内存有 bootstrap URI；
  - directUrl：ready 时 http://127.0.0.1:<port>/<bootstrap path>?<bootstrap query>；否则 null；
  - note：未就绪原因/使用说明；
  - token 只在内存，不写日志/不持久化；响应 Cache-Control: no-store。
- 前端对话框：展示两步（SSH 命令、直连 URL）+ 复制按钮 + 说明；保留参考实现截图样式/交互（DirectAccessDialog.vue）。
- 说明：OAuth 回调只接受回环；隧道只是把本机回环映射到服务器实例端口，不改变 DSH 监听。

---

## 7. 整合包（.dspack / Overture）

> 参考实现：pcl-dsh-core/.../pack/{PackService,SafeArchive,TarSafety,PackInspection}.java 与 PackServiceTest（25 个用例）。必须逐项移植安全规则，不得简化。

### 7.1 格式识别
- ZIP 容器，必须恰好一个标记文件：
  - dspack.json：{"format":"dspack","version":2|3} + manifest.json（manifestVersion 4/5）；type=profile 或（v3+mv5）dshhome。
  - dsh-snapshot.json：Overture snapshot format 1；profiles/<id>/profile.yaml（YAML，含 dshVersion 可选）。
  - dsh-pack.yaml：明确拒绝“Overture YAML 清单格式尚未支持”。
- 拒绝：旧 tgz、缺版本标记、多个冲突标记、不支持版本。
- 预览响应字段见 API 规格；校验后生成 previewId，TTL 30min；归档保留在 cache/uploads 或 pack-staging，0600。

### 7.2 安全归档规则（必须全部实现）
参考 SafeArchive/TarSafety：
- 归档 ≤2 GiB；解包总量 ≤6 GiB；单文件 ≤2 GiB；元数据（manifest/package.json）≤4 MiB；条目数 ≤400,000。
- ZIP 原始路径包含反斜杠 → 拒绝；路径必须相对、非空、≤1000 字符；禁止 .././空段/尾随点/尾随空格/控制字符/<>:"|?*/Windows 保留名（CON/PRN/AUX/NUL/COM1-9/LPT1-9）。
- 路径重复/大小写冲突/Unicode NFC 归一后冲突/文件与目录冲突 → 拒绝。
- 禁止符号链接/硬链接/特殊文件；ZIP 加密/不支持压缩 → 拒绝；条目 CRC/大小校验（解压实际字节与声明一致）；压缩率 >1000 且原始 >1 MiB → 拒绝。
- 解包目标必须在目标目录内；目标父链不得包含符号链接/reparse point；目标存在且非普通文件 → 拒绝；崩溃/取消时清理。
- npm tarball（如有 vendored tarball）用 TarSafety：必须位于 package/ 下、gzip 前后限、PAX/GNU 元数据大小限制、无链接/特殊/稀疏、包名/版本校验、CRC/尾随数据检查；不解包执行任何包代码。
- 所有解包操作可取消（context）；中断返回明确错误。

### 7.3 预览
- multipart 字段名 file；流式落盘（不整读内存）；计算 SHA-256；解析/校验；生成 previewId（随机 12+ hex）；TTL 30min。
- 返回：name、format、version、dshVersion、profiles、unpackedBytes、warnings。
- 预览不安装/不执行；只做结构检查与安全校验。

### 7.4 导入（Job）
1. 校验 previewId/过期/哈希；重算 SHA-256（防替换）。
2. 解析 runtime 版本：请求 version > manifest dshVersion > 已安装兼容版本 > 已安装最新；无合适 runtime 则先安装（Job 内同步执行安装）。
3. 解包到 staging（安全规则）；写入实例目录结构（dsh-home/workspace/agents-home）；恢复 profile 依赖：
   - <node> <pnpm.cjs> install --ignore-scripts --config.ignore-pnpmfile=true --registry=<registry> --store-dir=<data>/cache/pnpm-store [--frozen-lockfile]
   - 有 pnpm-lock.yaml 时先 --frozen-lockfile；仅当 ERR_PNPM_OUTDATED_LOCKFILE 且导入允许刷新时，备份原锁为 .hdsl-original-pnpm-lock.yaml，再去掉 frozen 重跑；其他错误失败。
4. 创建实例（名称/版本/profile/端口），登记清单；失败整体回滚到 trash，不污染现有实例。
5. Job result {instanceId,name}；日志实时上报。
- 无账户：不做账户绑定/overlay/密钥注入。

### 7.5 导出（Job）
- 参数 {instanceId, mode:"profile"|"home"}。
- profile：导出指定 profile 的 package.json/lock/依赖清单/必要 profile 文件；home：导出整个 dsh-home（含多 profile；安全排除凭据/会话/日志/缓存）。
- 排除：auth/token/credentials/secret/oauth/session/logs/cache/trash 等参考实现黑名单；不含账户文件（无账户）。
- 生成 ZIP（安全路径/大小限制），登记 artifact（TTL 24h），返回 {artifactId,filename,size}；下载接口流式、no-store、正确文件名。
- 导出失败清理临时文件；不破坏实例。

---

## 8. 已装插件

> 参考实现：pcl-dsh-core/.../runtime/PluginService.java、RuntimeService 的 ensurePnpmShim/pluginBridge。

### 8.1 列表
- GET /api/instances/{id}/plugins?profile=...
- 读取 instances/<id>/dsh-home/profiles/<profile>/package.json（仅元数据，不启动/不执行 profile）：
  - dependencies/optionalDependencies：包名→版本；
  - dsh.profile.bundles：字符串或 {name|package}；加入 bundles，版本标记“内置”，未在 dependencies 时视为内置组合包。
- 对每个包尝试读取已安装 package.json：优先 profile/node_modules；再 runtime node_modules；读取 version/description/dsh.bundle；不存在则标记“待恢复依赖”。
- 返回 PluginDto {name,version,description,source}；source 用于前端区分“官方/内置/社区/本地”（参考实现：官方前缀 @deepseek-ai/，其余展示来源摘要；保持行为可由前端决定）。
- profile 不存在/未初始化：返回空列表（不 404）。
- 实例必须存在；profile 参数非法 → 400。

### 8.2 变更（安装/更新/移除）
- 前置：实例已停止；单任务空闲；profile 校验；该运行时能力 pluginManagement=true，否则 422/409 可操作错误。
- 命令（经 Node 桥，参考 PluginService.mutate）：
  - node <pluginBridge.mjs> <pnpm.cjs> <dsh-entry> plugin --profile <profile> add|update|remove [<spec>] --config.ignore-scripts=true --config.ignore-pnpmfile=true
  - 工作目录 = instance workspace；环境 = DSH 环境；超时 20min。
  - update 不带 spec（原地更新）；remove 要求完整 npm 包名且必须是 profile 独立依赖（不能移除内置 bundles）；add 接受 npm 包名/npm 坐标/GitHub 坐标/file:/link: 相对路径（锚定到 workspace 绝对路径）。
  - 拒绝以 - 开头或含控制字符的 spec；校验包名长度 ≤214、合法 npm 名。
- Node 桥（pluginBridge）目的：把 DSH 对 pnpm 的 spawn 路由到内置 pnpm（node pnpm.cjs），禁止 shell 转发；若 DSH 新版使用未支持的 shell 命令，fail-closed 报错（不执行）。
- 成功后 Job result 摘要；前端刷新列表；提示重启实例后生效。
- 不做社区目录/检索/安装目标解析。

---

## 9. 运行时/数据目录约定

- data-root 下：config/、instances/、runtimes/、cache/、logs/、trash/。
- 实例目录：instances/<id>/{dsh-home,workspace,agents-home}；实例 id 格式 i-<12hex>。
- 运行时：runtimes/<semver>/；安装临时目录 runtimes/.install-*；删除进 trash。
- 缓存：pnpm-store、npm、registry-metadata、probes、uploads、exports、pack-staging。
- 日志：manager-YYYY-MM-DD.log、audit-YYYY-MM-DD.log；单行、脱敏。
- 端口：3080–65535 自动分配；启动前检查 127.0.0.1:port 不可连接；运行实例上限由 `--pcl.max-running-instances` 控制（默认 0=不限制）。
- 文件权限：data 0700；config/secret 类 0600；临时上传/解包目录 0700。

---

## 10. 参考实现行号索引

- Node/pnpm 查找：RuntimeService.java:161-208
- 安装/pnpm 参数：RuntimeService.java:210-242
- 发行批次 overrides：RuntimeService.java:244-380
- 能力探测：RuntimeService.java:382-444
- 启动命令/环境：RuntimeService.java:446-528
- profile 依赖恢复：RuntimeService.java:530-572
- 进程/token/就绪/停止：ProcessService.java:38-130
- 直连登录：InstanceService.java:456-500（响应 DTO 见 ApiDtos.DirectAccessDto）
- 反代路径/重写：ProxyTargets.java:59-211
- 反代转发：WorkbenchProxyController.java:92-230
- WS 握手：WorkbenchHandshakeInterceptor.java:45-118
- WS 双向转发：WorkbenchWebSocketHandler.java:55-254
- 安全归档：SafeArchive.java:1-153；TarSafety.java:1-83
- 整合包：PackService.java:1-683
- 插件：PluginService.java:1-103；RuntimeService.java:558-603
- Job/SSE：JobManager.java:34-111,183-209,384-444

## 11. 验收提示

- 所有 DSH 交互必须在 WSL 内、对 runtime 副本/独立 data-root 执行；不碰 Windows DSH，不碰参考仓库。
- 真实 DSH 验收至少覆盖：安装 rc.3 → 启动 → 工作台 HTTP/静态/SSE/WS → 直连登录 URL → 停止/端口释放 → 再次启动。
- 任何安全规则（归档/路径/代理）必须有 Go 测试 + 至少一次真实样本验证。
- 若发现 DSH rc.3 与参考实现的差异，记录到 docs/ACCEPTANCE.md 的“已知差异”并在代码中 fail-closed/明确错误，不得静默降级。