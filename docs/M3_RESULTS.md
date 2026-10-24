# PCL-DSH-Go M3 验收报告（整合包 + 已装插件）

> 状态：**定稿（qa-release 产出、Lead 独立复跑并复核通过，2026-10-24）**。
> 日期：2026-10-24。里程碑：M3（安全归档、dspack v2/v3 + Overture snapshot v1 预览/导入/导出、artifact 下载、已装插件列表/安装/更新/移除）。
> 环境：WSL2 Ubuntu 26.04.1；Windows Go 1.27.3 交叉编译；内置 Node 24.22.1 + pnpm 11.28.5；真实 DSH **0.2.1-rc.3**（复用既有真实安装，软链进 fresh data-root，未重复安装）；隔离 Nginx 1.28.3 仅用于链路 subset（未改系统 /etc/nginx 与 :80）。
> 边界：真实 manager/DSH 仅跑在 WSL `/tmp/pcl-dsh-go-test` 独立 data-root；token/密码/归档内容 0 泄漏；证据统一脱敏 `***`。

## 1. 结论摘要

- **M3 退出判据满足**：正式 E2E `scripts/wsl/m3-acceptance.sh` **124/124 PASS，EXIT_CODE=0**（证据 `docs/evidence/m3/m3-acceptance-20261024-171548.log.txt` + manager 日志）。
- 构建：当前工作树 linux/amd64，`-buildvcs=false`，version 0.3.0-m3，sha256 `baa9d5b31cb20b5b761f5d85440625d3f2653e034cd20402aa058a7a34ca32ff`；manager 127.0.0.1:18080，可选 Nginx 127.0.0.1:8088。
- **G6**：44/44 恶意样本 preview 全部 400 VALIDATION（无 upload 残留/无实例创建）；合法 dspack v2/v3 profile、dshhome(m3home)、Overture v1 预览字段正确；真实实例 export profile → artifact → preview → import → launch → /workbench 200 往返通过；home 负向（web-only 正确 Job FAILED VALIDATION）与正向（dshhome m3home → export home → artifact → re-preview）均通过；失败导入验证回滚到 trash。
- **G7**：独立实例上本地 `file:` 插件 add→list→stop/start→update→remove 全部 Job SUCCEEDED；运行中/忙 409；8 类非法 spec/参数同步 400 VALIDATION 且不创建 Job；内置 bundle/非独立依赖移除 Job FAILED VALIDATION；artifact 401/404 正确。
- 偏差闭环：M3 期间发现并修复插件 spec 校验偏差（task-48/task-49/task-50）——此前 `@bad//name`、`file:../../outside`、`file:/etc/passwd`、`link:../outside` 仅 Job INTERNAL；修复后 8 类统一同步 400 VALIDATION，实例相关 2 类 Job VALIDATION。
- 未发现未解决 P0/P1；已知限制见 §7。

## 2. 环境与版本

| 项 | 值 |
| --- | --- |
| manager | 当前工作树 linux/amd64 重建（version 0.3.0-m3），sha256 `baa9d5b31cb20b5b761f5d85440625d3f2653e034cd20402aa058a7a34ca32ff` |
| 目标监听 | manager 127.0.0.1:18080；隔离 Nginx 127.0.0.1:8088（M2 模板适配；系统 :80 未动） |
| DSH runtime | 0.2.1-rc.3 真实安装复用（`/tmp/pcl-dsh-go-test/e2e-data/runtimes/0.2.1-rc.3` 软链进 fresh data-root；`runtime_reused=yes`） |
| Node/pnpm | 24.22.1 / 11.28.5（随包工具链） |
| 样本 | `testdata/pack/malicious/` 44 个恶意 .dspack；`testdata/pack/valid/` 4 个合法样本；`testdata/pack/import-failure/` 回滚样本；`testdata/plugin/local-test-plugin` + malicious-specs |
| 生成 | `scripts/m3-gen-pack-samples.py` 确定性生成（重跑 SHA256SUMS 一致；无网络/无真实凭据） |

## 3. 交付物

