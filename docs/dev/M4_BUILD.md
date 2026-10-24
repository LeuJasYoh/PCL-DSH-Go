# M4 WebUI 构建与 E2E 环境说明（Windows 构建 / WSL 运行）

> owner：backend-core（M4-D）。范围：WebUI 构建工具链与浏览器环境调研，**不包含** `web/src` 迁移。
> 关联：`docs/M4_INTERFACES.md` `5、`docs/UI_MIGRATION.md` `7/`8、`docs/IMPLEMENTATION_SPEC.md` `8。
> 本文所有结论均在开发机（Windows 11 + WSL2 Ubuntu 26.04.1）实测，实测记录见 `11。

## 0. TL;DR

- 构建在 **Windows** 执行：`pwsh -File scripts/frontend-build.ps1`（一键 npm ci + type-check + vite build + dist 校验），产物 `web/dist`（基线 30 文件 / 944,285 B ≈ 0.90 MB）。
- 运行/验收在 **WSL**：Go manager 加 `--server.static-dir=/mnt/d/Projects/DSHL/PCL-DSH-Go/web/dist` 提供静态资源，Windows 浏览器/Playwright 经 WSL localhost 转发访问 `http://127.0.0.1:18080`。
- **E2E 推荐位置：Windows + Playwright `channel: 'msedge'`**（Edge 已装，无需下载浏览器；本机 Playwright 1.55.0 + Edge 155.0.4283.62 实测 headless 可用，Lead 亦独立验证）。
- WSL 内 node/npm 同样可用（Node v24.22.1 / npm 11.19.1，位于 `/opt/pcl-dsh-go/tools/node/bin`），但 WSL 无浏览器，且 `node_modules` 不可跨 OS 复用；因此只用于运行 Go 服务，不用于 E2E。
- `web/node_modules`、`web/dist` 均被 `web/.gitignore` 忽略，不得提交；`package-lock.json` 必须保持不变（脚本会前后比对 SHA-256）。

## 1. 前置条件与环境基线

| 项 | 实测值 | 说明 |
| --- | --- | --- |
| 操作系统 | Windows 11 + WSL2 Ubuntu 26.04.1 LTS (x86_64) | 构建在 Windows，运行在 WSL |
| Node.js | v24.22.1 | `web/package.json` engines 要求 >= 20.19.0 |
| npm | 11.19.1 | Windows 与 WSL 同版本 |
| 前端框架 | Vue 3.5.22 / Vite 7.1.7 / vue-tsc 3.0.6 / TypeScript 5.9.2 | 见 `web/package.json` |
| Playwright | `@playwright/test` 1.55.0 | `npm ci` 已包含，浏览器二进制不自动下载 |
| Edge | 155.0.4283.62 | `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe` |
| npm registry（Windows） | `https://registry.npmmirror.com` | 用户级 npm 配置；脚本不覆盖 registry |
| npm registry（WSL） | `https://registry.npmjs.org/` | 默认配置，与 Windows 不同 |
| WSL 浏览器 | 无（chrome/chromium/edge 均未安装） | 因此 E2E 必须在 Windows 跑 |

安装依赖只需一次 `npm ci`；`node_modules` 已存在时也建议由脚本统一管理（`npm ci` 会先清空再安装，保证与 lock 完全一致）。

## 2. 一键构建脚本：`scripts/frontend-build.ps1`

### 2.1 用法

```powershell
# 完整可复现构建（npm ci + type-check + build + dist 校验）
pwsh -File scripts/frontend-build.ps1

# 本地快速迭代：复用已有 node_modules（缺少 node_modules 时直接失败）
pwsh -File scripts/frontend-build.ps1 -SkipInstall

# 指定其它 web 目录（一般不需要）
pwsh -File scripts/frontend-build.ps1 -WebRoot D:\path\to\web
```

要求 PowerShell 7（`#Requires -Version 7.0`）。脚本从自身位置推导仓库根，可在任意工作目录调用。

### 2.2 执行步骤

