#!/usr/bin/env node
// PCL-DSH-Go plugin bridge (docs/DSH_INTEGRATION.md 8).
//
// Routes the DSH CLI's pnpm spawns to the bundled pnpm.cjs and rejects shell
// based plugin commands: if a DSH release uses an unsupported shell command the
// bridge fails closed instead of executing it.
import { createRequire, syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
import { basename } from "node:path";

const [pnpmScript, dshEntry, ...dshArgs] = process.argv.slice(2);
if (!pnpmScript || !dshEntry) {
  throw new Error("HDSL plugin bridge requires explicit pnpm.cjs and dsh entry paths");
}
const child = createRequire(import.meta.url)("node:child_process");
for (const name of ["spawnSync", "spawn"]) {
  const original = child[name];
  child[name] = function (command, argv, options) {
    const leaf = typeof command === "string" ? basename(command).toLowerCase() : "";
    if ((leaf === "pnpm" || leaf === "pnpm.cmd" || leaf === "pnpm.exe") && Array.isArray(argv)) {
      return original.call(child, process.execPath, [pnpmScript, ...argv], { ...options, shell: false });
    }
    if (options?.shell || /^(cmd(?:\.exe)?|powershell(?:\.exe)?|pwsh(?:\.exe)?|sh|bash)$/.test(leaf)) {
      throw new Error("HDSL cannot safely route this new shell-based plugin command; an adapter update is required");
    }
    return original.apply(child, arguments);
  };
}
child.exec = child.execSync = () => {
  throw new Error("HDSL plugin bridge rejects unstructured shell commands");
};
syncBuiltinESMExports();

process.argv = [process.execPath, dshEntry, ...dshArgs];
const cli = await import(pathToFileURL(dshEntry).href);
if (typeof cli.runCli === "function") {
  await cli.runCli({ packageManager: { command: process.execPath, args: [pnpmScript] } });
}
