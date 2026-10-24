// PCL-DSH-Go M2-E | Windows Edge headless CDP client (run with Windows node).
// Injects the manager JSESSIONID cookie, opens /workbench/{id}/ through Nginx,
// asserts SPA render + observes the /api/remote.mux WebSocket, screenshots the page.
//
// Env: CDP_PORT (9223), CDP_URL (required, no token in URL), CDP_COOKIE (0600 file
// containing the JSESSIONID value), CDP_OUT (PNG path, required),
// CDP_COOKIE_DOMAIN (default 127.0.0.1), CDP_EXPECT_TITLE (default "DeepSeek Harness"),
// CDP_WAIT_MS (default 2500), CDP_RESULT_OUT (optional JSON result path).
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const PORT = process.env.CDP_PORT || "9223";
const TARGET_URL = process.env.CDP_URL || "";
const COOKIE_FILE = process.env.CDP_COOKIE || "";
const OUT = process.env.CDP_OUT || "";
const RESULT_OUT = process.env.CDP_RESULT_OUT || "";
const DOMAIN = process.env.CDP_COOKIE_DOMAIN || "127.0.0.1";
const EXPECT_TITLE = process.env.CDP_EXPECT_TITLE || "DeepSeek Harness";
const WAIT_MS = Number(process.env.CDP_WAIT_MS || "2500");

const failures = [];
function fail(msg) { failures.push(msg); }
function mask(text) {
  return String(text == null ? "" : text)
    .replace(/([?&]token=)[^&\s"']+/g, "$1***")
    .replace(/(JSESSIONID|XSRF-TOKEN|dsh-auth-[A-Za-z0-9]+)=[^;,\s"']+/g, "$1=***");
}
function finish(code) {
  const json = JSON.stringify(result, null, 2);
  if (RESULT_OUT) { try { writeFileSync(RESULT_OUT, json); } catch (e) { /* best effort */ } }
  console.log(json);
  process.exit(code);
}
const result = { ok: false, title: null, url: null, rootTextLength: 0, rootTextSnippet: "",
  dshBoot: null, ws101: false, wsUrls: [], wsHandshakes: [], consoleErrors: [], pageExceptions: [],
  screenshot: { path: OUT, bytes: 0, width: 0, height: 0 }, loadMs: null, failures };
if (!TARGET_URL) { fail("CDP_URL missing"); finish(2); }
if (!COOKIE_FILE) { fail("CDP_COOKIE missing"); finish(2); }
if (!OUT) { fail("CDP_OUT missing"); finish(2); }

let cookieValue = "";
try {
  cookieValue = readFileSync(COOKIE_FILE, "utf8").trim();
  if (cookieValue.startsWith("JSESSIONID=")) cookieValue = cookieValue.slice("JSESSIONID=".length);
} catch (e) { fail("cookie file unreadable: " + e.message); finish(2); }
if (!cookieValue) { fail("cookie value empty"); finish(2); }

async function getTargets() {
  let lastError = null;
  for (let i = 0; i < 30; i += 1) {
    try {
      const res = await fetch("http://127.0.0.1:" + PORT + "/json/list");
      const list = await res.json();
      const page = list.find((t) => t.type === "page");
      if (page) return page;
      lastError = new Error("no page target");
    } catch (e) { lastError = e; }
    await sleep(300);
  }
  throw lastError || new Error("no CDP target");
}

let page;
try { page = await getTargets(); } catch (e) { fail("CDP target unavailable: " + e.message); finish(2); }
const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const events = [];
const t0 = Date.now();
ws.onmessage = (ev) => {
  let msg;
  try { msg = JSON.parse(ev.data); } catch (e) { return; }
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); return; }
  events.push(msg);
};
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
function send(method, params = {}) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, (msg) => (msg.error ? reject(new Error(method + ": " + JSON.stringify(msg.error))) : resolve(msg.result)));
    ws.send(JSON.stringify({ id, method, params }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error(method + ": timeout")); } }, 30000);
  });
}

