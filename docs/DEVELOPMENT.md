# 开发与发布

本文面向贡献者，说明 PCL-DSH-Go 当前版本（根目录 `VERSION`）的本地构建、测试、CI 与发布流程。贡献流程与 PR 要求见 [CONTRIBUTING.md](../CONTRIBUTING.md)。

## 1. 工具链

| 项 | 版本/说明 |
| --- | --- |
| Go | **1.27+**（go.mod 声明 `go 1.27.0`） |
| Node.js | 24.22.1（仅前端构建/Playwright 需要） |
| npm | 11.x |
| 浏览器 | Playwright 使用 Windows Edge channel（`channel: 'msedge'`），无需下载浏览器 |
| 系统 | 开发机 Windows + WSL2 Ubuntu（或 Linux）；管理器运行验证只在 WSL/Linux |

## 2. 仓库结构

```text
cmd/pcl-dsh-go/        # 管理器入口（flags → 日志/data-root/锁 → HTTP server）
internal/config/       # 启动参数、data-root、flock
internal/logging/      # slog/审计/脱敏/日志 tail
internal/tools/        # 内置 Node/pnpm 定位与版本探测
internal/store/        # manager/instances/admin JSON、原子写、trash
internal/process/      # 实例进程管理（进程组/就绪/token/停止）
internal/runtime/      # DSH 版本枚举、pnpm 安装、探测、启动 spec
internal/app/          # 编排层（实例/运行时/设置/并发上限）
internal/server/       # HTTP 服务接线/静态/优雅停机
internal/server/api/   # REST API/中间件/DTO
internal/server/security/ # 会话/CSRF/限流/改密/审计
internal/server/job/   # Job + SSE
internal/server/proxy/ # /workbench 反代 HTTP/SSE/WS
internal/pack/         # dspack/Overture 预览/导入/导出
internal/plugin/       # 已装插件管理（Node 桥）
internal/safe/         # ZIP/TAR 安全归档
internal/artifact/     # artifact 存储与下载
internal/directaccess/ # SSH 隧道/一次性 URL
internal/passhash/     # 密码 8–256 字符、版本化哈希与旧 BCrypt 校验
internal/profile/      # Unicode Profile 名称规则
internal/ports/        # 实例端口范围
internal/protocol/     # 管理器 Cookie/CSRF 名称
internal/apierr/       # 统一错误模型
VERSION                # 项目版本权威来源
web/                   # Vue 3 + TypeScript + Vite WebUI
packaging/             # install/uninstall/reset、systemd、Nginx 模板
scripts/               # 构建/验收脚本（frontend-build.ps1、build-release.sh、wsl/…）
docs/                  # 用户文档；历史设计归档在 docs/dev/
.github/workflows/     # CI
```

## 3. 构建后端

```bash
# Linux / WSL 本机
VERSION="$(cat VERSION)"
go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
go test ./...

# Windows 开发机交叉编译 Linux 二进制
VERSION="$(cat VERSION)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
```

`main.version` 注入进程版本，会出现在 `/api/health`、日志和 WebUI；缺省为 `dev`。构建后可用 `bin/pcl-dsh-go --version` 核对注入结果。不要在 Windows 上运行管理器进程（只做编译/测试/浏览器）。

## 4. 构建前端

```powershell
pwsh -File scripts/frontend-build.ps1            # npm ci + type-check + vite build + dist 校验
pwsh -File scripts/frontend-build.ps1 -SkipInstall  # 复用已有 node_modules（快速迭代）
```

产物在 `web/dist`。前端开发：`cd web && npm run dev`（Vite 127.0.0.1:5173，代理 `/api`、`/workbench` 到 127.0.0.1:8080）。Windows 构建、WSL 运行的分工与代理说明见 [dev/README.md](dev/README.md) 中的历史环境说明（命令仍可参考）。

## 5. 测试

```bash
# Go：格式化/静态检查/单测（Linux 专用 build-tag 测试在 Windows 只编译不运行）
gofmt -l .        # 应无输出
go vet ./...
go test ./...

# Go Linux 测试：交叉编译测试二进制后在 WSL 运行
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o bin/tests/pkg.test ./internal/safe
wsl -d Ubuntu -- /mnt/d/Projects/DSHL/PCL-DSH-Go/bin/tests/pkg.test
```

- 仓库内 `scripts/wsl/` 提供本地验收脚本（安装/反代/安全/整合包/部署等），需在 WSL 内以隔离临时目录运行。
- 前端 E2E：`cd web && npm run e2e`（Playwright 1.55，Windows Edge channel；需后端运行，环境说明见 [dev/README.md](dev/README.md) 与 qa 验收脚本）。
- 测试不得连接/修改真实 Windows DSH 或参考仓库；真实 DSH 测试只用 WSL 临时 data-root，结束后清理。

## 6. CI

`.github/workflows/ci.yml` 在 push/PR 到 `main` 时运行：

