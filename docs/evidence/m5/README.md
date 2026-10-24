# M5 证据索引（docs/evidence/m5/）

> task-62（M5-C 独立验收脚本）产出；M5 收尾报告 docs/M5_RESULTS.md（task-64）。
> 权威：docs/M5_INTERFACES.md §1–§5、docs/ACCEPTANCE.md G9/G10。

| 文件 | 内容 | 生成方式 |
| --- | --- | --- |
| m5-acceptance-20261024-184616.log.txt（其余运行保留最近一次） | 验收运行证据（脱敏，含 PASS/FAIL/INFO、`EXIT_CODE`） | `bash scripts/wsl/m5-acceptance.sh --selfcheck\|--isolated\|--live\|--full` |
| m5-acceptance-<ts>-manager.log.txt | isolated 模式 release manager 日志（脱敏） | 同上 |

## 脚本模式

- `--selfcheck`：检查契约文档/工具链/release 构建脚本语法/打包 Nginx 模板隔离 `nginx -t`/release 与 live 是否就绪（可先跑）。
- `--isolated`：解包 release（目录或 tar.gz）→ 校验布局/SHA256SUMS/无运行时数据 → 用 release 内置 manager+static+data-root 隔离启动 → health/版本/静态/SPA/穿越/401/initial-password 0600/内置 node/登录（不改密）→ 停进程/日志 ERROR/泄漏扫描。
- `--live`：只读检查实际 WSL 部署（Nginx :80、systemd active/沙箱、SPA/静态/api、健康版本、401/404、security headers、`/opt` current 软链、data/initial-password 权限、Nginx access_log off、日志泄漏/ERROR）。不写配置、不改密码。
- `--full`：selfcheck + isolated + live；release/live 未就绪时 isolated/live 返回 3（PENDING，不是失败）。

## 约束

- 不 commit；不修改用户密码；不碰 live `/opt`、systemd、Nginx 配置（live 仅读）。
- isolated 使用独立临时 prefix/data-root（/tmp/pcl-dsh-go-test/m5/...），完成后清理 secrets/进程。
- token/密码/会话不写入证据；日志脱敏 `***`。