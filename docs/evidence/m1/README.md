# M1 证据索引（docs/evidence/m1/）

> task-21（M1-0）建立；task-19（M1-I 正式契约 + 真实 DSH E2E）已执行；task-20（M1-J 报告）见 docs/M1_RESULTS.md。
> 权威契约：docs/M1_INTERFACES.md、docs/IMPLEMENTATION_SPEC.md §4/§5、docs/ACCEPTANCE.md G1/G2/G3/G5。
> 2026-10-24 Lead 冻结全部契约歧义（矩阵 §7 RESOLVED）；task-19 按冻结口径断言。

| 文件 | 内容 | 生成方式 |
| --- | --- | --- |
| `contract-matrix.md` | M1 端点 × 鉴权/CSRF × 请求 × 成功/失败码 + 错误码表 + G1/G2/G3/G5 追踪 + 冻结口径（RESOLVED）+ **task-19 实际验证结果 §9** | task-21 维护，task-19 回填 |
| `m1-acceptance-20261024-152121.log.txt` | **正式 M1 E2E 证据**：--full 209/209 检查 PASS、EXIT_CODE=0（install Job/SSE、launch、workbench HTTP/静态/SSE/WS、direct-access、stop/delete/trash、重启持久化、权限/Windows DSH/参考仓库） | `M1_WIRING_READY=1 bash scripts/wsl/m1-acceptance.sh --full` |
| `m1-acceptance-20261024-152121-manager.log.txt` | 本次 E2E 的 manager 日志（脱敏；0 ERROR、0 token/密码泄漏） | 同上 |
| `windows-dsh-baseline-before.txt` | Windows DSH 程序目录只读基线（find -printf 清单 sha256；用户 .dsh 仅只读 INFO） | `bash scripts/wsl/m1-acceptance.sh --baseline` |
| `m1-acceptance-selfcheck.txt` | 脚本自检（bash -n、helper/phase 定义、契约输入、脱敏、工具链、Windows 基线） | `bash scripts/wsl/m1-acceptance.sh --selfcheck` |
| `m1-acceptance-full-refusal.txt` | 未设置 M1_WIRING_READY=1 时 --full 的安全拒绝（exit 3，不伪造通过） | `bash scripts/wsl/m1-acceptance.sh --full` |

## 脚本与辅助

- `scripts/wsl/m1-acceptance.sh`：M1 验收入口（selfcheck / baseline / full 三模式；fresh data-root 0700、manager 生命周期、0600 cookie jar 退出必删、脱敏、Windows DSH 基线、失败保留现场；EXIT_CODE 写入证据）。
- `scripts/wsl/m1-phases.sh`：--full 的 E2E phase 实现（G1/G2/G3/G4-M1/G5 + 权限/未越界）。
- `scripts/wsl/lib/job-sse.mjs`：Job SSE 客户端（id/event/data、心跳、Last-Event-ID 重放、终态与 monotonic 断言）。
- `scripts/wsl/lib/windows-dsh-baseline.sh`：只读 Windows DSH 基线/校验（程序目录严格比对，用户 .dsh 仅 INFO）。

## 说明

- 真实 DSH 仅在 WSL /tmp/pcl-dsh-go-test 独立 data-root/m1-data 运行；不触碰 Windows DSH、/opt 既有部署与参考仓库。
- 证据统一 *.log.txt（仓库 .gitignore 忽略 *.log）；token/密码/会话 cookie 写入前统一脱敏为 ***。
- Windows DSH 程序目录基线 hash（2026-10-24）：d21dbbc5855f4bfdf5e2c6542c872930e3546a239e15b699cde37d1eb0d12351。
