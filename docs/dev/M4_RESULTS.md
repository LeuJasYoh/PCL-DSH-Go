# PCL-DSH-Go M4 验收报告（WebUI / G8）

> 状态：**定稿（qa-release 产出，Lead 独立复跑/复核后定稿）**。日期 2026-10-24。
> 环境：Windows Node 24.22.1 / npm 11.19.1 / Edge 155.0.4283.62（Playwright 1.55.0 channel=msedge）；WSL Go manager 0.3.0-m4 + --server.static-dir=web/dist；fresh data-root /tmp/pcl-dsh-go-test/m4-data；真实 DSH 0.2.1-rc.3 复用。

## 1. 结论摘要

> 收尾复跑（task-59 home-notes 重叠修复后）：重新构建 dist（26 文件/652,427 B，title=PCL-DSH-Go），全量 E2E **18/18 PASS（39.6s）**；home 页面截图与布局 spec 复核无重叠。

- M4 G8 全量 Playwright E2E：18/18 PASS（39.6s，Edge channel）；证据 docs/evidence/m4/m4-e2e-final.log.txt + manager 日志 + 7 张页面截图。
- 构建：scripts/frontend-build.ps1 -SkipInstall 通过 type-check + vite build；dist 26 文件/652,427 B，title=PCL-DSH-Go。
- 覆盖登录/首页/实例/运行时/整合包/插件/任务日志/设置/关于 + 直连对话框；无账户/插件目录/背景/复制版本入口与请求；console error 白名单仅 401 noise；工作台入口触发 web-url/新窗口；直连字段与未就绪提示通过。
- **Lead 独立复核**：当前工作树重建前端（`npm ci` + `vue-tsc` + `vite build`，dist 26 文件/652,427 B，title=PCL-DSH-Go）与 linux/amd64 manager（`-X main.version=0.4.0-m4`）后，独立复跑全量 Playwright E2E：**18/18 PASS（40.9s，Edge channel）**；Lead 浏览器巡检 8 页面（1440x1000）**0 console error、0 账户/目录/背景请求**；发现并修复首页底部提示重叠（task-59，修复后 1100/960 两尺寸 boundingBox 间距 6px、无重叠，见 `docs/evidence/m4/lead-visual/`）。
- 无未解决 P0/P1；packs/plugins/export/SSE 为页面 mock，真实链路由 M3 124/124 证据覆盖。

## 2. 交付物（qa 写范围）

