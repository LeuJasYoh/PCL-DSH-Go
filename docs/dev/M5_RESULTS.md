# PCL-DSH-Go M5 验收报告（发行/部署 G9/G10）

> 状态：**定稿（qa-release 产出，Lead 独立复跑/复核后定稿）**。日期 2026-10-24。
> 部署：WSL Ubuntu + systemd + Nginx :80，release 1.0.0；Windows 访问 http://localhost/。
> 证据：docs/evidence/m5/（QA full 日志 + Lead live 安装/登录证据）。

## 1. 结论摘要

- release：pcl-dsh-go-1.0.0-linux-amd64（VERSION=1.0.0）；最终 tar.gz 69,329,669 B（≈66 MiB），sha256=3c33e7ff77e3215b2a91cdbd3191a8480a856ae469c5e0479c3e4cbd2299af0c（含最终 docs/README；QA 验收使用同版本目录树，校验和/布局等价）。
- 独立验收脚本 scripts/wsl/m5-acceptance.sh --full：**71/71 PASS，0 FAIL，2 INFO，EXIT_CODE=0**（selfcheck 14 + isolated 35 + live 22）。
- isolated：release 目录 → install.sh --no-service 隔离 prefix/data 真实重装 → 内置 manager/static/tools 启动；同版本重装升级 + current 切到 1.0.0-prev 回滚 + 重启，admin.json/initial-password/instances 数据保留；无 live 写入。
- live 只读：:80 首页 200/title PCL-DSH-Go、SPA /instances 200、/api/health version 1.0.0、/api/state 401、/api/accounts 匿名 401（fail-closed；已登录 404 由 M1/M4 覆盖）、/workbench 401、Nginx server_tokens off、systemd active+沙箱、current→releases/1.0.0、data 0700、initial-password 0600、access_log off、日志 0 ERROR/0 token。
- Lead 实测：Windows http://localhost/ 200、登录页截图 docs/evidence/m5/m5-live-login.png、login 204 + /api/state 403 PASSWORD_CHANGE_REQUIRED（首登门槛）；QA 未改用户密码。
- **Lead 独立复核**：修复脚本后用当前 release 目录独立复跑 `scripts/wsl/m5-acceptance.sh --full`：**71/71 PASS，0 FAIL，2 INFO，EXIT_CODE=0**（scenario 184856，selfcheck+isolated+live）；并实测 live `systemctl restart pcl-dsh-go` 后服务 active、health/首页 200、initial-password 哈希不变（同一密码、数据保留）。Windows 侧 Edge 访问 `http://localhost/` 与 `http://127.0.0.1/` 均 200（`/login`、title=登录 · PCL-DSH-Go、0 console error），登录页截图见 §4。
- 未发现未解决 P0/P1；限制见 §5。

## 2. 交付物

| 交付物 | 说明 |
| --- | --- |
| release/pcl-dsh-go-1.0.0-linux-amd64(+tar.gz) | bin/static（dist 26 文件）/tools node+pnpm/packaging(install/uninstall/reset/systemd/nginx)/docs/licenses/VERSION/SHA256SUMS |
| scripts/wsl/m5-acceptance.sh | selfcheck/isolated/live/full 四模式；isolated 用 --no-service 隔离 prefix；live 只读；EXIT_CODE/证据/干净清理 |
| docs/evidence/m5/ | QA full 日志、isolated manager 日志、Lead live 安装日志/登录截图 |

## 3. G9/G10 覆盖映射

