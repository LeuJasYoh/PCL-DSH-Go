#!/usr/bin/env node
// PCL-DSH-Go M0-5 | validate an installed DSH runtime directory and print its CLI entry.
// Usage: node check-runtime.mjs <runtime-dir> <version>
import fs from "node:fs";
import path from "node:path";

const [runtime, version] = process.argv.slice(2);
if (!runtime || !version) throw new Error("usage: check-runtime.mjs <runtime-dir> <version>");
const packageDir = path.join(runtime, "node_modules", "@deepseek-ai", "dsh");
const manifestPath = path.join(packageDir, "package.json");
if (!fs.existsSync(manifestPath)) throw new Error("runtime not installed: " + manifestPath);
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
if (manifest.name !== "@deepseek-ai/dsh") throw new Error("unexpected package name: " + manifest.name);
if (manifest.version !== version) throw new Error("unexpected version: " + manifest.version + " (want " + version + ")");
const bin = manifest.bin;
const relative = typeof bin === "string" ? bin : bin && typeof bin === "object" ? bin.dsh : "";
if (!relative || typeof relative !== "string") throw new Error("no dsh CLI entry declared in package.json bin");
const script = path.resolve(packageDir, relative);
const realPackage = fs.realpathSync(packageDir);
const realScript = fs.existsSync(script) ? fs.realpathSync(script) : null;
if (!script.startsWith(packageDir + path.sep)) throw new Error("CLI entry escapes package dir: " + script);
if (!realScript || !fs.statSync(realScript).isFile()) throw new Error("CLI entry missing: " + script);
if (!realScript.startsWith(realPackage + path.sep)) throw new Error("CLI entry realpath escapes package dir: " + realScript);
const size = fs.statSync(realScript).size;
const deps = Object.keys(manifest.dependencies || {}).length;
console.log("runtime=" + runtime);
console.log("package=" + manifest.name + "@" + manifest.version);
console.log("entry=" + realScript);
console.log("entry_bytes=" + size);
console.log("manifest_dependencies=" + deps);
if (manifest.engines && manifest.engines.node) console.log("engines_node=" + manifest.engines.node);
