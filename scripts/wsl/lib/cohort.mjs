#!/usr/bin/env node
// PCL-DSH-Go M0-5 | release-cohort resolution for DSH runtime install.
// Mirrors PCL-DSH-Linux RuntimeService.releaseCohort: pin every @deepseek-ai/dsh-*
// dependency that this exact release declares at the same version.
//
// Usage:
//   node cohort.mjs --registry <url> --version <ver> --data-root <dir> --staging <dir> [--cache-file <path>]
//
// Writes <staging>/package.json + <staging>/pnpm-workspace.yaml, caches the cohort
// under <data-root>/cache/registry-metadata/<sha256(registry)>/cohort-<ver>.json
// (schema/validation compatible with the reference implementation; TTL 30 days).
// Prints a short human-readable summary. Never fetches/prints credentials.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";

const PACKAGE = "@deepseek-ai/dsh";
const MAX_COHORT = 2048;
const TTL_MS = 30 * 24 * 60 * 60 * 1000;
const REGISTRY_BODY_LIMIT = 32 * 1024 * 1024;

function parseArgs(argv) {
  const out = {};
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) throw new Error("unexpected argument: " + a);
    const key = a.slice(2);
    const value = argv[++i];
    if (value === undefined) throw new Error("missing value for --" + key);
    out[key] = value;
  }
  return out;
}