| 验收项 | 结果 |
| --- | --- |
| 无系统依赖工具链 | release 内置 Node v24.22.1/pnpm 可执行；isolated manager 用 release tool-root 启动并通过 health/static/API |
| 安装 | install.sh --no-service 隔离真实重装 rc=0、prefix current→releases/1.0.0、tree/static/tools 完整；live systemd active |
| 校验 | release/SHA256SUMS sha256sum -c 通过；tar sha256 与 Lead 一致；包内无 runtime data/secret |
| 登录/首登 | isolated: initial-password 0600 + 登录 204（不改密）；live: Lead 验证 login 204→/api/state 403 PASSWORD_CHANGE_REQUIRED |
| 实例/工作台 | M1–M4 真实 DSH E2E 覆盖；M5 QA 在 live 只读约束下未重复建实例/改密；live /workbench 未登录 401 |
| 升级/回滚 | isolated 同版本重装 + current 切 1.0.0-prev + 重启健康/登录 204；admin.json/initial-password/instances 不变（无第二版本，schema 兼容未覆盖） |
| 持久化 | isolated 重装/回滚/重启后数据保留；live 服务重启未在 QA 阶段执行（用户数据/只读约束） |
| 权限 | isolated/live data 0700、initial-password 0600；systemd NoNewPrivileges/PrivateTmp 生效 |
| 安全 | live manager 日志 0 ERROR/0 token；Nginx access_log off（/api,/workbench）、server_tokens off、日志/证据 0 泄漏；Windows DSH/参考仓库未改 |

## 4. 证据索引

| 文件 | 内容 |
| --- | --- |
| docs/evidence/m5/m5-acceptance-20261024-184616.log.txt | QA --full：71 PASS/0 FAIL/2 INFO/EXIT_CODE=0（脱敏） |
| docs/evidence/m5/m5-acceptance-20261024-184856.log.txt | **Lead 独立复跑 --full**（首轮）：71 PASS/0 FAIL/2 INFO/EXIT_CODE=0 |
| docs/evidence/m5/m5-acceptance-20261024-185644.log.txt | **最终 release 重装后 Lead 复跑 --full**：71 PASS/0 FAIL/2 INFO/EXIT_CODE=0 |
| docs/evidence/m5/m5-acceptance-20261024-184616-manager.log.txt | isolated release manager 日志（脱敏） |
| docs/evidence/m5/m5-live-install.log.txt | Lead live 安装/部署日志 |
| docs/evidence/m5/m5-live-login.png | Lead Windows 访问 http://localhost/ 登录页截图 |

## 5. 已知限制

- live 只读：QA 未登录 live、未改用户密码、未重启服务/升级（用户数据保护）；首登/登录由 Lead 实测。
- 无第二 release 版本：升级/回滚用“同版本重装 + current 切 prev + 重启”验证 current/data 语义，未覆盖跨版本 schema 兼容。
- tar.gz 仅校验 sha256（QA 隔离使用 release 目录树，未重复 tar 解包安装）。
- reset.sh/uninstall.sh 未对 live 执行（只读）；isolated 只验证 install 路径。
- 无系统 Node 的“PATH 移除”极端环境未单测（release 使用内置 tool-root；WSL 仍存在系统 node 路径）。
- TLS/HTTPS、Nginx 高并发/压测、Windows 侧浏览器多版本不在 M5 范围。

## 6. 阶段提交

| commit | 内容 |
| --- | --- |
| 本报告提交 | M5：release 打包（build-release.sh）、install/uninstall/reset、systemd、Nginx site 模板、release/ gitignore；CI workflow + M5_DEPLOY；qa 验收脚本/证据/M5_RESULTS；README M5 状态 |
| 前序 | M4 8f6726c、M3 d074508、M2 9573d9f、M1 7c5ae05、M0 7fcbd17（见各里程碑报告） |


## 7. Lead 复核结论

