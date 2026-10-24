# PCL-DSH-Go M3 测试矩阵（G6 整合包 / G7 已装插件）

> 状态：**task-38 产出（qa-release），待 Lead 复核；task-42 E2E 实施对象**。
> 权威：docs/M3_INTERFACES.md、docs/IMPLEMENTATION_SPEC.md §4.2/§4.3/§4.4/§5、docs/DSH_INTEGRATION.md §7/§8、docs/ACCEPTANCE.md G6/G7。
> 样本/夹具：`testdata/pack/`（由 `scripts/m3-gen-pack-samples.py` 确定性生成，无网络/无真实凭据）；插件夹具：`testdata/plugin/`。
> E2E 脚本/证据：task-42 `scripts/wsl/m3-acceptance.sh` + `docs/evidence/m3/*.log.txt`（脱敏）。

## 1. 样本清单

### 1.1 合法样本（preview 应 200 且字段正确）

| 文件 | 格式/版本 | 关键期望 |
| --- | --- | --- |
| `valid/dspack-v2-profile.dspack` | dspack v2 / manifestVersion 4 / type=profile | packPreviewDto: format=dspack, version=2/4, profiles=[default], dshVersion=0.2.1-rc.3, unpackedBytes>0 |
| `valid/dspack-v3-profile.dspack` | dspack v3 / manifestVersion 5 / type=profile | 同上（version=3/5），name=sample-v3 |
| `valid/dspack-v3-dshhome.dspack` | dspack v3 / manifestVersion 5 / type=dshhome | profiles=[m3home]（非 web/headless），defaultProfile=m3home；import→export home→re-preview 均 200 |
| `valid/overture-v1-minimal.dspack` | dsh-snapshot.json format=1 | format=overture、profiles=[demo]、dshVersion=0.2.1-rc.3 |

### 1.2 恶意样本（45 类，preview 应 400 VALIDATION 中文可操作、无落盘/无覆盖）

**容器 / 格式（01-13）**

| # | 样本 | class | 违反规则 |
| --- | --- | --- | --- |
| 01 | `01_not_zip.dspack` | not_zip | §7.1 必须 ZIP 容器 |
| 02 | `02_truncated_zip.dspack` | truncated_zip | §7.2 容器损坏 |
| 03 | `03_missing_dspack_marker.dspack` | missing_marker | §7.1 缺少 dspack.json |
| 04 | `04_missing_manifest.dspack` | missing_manifest | §7.1 dspack 需 manifest.json |
| 05 | `05_conflicting_markers.dspack` | conflicting_markers | §7.1 恰好一个标记/冲突 |
| 06 | `06_dsh_pack_yaml.dspack` | dsh_pack_yaml | §7.1 YAML 清单明确拒绝 |
| 07 | `07_unsupported_container_version.dspack` | unsupported_container_version | §7.1 version 2/3 |
| 08 | `08_unsupported_manifest_version.dspack` | unsupported_manifest_version | §7.1 manifestVersion 4/5 |
| 10 | `10_dshhome_with_v2_container.dspack` | dshhome_v2 | §7.1 dshhome 需 v3+mv5 |
| 11 | `11_empty_zip.dspack` | empty_zip | §7.1 缺标记 |
| 12 | `12_invalid_manifest_json.dspack` | invalid_manifest_json | §7.1 manifest 非 JSON |
| 13 | `13_manifest_metadata_too_large.dspack` | metadata_too_large | §7.2 metadata ≤ 4 MiB |

**路径安全（14-32）**