| 交付物 | 说明 |
| --- | --- |
| `testdata/pack/malicious/` | 44 恶意样本（容器/格式 12、路径 19、条目/压缩/Overture/manifest 13）+ cases.json + SHA256SUMS（校验通过） |
| `testdata/pack/valid/` | dspack v2(manifest v4)/profile=web、v3(mv5)/profile=web、v3(mv5)/dshhome=m3home（非 web/headless）、Overture v1 最小样本 + SHA256SUMS |
| `testdata/pack/import-failure/` | 合法 preview、依赖恢复必失败的导入回滚样本 + SHA256SUMS |
| `testdata/plugin/` | local-test-plugin（`@pcl-dsh/m3-test-plugin` 0.1.0，file: 相对 workspace）+ malicious-specs.json（10 类）+ SHA256SUMS |
| `scripts/wsl/m3-acceptance.sh` | M3 真实 E2E（fresh data-root、重建、恶意→合法预览→真实链路 import/export/roundtrip→home 正负→回滚→插件→Nginx subset→安全/平台；失败保留现场；EXIT_CODE） |
| `docs/evidence/m3/contract-matrix.md` | G6/G7 端点/错误码/安全规则矩阵 + 样本↔规则映射 + task-42/50 执行结果与偏差记录 |
| `docs/evidence/m3/m3-acceptance-20261024-171548.log.txt（+manager）` | 正式 E2E：**124/124 PASS，EXIT_CODE=0**（脱敏） |

## 4. G6 整合包证据映射

| 验收项 | 结果 |
| --- | --- |
| 恶意样本 ≥25 全部拒绝 | ✅ 44/44 → 400 VALIDATION 四字段；uploads 无残留、无实例创建；覆盖穿越/绝对路径/UNC/反斜杠/保留名/控制字符/大小写·NFC·文件目录冲突/重复/符号链接/特殊文件/加密/不支持压缩/CRC/压缩炸弹/声明大小超限/metadata 超限/格式版本/Overture 非法 |
| 合法 preview | ✅ dspack v2/v3 profile、dshhome(m3home)、Overture v1 → 200 且 format/version/dshVersion/profiles/unpackedBytes 正确 |
| 合成包 import 语义 | ✅ v3/dshhome import 202→SUCCEEDED 且创建实例；**最小 profile 无依赖，按设计不断言 launch**（已 stop/delete 清理） |
| 真实 export profile | ✅ create web 实例→launch SUCCEEDED（基线）→stop→export profile Job SUCCEEDED→artifact 下载 200（attachment/no-store/大小一致/ZIP 有效/含 dspack marker/无凭据·会话·日志·cache） |
| roundtrip | ✅ 导出包 preview 200→import→launch SUCCEEDED→/workbench 200（title/base）→stop/delete(trash) |
| home 导出负向 | ✅ web-only 实例 → Job FAILED VALIDATION“实例没有可导出的 profile”（参考实现合规行为） |
| home 导出正向 | ✅ 导入 dshhome(m3home) → export home Job SUCCEEDED→artifact→re-preview 200 |
| 失败回滚 | ✅ registry 指向关闭端口 → import Job FAILED INTERNAL；实例目录进 trash、instances store 无残留、既有实例/文件不变 |
| preview 不存在/过期 | ✅ import 未知 previewId → 202 + Job FAILED error.code=NOT_FOUND（实现无同步 preview 查询，矩阵已记录）；TTL 30min 到期未实际等待（见 §7） |
| artifact 安全 | ✅ 未登录 401；未知/过期 404 四字段；no-store/attachment；flow 审计无 token |

## 5. G7 已装插件证据映射

| 验收项 | 结果 |
| --- | --- |
| 列表 | ✅ 独立实例 GET plugins?profile=web 200，与 profile package.json 一致；内置 bundle 标注“内置” |
| 安装 | ✅ 复制 local-test-plugin 到实例 workspace → `file:./local-test-plugin` add→202 Job SUCCEEDED→列表出现 `@pcl-dsh/m3-test-plugin` |
| stop/start 生效 | ✅ stop→launch→stop 后列表仍包含插件 |
| 更新/移除 | ✅ PATCH update（无 spec）、DELETE remove（%40scope%2Fpkg 解码）均 202 Job SUCCEEDED；列表相应变化 |
| 运行中/忙 | ✅ 运行中 POST plugins→409 CONFLICT_INSTANCE_RUNNING；Job 忙→409 CONFLICT_BUSY |
| 非法 spec（修复后） | ✅ 8 类（leading_dash/control_char/spec_too_long/@bad//name/file:../../outside/file:/etc/passwd/link:../outside/invalid_profile）**同步 400 VALIDATION 四字段、不创建 Job**（jobs 数不变） |
| 实例相关负向 | ✅ builtin_bundle_remove、remove_not_installed → 202 后 Job FAILED error.code=VALIDATION（非 INTERNAL） |
| 422 能力 | ⏳ 真实 rc.3 pluginManagement=true，422 路径由 server 单测覆盖，未在 E2E 强制（见 §7） |