- **M5 达到退出条件（G9/G10）**：`m5-acceptance.sh --full` **71/71 PASS**（QA + Lead 独立复跑）；release 自包含内置 Node 24.22.1/pnpm 11.28.5 与 static；安装/隔离重装/升级回滚/数据保留/权限/校验和/系统服务/Nginx 链路均验证；live 部署 systemd active、Nginx :80、日志 0 token/0 ERROR。
- **实际交付**：WSL 已安装 release 1.0.0 到 `/opt/pcl-dsh-go/current`，数据 `/var/lib/pcl-dsh-go`（0700），systemd `pcl-dsh-go.service`，Nginx `:80`；用户从 Windows 浏览器访问 **http://localhost/**（备用 http://127.0.0.1/），首次登录用 `/var/lib/pcl-dsh-go/initial-password`，登录后强制改密；随后可在 WebUI 下载 DSH 0.2.1-rc.3、创建并启动实例、打开工作台。
- **发布决定**：版本 1.0.0；最终 release tar.gz 69,329,669 B（含内置 tools + 最终文档），SHA256SUMS/`.sha256` 齐备（sha256 `3c33e7ff…9af0c`）；是否打 Git tag（建议 v1.0.0）由用户决定。
- **接受的决定/限制**：live 验收为只读（不改用户密码/不重启 except Lead 的一次重启持久化验证）；升级/回滚用同版本重装 + current 切换验证（无第二版本跨 schema 场景）；tar 仅做 sha 校验（隔离安装用目录树）；reset/uninstall 未对 live 执行；WSL `http://localhost/` 在浏览器/curl 正常而 .NET Invoke-WebRequest 的 IPv6 首选可能超时（浏览器不受影响，备用 127.0.0.1）。
- **红线复核**：Windows DSH/参考仓库未改；真实 DSH 仅 WSL；证据 0 token/密码泄漏（密码仅在本对话交付路径/值）；GPL/HMCL 许可与版权声明保留。

> 报告结束。M5 证据位于 `docs/evidence/m5/`；发布包位于仓库忽略目录 `release/`（可重新构建）。
## 8. M5.1 增补（2026-10-24，release 1.0.1）

- **版本选择器修复**：创建实例的 Harness 版本从原生 `<input list>`/`<datalist>`（Edge 单击不展开）改为显式下拉（点击展开、37 项可滚动、点选/键盘/手写均可用）；Playwright 全量 **19/19 PASS**（含新增 21-version-picker）。
- **运行实例上限可配置**：`--pcl.max-running-instances`（M5.1 默认 1；**M5.2 起默认 0=不限制**；负数报错）；app 与 API 预检共用上限，409 `CONFLICT_RUNNING_INSTANCE` message 含当前上限；`install.sh --max-running-instances` 写入 systemd `ExecStart`。2C/1.6GiB 基线建议显式设 1。
- **验证**：QA 隔离多实例验收 **35/35 PASS**——limit=2 时两个实例同时运行且 `/workbench` 各 200，第三个 409（含“最多允许同时运行 2 个”），停掉一个后第三个可启动；显式 limit=1 时第二个仍 409（基线语义保留）；测试后进程/data-root 清理。**M5.2 起默认 0（不限制）**，默认场景两个实例可同时启动（见 M5.2 证据）。证据：`docs/evidence/m5/m5.1-acceptance-20261024120146.log.txt`、`m5.1-e2e-run1.log.txt`、`m5.1-version-dropdown.png`、`m5.1-two-instances.png`。
- **最终 live 验收（2026-10-24 20:13）**：release **1.0.1**（final tar sha256 `86c5abb1d520932eeec83794301c1f3341fe8b301509e3392cbc837a329c3258`）重装后 `m5-acceptance.sh --full` **71/71 PASS**（selfcheck 14 + isolated 35 + live 22，EXIT_CODE=0）；为匹配真实用户已首登改密（initial-password 按设计删除），验收脚本将 live 检查修正为“文件 0600 或 admin.json 已初始化且 0600 均 PASS”。证据：`docs/evidence/m5/m5-acceptance-20261024-201351.log.txt`。
- **交付**：release **1.0.1** 已重建并重装 live（数据/管理员密码保留，`/opt/pcl-dsh-go/current -> releases/1.0.1`；systemd 上限=1）；Go 全仓测试全绿（含修复 Windows 端口区间导致的 pack 测试抖动）。