1. 检查 `node`/`npm` 存在、Node 主版本 >= 20；打印版本。
2. 检查 `web/package.json`、`web/package-lock.json` 与 `scripts.type-check`/`scripts.build`；记录 lock 的 SHA-256。
3. `npm ci --no-audit --no-fund`（`-SkipInstall` 时跳过；缺 `node_modules` 则报错退出）。
4. 再次计算 lock SHA-256，若变化立即失败（防误提交锁文件改动）。
5. `npm run type-check`（`vue-tsc --noEmit`）。
6. `npm run build`（`vue-tsc --noEmit && vite build`；会再次做类型检查，属预期）。
7. 校验 `web/dist`：
   - 存在且非空；`index.html`、`favicon.webp`、`licenses/GPL-3.0.txt`、`licenses/HMCL-ADDITIONAL-TERMS.md` 存在且非空；
   - `assets/` 至少 1 个 `.js` 与 1 个 `.css`；
   - `index.html` 引用了至少一个 `/assets/...`，且被引用的文件实际存在；
   - dist 总大小在合理区间（>50 KB 且 <50 MB）。
8. 打印 dist 清单（逐文件字节数）、文件数与总大小，输出 `BUILD OK`，退出码 0。

任一步失败：打印 `BUILD FAILED: <中文原因>` 并以 **退出码 1** 结束（CI/脚本可直接用 `$LASTEXITCODE` 判定）。

### 2.3 实测输出摘要（干净 node_modules 场景）

```
==> 安装依赖（npm ci）
added 67 packages in 5s
    npm ci 完成，用时 5.3s
    package-lock.json 未被修改
==> 类型检查（vue-tsc --noEmit）
    类型检查通过，用时 2.6s
==> 生产构建（vite build）
✓ 112 modules transformed ... ✓ built in 1.42s
    vite build 完成，用时 4.5s
==> 校验 web/dist
dist 清单：30 个文件，合计 944,285 字节（0.90 MB）
...
==> BUILD OK
```

- 完整脚本耗时约 **13.5s**（npm 缓存已热的干净 `node_modules` 重装；冷缓存视网络而定）。
- `-SkipInstall` 重跑约 8.3s。
- 失败路径实测：删除 `web/node_modules` 后执行 `-SkipInstall` → `BUILD FAILED: -SkipInstall 需要已有的 web/node_modules；请去掉该参数执行 npm ci`，退出码 1。

### 2.4 关于 npm 警告

`npm ci` 会提示 1 个 install script 未被 npm 11 的 allowScripts 策略覆盖（`esbuild@0.25.12` postinstall）。这是 npm 11 的安全提示；esbuild 的平台二进制由 optional dependency 提供，本项目构建实测正常。若未来出现 esbuild 二进制缺失，可按提示执行 `npm install-scripts approve esbuild` 或 `npm rebuild esbuild`，不要修改 lock 文件。

### 2.5 并行开发期间的预期失败（重要）

M4 期间 `web/src/**` 由 frontend-core（共享 api/types/stores/router）与 frontend-views（其余页面）并行修改，**中间状态的 type-check 可能暂时失败**（典型：旧 Account/Background/Catalog 代码尚未删完、新类型还没接线）。此时构建脚本会按设计以退出码 1 结束并打印具体 TS 错误文件行——这不是构建工具/环境问题，等前端迁移提交后在干净树上重跑即可。Lead/qa 的最终门禁以迁移完成后的树为准；本任务验收使用的是 M4 迁移前的基线提交（见 `11）。

## 3. web/dist 结构与校验基线

基线（参考前端原样导入，M4 迁移前）`web/dist`：

```
dist/
  index.html                         641 B
  favicon.webp                   154,502 B
  assets/
    index-<hash>.js              136,617 B
    index-<hash>.css              29,311 B
    <Page>-<hash>.js / *.css      （按路由懒加载，含 Runtimes/Packs/Plugins/Logs/Settings/Home/Instances/Login 等）
    whale-0[1-5]-<hash>.webp      （背景图资源）
    hdsl-<hash>.webp
  licenses/
    GPL-3.0.txt                   35,149 B
    HMCL-ADDITIONAL-TERMS.md       1,683 B
```

合计 **30 个文件 / 944,285 字节（0.90 MB / 磁盘占用约 0.94 MB）**，与 `docs/M4_INTERFACES.md` `5 的“约 30 文件 0.94MB”一致。

