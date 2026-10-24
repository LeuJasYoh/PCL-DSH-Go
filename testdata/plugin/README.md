# testdata/plugin — M3 已装插件测试夹具（G7）

由 `scripts/m3-gen-pack-samples.py` 生成（可重跑，无外部下载）。

| 路径 | 用途 |
| --- | --- |
| `local-test-plugin/` | 合成 npm 包（`@pcl-dsh/m3-test-plugin` 0.1.0），用于 E2E：复制到实例 workspace 后以 `file:./local-test-plugin` 方式 add/update/remove |
| `malicious-specs.json` | 插件变更 spec 拒绝用例（前导 -、控制字符、超长、非法 npm 名、file:/../绝对路径、link 逃逸、内置 bundle 移除、非独立依赖移除、非法 profile），期望 400 VALIDATION |
| `SHA256SUMS` | 本目录夹具 sha256 清单（`sha256sum -c SHA256SUMS`） |

## E2E 使用约定（task-42）

1. 复制目录：`cp -r testdata/plugin/local-test-plugin <instance>/workspace/local-test-plugin`（相对 workspace，file: 锚定 workspace）。
2. `POST /api/instances/{id}/plugins {spec:"file:./local-test-plugin"}` → 202 Job → SUCCEEDED → GET plugins 列表含 `@pcl-dsh/m3-test-plugin`。
3. update 无 spec（原地更新）/ remove 完整包名；实例运行中或全局 Job 忙 → 409；内置 bundle 移除 → 400。
