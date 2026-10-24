# PCL-DSH-Go

PCL-DSH-Go 是 [PCL-DSH-Linux](https://github.com/LeuJasYoh/pcl-dsh-linux) 的 Go 重写版：在 Linux 服务器上以 WebUI 管理 DeepSeek Harness（DSH）实例的管理器。

> 状态：**M0 垂直切片 + M1 后端核心 + M2 反代硬化/Nginx + M3 整合包与插件已完成**（2026-10-24，真实 DSH `0.2.1-rc.3` 在 WSL 验收通过）；M4（WebUI）、M5（发布）等待实现。本仓库包含计划、实现规格、DSH 集成契约、前端迁移指南、验收标准、任务拆解与实现提示词。

## 目标

- 按精确版本安装 DSH 运行时，创建/启停 DSH 实例；
- /workbench/{id}/ 反代 DSH 工作台（HTTP / SSE / WebSocket）；
- 任务与日志、整合包导入导出、已装插件管理、直连登录（SSH 隧道 + 一次性 token URL，SSH 用户可配置）；
- 保留 WebUI；不含账户系统、社区插件目录、背景图管理、旧数据兼容；
- 内置 Node 24.22.1 + pnpm 11.28.5，服务器无需系统 Node/npm/JDK。

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