| # | 样本 | class | 违反规则 |
| --- | --- | --- | --- |
| 14 | `14_zip_slip_parent.dspack` | zip_slip_parent | §7.2 禁止 .. |
| 15 | `15_zip_slip_deep.dspack` | zip_slip_deep | §7.2 禁止 .. |
| 16 | `16_absolute_unix.dspack` | absolute_unix | §7.2 必须相对 |
| 17 | `17_absolute_windows.dspack` | absolute_windows | §7.2 盘符/绝对路径 |
| 18 | `18_unc_path.dspack` | unc_path | §7.2 UNC 拒绝 |
| 19 | `19_backslash_path.dspack` | backslash_path | §7.2 反斜杠拒绝 |
| 20 | `20_windows_reserved.dspack` | windows_reserved | §7.2 CON/NUL/COM1-9/LPT1-9 |
| 21 | `21_trailing_dot.dspack` | trailing_dot | §7.2 尾随点 |
| 22 | `22_trailing_space.dspack` | trailing_space | §7.2 尾随空格 |
| 23 | `23_colon_ads.dspack` | colon_ads | §7.2 冒号/ADS |
| 24 | `24_control_char_name.dspack` | control_char_name | §7.2 控制字符 |
| 25 | `25_empty_segment.dspack` | empty_segment | §7.2 空段 |
| 26 | `26_dot_segment.dspack` | dot_segment | §7.2 . 段 |
| 27 | `27_path_too_long.dspack` | path_too_long | §7.2 路径 ≤1000 |
| 28 | `28_forbidden_char.dspack` | forbidden_char | §7.2 <>:"|?* |
| 29 | `29_case_collision.dspack` | case_collision | §7.2 大小写冲突 |
| 30 | `30_nfc_collision.dspack` | nfc_collision | §7.2 NFC 归一冲突 |
| 31 | `31_file_dir_collision.dspack` | file_dir_collision | §7.2 文件/目录冲突 |
| 32 | `32_duplicate_path.dspack` | duplicate_path | §7.2 重复路径 |

**条目/压缩/Overture/manifest（33-46）**

| # | 样本 | class | 违反规则 |
| --- | --- | --- | --- |
| 33 | `33_symlink_entry.dspack` | symlink_entry | §7.2 符号链接拒绝 |
| 34 | `34_special_file.dspack` | special_file | §7.2 特殊文件拒绝 |
| 35 | `35_encrypted_zip.dspack` | encrypted_zip | §7.2 加密 ZIP 拒绝 |
| 36 | `36_unsupported_compression.dspack` | unsupported_compression | §7.2 不支持压缩拒绝 |
| 37 | `37_corrupt_crc.dspack` | corrupt_crc | §7.2 CRC 不一致 |
| 38 | `38_compression_bomb.dspack` | compression_bomb | §7.2 压缩率 >1000（>1 MiB） |
| 39 | `39_declared_file_too_large.dspack` | max_file_bytes | §7.2 单文件 ≤2 GiB |
| 40 | `40_declared_total_too_large.dspack` | max_unpacked_bytes | §7.2 解包总量 ≤6 GiB |
| 42 | `42_overture_format_2.dspack` | overture_format | §7.1 snapshot format 1 |
| 43 | `43_overture_missing_profile.dspack` | overture_missing_profile | §7.1 profiles/<id>/profile.yaml |
| 44 | `44_overture_traversal_profile.dspack` | overture_path_traversal | §7.2 安全路径 |
| 45 | `45_overture_invalid_yaml.dspack` | overture_invalid_yaml | §7.1 profile.yaml 解析 |
| 46 | `46_profile_name_traversal.dspack` | profile_name_traversal | §7.2 安全 profile 标识 |

> 注：40 之后无 41（400000 条目上限实体样本约 40 MB，改由 internal/safe 单测覆盖）；恶意样本共 44 个 .dspack，`cases.json` 为机器可读清单，`SHA256SUMS` 为 sha256 清单。

### 1.3 插件夹具

| 路径 | 用途 |
| --- | --- |
| `testdata/plugin/local-test-plugin/` | 本地合成 npm 包（`@pcl-dsh/m3-test-plugin` 0.1.0，无外部依赖/不执行代码），用于 E2E file: add → list → stop/start 生效 → update → remove |
| `testdata/plugin/malicious-specs.json` | 10 个非法 spec/profile 变更用例（见 §5） |

## 2. G6 API 契约矩阵（POST preview / import / export / artifact）

