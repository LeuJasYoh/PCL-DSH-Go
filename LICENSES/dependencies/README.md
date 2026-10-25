# 依赖许可目录 / Dependency licenses

本目录保存 PCL-DSH-Go 当前发行内容实际使用的第三方许可文本：

| 目录 | 组件 | 用途 |
| --- | --- | --- |
| `gorilla-websocket-1.5.3` | gorilla/websocket | WebSocket 代理 |
| `golang-x-crypto-0.58.0` | golang.org/x/crypto | 密码哈希等 |
| `go-yaml-3.0.1` | gopkg.in/yaml.v3 | Overture profile.yaml |
| `vue-3.5.22` | Vue | WebUI |
| `pinia-3.0.3` | Pinia | WebUI 状态 |
| `vue-router-4.5.1` | Vue Router | WebUI 路由 |
| `openpgp-6.3.2` | OpenPGP.js | pnpm 11 的签名/校验功能（随内置 pnpm 分发） |

Node 24.22.1、pnpm 11.28.5 的许可文本已分别随内置工具链保存在发行包
`tools/node/LICENSE` 和 `tools/pnpm/node_modules/pnpm/LICENSE`。

`LICENSES/dependencies/` 下仍保留的 Java/JavaFX/Jackson/Commons 等目录来自 HDSL 上游，
仅作为历史来源记录；PCL-DSH-Go 的 Go 发行包不包含这些 Java 运行时依赖。
