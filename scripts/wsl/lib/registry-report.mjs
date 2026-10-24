#!/usr/bin/env node
// PCL-DSH-Go M0-5 | record the @deepseek-ai/dsh registry state relevant to the baseline.
// Usage: node registry-report.mjs --registry <url> --version <ver> --outdir <dir>
// Prints a text report (safe to keep as evidence: no credentials are involved).
import fs from "node:fs";
import path from "node:path";

function parseArgs(argv) {
  const out = {};
  for (let i = 2; i < argv.length; i++) {
    if (!argv[i].startsWith("--")) throw new Error("unexpected argument: " + argv[i]);
    out[argv[i].slice(2)] = argv[++i];
  }
  return out;
}
const args = parseArgs(process.argv);
const registry = (args.registry || "https://registry.npmjs.org").replace(/\/+$/, "");
const version = args.version;
const outdir = args.outdir;
if (!version || !outdir) throw new Error("usage: registry-report.mjs --registry <url> --version <ver> --outdir <dir>");
fs.mkdirSync(outdir, { recursive: true, mode: 0o700 });

function semverCompare(a, b) {
  const pa = a.split("-"), pb = b.split("-");
  const na = pa[0].split(".").map(Number), nb = pb[0].split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if ((na[i] || 0) !== (nb[i] || 0)) return (na[i] || 0) - (nb[i] || 0);
  }
  if (!pa[1] && pb[1]) return 1;
  if (pa[1] && !pb[1]) return -1;
  if (pa[1] && pb[1]) return pa[1] < pb[1] ? -1 : pa[1] > pb[1] ? 1 : 0;
  return 0;
}

const started = Date.now();
const res = await fetch(registry + "/@deepseek-ai%2fdsh", {
  headers: { accept: "application/json" },
  redirect: "error",
  signal: AbortSignal.timeout(60000),
});
const elapsedMs = Date.now() - started;
if (res.status !== 200) throw new Error("registry HTTP " + res.status);
const raw = await res.text();
fs.writeFileSync(path.join(outdir, "dsh-registry.json"), raw, { mode: 0o600 });
const packument = JSON.parse(raw);
const versions = Object.keys(packument.versions || {});
const target = version;
const hasTarget = Object.prototype.hasOwnProperty.call(packument.versions || {}, target);
const times = packument.time || {};
const sorted = [...versions].sort((a, b) => semverCompare(b, a));

console.log("registry=" + registry);
console.log("fetched_at=" + new Date().toISOString());
console.log("fetch_ms=" + elapsedMs);
console.log("body_bytes=" + Buffer.byteLength(raw));
console.log("dist_tags=" + JSON.stringify(packument["dist-tags"] || {}));
console.log("total_versions=" + versions.length);
console.log("target=" + target);
console.log("target_available=" + hasTarget);
console.log("target_published_at=" + (times[target] || "n/a"));
console.log("latest_versions_desc=" + sorted.slice(0, 12).join(","));
console.log("recent_publish_times=");
for (const v of versions.slice(-8)) console.log("  " + v + " " + (times[v] || "n/a"));

if (!hasTarget) {
  console.log("RESULT=target_missing");
  process.exitCode = 2;
} else {
  const metaRes = await fetch(registry + "/@deepseek-ai%2fdsh/" + target, {
    headers: { accept: "application/json" },
    redirect: "error",
    signal: AbortSignal.timeout(60000),
  });
  if (metaRes.status !== 200) throw new Error("version manifest HTTP " + metaRes.status);
  const manifestText = await metaRes.text();
  fs.writeFileSync(path.join(outdir, "dsh-" + target + ".manifest.json"), manifestText, { mode: 0o600 });
  const manifest = JSON.parse(manifestText);
  const dist = manifest.dist || {};
  console.log("manifest_name=" + manifest.name);
  console.log("manifest_version=" + manifest.version);
  console.log("engines=" + JSON.stringify(manifest.engines || {}));
  console.log("package_manager=" + (manifest.packageManager || "n/a"));
  console.log("bin=" + JSON.stringify(manifest.bin || {}));
  console.log("dependencies=" + Object.keys(manifest.dependencies || {}).length);
  console.log("optional_dependencies=" + Object.keys(manifest.optionalDependencies || {}).length);
  console.log("peer_dependencies=" + Object.keys(manifest.peerDependencies || {}).length);
  console.log("dist_tarball=" + (dist.tarball || "n/a"));
  console.log("dist_integrity=" + (dist.integrity || "n/a"));
  console.log("dist_shasum=" + (dist.shasum || "n/a"));
  console.log("unpacked_size=" + (dist.unpackedSize || "n/a"));
  console.log("RESULT=target_available");
}