| 方法/路径 | 鉴权/CSRF | 请求 | 成功 | 关键失败 | 说明 |
| --- | --- | --- | --- | --- | --- |
| POST /api/packs/preview | 登录 / 必需 | multipart `file`（流式，≤2 GiB） | 200 PackPreviewDto | 400 VALIDATION；413 PAYLOAD_TOO_LARGE；409 CONFLICT_BUSY；401 | 不占 Job；预览 TTL 30min；SHA-256 落盘 0600；不执行/不安装 |
| POST /api/packs/import | 登录 / 必需 | `{previewId,name?,version?}` | 202 JobView（IMPORT_PACK） | 400 VALIDATION；409 CONFLICT_BUSY；401；**preview 不存在/过期表现为 202 + Job FAILED error.code=NOT_FOUND**（实现无同步预览查询方法） | Job result `{instanceId,name}`；失败整体回滚到 trash；版本缺失时 Job 内安装 runtime |
| POST /api/packs/export | 登录 / 必需 | `{instanceId,mode:"profile"\|"home"}` | 202 JobView（EXPORT_PACK） | 400 VALIDATION；404；409 BUSY；401 | Job result `{artifactId,filename,size}`；排除凭据/会话/日志/缓存 |
| GET /api/artifacts/{id} | 登录 / — | — | 200 文件流 attachment/no-store | 404 NOT_FOUND（不存在/过期）；401 | TTL 24h；id 12hex |

**PackPreviewDto**：`{previewId,name,format,version,dshVersion,profiles[],unpackedBytes,warnings[]}`。
**Job/SSE**：沿用 M1/M2 Job 契约（`j-<12hex>`、state/log/result/error、Last-Event-ID、心跳 15s、单任务忙 409）；失败 error 四字段。

## 3. G6 安全规则 → 样本映射（preview 必拒绝）

| 规则 | 覆盖样本 | 必须验证 |
| --- | --- | --- |
| 容器/格式标记（恰一/版本/JSON/YAML 拒绝） | 01-12 | 400 VALIDATION；不生成 previewId；无临时文件残留（upload 场景清理） |
| metadata ≤4 MiB / 单文件 ≤2 GiB / 解包 ≤6 GiB | 13,38,39,40 | 400；不解包；无磁盘增长；temp 清理 |
| 路径规范（相对/无..、无控制/保留/尾随/非法字符/长度） | 14-28 | 400；错误中文可操作；dest 不存在/不覆盖 |
| 路径冲突（重复/大小写/NFC/文件目录） | 29-32 | 400；不覆盖已有文件；无部分解包残留 |
| 链接/特殊文件/加密/压缩方式/CRC | 33-37 | 400；不落盘；不执行 |
| Overture snapshot 格式/profile.yaml/路径 | 42-45 | 400；不安装 |
| profile 标识注入 | 46 | 400；不创建实例/不写 dsh-home |
| 导入失败回滚 | （task-42 构造） | 实例目录→trash、store 无残留、原实例/文件不变 |

## 4. G7 API 契约矩阵（已装插件）

| 方法/路径 | 鉴权/CSRF | 请求 | 成功 | 关键失败 |
| --- | --- | --- | --- | --- |
| GET /api/instances/{id}/plugins?profile= | 登录 / — | — | 200 PluginDto[] | 400 profile 非法；404 实例；401 |
| POST /api/instances/{id}/plugins | 登录 / 必需 | `{spec}`（add） | 202 JobView（PLUGIN_MUTATE） | 400 VALIDATION；404；409 CONFLICT_BUSY / CONFLICT_INSTANCE_RUNNING；422 UNSUPPORTED_RUNTIME；401 |
| PATCH /api/instances/{id}/plugins/{*name} | 登录 / 必需 | —（update） | 202 JobView | 同上（name 需完整 npm 包名） |
| DELETE /api/instances/{id}/plugins/{*name} | 登录 / 必需 | —（remove） | 202 JobView | 同上 + 400（内置 bundle/非独立依赖） |

**PluginDto**：`{name,version,description,source}`；列表来源=dependencies/optionalDependencies + dsh.profile.bundles；未安装依赖 version=“待恢复依赖”；profile 不存在/未初始化→[]（非 404）；实例不存在→404。
**Job result**：`{instanceId,profile,action,name}`；Job 内 20min 超时；Node 桥失败 fail-closed。

## 5. G7 spec/状态矩阵（task-42 执行）

