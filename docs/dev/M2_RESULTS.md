# PCL-DSH-Go M2 验收报告（反代硬化 + Nginx 生产链路）

> 状态：**定稿（qa-release 产出、Lead 独立复跑并复核通过，2026-10-24）**。
> 日期：2026-10-24。里程碑：M2（/workbench HTTP/SSE/WS 硬化、TrustedProxy、Nginx 生产模板 + WSL 链路、真实浏览器经 Nginx、安全/错误路径）。
> 环境：WSL2 Ubuntu 26.04.1；Windows Go 1.27.3 交叉编译；内置 Node 24.22.1 + pnpm 11.28.5；真实 DSH **0.2.1-rc.3**；Nginx **1.28.3**（隔离实例 8088，未改系统 /etc/nginx 与 :80）；Windows Edge **155.0.4283.62** headless CDP。
> 边界：真实 DSH/manager 仅跑在 WSL /tmp/pcl-dsh-go-test 独立 data-root；runtime 复用既有真实安装（软链，不重复安装）；token/密码/会话 cookie 全流程内存/临时文件（0600，用完删除），证据统一脱敏为 `***`。

## 1. 结论摘要

- **M2 退出判据全部满足**：proxy 单测/竞态（task-23）、TrustedProxy（task-24）、Nginx 模板（task-25）、链路 E2E（task-26 **67/67 PASS**，含 Nginx 路径断言）、真实浏览器经 Nginx（task-27 **36/36 PASS**）、安全/错误路径（task-28 **61/61 PASS**；task-30 修复后 task-31 复跑 **57/57 PASS**）。
- Nginx 生产模板：`packaging/nginx/pcl-dsh-go.conf`（listen 80；`^~ /api/` 与 `^~ /workbench/` 反代 127.0.0.1:8080；HTTP/1.1、SSE 不缓冲、大 body 流式、3600s 超时、WS Upgrade、access_log off、静态/SPA、server_tokens off、IP 白名单注释、Host 端口保留 map）；README 覆盖依赖/安装/TLS/运维。
- 真实浏览器：Edge headless CDP 注入登录后的 JSESSIONID，经 `http://127.0.0.1:8088/workbench/{id}/` 打开真实 DSH 工作台：title=DeepSeek Harness、#root 渲染欢迎/选择工作区、`__DSH_BOOT__` 存在、**WS /api/remote.mux 101**、console errors=0、截图非空白（500x320, 34,164 B）。
- 安全/错误路径：未登录 401、停止实例 409、未知实例 404、manager 停止 Nginx 502、CSRF 403、错误四字段、SSRF/Host/X-Forwarded-Host/端口注入无外连、编码/反斜杠/控制字符路径 400、日志/证据 0 token/密码/会话泄漏、Windows DSH 程序树 hash 不变、参考仓库 git 基线不变、/etc/nginx 未改。
- **1 项契约偏差已修复（task-30/task-31）**：直连 manager 的原始 dot-segment（`/workbench/{id}/../../etc/passwd`、`%2e%2e`、`..%2f`）与 absolute-form `http://...` 此前被 Go ServeMux cleanPath 先返回 307；task-30 在 mux 前加 raw path guard 后，task-31 重建二进制复跑确认 **全部 400 BAD_REQUEST 四字段（traceId、无 Location、无文件泄漏）**；经 Nginx 的原始 dot-segment 由 Nginx 先规范化（200 静态、不触达 manager），属预期。
- 未发现 0-day 级风险；无未解决 P0/P1。

## 2. 环境与版本

