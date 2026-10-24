# PCL-DSH-Go

PCL-DSH-Go 是 [PCL-DSH-Linux](https://github.com/LeuJasYoh/pcl-dsh-linux) 的 Go 重写版：在 Linux 服务器上以 WebUI 管理 DeepSeek Harness（DSH）实例的管理器。

> 状态：**M0–M5 全部完成**（2026-10-24）：骨架/后端 API/反代/Nginx、整合包与插件、WebUI（Playwright 18/18）、发行打包与 WSL 实际部署（`release 1.0.0`，systemd + Nginx `:80`，`m5-acceptance --full` 71/71）。当前可在 Windows 浏览器访问 **http://localhost/** 使用。本仓库包含计划、实现规格、DSH 集成契约、前端迁移指南、部署指南、验收标准、任务拆解与实现提示词。

## 目标

- 按精确版本安装 DSH 运行时，创建/启停 DSH 实例；
- /workbench/{id}/ 反代 DSH 工作台（HTTP / SSE / WebSocket）；
- 任务与日志、整合包导入导出、已装插件管理、直连登录（SSH 隧道 + 一次性 token URL，SSH 用户可配置）；
- 保留 WebUI；不含账户系统、社区插件目录、背景图管理、旧数据兼容；
- 内置 Node 24.22.1 + pnpm 11.28.5，服务器无需系统 Node/npm/JDK。

## M5 状态（2026-10-24）

M5 发布/部署已完成：**release 1.0.1**（Go 二进制 + `web/dist` + 内置 Node 24.22.1/pnpm 11.28.5 + systemd/Nginx/install 脚本 + 许可），`SHA256SUMS` 校验；`scripts/wsl/m5-acceptance.sh --full` **71/71 PASS**（QA 与 Lead 独立复跑）。

- **WSL 实际部署**：`/opt/pcl-dsh-go/current -> releases/1.0.1`，数据 `/var/lib/pcl-dsh-go`（0700），systemd `pcl-dsh-go.service`（沙箱/NoNewPrivileges，运行上限默认 1），Nginx 监听 `:80`（API/工作台反代、SSE/WS、2G body、access_log off）；Windows 浏览器访问 **http://localhost/**（备用 `http://127.0.0.1/`）。
- **首次登录**：初始密码在 `/var/lib/pcl-dsh-go/initial-password`（0600），登录后强制改密；随后在「下载」页安装 DSH `0.2.1-rc.3`、创建并启动实例、「打开 Web 界面」进入工作台。
- **运维**：安装/升级/回滚/卸载/重置/排障见 [docs/M5_DEPLOY.md](docs/M5_DEPLOY.md)；服务 `systemctl {status,restart,stop} pcl-dsh-go`；Nginx site `/etc/nginx/sites-available/pcl-dsh-go.conf`。
- **构建发布包**：Windows 构建前端（`scripts/frontend-build.ps1`）+ linux/amd64 二进制后，在 WSL 运行 `bash scripts/build-release.sh` → `release/pcl-dsh-go-<version>-linux-amd64.tar.gz` + SHA256（`release/` 已 gitignore）。

### M5.1 增补（2026-10-24）

- 创建实例的 Harness 版本选择从原生 datalist 改为**显式下拉**（点击展开、可滚动、键盘可选、保留手动输入）；Playwright 19/19 PASS。
- 运行实例上限改为可配置：`--pcl.max-running-instances N`（默认 1，`0`=不限制；负数启动报错）；`install.sh --max-running-instances N` 写入 systemd；达到上限返回 409 `CONFLICT_RUNNING_INSTANCE`（message 含当前上限）。多实例真实 E2E：limit=2 两实例同时运行 + 第三个被拒，默认 1 基线保留（35/35 PASS）。
已知限制：本里程碑未覆盖 TLS（由运维在 Nginx 终止）、高并发/压测、跨版本 schema 升级；live 验收为只读（Lead 已做一次 systemd 重启持久化验证）。

## M4 状态（2026-10-24）

M4 WebUI 已完成并通过 **Playwright E2E 18/18 PASS**（Edge 155 channel，Lead 独立复跑 18/18；证据 `docs/evidence/m4/`）：

- `web/`：复用参考 Vue 3 + TS + Vite + Pinia 并瘦身，8 个页面（登录/首页/实例/运行时/整合包/已装插件/任务与日志/设置）+ 直连登录对话框 + 关于/许可；**无账户/社区目录/背景图/复制版本**入口、文案与网络请求；
- Go 静态服务：`--server.static-dir` 提供 SPA fallback、MIME/缓存、404/400/405 与路径穿越防护（未配置时保留占位页）；生产 Nginx 静态根指向同一 `dist`；
- 构建/运行：Windows `pwsh -File scripts/frontend-build.ps1`（npm ci + vue-tsc + vite build，dist 26 文件/≈0.65MB）；WSL 内 manager 加 `--server.static-dir=/mnt/d/Projects/DSHL/PCL-DSH-Go/web/dist` 即可直接访问 WebUI；
- E2E：`cd web && npx playwright test`（Windows + Edge channel，自动启停 Go manager + fresh data-root，证据/截图入 `docs/evidence/m4/`）。

已知限制：实例进程日志无独立 API（页面展示 manager 日志 tail + Job SSE）；整合包/插件页面 E2E 的导入导出与插件变更使用页面级 mock，真实链路由 M3 真实 E2E（124/124）覆盖；仅 Edge 浏览器/桌面尺寸验证。

## M3 状态（2026-10-24）

M3 已完成并通过真实 DSH `0.2.1-rc.3` + 真实 DSH 插件 CLI 验收（正式 E2E **124/124 PASS**，Lead 独立复跑一致；证据 `docs/evidence/m3/`）：

- 安全归档 `internal/safe`：ZIP/TAR 穿越/链接/限值/CRC/压缩比/取消，44 个恶意样本全部拒绝；
- 整合包 `internal/pack`：dspack v2/v3、Overture snapshot v1 预览（30min TTL）、导入（runtime 解析/安全 staging/依赖恢复/外部资源/失败回滚 trash）、导出（profile/home、凭据与缓存黑名单、24h artifact）；
- 已装插件 `internal/plugin`：列表 + 通过 go:embed Node 桥执行 DSH CLI 的安装/更新/移除（pnpm 路由到内置工具链，非法 spec 同步 400）；
- API：`POST /api/packs/preview|import|export`、`GET /api/artifacts/{id}`、`GET/POST/PATCH/DELETE /api/instances/{id}/plugins`，导入/导出/插件变更均为 Job+SSE。

### 一键验收（WSL）

```bash
bash scripts/wsl/m3-acceptance.sh   # fresh data-root + 真实 DSH/真实插件 CLI，约 3-5 分钟
```

生产注意：home 导出要求实例默认 profile 非 web/headless（与参考实现一致）；导入后需能访问 npm registry 恢复 profile 依赖。

## M2 状态（2026-10-24）

M2 已完成并通过真实 DSH `0.2.1-rc.3` + **Nginx 生产链路**验收（Nginx 隔离实例 8088：链路 67/67、真实浏览器 36/36、安全/错误路径 57/57；Lead 独立复跑一致；证据见 `docs/evidence/m2/`）：

- `/workbench` HTTP/SSE/WS 硬化：Origin 同源写保护、动态 hop-by-hop、统一四字段错误体、SSE 不缓冲/断连取消、10MiB 流式 body、WS dial-first（上游 401/404/409/502 → HTTP JSON、子协议透传、1MiB/关闭码）、raw dot-segment 路径 400；
- TrustedProxy：仅回环来源且 `X-Real-IP` 为合法裸 IP 时作为登录限流/审计客户端 IP；
- `packaging/nginx/pcl-dsh-go.conf` + README：静态/SPA、`^~ /api/` 与 `^~ /workbench/`、HTTP/1.1、SSE 不缓冲、大 body 流式、3600s 超时、WS Upgrade、Host 端口保留、access_log off、server_tokens off。

### 在 WSL 验证 M2（不修改系统 Nginx）

```bash
bash scripts/wsl/m2-nginx-e2e.sh     # 真实 manager+DSH 经隔离 Nginx(8088)：67 项
bash scripts/wsl/m2-browser-e2e.sh   # Windows Edge headless CDP 经 Nginx：36 项（截图入证据）
bash scripts/wsl/m2-security-e2e.sh  # SSRF/路径/认证边界/错误体/日志泄漏：57 项
```

生产安装：将 `packaging/nginx/pcl-dsh-go.conf` 放入 Nginx `conf.d/`（manager 保持 127.0.0.1:8080，TLS 由运维在 Nginx 终止）；systemd/install.sh 在 M5 提供。

## M1 状态（2026-10-24）

M1 后端核心已完成并通过真实 DSH `0.2.1-rc.3` 黑盒验收（**209/209 检查 PASS**，证据见 `docs/evidence/m1/`）：
正式 v1 REST API（认证/会话/CSRF/登录限流/首登改密/审计）、实例与运行时 CRUD、Job+SSE（安装/探测/启动，取消/超时/续传）、设置、日志 tail、直连登录后端、`/workbench` 登录门槛。M0 的 dev-mode 调试 API 已删除；整合包/插件（M3）、WebUI（M4）、Nginx/发行包（M2/M5）尚未实现。

### 构建与首次启动（WSL）

```bash
cd /mnt/d/Projects/DSHL/PCL-DSH-Go
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=0.2.0-m1" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
/mnt/d/Projects/DSHL/PCL-DSH-Go/bin/pcl-dsh-go \
  --server.address=127.0.0.1 --server.port=18080 \
  --pcl.data-root=/tmp/pcl-dsh-go-test/m1-data \
  --pcl.tool-root=/opt/pcl-dsh-go/tools
```

首次启动生成 `<data-root>/initial-password`（0600，24 位）：用它 `POST /api/auth/login`，随后必须 `PUT /api/auth/password` 改密；之后所有 `/api/**`（除 health/login/me）需要 `JSESSIONID` 会话 + `XSRF-TOKEN`/`X-XSRF-TOKEN` 双提交。`POST /api/instances/{id}/launch`、`POST /api/runtimes`、`POST /api/runtimes/{version}/probe` 返回 202 Job，事件通过 `GET /api/jobs/{id}/events`（SSE）消费。

### 一键验收

```bash
bash scripts/wsl/m1-acceptance.sh --full   # fresh data-root + 真实安装 rc.3，约 3–4 分钟
```

## M0 状态（2026-10-24）

M0 垂直切片已完成并通过真实 DSH `0.2.1-rc.3` 验收（WSL）：Go 骨架、内置 Node/pnpm 定位、实例 store、进程生命周期（token/就绪/停止/端口释放）、运行时安装/探测、`/workbench` HTTP/SSE/WS 反代、最小管理服务接线。认证/Job/整合包/插件/WebUI 属于 M1–M4，尚未实现。

### 构建（Windows 开发机交叉编译 Linux 二进制）

```bash
cd PCL-DSH-Go
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=0.1.0-m0" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
```

### 在 WSL 中运行 M0（不修改 Windows DSH）

```bash
mkdir -p /tmp/pcl-dsh-go-test/data
/mnt/d/Projects/DSHL/PCL-DSH-Go/bin/pcl-dsh-go \
  --server.address=127.0.0.1 --server.port=18080 --pcl.dev-mode \
  --pcl.data-root=/tmp/pcl-dsh-go-test/data \
  --pcl.tool-root=/opt/pcl-dsh-go/tools
```

M0 调试 API（仅 `--pcl.dev-mode` 启用并绑定 127.0.0.1；M1 由正式认证 API 取代）：

- `GET /api/health`
- `POST /api/m0/runtimes/install` `{"version":"0.2.1-rc.3"}`（真实 pnpm 安装，约 50s / 约 500MB）
- `POST /api/m0/instances` `{"name":"工作实例","version":"0.2.1-rc.3","profile":"web","port":3080}`
- `POST /api/m0/instances/{id}/launch`、`POST /api/m0/instances/{id}/stop`、`GET /api/m0/state`
- 浏览器访问 `http://127.0.0.1:18080/workbench/{id}/` 打开真实 DSH 工作台

### 验收

- `gofmt -l .` 无输出、`go vet ./...`、`go test ./...` 全绿（Linux 专用用例用 `GOOS=linux go test -c` 交叉编译后在 WSL 执行）。
- 真实 DSH 验收：WSL 内执行 `scripts/wsl/m0-acceptance.sh`；证据见 `docs/evidence/m0/`。
- 管理器不在 Windows 上运行；真实 DSH 测试只在 WSL 的 `/tmp` 目录，测试后清理。

## 文档

- [M5 验收报告（发行/部署 G9/G10）](docs/M5_RESULTS.md)
- [M5 部署与运维指南](docs/M5_DEPLOY.md)
- [M5 冻结决策](docs/M5_INTERFACES.md)
- [M4 验收报告（WebUI / G8）](docs/M4_RESULTS.md)
- [M4 构建与运行指南](docs/M4_BUILD.md)
- [M4 冻结决策](docs/M4_INTERFACES.md)
- [M3 验收报告（整合包 + 已装插件）](docs/M3_RESULTS.md)
- [M3 冻结决策](docs/M3_INTERFACES.md)
- [M2 验收报告（反代硬化 + Nginx 生产链路）](docs/M2_RESULTS.md)
- [M2 冻结决策](docs/M2_INTERFACES.md)
- [M1 验收报告（后端 v1 API + 真实 DSH 集成）](docs/M1_RESULTS.md)
- [M0 验收报告（真实 DSH 0.2.1-rc.3）](docs/M0_RESULTS.md)
- [M0 冻结接口](docs/M0_INTERFACES.md)
- [开发计划](docs/DEVELOPMENT_PLAN.md)
- [实现规格](docs/IMPLEMENTATION_SPEC.md)
- [DSH 集成契约](docs/DSH_INTEGRATION.md)
- [前端迁移指南](docs/UI_MIGRATION.md)
- [验收标准](docs/ACCEPTANCE.md)
- [任务拆解（Agent Teams）](docs/TASK_BREAKDOWN.md)
- [给实现 AI 的提示词](docs/AGENT_PROMPT.md)

## 环境与约束

- 开发：Windows + Go 1.27.3；验证：WSL2 Ubuntu 26.04.1（Node 24.22.1、pnpm 11.28.5、Nginx 1.28.3）。
- 部署：Linux x86_64、HTTP + Nginx + systemd、单用户、最多 1 个 DSH 实例。
- **不修改 Windows DSH 任何文件**；真实 DSH 测试只在 WSL 副本/临时数据目录。

## 许可

GPL-3.0-or-later + HMCL 第 7 条附加条款（见 [LICENSE](LICENSE)、[LICENSES](LICENSES/)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)）。
本项目复用 PCL-DSH-Linux / HDSL 的界面与设计，相关版权与许可声明保留。