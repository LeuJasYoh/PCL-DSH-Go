# M2 冻结决策（反代硬化 + Nginx 生产链路）

> 状态：**M2 冻结（2026-10-24，Lead）**。跨包/行为变更先报 Lead 并更新本文件。
> 权威：docs/IMPLEMENTATION_SPEC.md §5、docs/DSH_INTEGRATION.md §6、docs/ACCEPTANCE.md G4/G10、docs/M2_INTERFACES.md（本文件）。
> M2 范围：/workbench HTTP/SSE/WS 完整语义、TrustedProxy（Nginx 真实 IP）、Nginx 生产配置模板 + WSL 链路联调、真实浏览器经 Nginx 打开工作台、安全/错误路径测试。**不含** WebUI（M4）、整合包/插件（M3）、发行包/systemd（M5）。

## 1. 包归属（write scope）

| 包/目录 | M2 工作 | owner |
|---|---|---|
| internal/server/proxy/ | HTTP/SSE/WS 硬化 + 错误体统一 + 测试 | proxy |
| internal/server/api/（clientIP 等） | TrustedProxy 客户端 IP、测试 | backend-server |
| packaging/nginx/、packaging/README（Nginx 部分） | Nginx 生产模板 + 说明 | qa-release |
| scripts/wsl/ | M2 链路 E2E / 浏览器 CDP / 安全测试脚本 + 证据 | qa-release |
| docs/M2_INTERFACES.md、README、最终验收 | Lead | Lead |

## 2. TrustedProxy 客户端 IP（backend-server）

- `api.clientIP(r)`：默认仍用 `r.RemoteAddr`；**仅当 RemoteAddr 的主机是回环地址（127.0.0.1 / ::1）且 `X-Real-IP` 是合法 IP** 时，以 X-Real-IP 作为客户端 IP（用于登录限流与审计）。
- 不使用 X-Forwarded-For（可伪造链）；不信任非回环来源的 X-Real-IP。
- manager 默认只监听 127.0.0.1（M2 不改绑定策略）；Nginx 在宿主机回环上反代。
- 测试：RemoteAddr 回环+X-Real-IP 生效；非回环时忽略 X-Real-IP；非法/超长/控制字符 X-Real-IP 忽略；登录限流按 X-Real-IP 隔离（两个 IP 互不影响）；审计事件带真实 IP。

## 3. HTTP 反代硬化（proxy）

1. **Origin 校验（防 CSRF/跨站写）**：对 POST/PUT/PATCH/DELETE，若请求带 `Origin`，则 Origin 的 host:port（默认端口归一化 http→80/https→443）必须与请求 Host 同源，否则 **403 FORBIDDEN JSON**；无 Origin（非浏览器客户端）放行。GET/HEAD 不校验。
   **原始路径防护**：/workbench 请求在进入 ServeMux **之前**必须拒绝含 `..`（Path 或 EscapedPath，含编码形式解码后）或控制字符的原始路径 → 400 BAD_REQUEST JSON；否则 Go ServeMux 的 cleanPath 会先 307 规范化，违反 G4“路径含 .. → 400”。
2. **动态 hop-by-hop**：除固定列表外，移除请求头 `Connection` 中列出的 header 名（RFC 7230 §6.1）；响应侧由 httputil.ReverseProxy 处理并补测试。
3. **错误体统一**：/workbench 代理错误 JSON 改为四字段 `{code,message,details,traceId}`（details 恒 `{}`，traceId 取响应头 X-Trace-Id；无则空字符串）；覆盖 400/401/403/404/409/502/504。
4. **SSE**：逐块 flush；对 `text/event-stream` 响应设置 `X-Accel-Buffering: no`；客户端断开时取消上游请求；不设整体时长超时（仅 60s 响应头超时）。
5. **请求体流式转发**：POST/PUT/PATCH 大 body（≥10 MiB）逐块转发不整读内存；GET/HEAD/DELETE body 丢弃语义保持。
6. **HEAD**：不写 body；`Content-Length` 语义正确。
7. **日志安全**：不记录 query/token/Cookie/请求头原文/上游 body；错误只记录分类。

## 4. WebSocket 反代硬化（proxy）

**顺序重构（关键）**：先校验 Origin/方法/目标 → **先向上游 dial**（携带客户端 Sec-WebSocket-Protocol 列表、DSH Cookie、Origin 改写回环）→ 上游非 101 时直接返回 HTTP JSON 错误（映射：400 BAD_REQUEST、401 UNAUTHORIZED、403 FORBIDDEN、404 INSTANCE_NOT_FOUND、409 INSTANCE_NOT_READY、502 UPSTREAM_ERROR）→ 上游 101 后再升级浏览器连接，并在响应 `Sec-WebSocket-Protocol` 中回传**上游实际选择的子协议**（不得回显客户端首个 offer 代替上游选择）。