| 项 | 值 |
| --- | --- |
| manager 构建 | 当前工作树 linux/amd64，`-buildvcs=false`，version=0.3.0-m2，8,806,560 B，sha256 `afeea3465f8c3ffb43a225eb292587eccf304b9c686565333a56309f8b7e16ab` |
| 目标监听 | manager 127.0.0.1:18080；隔离 Nginx 127.0.0.1:8088（模板适配 listen/静态根/上游端口，其余逐字）；系统 :80 服务全程未动 |
| DSH runtime | 0.2.1-rc.3（复用 M0 正式 E2E 的真实安装 `/tmp/pcl-dsh-go-test/e2e-data/runtimes/0.2.1-rc.3`，软链进新 data-root；M1 的 m1-data runtime 已在 M1 runtime-delete 用例移入 trash） |
| Nginx | Ubuntu nginx/1.28.3；模板 nginx -t 通过（隔离适配 + 生产指令逐项渲染断言） |
| 浏览器 | Windows Edge 155.0.4283.62 headless（--headless=new --remote-debugging-port=9223 --remote-allow-origins=*），Windows node v24.22.1 CDP 客户端 |
| 本地代理注意 | WSL 环境存在 HTTP_PROXY(127.0.0.1:7897) 且 no_proxy 通配不生效；E2E 脚本统一 `NO_PROXY='*'` 直连回环，避免代理干扰安全断言 |

## 3. 交付物

| 交付物 | 说明 |
| --- | --- |
| `packaging/nginx/pcl-dsh-go.conf` | Nginx 生产模板（见 §4/§5）：Host 端口保留 map、WS map、SSE/大 body、静态/SPA、日志安全 |
| `packaging/nginx/README.md` | 依赖/拓扑/安装位置/与 systemd 关系/TLS/关键指令原因/验证命令 |
| `packaging/README.md` | packaging 索引（M5 复用） |
| `scripts/wsl/m2-nginx-e2e.sh` | 真实 manager+DSH 经 Nginx 链路 E2E（66 项） |
| `scripts/wsl/m2-browser-e2e.sh` | 浏览器经 Nginx E2E（setup/verify 两阶段；cookie/Edge/清理/证据） |
| `scripts/wsl/lib/cdp-browser.mjs` | Windows Edge headless CDP 客户端（cookie 注入/title/root/__DSH_BOOT__/WS 101/截图/console 统计） |
| `scripts/wsl/m2-security-e2e.sh` | SSRF/Host/路径/控制字符/认证边界/错误四字段/日志泄漏/平台未越界（61 项） |
| `docs/evidence/m2/` | 各轮脱敏证据（见 §9） |

## 4. G4 全量证据（HTTP/静态/SSE/WS/浏览器/Nginx/错误路径）

| 检查 | 证据/结果 |
| --- | --- |
| 静态 GET /、静态资源 | 200；SPA fallback /console/route→200 index；index no-store、静态 immutable；server_tokens off |
| /api/health 经 Nginx | 200 + X-Trace-Id 12hex 透传；upstream 收到 Host(含端口)/X-Real-IP/X-Forwarded-For/X-Forwarded-Proto |
| 登录/改密/CSRF 经 Nginx | 204/204；缺 X-XSRF → 403 CSRF_INVALID 四字段；实例/Job 操作经 Nginx 正常 |
| 实例/Job 经 Nginx | create 201→launch 202 Job→轮询 SUCCEEDED→运行中；probe Job SUCCEEDED（软链 runtime 被识别） |
| /workbench 门槛 | 未登录 401 四字段；登录后 302 token（脱敏）→ DSH 303 Location ./ + Set-Cookie Path=/workbench/{id} → 页面 200 title/base href=./ |
| 静态/插件资源 | `assets/index-*.js` 200；`plugins/??...` bundle 200 |
| SSE | /plugins/events 首块 `: connected` 0ms + data 逐块（不缓冲） |
| WS | /api/remote.mux 101 升级、ready 帧、close 1000 |
| 真实浏览器 | Edge CDP 经 Nginx：title=DeepSeek Harness、#root 渲染、__DSH_BOOT__、WS 101、console errors=0、截图 500x320/34,164B |
| 错误路径 | stopped /workbench 409 INSTANCE_NOT_READY 四字段；unknown 404；manager down→Nginx 502（/api 与 /workbench）；坏 Origin 403、同源 Origin 放行 |
| 日志安全 | Nginx access_log off 生效（无 /api 行、无 query/token/cookie、静态请求仍记录证明 logging 正常） |

