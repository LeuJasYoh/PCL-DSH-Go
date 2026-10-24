# testdata/pack — M3 整合包测试样本（G6）

由 `scripts/m3-gen-pack-samples.py` 确定性生成（无网络、无外部文件、无真实凭据/ token）：
时间戳/内容固定，重复运行结果一致。

## 目录

| 路径 | 内容 |
| --- | --- |
| `valid/` | 合法样本：dspack v2(manifest v4)/profile、dspack v3(manifest v5)/profile（profile=web）、dspack v3(manifest v5)/dshhome（profile=m3home，非 web/headless）、Overture snapshot format 1 最小样本 |
| `malicious/` | **44 个恶意 .dspack 样本**（容器/格式、路径、条目/压缩、Overture、manifest 字段等） |
| `malicious/cases.json` | 每个样本的 class / 违反规则（§7.1/§7.2）/ 期望 400 VALIDATION |
| `malicious/SHA256SUMS`、`valid/SHA256SUMS` | sha256 清单（`sha256sum -c` 在对应目录内校验） |

## 重新生成与校验

```bash
python3 scripts/m3-gen-pack-samples.py
cd testdata/pack/malicious && sha256sum -c SHA256SUMS
cd ../valid && sha256sum -c SHA256SUMS
```

## 说明/限制

- 所有恶意样本均应被 `POST /api/packs/preview` 以 400 VALIDATION 拒绝（不落盘/不覆盖）；详细映射见 `docs/evidence/m3/contract-matrix.md`。
- 2 GiB 归档大小上限、400000 条目上限用 Go 单测覆盖（生成对应实体样本过大，未纳入仓库）；本目录以 declared-size 样本覆盖单文件/解包总量超限路径。
- 合法样本仅含最小结构（marker + manifest + 少量 profile 文件），用于 preview/import 契约；真实 DSH 安装/启动由 task-42 E2E 使用既有 runtime。