| job | 内容 |
| --- | --- |
| backend | gofmt / go vet / go test / GOOS=linux 构建 + ELF 校验，上传 manager artifact |
| frontend | Node 24 npm ci / type-check / vite build，校验 dist，上传 web dist |
| release-smoke | 用极小假 tools 跑 `scripts/build-release.sh`，校验发行包布局、安装脚本 help、SHA256SUMS 与 tar.gz，上传 release artifact |

本地等价命令见第 3/4/7 节。真实安装/E2E 不在 GitHub Hosted Runner 上运行（需要 root/systemd/Nginx/WSL/Edge/DSH）。

## 7. 构建发布包

```bash
# 1) Linux/amd64 二进制（版本注入）
VERSION="$(cat VERSION)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" -o bin/pcl-dsh-go ./cmd/pcl-dsh-go
# 2) 构建前端 web/dist
# 3) 打包（需 Linux/WSL；tools 默认 /opt/pcl-dsh-go/tools；版本默认读取根 VERSION）
bash scripts/build-release.sh \
  --tools /opt/pcl-dsh-go/tools \
  --out release
```

脚本会组装 `bin/static/tools/packaging/docs/README/LICENSE/LICENSES`、生成包内 `SHA256SUMS` 与 `<name>.tar.gz(.sha256)`，并打印文件数/大小/校验和。发布前确认 README/CHANGELOG/版本号一致，并在干净 WSL 环境按 [DEPLOYMENT.md](DEPLOYMENT.md) 安装验证。

## 8. 版本与发布流程

- **项目版本**（如 `0.2.1`）独立于 DSH 运行时版本（如 `0.2.1-rc.3`），两者不是同一版本体系。
- 仓库根 `VERSION` 是项目版本的权威来源；CI 与文档构建命令读取它，再通过 `-X main.version` 写入二进制。`--version`、API 和 WebUI 展示注入值，程序运行时不读取源码文件，直接 `go build` 未注入时显示 `dev`。
- 版本变更时更新根 `VERSION` 与 `docs/CHANGELOG.md`，同步 `web/package.json` 和 `web/package-lock.json` 的项目版本（不是依赖版本；CI 会检查 package.json）。前端展示以后端返回值为准，npm 元数据目前仍需同步维护。
- `scripts/build-release.sh` 默认读取根 `VERSION`，保留 `--version <ver>` 显式覆盖；覆盖时二进制也必须注入同一个版本，脚本会拒绝不一致的包。
- 日常提交、推送不等于发版；本轮变更保留在 `Unreleased`，正式发行时再确定版本号、创建 tag 并生成发布包。
- 发布包内置 Node 24.22.1 + pnpm 11.28.5（`tools/`），目标机不依赖系统 Node/npm；发布产物为 Linux amd64 release tar.gz + sha256，发布说明引用 CHANGELOG。
- 发版前检查：CI 全绿、`scripts/frontend-build.ps1` 成功、release 构建成功、干净 WSL 安装/升级/回滚验收通过。
- Windows 交叉构建：Linux 二进制可用 `GOOS=linux GOARCH=amd64` 构建，并以同一 `-X main.version` 生成 Windows 检查二进制验证版本；`build-release.sh` 可设 `PCL_DSH_CROSS_BUILD=1` 跳过 ELF 执行校验并对 NTFS 文件强制可执行归档模式。正式发布仍建议在 WSL/Linux 重跑脚本与安装验收。
- 不要提交 `bin/`、`web/dist`、`release/`、`node_modules` 等构建产物。

## 9. 代码与文档规范

- Go：gofmt、go vet、每包单测；跨包接口变更需同步测试与文档；文件首行 SPDX：`// SPDX-License-Identifier: GPL-3.0-or-later`。
- 安全：认证/会话/CSRF/日志脱敏/归档/路径/SSRF 相关改动必须有测试，并优先 fail-closed。
- 前端：TypeScript 严格模式、`npm run type-check` 通过；共享样式/API/Store 改动先沟通。
- 文档：用户文档（`docs/` 根）保持面向用户；历史设计/里程碑资料放 `docs/dev/`，不要混入当前说明。新增文档需检查相对链接与图片路径。
- 许可：新增文件保留 GPL-3.0-or-later + HMCL 附加条款声明；复制第三方内容需保留其版权声明并更新 `THIRD_PARTY_NOTICES.md`。

## 10. 红线约束

- **禁止修改/运行 Windows DSH**：不读写 D:\DevelopSoftware\DSH\**、用户 .dsh 目录或 Windows DSH 管理的实例/配置/进程。
- **参考仓库只读**：不修改 PCL-DSH-Linux 等参考实现；可复制内容需遵守 GPL 并在提交中说明。
- 真实 DSH 测试只在 WSL/Linux 临时目录，测试后清理；不得在 Windows 上常驻运行管理器。
- 不提交密钥/密码/日志/数据/构建产物；不在日志或文档中粘贴真实 token。
- 不绕过安全校验（归档/路径/代理/认证）或在生产默认开启调试接口。