## 5. 安全 / G10 相关（task-28）

| 检查 | 结果 |
| --- | --- |
| SSRF：Host 注入（evil.example / user@host / IPv6:port）直连 manager 与经 Nginx | 本地 health 仍 200/4xx，无外连；Nginx 经 Host 注入不影响上游 |
| X-Forwarded-Host 伪造 | 直连 manager 的 direct-access sshHost 仍取 Host（127.0.0.1），不使用 X-Forwarded-Host |
| Host 端口注入 | direct-access tunnel port 取实例端口（非 Host），无 SSRF |
| 路径遍历/控制字符（编码 dot、反斜杠、NUL、CRLF） | 直连 manager 400；经 Nginx 规范化/拒绝，无 /etc/passwd 内容 |
| 原始 dot-segment / absolute-form http:// 形态 | **400 BAD_REQUEST 四字段（task-30 修复后）；无 Location、无文件读取** |
| 认证边界 | manager 直连与经 Nginx 的未登录 401 四字段一致 |
| 平台未越界 | Windows DSH 程序树 hash 前后相同；参考仓库 git status 基线相同；/etc/nginx 未改；系统 :80 未动 |
| 秘密泄漏扫描 | manager 日志/Nginx 日志/证据：0 token/JSESSIONID/密码 |
| manager 日志 | ERROR 0；data-root 0700、config 0600 |
| Edge 清理 | Edge 进程全杀（m2-browser msedge=0）、pid 删除、profile/临时 cookie 删除 |

## 6. 与 M1 决策/实现的差异与演进

1. **Host 端口保留（M2-C 发现，已修复）**：M1 模板 `Host $host` 丢端口；manager proxy Origin 同源校验比较 Origin host:port 与 Host，非 80 监听（如 WSL 8088）会 403 写请求/WS。模板改为 `map $http_host $pcl_dsh_host` + 两处 `Host $pcl_dsh_host`；生产 listen 80 行为不变，非 80 正确。task-26 用“坏 Origin 403 / 同源 Origin 放行 + WS 101”验证。
2. **/workbench 错误体统一（M2-A）**：M1 §5.2 记录的 proxy 错误体例外已消除；409/404/502 均四字段（details={}、traceId 取 X-Trace-Id）。
3. **Origin 同源写保护（M2-A）**：POST/PUT/PATCH/DELETE 带 Origin 时校验；坏 Origin 403 四字段、无 Origin 放行（非浏览器客户端）；GET/HEAD 不校验。
4. **TrustedProxy（M2-B）**：仅回环 RemoteAddr + 合法 X-Real-IP 才作为客户端 IP；E2E 经 Nginx（Nginx→manager 回环）登录限流/审计获得真实客户端 IP。
5. **X-Forwarded-Proto**：模板透传便于排障，manager 不使用/不信任。
6. **本地 HTTP 代理干扰**：WSL HTTP_PROXY 使 Host 注入测试被代理 502；E2E 统一 NO_PROXY='*'，该问题属测试环境非产品。
7. **浏览器 cookie 文件权限**：/mnt/d（drvfs）无法强制 0600，改为 WSL /tmp 0600 + Windows 侧 \\wsl.localhost UNC 读取；用完删除。

## 7. 已知偏差 / 未覆盖 / 风险

**已修复偏差（task-30 / task-31，见 §9 新证据）**
- 直连 manager 的原始 dot-segment（`/workbench/{id}/../../etc/passwd`）与 absolute-form `http://...` 此前返回 307（Go ServeMux cleanPath 先于 proxy 校验）。backend-server 的 task-30 在 `withTracing` 内、mux 前增加 `withWorkbenchRawPathGuard`（仅 `/workbench` 前缀；URL.Path/EscapedPath 解码后含 `..` 或控制字符 → 400 BAD_REQUEST 四字段，且先于会话校验）；QA task-31 重建二进制复跑：7/7 raw 样本 + absolute-form + 未登录场景全部 **400 BAD_REQUEST 四字段、无 Location、无 /etc/passwd 泄漏**；正常 `/workbench`、`/api` 行为不变。经 Nginx 的原始 dot-segment 被 Nginx 先规范化（200 静态、不触达 manager），属预期。