注意：M4 迁移会删除账户/背景/社区目录等页面，最终 dist 的文件数/大小会**变小**（例如不再有 `AccountsView-*.js`、`whale-*.webp` 可能只保留默认背景）。脚本只校验**不变量**（index/资源/许可/大小区间），不绑定具体路由 chunk，因此迁移前后都应通过；若校验失败先看是否漏了许可文件或 `index.html` 资源引用。

## 4. Windows 构建 vs WSL 运行分工

| 阶段 | 平台 | 推荐命令/要点 |
| --- | --- | --- |
| 安装依赖 + 类型检查 + 构建 | **Windows** | `pwsh -File scripts/frontend-build.ps1`；产物 `web/dist` |
| Go manager 运行与静态资源 | **WSL** | `--server.static-dir=/mnt/d/Projects/DSHL/PCL-DSH-Go/web/dist` |
| 浏览器 E2E / 验收 | **Windows** | Playwright `channel: 'msedge'`，baseURL `http://127.0.0.1:18080` |
| 前端 dev server（联调） | **Windows** | `cd web && npm run dev` → `http://127.0.0.1:5173`，Vite 代理到 `127.0.0.1:8080` |
| 生产静态服务（可选） | WSL / Nginx | Nginx root 指 `web/dist`；或 Go 静态服务（M4-C） |

### 4.1 典型全链路（Windows 构建 → WSL 跑 Go → Windows 浏览器/Playwright）

```powershell
# 1) Windows：构建前端
cd D:\Projects\DSHL\PCL-DSH-Go
pwsh -File scripts/frontend-build.ps1

# 2) Windows：交叉编译 Linux manager（若 bin/pcl-dsh-go 已是最新可跳过）
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
go build -trimpath -ldflags "-s -w" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED
```

```bash
# 3) WSL：启动 manager（fresh data-root；静态目录用 /mnt/d 绝对路径）
cd /mnt/d/Projects/DSHL/PCL-DSH-Go
rm -rf /tmp/pcl-dsh-go-m4/data
./bin/pcl-dsh-go \
  --server.address=127.0.0.1 --server.port=18080 \
  --pcl.data-root=/tmp/pcl-dsh-go-m4/data \
  --pcl.tool-root=/opt/pcl-dsh-go/tools \
  --server.static-dir=/mnt/d/Projects/DSHL/PCL-DSH-Go/web/dist
# 首启初始密码文件：/tmp/pcl-dsh-go-m4/data/initial-password（不打印密码）
```

Windows 浏览器/Playwright 访问 `http://127.0.0.1:18080`（WSL2 localhost 转发；可用 `Invoke-WebRequest http://127.0.0.1:18080/api/health` 自检）。若换端口/仓库位置，同步修改 `--server.static-dir` 与 baseURL。

### 4.2 WSL 内 node/npm 可用性（实测）

- 路径：`/opt/pcl-dsh-go/tools/node/bin/{node,npm,npx}`，Node **v24.22.1** / npm **11.19.1**，还有 `corepack`。
- WSL 内 npm registry 为默认 `https://registry.npmjs.org/`（Windows 为 npmmirror），网络策略可能不同。
- WSL 无 Chrome/Chromium/Edge，**不能跑浏览器 E2E**。
- 不建议在 WSL 构建前端：`node_modules` 是平台相关的（Windows 装的 esbuild/rollup 二进制不能给 Linux 用），必须删掉重装；`/mnt/d` 上大量小文件 I/O 明显慢于 Windows 本地盘。若确实需要 WSL 构建：先删除 `web/node_modules`，再在 WSL 执行 `npm ci --no-audit --no-fund && npm run type-check && npm run build`，产物同样写入 `/mnt/d/.../web/dist`（之后 Windows 侧不要再复用该 `node_modules`）。

## 5. 前端 dev server 与 API 代理

`web/vite.config.ts` 已配置：

- `server.host = 127.0.0.1`、`server.port = 5173`、`strictPort = false`（5173 被占用会自动换端口，注意终端输出）。
- 代理（保持浏览器同源，便于共享会话/XSRF Cookie）：
  - `/api` → `http://127.0.0.1:8080`（`changeOrigin: false`）
  - `/workbench` → `http://127.0.0.1:8080`，`ws: true`（SSE/WS 可用）
