# pcl-dsh-go-web

PCL-DSH-Go 的 Web 管理界面（Vue 3 + TypeScript + Vite + Vue Router + Pinia），
由参考前端 pcl-dsh-web 瘦身后接入 Go 管理器 API（契约见 ../docs/M4_INTERFACES.md）。

## 页面

登录/改密、启动（首页）、实例、下载（运行时）、整合包、已装插件、任务与日志、设置，
以及实例页/首页的直连登录对话框。M4 已删除账户/绑定、社区插件目录、背景上传/切换、
实例复制与改版本入口（版本/profile 只在创建实例时可编辑）。

## 开发

    npm install
    npm run dev        # http://127.0.0.1:5173，/api 与 /workbench 代理到 Go 管理器 127.0.0.1:8080
    npm run type-check # vue-tsc --noEmit
    npm run build      # vue-tsc --noEmit && vite build，产物在 dist/
    npm run preview    # 预览构建产物

需要 Node.js >= 20.19（Windows 上 Node 24 已验证）。Go 管理器默认监听
--server.port=8080；也可以把 dist/ 交给 Nginx 或 Go 静态服务（--server.static-dir）托管。

## 结构

    src/
      api/          client.ts（fetch/XHR、CSRF、401、上传进度）、types.ts、normalize.ts、index.ts
      components/   AppShell/PageHeader/SideNav/FooterBar、首页启动区与任务卡、公共弹窗/徽标
      router/       7 个业务路由 + /login，登录守卫
      stores/       auth / appState / instances / settings / notify / jobs（任务 + SSE）
      styles/       tokens/base/layout/components
      utils/        display/backgrounds/errors/validation/clipboard/jobs/download
      views/        Login/Home/Instances/Runtimes/Packs/Plugins/Logs/Settings
      assets/       backgrounds/whale-01.webp（固定默认背景）、icons/hdsl.webp
    public/licenses/ GPL-3.0.txt、HMCL-ADDITIONAL-TERMS.md（设置页关于/许可弹窗读取）

## API 契约要点

- 认证：GET /api/auth/me、POST /api/auth/login、POST /api/auth/logout、PUT /api/auth/password；
  非 GET 请求从 XSRF-TOKEN Cookie 读取并放入 X-XSRF-TOKEN 头，所有请求 credentials: include。
- 状态：GET /api/state（instances/runtimes/status/settings/availableVersions/warnings，无 accounts）；
  空闲 1.5s 轮询，任务期间 3s，未登录不轮询。
- 实例：创建 {name,version,profile,port}；编辑只发 {name?,port?}（version/profile 不可变，
  发送会被后端以 409 CONFLICT_UNSUPPORTED_CHANGE 拒绝）；launch/stop/web-url/direct-access。
- Job/SSE：202 Job View 后订阅 GET /api/jobs/{id}/events（state/log/result/error，
  EventSource 自动携带 Last-Event-ID），任务 id 存入 localStorage 以便刷新恢复。
- 整合包：POST /api/packs/preview（multipart file，XHR 进度）、POST /api/packs/import、
  POST /api/packs/export，下载走 GET /api/artifacts/{id}。
- 插件：GET /api/instances/{id}/plugins?profile=、POST（{spec}）、PATCH、DELETE（后三者返回 202 Job）。
- 设置：{proxy,registry,directAccessSshUser}；错误统一 {code,message,details,traceId}（中文 message）。

## 许可

界面改编自 Hello Minecraft! Launcher（版权所有 © 2013-2026 huangyuhui 及贡献者），
按 GPL-3.0-or-later + HMCL 附加条款发布；相关文本随 public/licenses/ 打包。
