# 许可与第三方声明 / License and third-party notices

## 项目许可 / Project license

PCL-DSH-Go is distributed under **GNU GPL version 3 or any later version
(GPL-3.0-or-later)**, together with the HMCL additional terms in
[LICENSES/HMCL-ADDITIONAL-TERMS.md](LICENSES/HMCL-ADDITIONAL-TERMS.md). The full
GPL text is in [LICENSE](LICENSE). The corresponding source is this repository.

The Go manager, packaging, tests and project documentation are Copyright (c) 2026
LeuJasYoh and contributors.

The WebUI and interaction design are adapted from
[PCL-DSH-Linux](https://github.com/LeuJasYoh/pcl-dsh-linux) / HDSL, which derives
from Hello Minecraft! Launcher (HMCL). HDSL is Copyright (c) 2026 jiefing. HMCL is
Copyright © 2013–2026 huangyuhui and contributors; individual upstream files keep
their own notices. HMCL's additional terms require modified distributions to use
a distinguishable name/version and retain the displayed copyright notice. This
project uses the name and independent version **PCL-DSH-Go** and is not an
official HMCL release.

The original HDSL MIT grant remains available for code that HDSL originally
provided under MIT: [LICENSES/HDSL-MIT.txt](LICENSES/HDSL-MIT.txt). It does **not**
relicense HMCL code, the adapted WebUI, or the combined GPL work as MIT.

This notice records the current GPL distribution. It does not change the license
of already-published releases, and it does not claim that an MIT transition has
been completed.

## 前端与素材 / WebUI and artwork

The WebUI contains modified code, styles and tests from the GPL/HMCL-derived
PCL-DSH-Linux frontend. The design tokens document their HMCL origin in
`web/src/styles/tokens.css`. The default background, mascot icon and favicon are
the HDSL artwork converted for the WebUI; the artwork record is retained in the
HDSL project and does not include HMCL wallpaper or HMCL brand assets.

The HMCL upstream reference is fixed at commit
[`77eee17d361996259a48cc7896006a57d2e34a2a`](https://github.com/HMCL-dev/HMCL/tree/77eee17d361996259a48cc7896006a57d2e34a2a).
The additional terms are reproduced in
[LICENSES/HMCL-ADDITIONAL-TERMS.md](LICENSES/HMCL-ADDITIONAL-TERMS.md).

## 当前发行依赖 / Bundled runtime dependencies

These components are distributed with the Linux release (Go binary, WebUI, or
the bundled Node/pnpm tools). They keep their own licenses; the repository copies
the corresponding texts under `LICENSES/dependencies/` or inside the tool tree.

| Component | Version | License | Notice / text |
| --- | --- | --- | --- |
| [gorilla/websocket](https://github.com/gorilla/websocket) | 1.5.3 | BSD-2-Clause | Copyright (c) 2013 The Gorilla WebSocket Authors; `LICENSES/dependencies/gorilla-websocket-1.5.3/LICENSE` |
| [Go x/crypto](https://pkg.go.dev/golang.org/x/crypto) | 0.58.0 | BSD-3-Clause | Copyright 2009 The Go Authors; `LICENSES/dependencies/golang-x-crypto-0.58.0/LICENSE` |
| [go-yaml/yaml v3](https://pkg.go.dev/gopkg.in/yaml.v3) | 3.0.1 | MIT and Apache-2.0 (per file) | Copyright notices and both texts: `LICENSES/dependencies/go-yaml-3.0.1/LICENSE` |
| [Vue](https://github.com/vuejs/core) | 3.5.22 | MIT | Copyright (c) 2018-present Yuxi (Evan) You; `LICENSES/dependencies/vue-3.5.22/LICENSE` |
| [Pinia](https://github.com/vuejs/pinia) | 3.0.3 | MIT | Copyright (c) 2019-present Eduardo San Martin Morote; `LICENSES/dependencies/pinia-3.0.3/LICENSE` |
| [Vue Router](https://github.com/vuejs/router) | 4.5.1 | MIT | Copyright (c) 2019-present Eduardo San Martin Morote; `LICENSES/dependencies/vue-router-4.5.1/LICENSE` |
| [Node.js](https://nodejs.org/) | 24.22.1 | MIT and bundled third-party terms | Full text and notices: `tools/node/LICENSE` |
| [pnpm](https://github.com/pnpm/pnpm) | 11.28.5 | MIT | `tools/pnpm/node_modules/pnpm/LICENSE` |
| [OpenPGP.js](https://openpgpjs.org/) (inside pnpm) | 6.3.2 | LGPL-3.0-or-later | `LICENSES/dependencies/openpgp-6.3.2/LICENSE`; source: <https://github.com/openpgpjs/openpgpjs/tree/v6.3.2> |

`tools/node` also contains npm (Artistic-2.0) and Corepack (MIT); their license
files remain in the Node directory. The exact npm package versions are in the
release's `tools/` tree and `web/package-lock.json`.

Build-only tools such as Vite, TypeScript and Playwright are not shipped in the
manager runtime; their license metadata remains in `web/package-lock.json`.

The bundled pnpm distribution contains OpenPGP.js source code. Its LGPL license
text and exact upstream source tag are included above. For a copy of the
corresponding source with this distribution, contact the maintainers; retain this
notice when redistributing the toolchain.

## 外部程序与历史声明 / External programs and historical notices

DSH runtimes are downloaded separately from the configured npm registry; they are
not embedded in this release and remain subject to their own licenses. User
plugins and integration packs are separate third-party content.

The Java/JavaFX/Jackson/Commons license directories still present under
`LICENSES/dependencies/` are retained from the HDSL upstream provenance record.
They describe HDSL's Java application and are **not** runtime dependencies of this
Go release. The actual release dependencies are listed in the table above.

## 源代码 / Source code

The complete source for this GPL release is the repository at
<https://github.com/LeuJasYoh/PCL-DSH-Go>. Keep `LICENSE`, `LICENSES/`,
`THIRD_PARTY_NOTICES.md` and the corresponding source available when
redistributing a modified build.