**未覆盖（M3/M4/M5）**
- 504 超时/上游断连故障注入；多客户端并发/压测；TLS/HTTPS 终止（运维 Nginx 配置示例仅文档）；真实 SSH 隧道执行（仅 direct-access DTO/token）；Playwright（用 Edge CDP 替代）；IPv6 入口；Nginx reload 零停机；manager 崩溃/重启中的请求；Windows Edge 视口较小（500x320）不覆盖响应式。
- WebUI/登录页/Job UI/直连对话框（M4）；整合包/插件（M3）；发行包/systemd/install.sh（M5）。

## 8. M3 输入

1. 整合包/插件下载与 artifact 的 Nginx 暴露面：沿用 `/api/` 反代与 2048m body、流式、access_log off、错误体四字段；pack 安装 Job 复用 Job/SSE 客户端（scripts/wsl/lib/job-sse.mjs）。
2. 错误模型统一：/workbench proxy 四字段已统一，M3 新增端点直接复用 apierr；504/超时错误需补齐分类。
3. 模板/部署：packaging/nginx 模板被 M5 install.sh 直接安装；M3 如需 artifacts 静态路径，另行规划 location 与缓存策略。
4. 测试基建：scripts/wsl/m2-*.sh 的 setup/cleanup/日志脱敏/Windows 基线可复用于 M3；真实 DSH runtime 复用策略保持（软链、不重复安装）。
5. 安全：路径 dot-segment 偏差修复；大 body/上传限制与 Nginx 2048m 对齐；日志敏感 query 继续 off。

## 9. 证据索引（docs/evidence/m2/）

| 证据 | 内容 |
| --- | --- |
| `m2-nginx-e2e-20261024-161154.log.txt`（+ manager.log.txt） | task-26/31 Nginx 链路 E2E（含 Nginx 路径断言）：**67/67 PASS，EXIT_CODE=0** |
| `m2-nginx-e2e-20261024-161346.log.txt`（+ manager.log.txt） | **Lead 独立复跑**（当前工作树 + 重建二进制 sha 一致）：**67/67 PASS，EXIT_CODE=0** |
| `m2-browser-e2e-20261024-155537.log.txt` | task-27 浏览器经 Nginx：**36/36 PASS，EXIT_CODE=0** |
| `browser-workbench-nginx.png` | 真实 Edge 渲染截图（500x320，非空白） |
| `browser-workbench-nginx.result.json.txt` | 浏览器断言 JSON（title/root/__DSH_BOOT__/WS101/截图元数据，脱敏） |
| `m2-security-e2e-20261024-161033.log.txt`（+ manager.log.txt） | task-28/31 安全/错误路径（task-30 修复后复跑）：**57/57 PASS，EXIT_CODE=0**；含 7/7 raw dot-segment + absolute-form 400 四字段、未登录 guard 先于 session |
| `m2-security-e2e-20261024-161421.log.txt`（+ manager.log.txt） | **Lead 独立复跑**：**57/57 PASS，EXIT_CODE=0**（dot-segment/absolute-form 400 四字段、SSRF/日志/平台未越界全 PASS） |

## 10. 阶段提交（待 Lead 填写/确认）

| commit | 内容 |
| --- | --- |
| 888e203 | chore(m2): freeze M2 proxy-hardening and Nginx decisions（docs/M2_INTERFACES.md） |
| e96b5dc | feat(m2): harden workbench proxy and support trusted-proxy client IP（proxy + task-24 api） |
| 本报告提交 | feat(m2): Nginx template + M2 E2E scripts/evidence + raw dot-segment guard + M2_RESULTS/README（task-25/26/27/28/30/31 及本报告） |

> task-23/24 已随 e96b5dc 入库；task-25/26/27/28/30/31 与本报告在“本报告提交”中入库。