try {
  await send("Page.enable");
  await send("Runtime.enable");
  await send("Network.enable");
  await send("Log.enable");
  const cookie = await send("Network.setCookie", {
    name: "JSESSIONID", value: cookieValue, domain: DOMAIN, path: "/",
    httpOnly: true, secure: false, sameSite: "Lax",
  });
  if (!cookie || cookie.success !== true) fail("Network.setCookie did not succeed: " + JSON.stringify(cookie));
  const loaded = new Promise((res) => {
    const timer = setInterval(() => {
      if (events.some((e) => e.method === "Page.loadEventFired")) { clearInterval(timer); res(true); }
    }, 50);
    setTimeout(() => { clearInterval(timer); res(false); }, 25000);
  });
  await send("Page.navigate", { url: TARGET_URL });
  const didLoad = await loaded;
  if (!didLoad) fail("Page.loadEventFired timeout");
  await sleep(WAIT_MS);
  result.loadMs = Date.now() - t0;

  const evaluates = {
    title: "document.title",
    url: "location.href",
    rootText: "(function(){var r=document.querySelector('#root');var b=document.body;var t=(r&&r.innerText)||(b&&b.innerText)||'';return t.slice(0,600);})()",
    boot: "(function(){try{var b=window.__DSH_BOOT__;if(!b)return {present:false};var rd=b.release||b.version||null;var ks=Object.keys(b).slice(0,10);return {present:true,release:String(rd||''),keys:ks};}catch(e){return {present:false,error:String(e)};}})()",
  };
  const values = {};
  for (const [key, expression] of Object.entries(evaluates)) {
    const r = await send("Runtime.evaluate", { expression, returnByValue: true });
    values[key] = r && r.result ? r.result.value : undefined;
  }
  result.title = values.title == null ? null : String(values.title);
  result.url = mask(values.url);
  const rootText = values.rootText == null ? "" : String(values.rootText);
  result.rootTextLength = rootText.trim().length;
  result.rootTextSnippet = mask(rootText.replace(/\s+/g, " ").trim().slice(0, 240));
  result.dshBoot = values.boot && typeof values.boot === "object" ? values.boot : null;

  const createdById = new Map();
  for (const ev of events) {
    if (ev.method === "Network.webSocketCreated" && ev.params) {
      const url = mask(ev.params.url || "");
      createdById.set(ev.params.requestId, url);
      if (url.indexOf("/api/remote.mux") >= 0 && result.wsUrls.indexOf(url) < 0) result.wsUrls.push(url);
    }
  }
  const wsHandshakes = () => {
    const list = [];
    for (const ev of events) {
      if (ev.method === "Network.webSocketHandshakeResponseReceived" && ev.params && ev.params.response) {
        const response = ev.params.response;
        const url = mask(response.url || createdById.get(ev.params.requestId) || "");
        list.push({ url, status: response.status || 0 });
        if (url.indexOf("/api/remote.mux") >= 0 && response.status === 101) result.ws101 = true;
      }
    }
    return list;
  };
  const wsDeadline = Date.now() + Number(process.env.CDP_WS_WAIT_MS || "8000");
  while (Date.now() < wsDeadline && !result.ws101) {
    if (wsHandshakes().some((h) => h.url.indexOf("/api/remote.mux") >= 0 && h.status === 101)) break;
    await sleep(150);
  }
  result.wsHandshakes = wsHandshakes();
  for (const ev of events) {
    if (ev.method === "Runtime.consoleAPICalled" && ev.params && ev.params.type === "error") {
      result.consoleErrors.push(mask((ev.params.args || []).map((a) => a.value || a.description || "").join(" ")).slice(0, 200));
    }
    if (ev.method === "Runtime.exceptionThrown") {
      const d = ev.params && ev.params.exceptionDetails;
      result.pageExceptions.push(mask((d && (d.text || (d.exception && d.exception.description))) || "").slice(0, 200));
    }
  }

  const shot = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  const buf = Buffer.from(shot.data, "base64");
  mkdirSync(dirname(OUT), { recursive: true });
  writeFileSync(OUT, buf);
  result.screenshot = { path: OUT, bytes: buf.length, width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };

  if (result.title !== EXPECT_TITLE) fail("title mismatch: " + JSON.stringify(result.title) + " != " + JSON.stringify(EXPECT_TITLE));
  if (result.rootTextLength <= 0) fail("page root text empty");
  if (!result.dshBoot || result.dshBoot.present !== true) fail("window.__DSH_BOOT__ missing");
  if (result.screenshot.bytes < 5000) fail("screenshot too small: " + result.screenshot.bytes + " bytes");
  if (result.screenshot.width < 200 || result.screenshot.height < 200) fail("screenshot dimensions too small");
  result.ok = failures.length === 0;
  ws.close();
  finish(result.ok ? 0 : 2);
} catch (e) {
  fail("exception: " + (e && e.message ? e.message : String(e)));
  try { ws.close(); } catch (e2) { /* ignore */ }
  finish(2);
}
