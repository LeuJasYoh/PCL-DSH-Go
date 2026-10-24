# M1 证据索引（docs/evidence/m1/）

> task-21（M1-0）建立；task-19（M1-I 正式契约 + 真实 DSH E2E）与 task-20（M1-J 报告）会在此追加。
> 权威契约：docs/M1_INTERFACES.md、docs/IMPLEMENTATION_SPEC.md §4/§5、docs/ACCEPTANCE.md G1/G2/G3/G5。
> 2026-10-24 Lead 冻结全部契约歧义（矩阵 §7 RESOLVED）；task-19 按冻结口径断言。

| 文件 | 内容 | 生成方式 |
| --- | --- | --- |
| `contract-matrix.md` | M1 端点 × 鉴权/CSRF × 请求 × 成功/失败码 + 错误码表 + G1/G2/G3/G5 追踪 + M1 不实现端点 + **冻结口径（RESOLVED）** | task-21 手工维护（2026-10-24 按 Lead 冻结口径修正） |
| `windows-dsh-baseline-before.txt` | Windows DSH 程序目录只读基线（`find -printf` 清单 sha256；用户 .dsh 仅只读信息） | `bash scripts/wsl/m1-acceptance.sh --baseline` |
| `m1-acceptance-selfcheck.txt` | M1 验收脚本自检输出（`bash -n`、helper/必需输入/脱敏/工具链/Windows 基线函数；33 项全 PASS） | `bash scripts/wsl/m1-acceptance.sh --selfcheck` |
| `m1-acceptance-full-refusal.txt` | 未标记 wiring 时 `--full` 的安全拒绝证据（exit 3，不伪造通过） | `bash scripts/wsl/m1-acceptance.sh --full` |
| `e2e-<ts>.log.txt`（待 task-19 生成） | 正式 M1 契约 + 真实 DSH E2E 证据（脱敏）；失败保留 `/tmp/pcl-dsh-go-test/m1-scenario-<ts>/` | task-19 |

## 脚本与辅助

- `scripts/wsl/m1-acceptance.sh`：M1 验收 harness（fresh data-root 0700、manager 生命周期、0600 cookie jar 且退出必删、脱敏、Job 轮询/API/JSON helper、Windows DSH 基线、失败保留现场；E2E phases 由 task-19 填充）。
- `scripts/wsl/lib/windows-dsh-baseline.sh`：只读 Windows DSH 基线/校验函数（程序目录严格比对；用户 .dsh 因正在运行的 Windows DSH 自身写入而仅作 INFO）。
- 真实 DSH 只在 WSL `/tmp/pcl-dsh-go-test` 独立 data-root；不触碰 Windows DSH、/opt 既有部署与参考仓库。

## 说明

- 证据日志统一 `*.log.txt`（仓库 .gitignore 忽略 `*.log`）；token/密码/会话 cookie 在写入前统一脱敏为 `***`。
- Windows DSH 程序目录基线 hash（2026-10-24）：`d21dbbc5855f4bfdf5e2c6542c872930e3546a239e15b699cde37d1eb0d12351`。