## 11. M2 任务对照

| 任务 | owner | 状态 | qa 验证 |
| --- | --- | --- | --- |
| task-23 proxy 硬化 | proxy | completed | 经 task-26/28 实测（四字段、Origin、SSE/WS、大 body、错误路径） |
| task-24 TrustedProxy | backend-server | completed | 经 Nginx 登录/限流路径实际使用 X-Real-IP |
| task-25 Nginx 模板 + docs | qa-release | completed | 隔离 8088 nginx -t + 36/36 模板验证；task-26 链路复验 |
| task-26 Nginx 链路 E2E | qa-release | completed | 66/66 PASS |
| task-27 浏览器经 Nginx | qa-release | completed | 36/36 PASS + 截图 |
| task-28 安全/错误路径 + 本报告 | qa-release | completed | 61/61 PASS |
| task-30 raw dot-segment guard | backend-server | completed | 单测 10 target + go test ./... 15 包全绿（owner 报告） |
| task-31 修复后复跑 + 报告更新 | qa-release | completed | 57/57 security + 67/67 nginx，全部 400 四字段 |

## 12. M2 验收判据对照（M2_INTERFACES §7）

- proxy 单测/竞态：Lead/owner 负责（本报告不重复）；QA 侧经真实链路覆盖 §3/§4 全部用户可见条目。
- Nginx 模板 nginx -t 通过：✅（隔离适配 + 生产指令渲染断言）。
- WSL 8088 链路全部 PASS：✅（QA 67/67 + **Lead 独立复跑 67/67**）。
- 真实浏览器经 Nginx 打开工作台并有截图：✅（36/36 + PNG + WS 101 观测）。
- 安全/错误路径 0 泄漏、0 绕过：✅（QA 57/57 + **Lead 独立复跑 57/57**；raw dot-segment/absolute-form 均 400 四字段）。
- gofmt/go vet/go test + GOOS=linux 构建：✅ Lead 复跑全绿（15 包；binary 8,806,560 B / sha256 73e7ccb…，与 QA 重建一致）。
- 证据/报告入库推送：✅ 本次提交并推送 origin/main。

## 13. Lead 复核结论

- **M2 达到退出条件**：proxy 硬化单测/竞态全绿；Nginx 生产模板 nginx -t 通过；WSL 隔离 8088 链路 **67/67**；真实浏览器经 Nginx **36/36**（title/渲染/`__DSH_BOOT__`/WS 101/截图/0 console error）；安全/错误路径 **57/57**（含 dot-segment 修复后 400 四字段）；Lead 独立复跑结果一致。
- **偏差闭环**：原始 dot-segment 307 → task-30 raw path guard → 400 四字段（task-31 与 Lead 复跑确认）；经 Nginx 时被 Nginx 先规范化（200 静态、不触达 manager），属预期安全行为。
- **接受的上游/环境行为**：Nginx 路径规范化、Host 端口保留 map（非 80 监听必需）、WSL HTTP_PROXY 需 NO_PROXY='*'（测试环境）、浏览器视口 500x320（M4 响应式另测）。
- **未覆盖项如实保留**（§7）：504 故障注入/并发压测/TLS/IPv6/reload 零停机/真实 SSH 隧道；WebUI（M4）、整合包/插件（M3）、发行包/systemd（M5）。
- **M3 优先级建议**：① 安全归档核心（ZIP/TAR 规则 + 恶意样本，G6 前置）；② pack preview/import/export + artifact（复用 Job/SSE）；③ 插件列表/变更 Node 桥（G7）；④ 继续复用 m2 脚本基建与 Windows 基线。
- **红线复核**：Windows DSH 程序树 hash 前后相同、参考仓库 git 基线不变、/etc/nginx 与系统 :80 未改、真实 DSH 仅 WSL /tmp、证据 0 token/密码/会话泄漏。

> 报告结束。M2 验收证据位于 `docs/evidence/m2/`；token/密码/会话已脱敏为 `***`。
