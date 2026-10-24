# WebUI 复用与改造指南（PCL-DSH-Go v1.0）

> 策略：复用参考仓库 pcl-dsh-web（Vue 3 + TS + Vite + Pinia）并瘦身；不重做视觉。复制到本仓库 web/ 后按本文件删除/修改。
> 许可：前端代码是 HDSL/PCL 衍生作品，按 GPL-3.0-or-later + HMCL §7 发布；保留版权/许可声明与“关于/许可”入口。
> 参考仓库只读：D:\Projects\DSHL\PCL-DSH-Linux\pcl-dsh-web；不得修改参考仓库。

---

## 1. 复制范围

建议复制整个前端工程再删除：
- web/package.json、vite.config.ts、tsconfig*.json、index.html、src/、public/、e2e/（删除/调整 tests）
- 不复制 node_modules、dist、playwright-report、test-results
- 更新 package name 为 pcl-dsh-go-web；title/品牌为 PCL-DSH-Go
- 保留 LICENSE/版权声明；如资源来自 HDSL，保留 LICENSES 声明

---

## 2. 页面与导航

保留页面（8 个含登录）：
- 登录/改密（LoginView.vue）
- 首页/启动（HomeView.vue + HomeLaunchPane/HomeTaskCard）
- 实例（InstancesView.vue + InstanceDialog.vue）
- 运行时/下载（RuntimesView.vue；删除其中的插件目录 Tab/区块）
- 整合包（PacksView.vue）
- 已装插件（PluginsView.vue）
- 任务与日志（LogsView.vue）
- 设置（SettingsView.vue；新增直连 SSH 用户，删除背景/外观区块）

删除页面/入口：
- 账户页 AccountsView.vue、账户对话框 AccountDialog.vue、router /accounts、SideNav 的“账户”区与账户项。
- 账户绑定 UI（InstanceDialog 的账户选择；首页/实例页的账户状态/缺失提示）。
- 社区插件目录（RuntimesView 中 catalog 相关组件/状态/请求）。
- 背景上传/切换（SettingsView 中 appearance/background 区块、utils/backgrounds 的切换逻辑；保留一张默认背景图供 AppShell 使用）。
- 直连登录对话框保留（DirectAccessDialog.vue），SSH 用户改为设置值展示/提示。

---

## 3. 文件级清单