## 6. 与 spec/契约的差异与实现决策

1. **import 的 preview 404 语义**：PackService 冻结接口无“预览查询”，未知/过期 previewId 表现为 202 + Job FAILED error.code=NOT_FOUND；已在矩阵与 §4 记录，属后端已知决策。
2. **home 导出 web/headless 排除**：参考实现明确排除 web/headless；web-only 实例 home 导出失败是合规行为（负向断言），非缺陷。
3. **合成最小包不 launch**：最小 profile 无 DSH 依赖，DSH 不提供服务；仅用于 preview/格式与“import 创建实例”覆盖；launch/roundtrip 一律用真实导出包。
4. **插件 spec 校验修复**：task-48（plugin 包 ValidateMutationInput）→task-49（API 同步 400、不建 Job）→task-50（复跑）；E2E 124/124 验证修复后行为。失败 add 曾残留 node_modules，E2E 使用独立实例规避；修复后同步 400 不再进入 CLI。
5. **manager ERROR**：happy-path ERROR=0；负向用例（失败导入/插件 CLI 失败）产生预期 ERROR，最终 unexpected=0（按消息分类断言）。

## 7. 未覆盖 / 已知限制

- **413/2 GiB 上传**：未实际上传 2 GiB 文件（仅矩阵/单测覆盖上限语义）；preview TTL 30min 到期与 artifact TTL 24h 到期未实际等待（用未知/过期 id 覆盖 404 路径）。
- **422 UNSUPPORTED_RUNTIME**：真实 rc.3 支持 pluginManagement，未构造能力缺失 runtime；由 server/plugin 单测覆盖。
- **home 导入 roundtrip**：home 导出→下载→re-preview 已覆盖；未再次 import home 包创建实例（profile roundtrip 已完整覆盖）。
- **Nginx 全链路**：M3 只做 subset（preview/plugins/artifact/health/access_log off）；完整 Nginx 链路在 M2 已验证；M3 新增 multipart 大上传经 Nginx 未压测。
- **并发/压测**：未做多客户端并发 import/plugin；单任务忙 409 已覆盖。
- **WebUI/浏览器**：M4 范围；本里程碑仅 API/真实 CLI/真实 DSH 启动与 /workbench 页面。
- **插件 update 实际内容**：file: 本地包 update 为原地更新（版本未变），未覆盖真实版本升级差异。

## 8. M4 输入

1. **WebUI（G8）**：整合包上传/预览/导入进度、导出与 artifact 下载、插件列表/安装/更新/移除对话框；直接复用 M3 API/Job/SSE 契约与 PluginDto/PackPreviewDto；错误消息中文可操作已具备。
2. **错误映射**：preview 400/413、import Job NOT_FOUND/INTERNAL、插件同步 400 VALIDATION 与 Job VALIDATION 分类需前端区分展示。
3. **模板/部署（M5）**：Nginx 侧 multipart 大文件、artifact 下载路径与 access_log off 已由 M2 模板覆盖；M4 前端静态资源加入同模板。
4. **测试基建**：44 恶意样本 + 合法/回滚/插件夹具可复用为 M4 集成测试 data；`m3-acceptance.sh` 的 setup/清理/证据/脱敏模式可复用。
5. **安全**：继续保证 token/密码/归档内容 0 泄漏；插件 spec 同步校验已就位（前端可直接显示 400 原因）。

## 9. 证据与脚本索引

