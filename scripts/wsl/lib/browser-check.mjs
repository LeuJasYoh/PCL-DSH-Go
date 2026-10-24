#!/usr/bin/env node
// PCL-DSH-Go M0-10 | real-browser workbench check with Playwright (headless Chromium).
// Opens the manager workbench URL (no login in M0 dev mode), follows the 302 -> DSH 303 ->
// page flow, asserts title/#root rendering, records console errors (known 401 noise vs other),
// network status histogram and the /api/remote.mux WebSocket, and writes a screenshot.
//
// Usage: node browser-check.mjs --url <workbench url> --png <path> --json-out <path>
// Env: PLAYWRIGHT_BROWSERS_PATH must point at the installed browser root.
// Output is sanitized: token=... / dsh-auth-* / v1.* values are replaced by ***.
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
// The Playwright package lives under <test-root>/tools/web/node_modules, not in the repo;
// resolve it through an explicit require base so the helper can stay in scripts/wsl/lib.
const requireBase = process.env.PLAYWRIGHT_REQUIRE_BASE || path.join(process.cwd(), "package.json");
const { chromium } = createRequire(requireBase)("playwright");

function parseArgs(argv) {
  const out = {};
  for (let i = 2; i < argv.length; i++) {
    if (!argv[i].startsWith("--")) throw new Error("unexpected argument: " + argv[i]);
    out[argv[i].slice(2)] = argv[++i];
  }
  return out;
}
function sanitize(text) {
  return String(text)
    .replace(/([?&]token=)[^&\s"'<>]+/gi, "$1***")
    .replace(/dsh-auth-[A-Za-z0-9]+=[^;,\s"']*/g, "dsh-auth-***=***")
    .replace(/sk-[A-Za-z0-9_-]{12,}/g, "***");
}
function maskUrl(value) {
  return sanitize(String(value)).replace(/([?&]token=)[^&]*/gi, "$1***");
}

const args = parseArgs(process.argv);
if (!args.url || !args.png) {
  console.error("usage: browser-check.mjs --url <workbench url> --png <file> [--json-out <file>]");
  process.exit(4);
}
const result = {
  url: maskUrl(args.url),
  finalUrl: "",
  title: "",
  rootTextLength: 0,
  bodyTextLength: 0,
  rootSelector: "#root",
  durationMs: 0,
  responses: { total: 0, status2xx: 0, status3xx: 0, status401: 0, status403: 0, status4xxOther: 0, status5xx: 0 },
  statusSamples: [],
  consoleErrors: [],
  consoleErrorCount: 0,
  known401NoiseCount: 0,
  otherConsoleErrorCount: 0,
  pageErrors: [],
  requestFailures: [],
  websockets: [],
  pngBytes: 0,
  failures: [],
  error: null,
};
const started = Date.now();
const browser = await chromium.launch({ headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on("console", (message) => {
    if (message.type() !== "error") return;
    const text = sanitize(message.text());
    result.consoleErrorCount++;
    if (/401|unauthorized/i.test(text)) result.known401NoiseCount++;
    else result.otherConsoleErrorCount++;
    if (result.consoleErrors.length < 40) result.consoleErrors.push(text);
  });
  page.on("pageerror", (error) => {
    if (result.pageErrors.length < 20) result.pageErrors.push(sanitize(String(error)));
  });
  page.on("requestfailed", (request) => {
    if (result.requestFailures.length < 20) result.requestFailures.push(sanitize(request.method() + " " + maskUrl(request.url()) + " :: " + (request.failure() ? request.failure().errorText : "failed")));
  });
  page.on("response", (response) => {
    const status = response.status();
    result.responses.total++;
    if (status >= 200 && status < 300) result.responses.status2xx++;
    else if (status >= 300 && status < 400) result.responses.status3xx++;
    else if (status === 401) result.responses.status401++;
    else if (status === 403) result.responses.status403++;
    else if (status >= 400 && status < 500) result.responses.status4xxOther++;
    else if (status >= 500) result.responses.status5xx++;
    if (result.statusSamples.length < 60) result.statusSamples.push(status + " " + sanitize(response.request().method() + " " + maskUrl(response.url())).slice(0, 160));
  });
  page.on("websocket", (ws) => {
    const entry = { url: maskUrl(ws.url()), framesReceived: 0, framesSent: 0, closed: false };
    ws.on("framereceived", () => { entry.framesReceived++; });
    ws.on("framesent", () => { entry.framesSent++; });
    ws.on("close", () => { entry.closed = true; });
    result.websockets.push(entry);
  });

  await page.goto(args.url, { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(4000);
  result.finalUrl = maskUrl(page.url());
  result.title = await page.title();
  result.rootTextLength = await page.evaluate(() => {
    const element = document.querySelector("#root");
    return element ? (element.innerText || "").trim().length : 0;
  });
  result.bodyTextLength = await page.evaluate(() => (document.body ? (document.body.innerText || "").trim().length : 0));
  await page.screenshot({ path: args.png, fullPage: true });
  result.pngBytes = fs.existsSync(args.png) ? fs.statSync(args.png).size : 0;

  if (result.title !== "DeepSeek Harness") result.failures.push("title=" + JSON.stringify(result.title));
  if (result.rootTextLength < 10) result.failures.push("#root visible text too small: " + result.rootTextLength);
  if (result.pngBytes < 1024) result.failures.push("screenshot missing/too small: " + result.pngBytes + " bytes");
  if (result.pageErrors.length > 0) result.failures.push("uncaught page errors: " + result.pageErrors.length);
  if (result.otherConsoleErrorCount > 0) result.failures.push("non-401 console errors: " + result.otherConsoleErrorCount);
  const mux = result.websockets.find((ws) => /\/api\/remote\.mux/.test(ws.url));
  if (!mux) result.failures.push("no /api/remote.mux WebSocket observed");
} catch (error) {
  result.error = sanitize(String(error && error.message ? error.message : error));
  result.failures.push("browser error: " + result.error);
} finally {
  try { await browser.close(); } catch (e) {}
}
result.durationMs = Date.now() - started;
const json = JSON.stringify(result, null, 2) + "\n";
if (args["json-out"]) {
  fs.mkdirSync(path.dirname(args["json-out"]), { recursive: true });
  fs.writeFileSync(args["json-out"], json, { mode: 0o600 });
}
console.log("browser: url=" + result.url + " final_url=" + result.finalUrl);
console.log("browser: title=" + JSON.stringify(result.title) + " root_text_len=" + result.rootTextLength + " body_text_len=" + result.bodyTextLength + " duration_ms=" + result.durationMs);
console.log("browser: responses=" + JSON.stringify(result.responses) + " screenshot_bytes=" + result.pngBytes);
console.log("browser: console_errors=" + result.consoleErrorCount + " known_401_noise=" + result.known401NoiseCount + " other_errors=" + result.otherConsoleErrorCount);
if (result.consoleErrors.length) console.log("browser: console_error_samples=" + JSON.stringify(result.consoleErrors.slice(0, 10)));
if (result.pageErrors.length) console.log("browser: page_errors=" + JSON.stringify(result.pageErrors.slice(0, 10)));
if (result.requestFailures.length) console.log("browser: request_failures=" + JSON.stringify(result.requestFailures.slice(0, 10)));
console.log("browser: websockets=" + JSON.stringify(result.websockets.slice(0, 5)));
console.log("browser: failures=" + JSON.stringify(result.failures));
process.exit(result.failures.length === 0 ? 0 : 2);
