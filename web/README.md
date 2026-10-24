# pcl-dsh-web

PCL-DSH-Linux 的 Web 前端（Vue 3 + TypeScript + Vite + Vue Router + Pinia），
用于全量复刻 HDSL 桌面版界面。方案见 `../docs/IMPLEMENTATION_PLAN.md` §5。

## 开发

```bash
npm install
npm run dev        # http://127.0.0.1:5173，/api 与 /workbench 代理到 127.0.0.1:8080
npm run build      # vue-tsc --noEmit && vite build，产物在 dist/
npm run preview    # 预览构建产物
```

需要 Node.js >= 20.19（本机 Node 24 已验证）。

## Playwright E2E（方案 §5.4 核心验收）

E2E 默认自行拉起“一次性后端 + Vite”：后端用 `../pcl-dsh-server/target/pcl-dsh-linux.jar`
配临时数据目录 `pcl-dsh-web/.e2e-data`（首次启动生成初始密码），测试完成后停止进程并删除数据目录，
不会碰仓库里的 data/，也不需要手工启动服务。

```bash
npm install                    # 已含 @playwright/test
npm run e2e:install            # 首次执行：下载 Chromium 到用户缓存（不入仓库）
npm run e2e                    # 自动启动临时后端+Vite，跑全部用例，结束后清理
npm run e2e -- --headed        # 观察模式（等价 npm run e2e:headed）
npm run e2e:report             # 打开 HTML 报告（playwright-report/）
```

前置条件：JDK 21（`JAVA_HOME` 或 `D:/DevelopSoftware/Java/JDK-21`）与已构建的
`pcl-dsh-server/target/pcl-dsh-linux.jar`（`scripts/mvn.ps1 -B -ntp package`）。
E2E 会占用 **8080（后端）与 5173（Vite）**，运行前请停止本机/WSL/其他部署占用这两个端口的服务
（否则临时后端可能启动失败或连到错误的服务；`scripts/e2e-stack.ps1 start` 会做进程级就绪检查并给出日志）。

需要手工控制服务或调试时：

```powershell
pwsh -File scripts/e2e-stack.ps1 start     # 打印初始密码、数据目录与日志位置
pwsh -File scripts/e2e-stack.ps1 status
pwsh -File scripts/e2e-stack.ps1 stop      # 停止并删除 .e2e-data（-KeepData 保留数据）
$env:E2E_EXTERNAL_STACK=1; npm run e2e     # 复用已启动的 stack，测试结束不停止它
```

用例（Chromium、单 worker、登录态 storageState 复用）：

| 文件 | 覆盖 |
| --- | --- |
| `e2e/01-auth.spec.ts` | 未登录跳转 /login、错误密码提示、初始密码登录→强制改密→首页 |
| `e2e/10-home-layout.spec.ts` | 40px 页头、200px 侧栏、7 个导航+当前实例、启动按钮 200x55、960x650 无横向溢出 |
| `e2e/20-instances.spec.ts` | 创建实例校验（名称/版本/端口）、创建成功、选择当前实例、删除确认 |
| `e2e/30-runtimes.spec.ts` | 运行时版本刷新、安装 202+SSE 任务/日志（路由 mock） |
| `e2e/40-settings.spec.ts` | 代理/背景保存并落库、1.5s 轮询不覆盖未保存编辑 |
| `e2e/50-logs.spec.ts` | tail、SSE 实时日志、复制回退、取消任务（202 mock） |
| `e2e/60-about.spec.ts` | GPL 全文与 HMCL 附加条款弹窗 |
| `e2e/70-copy-version.spec.ts` | 复制/改版本 202 Job 刷新选中、旧后端 409 回退只读 |

失败时保留截图/trace（`test-results/`）与 HTML 报告（`playwright-report/`），二者均已在
`pcl-dsh-web/.gitignore` 中忽略；`.e2e-data` 同样忽略，不要提交。

> 注：`.e2e-data` 位于工程目录内，`vite.config.ts` 的 `server.watch.ignored` 已排除它
> （Windows 上 Vite 的文件监视句柄会让后端“移入回收目录”的 rename 失败）。

## 真实工作台联调（可选，不纳入 CI）

`scripts/live-workbench.cjs` 用 Playwright 驱动真实后端做一次端到端工作台冒烟：
登录 → 选择实例 → 未运行时 launch 并轮询 Job → 等待“运行中” → 打开 `/workbench/{id}/`
→ 等待 DSH 外壳 → 收集 WebSocket/console/pageerror → 截图 → 停止实例；失败时输出中文原因与失败截图。

