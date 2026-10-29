// SPDX-License-Identifier: MIT

// Package runtime implements DSH runtime discovery, installation, capability
// probing, and launch planning.
//
// It is the only package in PCL-DSH-Go that talks to the npm registry and to
// the DSH CLI. Behaviour is specified by docs/DSH_INTEGRATION.md sections 2, 3
// and 4; the exported surface is frozen by docs/M0_INTERFACES.md.
//
// Security notes:
//   - registry responses are bounded (32 MiB) and redirects are never followed;
//   - installs happen in a private staging directory and are moved into place
//     atomically after the package entry has been validated;
//   - capability probing is fail-closed: launch planning refuses to start a
//     runtime whose command line has not been verified;
//   - the DSH child process environment is built from a blank base, never
//     inherited, and never contains account credentials.
package runtime
