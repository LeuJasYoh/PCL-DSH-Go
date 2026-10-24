# PCL-DSH-Go Nginx 生产模板（packaging/nginx/）

M2 交付：`/api/` 与 `/workbench/` 反向代理 + SPA 静态托管的生产用 Nginx 模板，由 M5 的
`install.sh` 安装到目标机（本目录只提供文件与说明，不改宿主机配置）。

| 文件 | 用途 |
| --- | --- |
| `pcl-dsh-go.conf` | server 模板（http 上下文 include；含 WebSocket Upgrade map） |
| `README.md` | 本文档：依赖、安装位置、TLS/防火墙、运维命令 |

## 依赖与拓扑

- Nginx ≥ 1.18（使用 `map`、`proxy_http_version 1.1`、Upgrade 透传；Ubuntu 24.04/26.04 自带版本满足）。
- 管理器 `pcl-dsh-go` 只监听 `127.0.0.1:8080`（默认）。Nginx 在同一台机器上反向代理；不要把 8080 暴露到公网。
- 静态根：`/opt/pcl-dsh-go/current/release/static`（M4 前端构建产物；M4/M5 前目录可空/占位，SPA fallback 会回退 `index.html`）。
- 本模板安装在 Nginx 的 `http{}` 上下文（Ubuntu 默认 `/etc/nginx/conf.d/*.conf` 或 `sites-enabled` 均满足），因为顶层有 `map $http_upgrade $connection_upgrade`。

## 安装与启用

```bash
sudo cp packaging/nginx/pcl-dsh-go.conf /etc/nginx/conf.d/pcl-dsh-go.conf
sudo nginx -t
sudo systemctl reload nginx        # 或: sudo nginx -s reload
```

- 如需用 `sites-available/sites-enabled` 布局：把文件放到 `/etc/nginx/sites-available/pcl-dsh-go` 并 `ln -s` 到 `sites-enabled/`。
- `server_name _;` 是占位：生产建议改为实际域名（或由上层负载均衡转发）。
- 删除/停用：移除 `conf.d` 下文件后 `nginx -t && systemctl reload nginx`；不影响管理器进程。

## 与 systemd / 管理器进程的关系

- 管理器由 M5 的 systemd unit 常驻（`127.0.0.1:8080`，仅回环）。Nginx 只负责公网入口、TLS、静态与反代；两者生命周期独立。
- 管理器未启动/重启期间，Nginx 对 `/api/`、`/workbench/` 返回 502；页面静态仍可访问。
- `X-Real-IP` 由 Nginx 注入，管理器仅信任回环来源的该头（M2-B TrustedProxy），因此 **proxy_pass 必须走 127.0.0.1**，不要改成远端地址。

## TLS（由运维在 Nginx 终止）

- 本模板只监听 80（内网/测试/由前置 LB 终止 TLS 的场景）。公网部署建议增加 443 server：
  `listen 443 ssl; ssl_certificate ...; ssl_certificate_key ...;`，并将 80 重定向到 443。
- 证书、HSTS、加密套件属于部署环境策略，不写入本模板；M5 的安装文档会给出示例。

## 关键行为与原因

| 指令 | 原因 |
| --- | --- |
| `proxy_buffering off` | Job SSE（`/api/jobs/{id}/events`）与工作台事件流必须逐块到达（G3/G4） |
| `proxy_request_buffering off` | 大 body/上传不先落 Nginx 临时盘，保持流式转发 |
| `proxy_read_timeout/proxy_send_timeout 3600s` | 长会话、SSE、WebSocket 空闲期不应被 60s 默认值切断 |
| `Upgrade/Connection map` | `/workbench/{id}/api/remote.mux` WebSocket 握手（DSH 工作台） |
| `X-Forwarded-Proto` | 排障用（管理器当前不信任/不使用该头；真实 IP 仅信回环 `X-Real-IP`） |
| `Host $pcl_dsh_host`（map） | 保留客户端 Host 的端口（如 `:8088`）；管理器 proxy Origin 同源校验比较 Origin host:port 与 Host，`$host` 丢端口会导致非 80 监听的写请求/WS 403 |
| `access_log off`（/api、/workbench） | 一次性 token 可能在 query 中；敏感路径不落访问日志 |
| `client_max_body_size 2048m` | 上传/整合包最大 2 GiB（服务端另有流式限制） |
| `server_tokens off` | 不暴露 Nginx 版本 |
| `index.html no-store`、静态哈希资源长缓存 | 前端升级即时生效 + 资产缓存命中 |
| 可选 `allow/deny` 注释 | 内网部署可限制来源网段（默认注释，不改变默认行为） |

## 验证（不修改宿主机 Nginx 配置）

生产配置文件语法检查：

```bash
sudo nginx -t
```

开发/CI（WSL）用隔离实例验证模板（模板临时替换 listen=8088 与静态根为 /tmp fixture，不动 `/etc/nginx` 与 80 端口服务）：

```bash
bash scripts/wsl/m2-nginx-e2e.sh    # M2-D 提供；生成 /tmp/pcl-dsh-go-test/m2-nginx 并用真实 manager 验证
```

M2 验证结果见 docs/M2_RESULTS.md（§ Nginx 链路）；真实浏览器经 Nginx 打开工作台见 M2-E 证据。

## 许可

GPL-3.0-or-later（见仓库 LICENSE / LICENSES）。模板由 PCL-DSH-Go 项目维护，不修改 Nginx 上游默认文件。