前置：后端已构建并运行（含 `/workbench` 反代）、该实例能真正启动 DSH 运行时（Node/pnpm 可用、网络可达）、
已执行过 `npm run e2e:install`（Chromium）。

```powershell
# 可选：用临时数据目录启动真实后端（与 E2E 相同的 stack；也可连自己的部署）
pwsh -File scripts/e2e-stack.ps1 start

$env:BASE_URL="http://127.0.0.1"        # 默认值
$env:ADMIN_PASSWORD="你的管理员密码"     # 必填；初始密码见 data/initial-password
# $env:NEW_PASSWORD="新密码"            # 仅当首次登录还需强制改密时
# $env:INSTANCE_ID="i-..."              # 可选，默认当前实例或第一个
# $env:SCREENSHOT_PATH="test-results/live-workbench.png"
node scripts/live-workbench.cjs

pwsh -File scripts/e2e-stack.ps1 stop   # 如使用临时 stack
```

其他可选环境变量：`SESSION_STATE`（复用已登录的 Playwright storageState）、`KEEP_RUNNING=1`（保留实例运行）、
`HEADED=1`（显示浏览器）、`BROWSER_CHANNEL=msedge`/`BROWSER_EXECUTABLE`（浏览器选择）、
`WORKBENCH_TIMEOUT_MS`/`READY_TIMEOUT_MS`/`LAUNCH_TIMEOUT_MS`（超时，默认 60s/120s/20min）。

注意：联调会真实安装/启动 DSH 运行时与 Node 进程，耗时取决于网络；运行期间不要同时执行后端打包
（Windows 下 jar 被占用会阻塞 repackage）。该脚本默认不运行，也不属于 `npm run e2e`。

## 结构

```
src/
  api/          client.ts（fetch 封装、CSRF、401、FormData、XHR 上传进度）、types.ts、
                normalize.ts、index.ts（含 packs/artifacts）
  components/   AppShell/PageHeader/SideNav/FooterBar/AppIcon、首页启动区与任务卡、
                BaseModal/ConfirmDialog/InstanceDialog/AccountDialog、TaskProgressStrip
  router/       8 个业务路由 + /login，登录守卫
  stores/       auth / appState / instances / settings / notify / jobs（任务 + SSE）
  styles/       tokens.css（HMCL 蓝调色板与度量）、base/layout/components.css
  utils/        display/backgrounds/errors/validation/clipboard/jobs/download
  views/        Login/Home/Instances/Runtimes/Accounts/Plugins/Packs/Logs/Settings
  assets/       backgrounds/whale-01..05.webp、icons/hdsl.webp（由桌面版 PNG 转 WebP）
e2e/            Playwright 核心验收用例、全局 setup/teardown 与 mock 辅助
scripts/e2e-stack.ps1  一次性后端（临时数据目录）+ Vite 的启动/停止脚本
scripts/live-workbench.cjs  真实 /workbench 联调探针（env 配置，默认不跑）
playwright.config.ts   Chromium、单 worker、失败截图/trace、登录态项目依赖
public/licenses/ GPL-3.0.txt、HMCL-ADDITIONAL-TERMS.md（关于页弹窗读取）
public/favicon.webp 网站图标（由 hdsl.webp 复制）
```

## 视觉与交互约定

- 页头 40px（主色 #5C6BC0）、侧栏 200px、首页启动按钮 200x55、按钮切换箭头 200ms 旋转。
- 正文 13px；字体栈 Microsoft YaHei UI / PingFang SC / Noto Sans CJK SC / Source Han Sans SC。
- 8px 滚动条；页面最小宽度 960px，小于 960px 时横向滚动，不做响应式改造（Q14）。
- 窗口按钮保留位置但 disabled，悬停提示「浏览器不支持窗口操作」。
- 背景图 cover + 右对齐（内置背景），非首页叠加半透明底色，与桌面版一致。
- 弹窗自研轻量组件：焦点陷阱、ESC 关闭、aria-modal，不引入 UI 框架。

## 任务与 SSE（方案 §4.3 / §5.3）

- 提交长任务（启动实例 / 安装运行时 / 探测能力）后，后端返回 202 Job View；
  `stores/jobs.ts` 记录 jobId 并订阅 `GET /api/jobs/{id}/events`。
- 事件：`state` / `log` / `result` / `error`；按 seq 去重，保留最近 4000 行。
- 使用浏览器原生 `EventSource`：断线自动重连时会携带 `Last-Event-ID`，服务端从该 seq 续传；
  连接反复失败时提示并提供「重新连接」。