| 场景 | spec/前置 | 期望 |
| --- | --- | --- |
| 本地包 add | `file:./local-test-plugin`（复制到 workspace） | 202 Job → SUCCEEDED；列表新增 `@pcl-dsh/m3-test-plugin` 0.1.0 |
| update | PATCH 完整包名（无 spec） | 202 Job → SUCCEEDED；版本/列表刷新 |
| remove | DELETE 完整包名 | 202 Job → SUCCEEDED；列表移除；stop/start 后仍无 |
| 前导 - / 控制字符 / 超长(>214) / 非法 npm 名 | malicious-specs.json #1-#4 | 400 VALIDATION 四字段 |
| file:/ ../ 逃逸、link 逃逸、绝对路径 | #5-#7 | 400 VALIDATION（锚定 workspace，拒绝逃逸） |
| 移除内置 bundle | #8 `@deepseek-ai/dsh-base` | 400 VALIDATION（不能移除内置） |
| 移除非独立依赖 | #9 not-installed | 400 VALIDATION |
| 非法 profile | #10 profile=../evil | 400 VALIDATION |
| 实例运行中变更 | 启动实例后 POST/PATCH/DELETE | 409 CONFLICT_INSTANCE_RUNNING |
| 全局 Job 忙 | 任一 Job 运行中发起变更 | 409 CONFLICT_BUSY（不排队） |
| 运行时无 pluginManagement | 可控 runtime 能力（如有） | 422 UNSUPPORTED_RUNTIME（或按 spec 的实现错误） |
| 未登录 | 无会话 | 401 UNAUTHORIZED 四字段 |

## 6. task-42 真实 E2E 执行结果（脚本 scripts/wsl/m3-acceptance.sh）

> **2026-10-24 task-42/50 执行：124/124 PASS，EXIT_CODE=0；证据 `docs/evidence/m3/m3-acceptance-20261024-171548.log.txt`（+ manager/nginx 日志，脱敏）。** 以下 checklist 已全部执行：

- [ ] fresh data-root + 当前工作树重建 linux 二进制（-buildvcs=false，记录 sha256）+ 复用已装真实 runtime（软链；注明未重复安装）。
- [ ] 恶意样本：循环 45 个 `testdata/pack/malicious/*.dspack` → POST /api/packs/preview（multipart file）全部 400 VALIDATION 四字段；data-root 无新增实例/无 upload 残留；日志无归档内容泄漏。
- [ ] 合法样本：4 个 `testdata/pack/valid/*.dspack` → preview 200 且字段正确（format/version/dshVersion/profiles/unpackedBytes）。
- [ ] preview 过期/不存在：随机 previewId import → 404；如实现支持短 TTL 则等待/构造过期 → 404（否则记录策略说明）。
- [ ] 导入：选择 v3 profile（和/或 Overture）→ 202 Job → 轮询 SUCCEEDED → 实例存在、可 launch（Job）→ /workbench 经 manager 200；Job result `{instanceId,name}`。
- [ ] 导入失败回滚：构造可失败导入（如无可用 runtime 且 registry 不可达/恶意合法外但缺依赖）→ Job FAILED/错误四字段；实例目录进 trash、store 无新实例、已有实例/文件不变。
- [ ] 导出往返：真实实例导出 profile → artifact 下载（Content-Disposition attachment、no-store、文件名/大小匹配）→ 产物再次 preview → import 新实例 → launch → /workbench 200；profile/home 各一次。
- [ ] 导出排除：artifact 内容解包检查不含 credentials/session/logs/cache/trash/离线账户文件；日志不含 token/密码/归档内容。
- [ ] 插件列表：GET plugins?profile= 与 profile package.json 的 dependencies/optionalDependencies/bundles 一致；未安装依赖标记“待恢复依赖”。
- [ ] 插件变更：复制 local-test-plugin 到实例 workspace → add file: → 列表变化 → stop/start 后生效 → update → remove；Job result 字段正确。
- [ ] 插件错误：运行中 409、忙 409、内置 bundle 移除 400、malicious-specs.json 10 例 400 四字段；失败摘要可操作。
- [ ] Nginx 链路（沿用 M2 模板隔离 8088，至少 API 直连 manager）：preview/import/export/artifact/plugins 经 Nginx 与直连行为一致；409/413/502 四字段；Nginx access_log off 无 token/query。
- [ ] artifact 未登录 401；过期/不存在 404。
- [ ] 安全/平台：token/密码/归档内容 0 泄漏；Windows DSH 程序树 hash 不变、参考仓库 git 基线不变、/etc/nginx 未改、端口/进程清理。
- [ ] 证据：docs/evidence/m3/m3-acceptance-<ts>.log.txt（脱敏）+ manager/Nginx 日志文本；失败保留现场；脚本自写 EXIT_CODE。