| 文件 | 内容 |
| --- | --- |
| `docs/evidence/m3/m3-acceptance-20261024-171548.log.txt` | 正式 M3 E2E（QA）：**124/124 PASS，EXIT_CODE=0**（脱敏） |
| `docs/evidence/m3/m3-acceptance-20261024-171945.log.txt`（+manager） | **Lead 独立复跑**（当前工作树重建）：**124/124 PASS，EXIT_CODE=0** |
| `...-manager.log.txt` | 本次 manager 全日志（脱敏） |
| `docs/evidence/m3/contract-matrix.md` | G6/G7 契约矩阵 + 样本映射 + task-42/50 执行结果/偏差闭环 |
| `scripts/wsl/m3-acceptance.sh` | E2E 入口（重建/部署/断言/清理/EXIT_CODE） |
| `scripts/m3-gen-pack-samples.py` | 确定性样本生成器（无网络） |
| `testdata/pack/` / `testdata/plugin/` | 恶意/合法/回滚样本 + 插件夹具 + SHA256SUMS |

## 10. 阶段提交（待 Lead 填写/确认）

| commit | 内容 |
| --- | --- |
| dc682b6 | chore(m3): freeze M3 pack/plugin interfaces and security rules |
| 2c2fbc3 | chore(m3): add gopkg.in/yaml.v3 for Overture profile.yaml parsing |
| 53d5d3c | feat(m3): integration packs and installed plugins（safe/pack/artifact/plugin/api/app/cmd 主体） |
| 本报告提交 | fix(m3): plugin spec synchronous VALIDATION（task-48/49）+ testdata/scripts/evidence/M3_RESULTS/README |

> task-48/49 的代码修复与 QA 样本/脚本/证据/本报告在“本报告提交”中入库；M3 全部提交已推送 origin/main。

## 11. 任务对照（QA 视角）

| 任务 | owner | 状态 | 结果 |
| --- | --- | --- | --- |
| task-38 M3-G 恶意样本 + 矩阵 | qa-release | completed | 44 恶意 + 4 合法 + 回滚/插件夹具 + contract-matrix |
| task-42 M3-K 真实 E2E | qa-release | completed | 121→修复后 **124/124 PASS** |
| task-48 plugin spec 校验（VALIDATION） | runtime-install | completed | 同步校验单一事实来源 |
| task-49 API 同步 400 | backend-server | completed | 8 类同步 400、不建 Job |
| task-50 修复后复跑 + 证据刷新 | qa-release | completed | 124/124 PASS，证据 171548 |
| task-43 M3-L 报告 | qa-release | in_progress | 本文件（待 Lead 复核） |

## 12. Lead 复核结论

- **M3 达到退出条件**：正式 E2E **124/124 PASS**（QA 171548 + **Lead 独立复跑 171945**，均 EXIT_CODE=0）；gofmt/vet/`go test ./...` 20 包全绿；linux/amd64 构建成功；真实 DSH rc.3 与真实插件 CLI 链路均验证。
- **偏差闭环**：插件非法 spec 从 Job INTERNAL 修正为**同步 400 VALIDATION**（task-48 单一校验入口 + task-49 API 前置校验 + task-50 复跑）；实例相关校验保留 Job FAILED VALIDATION。
- **接受的决定/行为**：import 未知 preview 为 202+Job NOT_FOUND（接口无查询）、web-only home 导出 400（参考实现合规）、合成最小包不声明可 launch（仅 preview/格式覆盖）、失败 add 可能残留 node_modules（同步校验后不再进入 CLI，E2E 用独立实例）。
- **未覆盖项如实保留**（§7）：2 GiB 实际上传/413、TTL 到期等待、422 能力缺失 E2E、home 包再次 import、Nginx 大上传压测、并发压测、真实版本升级差异。
- **M4 输入**：整合包上传/预览/导入进度/导出下载、插件管理对话框、错误分类展示（同步 400 vs Job VALIDATION vs NOT_FOUND）；复用 M3 API/Job/SSE/样本/脚本基建；M4 可立即启动（M1–M3 API 已冻结）。
- **红线复核**：Windows DSH 程序树 hash 前后相同、参考仓库 git 基线不变、系统 Nginx/:80 未改、真实 DSH 仅 WSL /tmp、证据 0 token/密码/归档内容泄漏。

> 报告结束。M3 验收证据位于 `docs/evidence/m3/`；token/密码/归档内容已脱敏为 `***`。