- jobId 写入 localStorage，页面刷新后通过 `GET /api/jobs/{id}` 恢复快照并重新订阅；
  刷新到已结束任务时会重放日志后关闭连接。
- 任务进行中：首页任务卡显示阶段/已用时/日志尾部，页头与底部状态同步；空闲轮询 1.5s，任务期间降为 3s。
- 取消通过 `POST /api/jobs/{id}/cancel`；日志页支持清空显示、复制（Clipboard API 不可用时弹出手动复制回退）。

## 后端契约与优雅降级

- 认证：`GET /api/auth/me`、`POST /api/auth/login`、`POST /api/auth/logout`、`PUT /api/auth/password`。
- 非 GET 请求从 `XSRF-TOKEN` Cookie 读取并放入 `X-XSRF-TOKEN` 头；所有请求带 `credentials: include`。
- 读取：`GET /api/state`、`/api/instances`、`/api/runtimes`、`/api/runtimes/available`、
  `/api/settings`、`/api/logs/tail?lines=800`、`/api/jobs/{id}`。
- 写入：实例 create/update/delete/selection/account/launch/stop、`GET /api/instances/{id}/web-url`、
  运行时 install/probe/delete、`PUT /api/settings`、`POST /api/jobs/{id}/cancel`（详见方案 §4.2）。
- 账户：`GET /api/accounts/providers`、`GET/POST /api/accounts`、`PUT/DELETE /api/accounts/{id}`
  （编辑时 apiKey 留空=保留；被实例引用时删除返回 409）。
- 插件：`GET /api/instances/{id}/plugins?profile=`、`POST /api/instances/{id}/plugins`、
  `PATCH/DELETE /api/instances/{id}/plugins/{name}`（后三者返回 202 Job，type=PLUGIN_MUTATE）。
- 插件目录：`GET /api/plugin-catalog?q=&page=&refresh=`（响应 entries）、
  `GET /api/plugin-catalog/{owner}/{repo}?refresh=`（targets）；status=INSTALLABLE/VIEW_PROJECT/UNRESOLVED。
- 资源：`POST /api/settings/background`（multipart，返回 `{background:"/api/assets/backgrounds/..."}`，
  再经 `PUT /api/settings` 生效）、`GET /api/assets/backgrounds/{name}`、`GET /api/artifacts/{id}`。
- 整合包：`POST /api/packs/preview`（multipart "file"，200，previewId 30 分钟有效）、
  `POST /api/packs/import` → 202 Job(IMPORT_PACK，result `{instanceId,name}`)、
  `POST /api/packs/export` → 202 Job(EXPORT_PACK，result `{artifactId,filename,size}`)。
- 实例复制/改版本：`POST /api/instances/{id}/copy` → 202 Job(COPY_INSTANCE，result `{instanceId,name}`)；
  `PUT /api/instances/{id}` 按 HTTP 状态区分：200=普通字段已保存，202=版本/profile 变更产生的 COPY_INSTANCE Job
  （原实例保留）；旧后端返回 409 CONFLICT_UNSUPPORTED_CHANGE 时前端回退版本/profile 只读。
- 后端未实现（404/405/501）或不可达时，页面显示明确的「接口尚未实现 / 加载失败」提示与重试按钮，
  绝不伪造实例、账户、日志或任务数据；轮询失败退避到 5 秒并自动恢复。
- 登录失败区分 401（密码错误）、423（限流锁定）、403 `PASSWORD_CHANGE_REQUIRED`（强制修改初始密码）。

## M2 第二增量（已完成）

- 实例：创建/编辑（名称、端口、账户；版本与 profile 只读并提示「下一版本提供」）、删除（确认后移入回收目录，
  展示 trashedPath）、选择当前实例、启动/停止、打开 Web（仅后端确认就绪时可用，新标签页）、状态徽标。
- 运行时：已安装列表与兼容性摘要、可用版本列表（刷新/手输/安装）、检测、移除（引用中 409 有中文提示）。
- 任务：全局 job store、首页任务卡（阶段/已用时/日志尾部）、任务页 SSE 日志、取消、复制回退、刷新恢复。
- 日志页：任务实时日志（仅在底部时自动滚底）、清空显示、复制、取消、重新连接、后端 tail 日志与失败提示。
- 设置：代理/Registry 可编辑并保存（与桌面版一致的中文校验文案）、5 张内置背景选择并保存；
  未保存编辑不会被 1.5s 轮询覆盖；自定义背景上传在接口未实现时明确提示「开发中」。