### 6.1 执行结论与偏差记录

- 恶意样本：**44/44 全部 400 VALIDATION**，uploads 无残留、无实例创建。
- 合法 preview：v2/v3 profile、dshhome(m3home)、Overture v1 字段正确；dshhome 的 profiles 必须非 web/headless（web 值的样本会被正确 400 拒绝）。
- **合成最小包只做 preview/格式覆盖**：v3/Overture import 仅断言创建实例，不 launch（最小 profile 无依赖，DSH 不提供服务）；dshhome 导入后用于 home 导出成功路径。
- 真实链路：create web 实例 → launch SUCCEEDED → stop → **export profile** → artifact 下载（attachment/no-store/大小一致）→ re-preview → import → launch SUCCEEDED → /workbench 200（title/base）→ stop/delete（trash）。
- home：**web-only 实例 export home → Job FAILED VALIDATION “实例没有可导出的 profile”**（符合参考实现）；导入合法 dshhome(m3home) → export home SUCCEEDED → artifact → re-preview 200。
- 失败回滚：registry 指向关闭端口 → import Job FAILED INTERNAL，实例目录进 trash、store 无残留。
- 插件：独立实例；file: add→list→stop/start→update→remove 全部 Job SUCCEEDED；运行中/忙 409；内置 bundle/非独立依赖 remove → Job FAILED VALIDATION。
- **插件 spec 偏差已修复（task-48/task-49/task-50）**：8 类非法 spec/参数（leading_dash、control_char、spec_too_long、@bad//name、file:../../outside、file:/etc/passwd、link:../outside、invalid_profile）**同步 400 VALIDATION 四字段且不创建 Job**；builtin_bundle_remove / remove_not_installed 属实例相关校验，**Job FAILED error.code=VALIDATION**（不再是 INTERNAL）；合法 add/update/remove 仍 202+SUCCEEDED。
- 安全/平台：artifact 未登录 401、未知/过期 404 四字段；manager/Nginx/证据 0 token/密码/归档内容泄漏；happy-path manager ERROR=0（负向用例产生的 ERROR 均属预期，unexpected=0）；data 0700/config 0600；Windows DSH/参考仓库/etc-nginx 未改；端口/进程清理。

## 7. 错误码速查（M3）

| status | code | 场景 |
| --- | --- | --- |
| 400 | VALIDATION | 恶意样本/格式/路径/超限/spec 非法/profile 非法/body 非法 |
| 401 | UNAUTHORIZED | 未登录 |
| 403 | CSRF_INVALID | 写请求缺/错 CSRF |
| 404 | NOT_FOUND | preview/artifact/实例不存在或过期 |
| 409 | CONFLICT_BUSY | 全局 Job 忙 |
| 409 | CONFLICT_INSTANCE_RUNNING | 插件变更时实例运行中 |
| 413 | PAYLOAD_TOO_LARGE | 上传/预览超限 |
| 422 | UNSUPPORTED_RUNTIME | 运行时缺少 pluginManagement 等能力 |
| 500 | INTERNAL | 未预期内部错误（中文 + traceId） |
| 502/504 | UPSTREAM_* | runtime/npm 上游失败或超时（导入/安装阶段） |

## 8. 约束、覆盖缺口与说明

- 所有样本内容为合成数据，**不含真实凭据/token**；生成脚本不下载外部文件、可重复。
- 未生成 2 GiB 归档/400000 条目实体样本（仓库体积）：由 internal/safe 单测覆盖；declared-size 与压缩率样本覆盖大小/比例路径。
- 参考实现中 vendored npm tarball、resource URL、snapshot links、私有文件跳过等规则不在 M3 Go 接口范围（M3_INTERFACES §2.1/§7.2 未列），未纳入恶意样本；如后端实现扩展需补样本。
- manifest 的 name/version 语义校验（semver）以实现为准；本矩阵不将“非法 semver”列入必拒样本（避免误判），task-42 可按实现记录观察。
- 恶意样本的“期望错误码”统一按 400 VALIDATION；若实现返回 413（大小类）等需在 task-42 记录实际并解释。
- 预览/导入的临时目录、uploads、preview store 清理需在 task-42 中检查（无残留）。
