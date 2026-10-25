# 贡献指南

感谢参与 PCL-DSH-Go！本文说明开发环境、PR 流程与贡献约定。项目以 **GPL-3.0-or-later + HMCL 第 7 条附加条款** 发布（见 [LICENSE](LICENSE)、[LICENSES/](LICENSES/)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)）。

## 行为准则

- 尊重其他贡献者，讨论聚焦技术问题。
- 不提交他人隐私数据、真实服务器地址、密码/token、日志中的敏感信息。
- 安全问题请按 [SECURITY.md](SECURITY.md) 私下报告，不要先开公开 issue。

## 环境准备

1. 安装 Go **1.27+**；前端开发另需 Node.js 24.22.1 + npm 11（仅构建/测试）。
2. Linux 运行与真实 DSH 测试建议使用 WSL2 或 Linux 主机；**不要在 Windows 上运行管理器或修改 Windows DSH 数据**。
3. 前端依赖安装与构建：`pwsh -File scripts/frontend-build.ps1`（Windows）或 `cd web && npm ci && npm run type-check && npm run build`。
4. 详细工具链、构建、测试、发布流程见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

## 开发流程

1. **先讨论**：较大功能/接口变更先开 issue 说明动机与方案，避免重复劳动。
2. Fork 并创建分支（建议 `feat/…`、`fix/…`、`docs/…`）。
3. 小步提交，提交信息说明“做了什么/为什么”，引用相关 issue。
4. 提交前运行检查（见下）。
5. 发起 PR，描述变更、测试方式与风险；UI 变更附截图（注意脱敏）。
6. 维护者 review 后合并；发布由维护者统一打 tag/构建 release。

## 提交前检查

```bash
gofmt -l .          # 无输出
go vet ./...
go test ./...       # 全绿
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

- 前端/文档变更：`pwsh -File scripts/frontend-build.ps1` 通过；文档相对链接/图片路径有效。
- 安全相关改动（认证/会话/CSRF/日志脱敏/归档/路径/SSRF）必须附测试，fail-closed。
- 新增 Go 文件首行加 SPDX：`// SPDX-License-Identifier: GPL-3.0-or-later`。
- 项目版本以仓库根 `VERSION` 为准；不要在其他脚本/代码里硬编码展示版本，`web/package.json` 和 `web/package-lock.json` 的项目版本需同步（CI 会检查 package.json）；详情见 [开发与发布](docs/DEVELOPMENT.md#8-版本与发布流程)。
- 不提交 `bin/`、`web/dist`、`release/`、`node_modules`、日志、数据、密钥或测试环境凭据。

## PR 检查清单

- [ ] 变更范围单一、有 issue/动机说明
- [ ] gofmt/go vet/go test（含 Linux 专项测试）通过
- [ ] 前端 type-check/build 通过（如涉及）
- [ ] 安全/权限行为有测试或说明
- [ ] 文档/CHANGELOG 已更新（用户可见行为变更）
- [ ] 未提交构建产物/密钥/日志；未修改 Windows DSH 或参考仓库
- [ ] 保留 GPL/HMCL 许可与第三方版权声明

## 贡献许可声明

提交 PR 即表示你同意：你的贡献以 **GPL-3.0-or-later** 授权并入本项目，并同意项目整体继续附带 HMCL 第 7 条附加条款；你确认有权提交该内容，复制自第三方的代码/资产会保留其版权与许可声明。

## 文档与报告

- 用户文档在 `docs/` 根目录；历史设计资料只放在 `docs/dev/`，不要混入用户文档。
- 真实 DSH 验收证据在 `docs/evidence/`（由维护者/QA 维护，勿放真实凭据）。
- 版本变更时更新根 `VERSION` 与 [docs/CHANGELOG.md](docs/CHANGELOG.md)，项目版本与 DSH 运行时版本相互独立。