- `server.watch.ignored` 忽略 `.e2e-data`/`test-results`/`playwright-report`（避免 Windows 上目录句柄导致后端 trash rename 失败）。

开发联调流程：

```powershell
# Windows：前端 dev server（HMR）
cd D:\Projects\DSHL\PCL-DSH-Go\web
npm run dev     # 按终端输出的 http://127.0.0.1:5173/ 访问
```

```bash
# WSL：Go manager 监听 8080（dev 代理目标固定为 8080）
cd /mnt/d/Projects/DSHL/PCL-DSH-Go
./bin/pcl-dsh-go --server.address=127.0.0.1 --server.port=8080 \
  --pcl.data-root=/tmp/pcl-dsh-go-m4/data --pcl.tool-root=/opt/pcl-dsh-go/tools
```

注意：dev 模式不需要 `--server.static-dir`（由 Vite 提供页面）；静态资源/生产形态才用 `4.1 的 18080 链路。Vite 代理目标端口是 8080，若 manager 改用其它端口，需要前端 owner 调整 `vite.config.ts`（当前文件属 frontend-core 写范围）。

## 6. E2E 推荐执行位置与 Playwright 浏览器

**结论：Windows 上执行 Playwright，浏览器用 `channel: 'msedge'`（本机已装 Edge），无需 `npx playwright install`/下载浏览器。**

实测（本机，2026-10-24）：

| 探测 | launch 选项 | 结果 |
| --- | --- | --- |
| Edge channel | `chromium.launch({ channel: 'msedge', headless: true })` | ✅ 成功，`browser.version() = 155.0.4283.62`，打开 `about:blank`、`page.title() = ""`、`1+1 = 2`、截图 4,254 B |
| 默认 chromium | `chromium.launch({ headless: true })` | ✅ 成功，`browser.version() = 140.0.7339.16`（本机已有 Playwright 浏览器缓存），截图 4,253 B |

> Lead 已独立用 Playwright 1.55.0 验证 `channel: 'msedge'` 可用，并确定“Edge channel 优先、不下载浏览器”为项目结论；上表为同机复核记录（临时脚本与截图都在系统 TEMP，未写入仓库）。

E2E 位置与执行（qa-release 负责 `web/e2e/**`、`web/playwright.config.ts`、`scripts/m4-*`）：

- 工作目录：`web/`（`npm run e2e` → `playwright test`）。
- 基线 `playwright.config.ts` 的 `use.baseURL` 是 `http://127.0.0.1:5173`（旧 Java 栈 + Vite dev）。M4 按 `docs/M4_INTERFACES.md` `5 改为 **Go 二进制 + 构建后 dist** 的生产形态：Windows 构建 → WSL 跑 Go（`--server.static-dir`）→ Playwright `baseURL=http://127.0.0.1:18080`；`baseURL` 与 stack 脚本由 qa 更新。
- 浏览器配置建议（qa 在 config 中设置）：`use: { ...devices['Desktop Chrome'], channel: 'msedge' }`（auth/chromium 两个 project 都继承）。
- `E2E_EXTERNAL_STACK=1` 表示复用外部已启动的 stack（参考 `web/e2e/global-setup.ts`）。
- 旧 `web/scripts/e2e-stack.ps1` 是 Java/Vite 版参考实现，qa 改造为启动 Go 二进制 + 使用 `web/dist`，并保留 `.e2e-data/`（gitignored）与 `auth` project/storageState 模式。
- 证据产物：`web/test-results/`、`web/playwright-report/`、`web/.e2e-data/` 均 gitignored；结论写入 `docs/M4_RESULTS.md`（qa）。

## 7. Edge CDP 回退方案（Playwright/Edge channel 不可用时）

优先方案是 Playwright `channel: 'msedge'`。若遇到 Edge 更新/企业策略导致 Playwright channel 启动失败，可用 **Windows Edge + CDP** 做冒烟/截图（参考工作区 `D:\Projects\DSHL\m2-tools\cdp-test.mjs`，该目录是本地工具，不属于 PCL-DSH-Go 仓库、不随包发布）：

```powershell
# 1) 启动 headless Edge 并开放 CDP（独立临时 profile，避免占用日常 Edge）
$edge = 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe'
$profile = Join-Path $env:TEMP 'pcl-dsh-edge-cdp'
Start-Process -FilePath $edge -ArgumentList @(
  '--headless=new', '--disable-gpu', '--remote-debugging-port=9223',
  "--user-data-dir=$profile", 'about:blank'
)
# 2) 确认端点
Invoke-RestMethod http://127.0.0.1:9223/json/version | Format-List Browser, 'WebSocket-Debugger-Url'

# 3) 用 CDP 脚本打开页面、取 title/text 并截图（m2-tools 为工作区参考脚本）
$env:CDP_PORT = '9223'
$env:CDP_URL  = 'http://127.0.0.1:18080/'
$env:CDP_OUT  = (Join-Path $env:TEMP 'm4-edge-cdp.png')
node D:\Projects\DSHL\m2-tools\cdp-test.mjs
# 输出示例：{"ok":true,"title":"...","text":"...","png":"...","bytes":12345,"events":42}
```

- 脚本行为：`GET /json/list` 找到 page target → WebSocket 连接 → `Page.enable/Runtime.enable/Network.enable` → `Page.navigate` → 等 `Page.loadEventFired`（20s 兜底）→ 取 `document.title`/body 文本 → `Page.captureScreenshot` 写 PNG → 关闭。
- 仅用于**冒烟/视觉截图**，不能替代 Playwright 的断言/等待/网络拦截；正式 E2E 仍优先修好 Playwright channel。
- 若 Edge 完全不可用：`cd web && npx playwright install chromium` 后把 project 的 `channel` 去掉（用默认 chromium；本机 `%LOCALAPPDATA%\ms-playwright\chromium-1187` 已有缓存）。
- 用完请结束 Edge 调试进程并清理 `$env:TEMP\pcl-dsh-edge-cdp`（无痕/独立 profile，不影响日常 Edge 配置）。

## 8. 常见问题（Troubleshooting）

| 现象 | 处理 |
| --- | --- |
| `-SkipInstall` 报“需要已有的 web/node_modules” | 去掉参数跑完整 `npm ci`；或先 `cd web && npm ci --no-audit --no-fund` |
| `npm ci` 报 lock 与 package.json 不一致（`EUSAGE`/`can only install with an existing package-lock`） | 由前端 owner（frontend-core）在 web/ 执行 `npm install` 更新 lock 并提交；**不要手工改 lock**，也不要绕过 `npm ci` |
| 脚本报 “package-lock.json 在构建过程中被修改” | 说明有工具改写了 lock（例如 `npm install <pkg>`）。还原 lock（`git checkout -- web/package-lock.json`）后重跑；新增依赖必须走 owner + commit |
| dist 校验失败（缺 `licenses/...`、`favicon.webp`、assets JS/CSS） | 检查 `web/public/` 下许可/图标是否被误删；检查 `index.html` 是否被改坏；`-SkipInstall` 重跑前可先删 `web/dist` |
| Windows 端口 5173/8080 被占用 | Vite `strictPort=false` 会自动换端口（看终端输出并同步 Playwright baseURL）；manager 端口冲突换 `--server.port`，dev 代理仍需前端改 `vite.config.ts` |
| WSL 里访问 `http://127.0.0.1:18080` 不通 | 确认 manager 监听的是 `127.0.0.1`、WSL2 localhost 转发开启；先在 WSL 内 `curl`，再用 Windows `Invoke-WebRequest`；必要时用 `wsl hostname -I` 的 IP 直连验证 |
| Playwright 启动报 “Executable doesn't exist” | 不要下载浏览器：使用 `channel: 'msedge'`；或按 `7 的 chromium 安装/Edge CDP 回退 |
| Playwright 报 Edge 被占用/无法启动 | 关闭手动打开的 Edge 或用独立 `--user-data-dir`；企业策略限制时按 `7 CDP 回退并记录限制 |
| `esbuild`/`rollup` 二进制相关错误 | 删除 `node_modules` 后重新 `npm ci`；必要时 `npm rebuild`；不要跨 Windows/WSL 复用 `node_modules` |
| 构建成功但页面 404/旧页面 | 确认 Go 进程启动参数含 `--server.static-dir` 且指向**绝对路径** `/mnt/d/.../web/dist`；检查 dist 是否刚重建；M4-C 静态服务对 `/assets/missing.js` 返回 404 是预期 |
| `npm run build` 内存/时间偏大 | 正常构建约 5s；若明显变慢先排查是否误把 `node_modules` 打进 dist，或杀毒软件实时扫描 |

## 9. 与仓库其他部分的边界

- 本脚本/文档只负责**构建工具与浏览器环境**；`web/package.json`、`vite.config.ts`、`web/src/**`、`web/e2e/**`、`web/playwright.config.ts` 的所有权见 `docs/M4_INTERFACES.md` `3（frontend-core / frontend-views / qa-release），本任务不修改。
- Go 静态资源服务（`--server.static-dir`）实现属 M4-C（backend-server）；本机代码已包含该 flag（`internal/config/config.go`），Web 路由行为以 `docs/M4_INTERFACES.md` `2 为准。
- M5 打包时把 `web/dist` 内容放入 release `static/`（Nginx 静态根 / Go fallback），构建命令仍用本脚本。

## 10. 快速检查清单（提交/集成前）

```powershell
# 1) 可复现构建与 dist 校验
pwsh -File scripts/frontend-build.ps1
# 2) 锁文件未被改动
git status --short web/package-lock.json   # 应无输出
# 3) dist/node_modules 不被提交
git check-ignore web/dist web/node_modules # 均应输出路径
```

## 11. 实测记录（2026-10-24，Windows 11 + WSL2）

| 时间（+08:00） | 命令/场景 | 结果 |
| --- | --- | --- |
| 17:40 | `pwsh -File scripts/frontend-build.ps1 -SkipInstall`（已有 node_modules） | 退出码 0；8.3s；dist 30 文件 / 944,285 B；lock SHA-256 不变 |
| 17:41 | 删除 `web/node_modules` 后 `-SkipInstall`（失败路径） | 退出码 1；`BUILD FAILED: -SkipInstall 需要已有的 web/node_modules...` |
| 17:41 | 干净 `node_modules` 全量构建 | 退出码 0；13.5s（npm ci 5.3s / 67 packages；type-check 2.6s；vite build 4.5s）；dist 30 文件 / 944,285 B（0.90 MB）；lock SHA-256 `B80338C5E632CDA2F8E0059C9D4B216237FCD1C0FA319AB504B3E284756FA896` 与构建前一致；node_modules 已恢复 |
| 17:37 | Playwright 探测（临时脚本，`NODE_PATH=web/node_modules`） | msedge channel ✅（Edge 155.0.4283.62，headless about:blank，截图 4,254 B）；默认 chromium ✅（140.0.7339.16，4,253 B）；截图在 `%TEMP%\m4-playwright-probe-*.png`，未写入仓库 |
| 17:36 | 环境采集 | Windows Node v24.22.1 / npm 11.19.1；Playwright 1.55.0；Vite 7.1.7 / vue-tsc 3.0.6 / TS 5.9.2；Edge 155.0.4283.62；WSL Ubuntu 26.04.1 x86_64，node v24.22.1 / npm 11.19.1（`/opt/pcl-dsh-go/tools/node/bin`），WSL 无浏览器；Windows npm registry=npmmirror，WSL npm registry=npmjs.org |
| 17:45 | 并行迁移中途再跑 `-SkipInstall`（web/src 已被其他 owner 改动） | 退出码 1；type-check 报旧 Account/Catalog/Background 代码的 TS 错误（如 `AccountsView.vue`、`AccountDialog.vue`、`SettingsView.vue`）；脚本按设计正确非零失败，见 `2.5。迁移完成后需重跑 |

> 备注：当前 `web/dist` 仍是 M4 迁移前的基线产物（仍含 `AccountsView-*.js` 等参考页面 chunk）；M4 迁移完成后文件数/总大小会变化，但脚本校验的不变量应继续通过。