| 文件/目录 | 操作 |
| --- | --- |
| src/views/AccountsView.vue | 删除 |
| src/components/AccountDialog.vue | 删除 |
| src/router/index.ts | 删除 accounts 路由；标题/品牌改 PCL-DSH-Go |
| src/components/SideNav.vue | 删除账户分类与账户 nav；保留实例区与通用区；可显示当前实例信息 |
| src/api/index.ts | 删除 accountsApi、catalogApi、settingsApi.uploadBackground；instancesApi 删除 bindAccount/copy；settings 类型增加 directAccessSshUser；directAccess 保留 |
| src/api/types.ts | 删除 AccountSummary/ProviderPreset/AccountInput/Catalog*/BackgroundAsset；InstanceSummary 删除 accountId；AppSettings 增加 directAccessSshUser；其余保留 |
| src/api/normalize.ts | 删除 accounts/catalog/background 归一化；instance/settings 适配新字段；directAccess 保留 |
| src/stores/state.ts | 删除 accounts ref/replaceAccounts；settings 默认值加 directAccessSshUser；state 响应无 accounts |
| src/stores/auth.ts | 基本保留（登录/改密/会话） |
| src/stores/jobs.ts | 保留（SSE/Job 状态机），如字段无变化无需改；检查 Job 类型无账户相关 |
| src/components/InstanceDialog.vue | 删除 props.accounts/accountId/accountChoices/账户表单；版本/profile 改为只读展示（创建时可编辑一次）；提交只带 name/port；删除“改版本”提示/复制 |
| src/views/InstancesView.vue | 删除账户列/绑定操作/复制与改版本入口（本期不做）；保留创建/编辑名称端口/删除/启停/选择 |
| src/views/HomeView.vue | 删除账户状态/账户警告；保留实例选择/启动/任务卡 |
| src/views/RuntimesView.vue | 删除 catalog 相关 import/state/tab/安装目标面板/请求；保留版本查询/安装/探测/删除 |
| src/views/PluginsView.vue | 保留已装插件列表/安装/更新/移除；无目录入口（原本可能无）；文案去掉账户相关 |
| src/views/PacksView.vue | 保留预览/导入/导出/下载；无账户过滤 |
| src/views/LogsView.vue | 保留 |
| src/views/SettingsView.vue | 保留 proxy/registry；新增“直连登录 SSH 用户”输入（默认 root，校验用户名）；删除背景/外观/上传；保留关于/许可 |
| src/components/DirectAccessDialog.vue | 保留；展示 sshUser（来自接口/设置）、tunnelCommand、directUrl、复制；未就绪提示 |
| src/components/AppShell.vue | 保留；背景固定默认（如 whale-01.webp）或纯色；标题/品牌 PCL-DSH-Go |
| src/utils/backgrounds.ts | 如果删除背景切换则删除/极简；保留默认背景常量 |
| src/assets/backgrounds/*.webp | 可只保留 1 张默认；其余可删（注意版权/许可） |
| src/assets/icons/hdsl.webp | 可替换为新图标或保留（注意商标/版权；建议后续替换） |
| src/styles/* | 保留；删账户/背景无关样式可清理 |
| e2e/* | 删除账户/目录/背景用例；保留并调整登录/实例/运行时/整合包/插件/日志/设置；新增“无账户也能启动”“直连对话框展示”用例 |

---

## 4. API/契约适配

- GET /api/state：无 accounts 字段；settings 含 {proxy,registry,directAccessSshUser}；instances 无 accountId。
- POST/PUT /api/instances：请求只允许 {name,version,profile,port}（创建）与 {name,port}（编辑）；不要发送 accountId；编辑 version/profile 会被 409 CONFLICT_UNSUPPORTED_CHANGE，前端 UI 应只读。
- GET /api/instances/{id}/direct-access：字段 {ready,port,directUrl,tunnelCommand,sshHost,sshUser,note}；sshUser 来自设置；展示可直接复制。
- GET/PUT /api/settings：请求/响应包含 directAccessSshUser；校验用户名（[A-Za-z0-9._-]+），默认 root。
- 删除：/api/accounts*、/api/instances/{id}/account、/api/instances/{id}/copy、/api/plugin-catalog*、/api/settings/background、/api/assets/backgrounds/*。
- 错误处理：沿用 reference 的 ApiError/describeApiError；新增错误码需可读中文。
- CSRF/Cookie：沿用 client.ts（XSRF-TOKEN/X-XSRF-TOKEN、JSESSIONID）；不要改。
- 轮询：沿用 state 1.5s/3s 轮询；未登录不轮询（已有逻辑）。
- SSE：沿用 jobs store EventSource 解析；后端事件名/字段必须匹配（见规范 §4.5）。

---

## 5. 登录/安全/首登

- 保留 LoginView：密码登录、登录失败/锁定提示、首登强制改密（403 PASSWORD_CHANGE_REQUIRED 跳转）。
- 登录后 /api/auth/me 返回 csrfToken；client 设置。
- 退出登录清除本地状态/停轮询。
- 不引入账户/Key 管理 UI；提示用户“在 DSH 工作台配置模型/API Key”（可在首页/设置放静态说明）。

---

## 6. 直连登录对话框（保留）

- 触发：实例运行中时，实例页/首页操作菜单“直连登录”。
- 内容：
  - 说明：DSH 网页登录 OAuth 只接受回环地址，远程需 SSH 隧道完成一次登录；
  - 步骤 1：show tunnelCommand + 复制按钮；
  - 步骤 2：show directUrl + 复制按钮（NULL 时禁用并给 note）；
  - sshUser 显示来自设置（默认 root），可在设置页修改；
  - 说明 token 仅本次实例运行有效；凭据保存在服务器实例目录，之后继续用工作台。
- 安全：只有已登录页面可打开；不把 URL 放入页面日志/埋点；复制用 navigator.clipboard，失败回退文本框（参考实现已有）。

---

## 7. E2E/测试调整

- 删除：账户相关 spec/用例；目录/背景相关用例；复制/改版本用例（若删除该功能）。
- 保留/调整：登录+首登改密；未登录跳转；实例创建/编辑名称端口/删除/启动停止；运行时查询/安装（mock/SSE）；整合包预览/导入/导出；插件列表/变更；日志/SSE/取消；设置保存。
- 新增：无账户入口断言；直连对话框字段/复制按钮；设置 SSH 用户保存后接口回显。
- 运行：先前端 build/type-check；E2E 需要后端（Go）运行；WSL 可用。
- 验收：无账户相关网络请求/路由/文案残留；所有页面可用；视觉与参考实现一致（允许品牌/默认背景差异）。

---

## 8. 前端构建

- 复制后 npm ci / npm run build；产物到 dist，由 Nginx/Go 静态资源提供。
- package.json 增加 engines node>=20.19；去掉账户/目录相关无用依赖。
- 保持 vite base 为 /（Nginx 根路径）；/api、/workbench 由 Nginx 反代。
- 保留 Playwright 配置；更新 e2e stack 启动 Go 后端（参考旧的 e2e-stack.ps1 改为调用 Go 二进制）。

---

## 9. 验收口径

- npm run build 通过；vue-tsc --noEmit 通过。
- 页面无账户入口、无社区目录/背景设置入口。
- 直连登录对话框可用。
- Playwright 全绿（调整后的用例）。
- 与后端 API 契约逐项核对（至少手工/curl + E2E）。
- 许可/关于页面可打开 GPL/HMCL 文本；版权声明保留。
