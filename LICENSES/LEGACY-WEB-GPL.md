# Legacy WebUI license carve-out

The root [MIT license](../LICENSE) applies to PCL-DSH-Go original code and the
clean-room 0.3.0 shell files. It does **not** relicense the pre-existing WebUI
that was adapted from PCL-DSH-Linux / HDSL and HMCL.

The following files remain under **GPL-3.0-or-later with the HMCL additional
terms** (`HMCL-ADDITIONAL-TERMS.md`):

- All pre-existing files under `web/src/` that were not rewritten for the
  0.3.0 clean-room shell, including the legacy views, components, styles,
  API/store helpers and tests.
- `web/public/licenses/` and the HMCL/HDSL notice files in this directory.
- Any release artifact or combined distribution that still contains those
  legacy views. Such a distribution remains subject to the GPL/HMCL terms
  until the legacy WebUI is fully replaced.

The clean-room shell files covered by MIT are:

- `web/src/components/Next*.vue`
- `web/src/composables/useInstanceActions.ts`
- `web/src/stores/ui.ts`
- `web/src/styles/next.css`
- `web/src/views/HomeView.vue`

Already published **0.3.0** release artifacts remain under the GPL/HMCL terms
they were published with; this notice does not retroactively relicense them.

This carve-out preserves the copyright and license notices of HDSL, HMCL and
other third-party authors. See [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).