- web/playwright.config.ts：baseURL 127.0.0.1:18080、channel msedge、auth project→storageState、workers=1。
- web/e2e/global-setup.ts / global-teardown.ts：wsl.exe 启 Go manager（fresh data-root、--server.static-dir、工具根、18080）→ health→initial-password→API 登录→改密→auth.json；E2E_EXTERNAL_STACK=1 复用；teardown 清栈。
- web/scripts/e2e-stack.ps1：Go+dist stack start/stop/restart/status、-KeepData、PID/state、日志/健康/端口检查。
- web/e2e/helpers.ts：Go API 适配（runtime 0.2.1-rc.3、selection 实际路由、CSRF、Job/SSE mock）。
- web/e2e/*.spec.ts：auth/home/instances/runtimes/settings/logs/about/packs/plugins/direct-access/evidence；删除 copy-version；无 accounts/catalog/background。
- docs/evidence/m4/：全量 E2E 日志、manager 日志、7 张页面截图（1280x720）。
- docs/M4_BUILD.md + scripts/frontend-build.ps1：可复现构建（npm ci/type-check/build + dist 校验）。
- Go 静态服务（internal/config + internal/server/api/static.go）与 `cmd` 透传 `--server.static-dir`（Lead 接线）；docs/M4_INTERFACES.md 冻结契约。
- docs/evidence/m4/lead-visual/：Lead 独立验收截图（登录页、修复前首页、修复后首页说明）。

## 3. G8 页面覆盖（18 tests / 11 files）

| 页面/功能 | 结果 |
| --- | --- |
| 登录/首登改密 | auth 3/3：未登录跳转且不轮询 state、错误密码中文提示、改密后登录进首页；setup API 改密/storageState |
| 首页 | 6 nav、40px header、200px sidebar、200x55 启动按钮、960 无横向溢出；无 legacy 文案 |
| 实例 | 创建校验、创建成功、选择当前实例、删除确认 |
| 运行时 | available 刷新、安装 202+SSE、任务卡/日志 |
| 设置 | proxy/sshUser 保存+API 校验；未保存编辑不被轮询覆盖 |
| 日志 | tail、SSE 日志、复制回退、cancel-task |
| 关于 | GPL 全文/HMCL 条款弹窗 |
| 整合包 | 合法预览 200、恶意 400 中文错误、导入 202+SSE、导出 202+SSE+下载提示 |
| 插件 | 列表/内置标识、安装 202+SSE、忙 409、非法 spec 400 |
| 直连 | tunnelCommand/directUrl/sshUser 只读字段、未就绪 note |
| 证据巡检 | 7 页截图、console 白名单（401 noise）、无 legacy 请求/文案、工作台入口 web-url/新窗口 |

## 4. 安全 / 边界

- 无 accounts/catalog/background/copy-version 入口、文案与网络请求（98 巡检断言）。
- console 未发现未预期 error（白名单仅 401/资源 noise）；token/密码/会话不入证据（.e2e-data gitignored 且跑完删除）。
- static-dir 由 task-53/Lead 接线；Go 静态服务 SPA/assets/404/穿越/405 由 owner 单测/Lead 实测；E2E 经真实 static-dir 服务。
- Windows DSH/参考仓库/系统 Nginx 未改；端口/进程清理。

## 5. 已知限制

- packs/plugins/export/SSE 页面级 mock（真实 G6/G7 由 M3 E2E 124/124 覆盖）；插件 422 UI 路径未触发（真实 rc.3 支持 pluginManagement，API/单测覆盖）。
- 仅 Edge channel；未覆盖移动端/无障碍/视觉回归；工作台入口只验证 UI 触发（真实 /workbench HTTP/SSE/WS 由 M2 证据）。

## 6. 证据索引

| 文件 | 内容 |
| --- | --- |
| docs/evidence/m4/m4-e2e-final.log.txt | 全量 Playwright：18 passed，RC=0 |
| docs/evidence/m4/m4-e2e-final-manager.log.txt | 本轮 manager 日志（脱敏） |
| docs/evidence/m4/screenshots/*.png | home/instances/runtimes/packs/plugins/logs/settings 截图（1280x720） |
| docs/evidence/m4/lead-visual/*.png | Lead 独立验收：登录页、修复前首页、修复后首页（task-59） |
| docs/M4_BUILD.md | 构建/E2E/浏览器/Edge CDP 回退操作指南 |

## 7. 阶段提交（待 Lead 填写）

| commit | 内容 |
| --- | --- |
| 3ca32cb | chore(m4): import reference Vue frontend baseline and freeze M4 interfaces |
| 本报告提交 | feat(m4): Vue WebUI integration + Go static serving（frontend-core/views、--server.static-dir、构建工具、Playwright harness+E2E、首页提示修复、README/报告） |

## 8. Lead 复核结论

- **M4 达到退出条件**：Playwright E2E **18/18 PASS**（QA 39.6s + **Lead 独立复跑 40.9s**，均 RC=0）；`npm ci`/`vue-tsc --noEmit`/`vite build` 通过；Go `gofmt`/`vet`/`test ./...` 全绿；linux/amd64 构建成功。
- **G8/G5 覆盖**：8 页面 + 直连对话框 + 关于/许可全部可用；无账户/目录/背景/复制版本入口、文案与网络请求（98 巡检）；console error 仅已说明的 401 噪音；直连字段/复制/未就绪提示与 SSH 用户设置通过。
- **偏差/修复**：Lead 视觉巡检发现首页底部两条提示重叠 → task-59 修复（`home-notes` 容器纵向堆叠），修复后 1100/960 与首次启动卡片组合均无重叠；Playwright E2E 复跑不回归。
- **接受的决定/限制**：实例进程日志无 API（LogsView 展示 manager tail + Job SSE，已注明）；packs/plugins 页面交互在 E2E 中以页面级 mock 验证（真实 G6/G7 链路已由 M3 124/124 覆盖）；仅 Edge/桌面尺寸（无移动端/视觉回归）；Go 静态目录为运行期参数（M5 打包时由 install.sh/systemd 传入 release `static/`）。
- **M5 输入**：release 组装 = Go 二进制 + `web/dist`（`static/`）+ vendored Node/pnpm + systemd/install/Nginx；构建脚本 `scripts/frontend-build.ps1` 可复用；Nginx 模板静态根改指 release `static/`；E2E harness 可在干净安装后复用（源改为安装目录）。
- **红线复核**：Windows DSH/参考仓库未改；真实 DSH 仅 WSL `/tmp`；证据 0 token/密码/会话泄漏；GPL/HMCL 许可与版权声明保留（登录页/关于/许可弹窗）。

> 报告结束。M4 证据位于 `docs/evidence/m4/`；前端构建产物 `web/dist` 为 gitignored 构建物，由 M5 打包流程生成。