- 账户：读取 `/api/state` 的 accounts 展示名称/提供商/模型/密钥有无与状态；新增/编辑给出「下一增量提供」提示。

## 本增量（M2 第三增量）完成度

- 账户：完整 CRUD——列表（名称/provider/baseUrl/model/状态、密钥有无）、新增/编辑弹窗（provider 下拉来自
  `/api/accounts/providers`，选择后自动填 baseUrl/model；API Key 密码框，编辑留空保留；关闭即清空）、
  删除确认（被引用 409 中文提示）、绑定当前实例（`PUT /api/instances/{id}/account`，运行中/不存在错误直接展示）。
- 插件：实例下拉、插件列表（版本 + 官方/社区徽标）、添加输入框（npm 包/GitHub/本地 file: 提示）、
  更新与移除（确认后 Job，进度经全局任务条/任务页展示，结束后自动刷新列表）。
- 插件目录（`/runtimes` 页「运行时 / 插件目录」Tab）：搜索/重新获取/分页、项目卡片、
  检查安装目标（展示包名@版本、仓库路径、核对来源链接）、选择实例与目标后安装（Job）；
  未解析/仅可查看项目不提供安装入口，只保留「查看项目」。
- 设置：自定义背景上传（PNG/JPEG ≤20MB → 应用预览 → 保存设置；接口未就绪时明确提示开发中）；
  内置背景仍可用，自定义背景按方案居中显示；关于页 GPL-3.0 全文与 HMCL 附加条款弹窗（文本随 `public/licenses/` 打包）。

## 本增量（M3 整合包增量）完成度

- 整合包页：选择 .dspack/ZIP 后经 XHR 流式上传并显示进度（可取消/重新选择）；预览展示名称、格式、包版本、
  Harness 版本、profile、解压体积与醒目警告；导入表单（实例名称默认包名、Harness 版本可手输且提示精确版本）、
  导入 Job 成功后自动选择新实例并跳转实例页；导出实例下拉+范围选择（web 实例仅 profile）、已停止校验、
  导出 Job 完成后自动经 `/api/artifacts/{id}` 下载并保留“重新下载”入口。
- 实例：复制按钮启用（弹窗输入新名称 → COPY_INSTANCE Job → 刷新并选中新实例）；编辑弹窗的 Harness 版本与
  profile 可编辑（改版本走 COPY_INSTANCE Job、原实例保留），旧后端 409 时自动回退只读并提示。
- 资源：5 张内置背景与图标由桌面版 PNG 转 WebP（背景 53–71 KB/张、图标 155 KB，原合计约 8.7 MB），
  引用、设置页缩略图、首页/登录背景与 favicon 全部更新；构建产物无 PNG 背景。

## 与桌面版的已知偏差

1. 桌面版状态文本位于标题栏；Web 同时提供底部 24px 状态栏（任务/后端连接/版本/许可），更适合浏览器。
2. 窗口拖动/缩放/最大化/关闭按钮置灰；「打开文件夹」动作不提供，实例路径改为只读展示。
3. 自定义背景走服务端上传与 `/api/assets/backgrounds/` 资源地址；上传后需再点“保存设置”写入配置（桌面版为本地文件路径）。
4. 「打开 Web」使用后端返回的地址：本增量后端返回 `http://127.0.0.1:<port>/` 回环地址（反代增量后改为
   `/workbench/{id}/`）；服务器部署在远端时该地址无法直连，需等反代增量。
5. 实例复制在实例卡片上以独立“复制”按钮提供（桌面版在“更多”菜单内）；交互语义一致。
6. 插件页/导出/复制/改版本均要求实例已停止（与后端一致）；插件目录安装需先确认目标。
7. “整个实例”导出要求实例使用非 web 的已初始化 profile（后端限制）；web 实例只能导出当前 profile。
8. 内置背景与图标已转 WebP（保留原尺寸）；桌面版原始 PNG 不再随前端分发，可从上游 HDSL 资源重新生成。
6. 内置背景仍为桌面版原始 PNG（合计约 7.3 MB），WebP/缩略图优化（方案 §5.7）待后续。

## 待办

- Playwright 验收用例（方案 §5.4 的 15 条）与桌面版截图对照。
- 反代增量接入后，将「打开 Web」切换为 `/workbench/{id}/` 并处理远端部署。
- 插件目录分页缓存与更多筛选（M3 打磨）。
- 自定义背景缩略图/WebP 二次压缩与首屏资源监控（当前首屏约 0.4 MB）。
- 打包发布时把 `dist/`、`public/licenses` 与静态资源接入 Spring Boot/Nginx 托管。
