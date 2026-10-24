# PCL-DSH-Go

PCL-DSH-Go 是 [PCL-DSH-Linux](https://github.com/LeuJasYoh/pcl-dsh-linux) 的 Go 重写版：在 Linux 服务器上以 WebUI 管理 DeepSeek Harness（DSH）实例的管理器。

> 状态：**M0 垂直切片已完成**（2026-10-24，真实 DSH `0.2.1-rc.3` 在 WSL 验收通过）；M1–M5（完整 API/反代硬化/整合包与插件/WebUI/发布）等待实现。本仓库包含计划、实现规格、DSH 集成契约、前端迁移指南、验收标准、任务拆解与实现提示词。

## 目标

- 按精确版本安装 DSH 运行时，创建/启停 DSH 实例；
- /workbench/{id}/ 反代 DSH 工作台（HTTP / SSE / WebSocket）；
- 任务与日志、整合包导入导出、已装插件管理、直连登录（SSH 隧道 + 一次性 token URL，SSH 用户可配置）；
- 保留 WebUI；不含账户系统、社区插件目录、背景图管理、旧数据兼容；
- 内置 Node 24.22.1 + pnpm 11.28.5，服务器无需系统 Node/npm/JDK。

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