其余保持并在真实 DSH 上验证：
- Origin/Host 同源（默认端口归一化；无 Origin 放行）；
- 双向文本/二进制帧；`SetReadLimit(1 MiB)` 双方向；ping/pong 保活；
- 关闭帧：转发上游 close code/reason（非法 code 归一化：1005/1006/1015/越界→1000）；close 握手 grace；
- 客户端断开时关闭上游；上游断开时关闭客户端；
- 不转发本应用 Cookie（JSESSIONID/XSRF-TOKEN），只转发 DSH Cookie；Origin 改写为 `http://127.0.0.1:<port>`；
- 测试：mock 上游 + 真实 DSH（qa）覆盖子协议选择差异/无子协议/上游 401/404/409/502/超大消息/关闭码。

## 5. Nginx 生产配置（packaging/nginx/）

模板：`packaging/nginx/pcl-dsh-go.conf`（可直接被 install.sh 在 M5 安装）。要求：
- listen 80；静态根 `/opt/pcl-dsh-go/current/release/static`（M4 前目录可空/占位）；
- `location ^~ /api/`：proxy_pass http://127.0.0.1:8080；HTTP/1.1；**Host 必须保留浏览器可见的 authority（含非默认端口）**：`map $http_host $pcl_dsh_host { default $http_host; '' $host; }` + `proxy_set_header Host $pcl_dsh_host`（否则 proxy 的 Origin 同源校验与 direct-access sshHost 在非 80 端口下会错）；`X-Real-IP $remote_addr`；`X-Forwarded-For $proxy_add_x_forwarded_for`；`proxy_read_timeout 3600s`；`proxy_buffering off`（SSE）；`client_max_body_size 2048m`；`access_log off`（避免敏感 query 落盘）；
- `location ^~ /workbench/`：同上（含 Host 保留）+ WebSocket Upgrade 支持（`map $http_upgrade $connection_upgrade`、`proxy_http_version 1.1`、`proxy_set_header Upgrade $http_upgrade`、`proxy_set_header Connection $connection_upgrade`）；`proxy_buffering off`；`proxy_read_timeout/proxy_send_timeout 3600s`；`access_log off`；
- 静态：`location /` try_files $uri $uri/ /index.html；不缓存 index.html；
- 可选安全：来源 IP 白名单注释示例、server_tokens off。
- 说明文档：`packaging/nginx/README.md`（依赖、安装位置、与 systemd 关系、TLS 由运维在 Nginx 终止的说明）。

WSL 联调配置：从模板生成到 `/tmp/pcl-dsh-go-m2/nginx.conf`，**监听 8088**（WSL 既有 Nginx 已占用 80，不修改系统配置），静态根用测试 fixture；`nginx -t -c` 通过后启动/停止均用独立 pid/日志路径。

## 6. 链路与浏览器验收（qa-release）

1. **链路 E2E（curl/Node）**：静态占位 200；`/api/health` 200 且 X-Trace-Id；登录/改密/CSRF 经 Nginx 正常；`/workbench` 302 token（脱敏）→200 页面→静态/plugins 200；SSE 分块（不缓冲）；WS 101+ready+close 1000；404/409/502 错误路径；Nginx `access_log off` + 证据/日志 token 扫描 0；manager 0 ERROR。
2. **真实浏览器经 Nginx**：Windows Edge 155 headless（或 WSL Playwright，如可安装）经 `http://127.0.0.1:8088`：
   - 通过 CDP `Network.setCookie` 注入登录后的 JSESSIONID（cookie 由 qa 经 Nginx 登录 API 获得，仅内存传递，不落证据）；
   - 打开 `/workbench/{id}/`，断言 title=DeepSeek Harness、页面渲染（截图 PNG 入库，URL/token 脱敏）、网络层观测 `/api/remote.mux` WebSocket 101；
   - 可选：console error 统计（登录前 401 噪音需标注）。
   - 失败必须如实记录（不得伪造）；若有真实缺陷立即报 Lead + owner。
3. **安全/错误路径**：SSRF（Host/path/端口注入、编码绕过）在 manager 直连与 Nginx 两种路径；`..`/控制字符 400；未登录 401；实例停止 409；manager 停止时 Nginx 502；token/密码/会话在 manager 日志、Nginx 日志、证据中 0 泄漏；Windows DSH 程序树 hash 前后相同、参考仓库未改。
4. 证据：`docs/evidence/m2/`；报告 `docs/M2_RESULTS.md`（G4 全量 + G10 相关，含未覆盖项/M3 输入/提交序列），Lead 复核后入库。

## 7. 验收判据（M2 退出）

- proxy 包单测/竞态全绿，覆盖 §3/§4 全部条目（含 Origin 403、动态 hop-by-hop、四字段错误体、大 body、WS dial-first 子协议/上游错误映射/close code/1MiB）。
- Nginx 模板 `nginx -t` 通过；WSL 8088 链路全部 PASS；真实浏览器经 Nginx 打开工作台并有截图。
- 安全/错误路径 0 泄漏、0 绕过；Windows DSH/参考仓库未改。
- `gofmt/go vet/go test ./...` 全绿 + GOOS=linux 构建；证据/报告入库并推送。