const args = parseArgs(process.argv);
const registry = (args.registry || "https://registry.npmjs.org").replace(/\/+$/, "");
const version = args.version;
const dataRoot = args["data-root"];
const staging = args.staging;
if (!version || !dataRoot || !staging) {
  throw new Error("usage: cohort.mjs --registry <url> --version <ver> --data-root <dir> --staging <dir>");
}
if (!/^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error("not an exact SemVer: " + version);
}
if (!/^https?:\/\//.test(registry)) throw new Error("registry must be http(s): " + registry);

const sha256 = (text) => crypto.createHash("sha256").update(text).digest("hex");
const cacheFile = args["cache-file"] || path.join(dataRoot, "cache", "registry-metadata", sha256(registry), "cohort-" + version + ".json");

function validPackageName(name) {
  return /^(@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/.test(name) && name.length > 0 && name.length <= 214;
}

function cohortDependencies(pkg) {
  const found = new Map();
  for (const kind of ["dependencies", "peerDependencies", "optionalDependencies"]) {
    const deps = pkg && pkg[kind];
    if (!deps || typeof deps !== "object") continue;
    for (const [name, spec] of Object.entries(deps)) {
      if (typeof spec !== "string") continue;
      if (!name.startsWith("@deepseek-ai/dsh-") || !validPackageName(name)) continue;
      if (spec !== version && spec !== "^" + version && spec !== "~" + version) continue;
      found.set(name, version);
    }
  }
  return [...found.keys()];
}

async function registryJson(suffix) {
  const url = registry + suffix;
  const res = await fetch(url, {
    headers: { accept: "application/json" },
    redirect: "error",
    signal: AbortSignal.timeout(60000),
  });
  if (res.status !== 200) throw new Error("registry HTTP " + res.status + " for " + suffix);
  const length = Number(res.headers.get("content-length") || "0");
  if (length > REGISTRY_BODY_LIMIT) throw new Error("registry body too large: " + suffix);
  const text = await res.text();
  if (Buffer.byteLength(text) > REGISTRY_BODY_LIMIT) throw new Error("registry body too large: " + suffix);
  return JSON.parse(text);
}

function cohortDigest(overrides) {
  const ordered = [...overrides.entries()].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  let canonical = registry + "\n" + version;
  for (const [name, value] of ordered) canonical += "\n" + name + "=" + value;
  return sha256(canonical);
}

function readCachedCohort() {
  if (!fs.existsSync(cacheFile)) return null;
  try {
    const saved = JSON.parse(fs.readFileSync(cacheFile, "utf8"));
    const values = saved.overrides;
    const now = Date.now();
    const checkedAt = Number(saved.checkedAtEpochMillis || 0);
    if (saved.schemaVersion !== 1 || saved.complete !== true || saved.package !== PACKAGE || saved.version !== version ||
        saved.registry !== registry || !values || typeof values !== "object" || Array.isArray(values) ||
        Object.keys(values).length > MAX_COHORT || saved.count !== Object.keys(values).length ||
        !(checkedAt > 0) || checkedAt > now + 5 * 60 * 1000 || now - checkedAt > TTL_MS) {
      throw new Error("expired or schema mismatch");
    }
    const overrides = new Map();
    for (const [name, value] of Object.entries(values)) {
      if (!name.startsWith("@deepseek-ai/dsh-") || !validPackageName(name) || value !== version) throw new Error("bad entry " + name);
      overrides.set(name, value);
    }
    if (cohortDigest(overrides) !== saved.sha256) throw new Error("digest mismatch");
    return overrides;
  } catch (e) {
    console.error("[cohort] ignoring cache " + cacheFile + ": " + e.message);
    return null;
  }
}

function saveCohort(overrides) {
  const saved = {
    schemaVersion: 1,
    complete: true,
    package: PACKAGE,
    version,
    registry,
    checkedAt: new Date().toISOString(),
    checkedAtEpochMillis: Date.now(),
    count: overrides.size,
    overrides: Object.fromEntries([...overrides.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1))),
    sha256: cohortDigest(overrides),
  };
  fs.mkdirSync(path.dirname(cacheFile), { recursive: true, mode: 0o700 });
  const tmp = cacheFile + ".tmp-" + crypto.randomUUID();
  fs.writeFileSync(tmp, JSON.stringify(saved, null, 2) + "\n", { mode: 0o600 });
  fs.renameSync(tmp, cacheFile);
}

async function resolveCohort() {
  const cached = readCachedCohort();
  if (cached) {
    console.log("[cohort] cache hit: " + cached.size + " pinned packages (" + cacheFile + ")");
    return cached;
  }
  console.log("[cohort] resolving release cohort for " + PACKAGE + "@" + version + " via " + registry);
  const entry = await registryJson("/" + PACKAGE.replace("/", "%2f") + "/" + version);
  if (entry.name !== PACKAGE || entry.version !== version) throw new Error("release manifest name/version mismatch");
  const visited = new Set();
  const overrides = new Map();
  let pending = cohortDependencies(entry);
  let checked = 0;
  while (pending.length > 0) {
    const batch = pending.filter((name) => !visited.has(name));
    pending = [];
    for (let i = 0; i < batch.length; i += 6) {
      const chunk = batch.slice(i, i + 6);
      const manifests = await Promise.all(chunk.map(async (name) => {
        if (visited.has(name)) return null;
        visited.add(name);
        if (visited.size > MAX_COHORT) throw new Error("cohort exceeds " + MAX_COHORT + " packages");
        const manifest = await registryJson("/" + name.replace("/", "%2f") + "/" + version);
        if (manifest.name !== name || manifest.version !== version) throw new Error("cohort manifest mismatch: " + name);
        return { name, manifest };
      }));
      for (const item of manifests) {
        if (!item) continue;
        overrides.set(item.name, version);
        checked++;
        for (const dep of cohortDependencies(item.manifest)) {
          if (!visited.has(dep) && !pending.includes(dep)) pending.push(dep);
        }
      }
      if (checked % 12 === 0 && pending.length > 0) console.log("[cohort] checked " + checked + ", pending " + pending.length);
    }
    if (visited.size > MAX_COHORT) throw new Error("cohort exceeds " + MAX_COHORT + " packages");
  }
  saveCohort(overrides);
  console.log("[cohort] pinned " + overrides.size + " same-release packages");
  return overrides;
}

const overrides = await resolveCohort();

const pkg = { name: "hdsl-runtime", private: true, dependencies: { [PACKAGE]: version } };
if (overrides.size > 0) pkg.pnpm = { overrides: Object.fromEntries([...overrides.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1))) };
fs.writeFileSync(path.join(staging, "package.json"), JSON.stringify(pkg, null, 2) + "\n", { mode: 0o600 });
const workspaceYaml = [
  "autoInstallPeers: true",
  "nodeLinker: hoisted",
  "resolutionMode: time-based",
  "strictPeerDependencies: false",
  "dangerouslyAllowAllBuilds: true",
  "",
].join("\n");
fs.writeFileSync(path.join(staging, "pnpm-workspace.yaml"), workspaceYaml, { mode: 0o600 });
console.log("[cohort] wrote " + path.join(staging, "package.json") + " and pnpm-workspace.yaml");
