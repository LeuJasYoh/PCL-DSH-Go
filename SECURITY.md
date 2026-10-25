# 安全策略

PCL-DSH-Go 是单管理员的 DSH 实例管理器，通常部署在可访问内网的 Linux 服务器上；安全默认值按“最小暴露面 + fail-closed”设计。本文说明漏洞报告方式、支持版本、安全模型与加固建议。

## 支持版本

| 版本 | 安全更新 |
| --- | --- |
| 0.2.x | ✅ 接收安全修复 |
| < 0.2.0 | ❌ 不支持（开发期版本，不建议部署） |

发布包中的 `VERSION` 与 WebUI/`/api/health` 显示项目版本；**项目版本与 DSH 运行时版本（如 `0.2.1-rc.3`）相互独立**。

## 报告漏洞

请通过 GitHub 仓库的 **Security → Report a vulnerability**（私有安全公告）提交，避免公开 issue 泄露细节。报告请包含：

- 受影响版本（`/api/health` 的 version 或 `VERSION`）与部署方式（Nginx/端口/WSL）；
- 复现步骤或 PoC、影响范围、需要的权限/前置条件；
- 相关日志/截图（务必脱敏：不要包含真实密码、token、Cookie、服务器地址）。

维护者会在确认后回复处理计划；修复发布前请勿公开细节。一般问题（非安全）请走普通 issue。

## 安全模型与默认防护

- **认证**：单管理员；密码使用 bcrypt-sha256（BCrypt cost 10，兼容旧 BCrypt 哈希）存储；新密码 8–256 字符；首次启动生成 24 位随机初始密码（0600，仅首启生成）；首次登录强制改密；连续失败按 IP 限流/锁定。
- **会话/请求**：服务端会话 Cookie（HttpOnly/SameSite、12h 滑动过期）；写请求 CSRF 双提交校验；Origin/Referer 同源校验。
- **网络边界**：管理器只监听 `127.0.0.1:8080`，公网只通过 Nginx；Nginx 对 `/api`、`/workbench` 关闭 access log（query 可能含一次性 token）；建议在 Nginx/前置代理终止 TLS。
- **权限**：服务以专用 `pcl-dsh` 用户运行；systemd 沙箱（NoNewPrivileges、ProtectSystem=strict、ProtectHome、PrivateTmp、ReadWritePaths 限数据目录）；数据目录 0700、凭据 0600。
- **日志**：应用/审计日志写前脱敏（token/password/api_key/authorization/cookie/URL token）；一次性 token 不以明文落日志。
- **不可信输入**：整合包上传走安全归档（路径穿越/绝对路径/Windows 保留名/链接/特殊文件/压缩炸弹/CRC/限值全校验）；静态服务防路径逃逸；工作台代理只允许回环上游并校验 Origin。
- **凭据隔离**：不保存 DSH/Provider API Key；DSH 的登录凭据由 DSH 自身管理。

## 加固建议

1. **不要暴露 8080**：管理器必须只监听 `127.0.0.1`；对公网只开放 Nginx 的 80/443。
2. **使用 TLS**：通过 Nginx 或前置代理配置 HTTPS，避免密码/会话在明文网络传输；HTTP 仅限本机/受信内网。
3. **强密码与最小权限**：首次登录后立即改密；直连登录 SSH 用户使用最小权限账号（默认 `root` 请按需调整）；限制 SSH 来源。
4. **防火墙与访问控制**：仅允许可信网段访问 WebUI；Nginx 模板中可按需启用 allow/deny。
5. **及时更新**：使用官方 release 并校验 SHA256；关注 CHANGELOG 的安全相关修复；升级前备份数据。
6. **只使用可信 registry/插件/运行时**：安装 DSH 运行时与插件会执行 npm 包中的代码（以 `pcl-dsh` 用户权限），registry/代理请在可信网络内配置。
7. **监控与备份**：监控 `/api/health`、服务日志与磁盘；定期备份 `/var/lib/pcl-dsh-go`（至少 `config/`+`instances/`）。

## 已知限制（安全相关）

- 单管理员、无 MFA、无多用户隔离；能登录管理员的用户即拥有全部实例/插件管理权限。
- 应用层 HTTP，TLS 依赖部署方；未启用 HTTPS 时会话/密码可能被网络嗅探（请仅在可信网络裸用）。
- DSH 实例与其插件运行在管理器同一系统用户下，无额外沙箱隔离；不受信任的插件等同于在服务器上执行代码。
- 工作台旧 Cookie 可能在升级/切换实例后残留导致异常（见 [CHANGELOG](docs/CHANGELOG.md#known-issues)），不是权限绕过，但请清除后使用。
- 不提供漏洞悬赏或 SLA；维护者按能力尽快处理。
