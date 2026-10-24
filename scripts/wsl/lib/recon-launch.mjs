#!/usr/bin/env node
// PCL-DSH-Go M0-5 | launch/ready/token/cookie/HTTP/SSE/WS recon against a real DSH runtime.
// Spawns DSH with the manager's white-listed environment, keeps the one-time bootstrap
// token and cookie in memory only, and writes sanitized findings under --outdir.
//
// Required env: DSH_NODE, DSH_ENTRY, DSH_VERSION
// Usage: node recon-launch.mjs --root /tmp/pcl-dsh-go-test --outdir <dir>
import fs from "node:fs";
import path from "node:path";
import net from "node:net";
import crypto from "node:crypto";
import { spawn, execFileSync } from "node:child_process";

function parseArgs(argv) {
  const out = {};
  for (let i = 2; i < argv.length; i++) {
    if (!argv[i].startsWith("--")) throw new Error("unexpected argument: " + argv[i]);
    out[argv[i].slice(2)] = argv[++i];
  }
  return out;
}
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const args = parseArgs(process.argv);
const ROOT = args.root || "/tmp/pcl-dsh-go-test";
const OUT = args.outdir || path.join(ROOT, "logs", "launch");
const NODE = process.env.DSH_NODE;
const ENTRY = process.env.DSH_ENTRY;
const VERSION = process.env.DSH_VERSION || "0.2.1-rc.3";
const REGISTRY = process.env.DSH_REGISTRY || "https://registry.npmjs.org";
const PORT = Number(process.env.DSH_PORT || "3080");
const TOOLS = process.env.PCL_DSH_TOOLS || "/opt/pcl-dsh-go/tools";
const BASE = "http://127.0.0.1:" + PORT;
if (!NODE || !ENTRY) throw new Error("DSH_NODE and DSH_ENTRY are required");

fs.rmSync(OUT, { recursive: true, force: true });
fs.mkdirSync(OUT, { recursive: true, mode: 0o700 });
const findings = [];
const note = (key, value) => findings.push(key + "=" + String(value));
const failures = [];

function sanitize(text) {
  return String(text)
    .replace(/([?&]token=)[^&\s"'<>]+/gi, "$1***")
    .replace(/\btoken=[^&\s"'<>]+/gi, "token=***")
    .replace(/dsh-auth-[A-Za-z0-9]+=[^;,\s"']*/g, "dsh-auth-***=***")
    .replace(/(cookie:\s*)[^\r\n]+/gi, "$1***")
    .replace(/(authorization:\s*)[^\r\n]+/gi, "$1***")
    .replace(/(set-cookie:\s*)[^\r\n]+/gi, "$1***")
    .replace(/sk-[A-Za-z0-9_-]{12,}/g, "***");
}
function append(file, text) {
  fs.appendFileSync(path.join(OUT, file), sanitize(String(text).replace(/\n$/, "")) + "\n");
}
function writeFile(file, text) {
  fs.writeFileSync(path.join(OUT, file), sanitize(text), { mode: 0o600 });
}
const stamp = () => new Date().toISOString();
function logLine(file, text) {
  append(file, stamp() + " " + text);
}

function buildEnv(runDir) {
  // Mirrors docs/DSH_INTEGRATION.md section 1.2; intentionally starts from a blank env
  // (only the documented white-list). No HOME, no inherited proxy, no account keys.
  return {
    DSH_HOME: path.join(runDir, "dsh-home"),
    DSH_AGENTS_HOME: path.join(runDir, "agents-home"),
    HDSL_NPM_REGISTRY: REGISTRY,
    NPM_CONFIG_REGISTRY: REGISTRY,
    NPM_CONFIG_CACHE: path.join(ROOT, "cache", "npm"),
    PNPM_HOME: path.join(TOOLS, "pnpm"),
    PNPM_STORE_DIR: path.join(ROOT, "cache", "pnpm-store"),
    NPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS: "false",
    NPM_CONFIG_PACKAGE_MANAGER_STRICT: "false",
    PNPM_CONFIG_AUTO_INSTALL_PEERS: "true",
    XDG_CACHE_HOME: path.join(ROOT, "cache"),
    DSH_TELEMETRY_MODE: "DISABLED",
    PATH: [path.dirname(NODE), path.join(ROOT, "tools", "shims"), path.join(TOOLS, "pnpm"), "/usr/local/bin:/usr/bin:/bin"].join(":"),
  };
}

function launchArgs(profile) {
  return [ENTRY, "--profile", profile, "--port", String(PORT), "--host", "127.0.0.1", "--no-open"];
}

function httpRequest(pathname, options) {
  const opts = options || {};
  const headers = Object.assign({}, opts.headers || {});
  if (opts.cookie) headers.Cookie = opts.cookie;
  return fetch(BASE + pathname, {
    method: opts.method || "GET",
    redirect: "manual",
    headers,
    signal: AbortSignal.timeout(opts.timeout || 5000),
  });
}

function headerDump(res) {
  const lines = [];
  for (const [name, value] of res.headers) {
    if (/^set-cookie$/i.test(name)) {
      for (const cookie of res.headers.getSetCookie()) lines.push("set-cookie(masked): " + maskSetCookie(cookie));
    } else if (/^(content-type|content-length|cache-control|location|x-frame-options|content-security-policy|referrer-policy|server|vary|allow)$/i.test(name)) {
      lines.push(name + ": " + (name === "location" ? maskLocation(value) : value));
    }
  }
  return lines.join("\n");
}

function maskLocation(value) {
  return String(value).replace(/([?&]token=)[^&\s]+/gi, "$1***");
}

function maskSetCookie(raw) {
  // Keep cookie attributes for the contract check, mask the cookie name/value pair.
  const parts = String(raw).split(";");
  const attrs = parts.slice(1).map((s) => s.trim()).filter(Boolean);
  const pair = parts[0] || "";
  const eq = pair.indexOf("=");
  const name = eq > 0 ? pair.slice(0, eq) : pair;
  const maskedName = /^dsh-auth-[A-Za-z0-9]+$/.test(name) ? "dsh-auth-***" : name;
  return maskedName + "=***" + (attrs.length ? "; " + attrs.join("; ") : "");
}

function parseSetCookie(raw) {
  const parts = String(raw).split(";");
  const pair = (parts[0] || "").trim();
  const eq = pair.indexOf("=");
  const name = eq > 0 ? pair.slice(0, eq) : "";
  const value = eq > 0 ? pair.slice(eq + 1) : "";
  const attrs = {};
  for (const attr of parts.slice(1)) {
    const trimmed = attr.trim();
    if (!trimmed) continue;
    const eq2 = trimmed.indexOf("=");
    if (eq2 < 0) attrs[trimmed.toLowerCase()] = true;
    else attrs[trimmed.slice(0, eq2).toLowerCase()] = trimmed.slice(eq2 + 1);
  }
  return { name, value, attrs, raw };
}

function cookieHeaderFrom(setCookie) {
  return setCookie.name + "=" + setCookie.value;
}

function firstHeader(res, name) {
  return res.headers.get(name) || "";
}

let current = null;
function handleLine(rawLine, source) {
  const line = rawLine.replace(/\u001b\[[0-9;]*m/g, "");
  if (!current) return;
  const elapsed = Date.now() - current.startedAt;
  const safe = sanitize(line);
  current.logs.push((elapsed / 1000).toFixed(2) + "s " + source + " | " + safe);
  if (current.logs.length > 4000) current.logs.shift();
  append("01-startup.log", (elapsed / 1000).toFixed(2) + "s " + source + " | " + safe);
  if (current.tokenUri) return;
  const marker = line.indexOf("dsh web:");
  if (marker < 0) return;
  const candidate = line.slice(marker + 8).trim().split(/\s+/)[0];
  let parsed;
  try {
    parsed = new URL(candidate);
  } catch (e) {
    current.tokenLineInvalid = candidate;
    return;
  }
  const hostOk = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(parsed.hostname);
  if (parsed.protocol !== "http:" || !hostOk || Number(parsed.port) !== PORT || parsed.username || parsed.password) {
    current.tokenLineInvalid = sanitize(candidate);
    note("token_line_rejected", "scheme/host/port/userinfo mismatch: " + sanitize(candidate));
    return;
  }
  current.tokenUri = parsed;
  current.tokenAt = Date.now();
  note("token_found", true);
  note("token_elapsed_ms", Date.now() - current.startedAt);
  note("token_uri_shape", "http://" + parsed.hostname + ":" + parsed.port + parsed.pathname + (parsed.search ? "?" + parsed.search.slice(1).split("&").map((p) => p.split("=")[0] + "=***").join("&") : ""));
  note("token_query_keys", [...parsed.searchParams.keys()].join(","));
  const tokenValue = parsed.searchParams.get("token") || "";
  note("token_length", tokenValue.length);
  note("token_path", parsed.pathname);
}

async function startInstance(runDir) {
  fs.rmSync(runDir, { recursive: true, force: true });
  fs.mkdirSync(path.join(runDir, "dsh-home"), { recursive: true, mode: 0o700 });
  fs.mkdirSync(path.join(runDir, "agents-home"), { recursive: true, mode: 0o700 });
  fs.mkdirSync(path.join(runDir, "workspace"), { recursive: true, mode: 0o700 });
  const env = buildEnv(runDir);
  note("launched_env_keys", Object.keys(env).sort().join(","));
  note("launched_env_HOME", env.HOME ? "present" : "absent");
  const child = spawn(NODE, launchArgs("web"), {
    cwd: path.join(runDir, "workspace"),
    env,
    detached: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const state = {
    child,
    startedAt: Date.now(),
    tokenUri: null,
    tokenAt: 0,
    tokenLineInvalid: null,
    logs: [],
    statuses: [],
    exit: null,
    exited: null,
  };
  state.exited = new Promise((resolve) => {
    child.on("exit", (code, signal) => {
      state.exit = { code, signal, at: Date.now(), elapsedMs: Date.now() - state.startedAt };
      resolve(state.exit);
    });
  });
  current = state;
  note("pid", child.pid);
  append("01-startup.log", "launch: " + sanitize([NODE, ...launchArgs("web")].join(" ")));
  append("01-startup.log", "cwd: " + path.join(runDir, "workspace"));
  let buffer = "";
  const onData = (chunk, source) => {
    buffer += chunk.toString("utf8");
    let idx;
    while ((idx = buffer.indexOf("\n")) >= 0) {
      handleLine(buffer.slice(0, idx), source);
      buffer = buffer.slice(idx + 1);
    }
  };
  child.stdout.on("data", (c) => onData(c, "out"));
  child.stderr.on("data", (c) => onData(c, "err"));
  await sleep(120);
  return state;
}

async function waitReady(state, timeoutMs) {
  const limit = timeoutMs || 90000;
  const started = Date.now();
  while (Date.now() - started < limit) {
    if (state.exit) throw new Error("process exited before ready: code=" + state.exit.code + " signal=" + state.exit.signal);
    const at = Date.now() - started;
    try {
      const res = await fetch(BASE + "/", { redirect: "manual", signal: AbortSignal.timeout(2000) });
      state.statuses.push({ at, status: res.status });
      const code = res.status;
      if ((code >= 200 && code < 400) || ((code === 401 || code === 403) && state.tokenUri)) {
        state.readyMs = Date.now() - started;
        return true;
      }
    } catch (e) {
      state.statuses.push({ at, error: String(e && e.cause ? e.cause : e).slice(0, 100) });
    }
    await sleep(400);
  }
  throw new Error("ready timeout after " + limit + "ms");
}

function describeStatuses(statuses) {
  return statuses.map((s) => (s.error ? s.at + "ms:ERR(" + s.error + ")" : s.at + "ms:" + s.status)).join(" ");
}

async function httpProbe(cookie) {
  const lines = [];
  const record = async (label, pathname, options) => {
    const started = Date.now();
    try {
      const res = await httpRequest(pathname, options);
      const body = await res.text();
      const elapsed = Date.now() - started;
      lines.push("### " + label + " (GET " + pathname + ")");
      lines.push("status=" + res.status + " elapsed_ms=" + elapsed);
      lines.push(headerDump(res));
      lines.push("body_bytes=" + Buffer.byteLength(body));
      lines.push("body_head=" + JSON.stringify(body.slice(0, 240).replace(/\s+/g, " ")));
      return { res, body, elapsed };
    } catch (e) {
      lines.push("### " + label + " (GET " + pathname + ")");
      lines.push("request_error=" + String(e && e.cause ? e.cause : e));
      return null;
    }
  };
  const noAuth = await record("anonymous-root", "/", { cookie: null });
  const invalidToken = await record("invalid-token", "/?token=invalid-recon-token", { cookie: null });
  return { lines, noAuth, invalidToken };
}

// Contract-relevant edge behaviour: token scope, HEAD, static gating, unknown API paths.
async function extraHttpChecks(cookie, token, assetPath) {
  const lines = [];
  const statuses = {};
  const check = async (label, pathname, displayPath, options) => {
    try {
      const res = await httpRequest(pathname, options);
      const body = await res.text();
      lines.push("### " + label);
      lines.push("request=" + ((options && options.method) || "GET") + " " + displayPath);
      lines.push("status=" + res.status);
      lines.push(headerDump(res));
      lines.push("body_bytes=" + Buffer.byteLength(body));
      lines.push("body_head=" + JSON.stringify(sanitize(body.slice(0, 160).replace(/\s+/g, " "))));
      statuses[label] = res.status;
      return res;
    } catch (e) {
      lines.push("### " + label + " request_error=" + String(e && e.cause ? e.cause : e));
      statuses[label] = "ERR";
      return null;
    }
  };
  await check("head-root", "/", "/", { cookie, method: "HEAD" });
  await check("token-replay", "/?token=" + token, "/?token=***", { cookie: null });
  await check("token-on-non-root", "/index.html?token=" + token, "/index.html?token=***", { cookie: null });
  const staticPath = assetPath || "/favicon.svg";
  await check("anonymous-static", staticPath, staticPath, { cookie: null });
  await check("cookie-static", staticPath, staticPath, { cookie });
  await check("anonymous-api", "/api/recon-unknown", "/api/recon-unknown", { cookie: null });
  await check("cookie-api", "/api/recon-unknown", "/api/recon-unknown", { cookie });
  return { lines, statuses };
}

function extractHtmlFacts(html) {
  const facts = {};
  const title = /<title[^>]*>([\s\S]*?)<\/title>/i.exec(html);
  facts.title = title ? title[1].trim() : "";
  const base = /<base[^>]*href=["']([^"']*)["']/i.exec(html);
  facts.baseHref = base ? base[1] : "(none)";
  const scripts = [...html.matchAll(/<script[^>]*\bsrc=["']([^"']+)["']/gi)].map((m) => decodeEntities(m[1]));
  const links = [...html.matchAll(/<link[^>]*\bhref=["']([^"']+)["']/gi)].map((m) => decodeEntities(m[1]));
  facts.scripts = scripts;
  facts.links = links;
  const absolute = [...html.matchAll(/https?:\/\/(?:127\.0\.0\.1|localhost|\[::1\])(?::\d+)?[^"'\s<>]*/gi)].map((m) => m[0]);
  facts.absoluteRefs = absolute;
  facts.relativeScripts = scripts.filter((s) => !/^(?:[a-z]+:)?\/\//i.test(s) && s !== "");
  facts.absoluteScripts = scripts.filter((s) => /^(?:[a-z]+:)?\/\//i.test(s));
  facts.hasTokenInHtml = /token=/i.test(html);
  facts.hasManifest = /manifest\.webmanifest/i.test(html);
  facts.hasPluginUrls = /plugins\//i.test(html);
  facts.hasCsp = /content-security-policy/i.test(html);
  facts.bytes = Buffer.byteLength(html);
  return facts;
}

function resolveAssetUrl(ref) {
  try {
    return new URL(ref, BASE + "/").toString();
  } catch (e) {
    return null;
  }
}

function decodeEntities(value) {
  return String(value)
    .replace(/&amp;/g, "&")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">");
}

async function fetchAsset(url, cookie) {
  const started = Date.now();
  try {
    const res = await fetch(url, { redirect: "manual", headers: { Cookie: cookie }, signal: AbortSignal.timeout(8000) });
    const buf = Buffer.from(await res.arrayBuffer());
    return { url, status: res.status, contentType: firstHeader(res, "content-type"), bytes: buf.length, body: buf.toString("utf8"), ms: Date.now() - started };
  } catch (e) {
    return { url, error: String(e && e.cause ? e.cause : e), ms: Date.now() - started };
  }
}

function jsAnalysis(jsFiles) {
  const patterns = {
    remote_mux: /remote\.mux/g,
    websocket: /new\s+WebSocket/g,
    websocket_lower: /websocket/gi,
    event_stream: /text\/event-stream/g,
    event_source: /EventSource/g,
    ws_scheme: /wss?:\/\//g,
    absolute_loopback: /https?:\/\/(?:127\.0\.0\.1|localhost|\[::1\])(?::\d+)?/g,
    events_route: /EVENTS?_ROUTE|eventsRoute|eventStream/gi,
    api_prefix: /["'](\/api\/[A-Za-z0-9._\-\/]*)["']/g,
    sse_paths: /["'](\/[A-Za-z0-9._\-\/]*(?:event|stream|sse)[A-Za-z0-9._\-\/]*)["']/gi,
    ws_paths: /["'](\/[A-Za-z0-9._\-\/]*(?:mux|socket|ws)[A-Za-z0-9._\-\/]*)["']/gi,
    token_query: /[?&]token=/g,
  };
  const results = { apiPaths: new Set(), ssePaths: new Set(), wsPaths: new Set(), matches: {}, files: [] };
  for (const file of jsFiles) {
    if (file.error || !file.body) continue;
    results.files.push({ url: file.url, bytes: file.bytes, status: file.status });
    for (const [key, pattern] of Object.entries(patterns)) {
      pattern.lastIndex = 0;
      let match;
      let count = 0;
      const samples = [];
      while ((match = pattern.exec(file.body)) !== null) {
        count++;
        if (samples.length >= 6) continue;
        const index = Math.max(0, match.index - 120);
        const end = Math.min(file.body.length, match.index + 220);
        samples.push(JSON.stringify(file.body.slice(index, end).replace(/\s+/g, " ")));
        if (key === "api_prefix" && match[1]) results.apiPaths.add(match[1]);
        if (key === "sse_paths" && match[1]) results.ssePaths.add(match[1]);
        if (key === "ws_paths" && match[1]) results.wsPaths.add(match[1]);
        if (samples.length >= 6 && count > 6) break;
      }
      if (count > 0) {
        results.matches[key] = results.matches[key] || { total: 0, samples: [] };
        results.matches[key].total += count;
        results.matches[key].samples.push(...samples.map((s) => file.url + " :: " + s));
      }
    }
  }
  return results;
}

async function sseProbe(candidates, cookie) {
  const results = [];
  for (const candidate of candidates) {
    const started = Date.now();
    try {
      const res = await fetch(BASE + candidate, {
        redirect: "manual",
        headers: cookie ? { Cookie: cookie, Accept: "text/event-stream" } : { Accept: "text/event-stream" },
        signal: AbortSignal.timeout(3500),
      });
      const contentType = firstHeader(res, "content-type");
      const chunks = [];
      const chunkTimes = [];
      if (res.body) {
        const reader = res.body.getReader();
        const deadline = Date.now() + 1200;
        while (Date.now() < deadline) {
          const read = await Promise.race([reader.read(), sleep(1500).then(() => ({ timeout: true }))]);
          if (read.timeout || read.done) break;
          chunks.push(Buffer.from(read.value).toString("utf8").slice(0, 400));
          chunkTimes.push(Date.now() - started);
        }
        try { reader.cancel(); } catch (e) {}
      }
      results.push({ candidate, status: res.status, contentType, cacheControl: firstHeader(res, "cache-control"), chunkCount: chunks.length, chunkTimes, firstChunk: sanitize(chunks.join("").slice(0, 300)), ms: Date.now() - started });
    } catch (e) {
      results.push({ candidate, error: String(e && e.cause ? e.cause : e), ms: Date.now() - started });
    }
  }
  return results;
}

function encodeWsFrame(opcode, payload) {
  const data = Buffer.isBuffer(payload) ? payload : Buffer.from(payload);
  const mask = crypto.randomBytes(4);
  let header;
  if (data.length < 126) {
    header = Buffer.from([0x80 | opcode, 0x80 | data.length]);
  } else if (data.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x80 | opcode;
    header[1] = 0x80 | 126;
    header.writeUInt16BE(data.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x80 | opcode;
    header[1] = 0x80 | 127;
    header.writeBigUInt64BE(BigInt(data.length), 2);
  }
  const masked = Buffer.alloc(data.length);
  for (let i = 0; i < data.length; i++) masked[i] = data[i] ^ mask[i % 4];
  return Buffer.concat([header, mask, masked]);
}

function decodeWsFrames(buffer) {
  const frames = [];
  let offset = 0;
  while (buffer.length - offset >= 2) {
    const b0 = buffer[offset];
    const b1 = buffer[offset + 1];
    const fin = (b0 & 0x80) !== 0;
    const opcode = b0 & 0x0f;
    const masked = (b1 & 0x80) !== 0;
    let length = b1 & 0x7f;
    let cursor = offset + 2;
    if (length === 126) {
      if (buffer.length < cursor + 2) break;
      length = buffer.readUInt16BE(cursor);
      cursor += 2;
    } else if (length === 127) {
      if (buffer.length < cursor + 8) break;
      length = Number(buffer.readBigUInt64BE(cursor));
      cursor += 8;
    }
    let maskKey = null;
    if (masked) {
      if (buffer.length < cursor + 4) break;
      maskKey = buffer.slice(cursor, cursor + 4);
      cursor += 4;
    }
    if (buffer.length < cursor + length) break;
    const payload = Buffer.from(buffer.slice(cursor, cursor + length));
    if (maskKey) for (let i = 0; i < payload.length; i++) payload[i] ^= maskKey[i % 4];
    frames.push({ fin, opcode, payload });
    offset = cursor + length;
  }
  return { frames, rest: buffer.slice(offset) };
}

function wsProbe(wsPath, cookie, label) {
  return new Promise((resolve) => {
    const result = {
      label,
      path: wsPath,
      status: null,
      upgraded: false,
      headers: {},
      frames: [],
      error: null,
      requestHeaders: [
        "GET " + wsPath + " HTTP/1.1",
        "Host: 127.0.0.1:" + PORT,
        "Upgrade: websocket",
        "Connection: Upgrade",
        "Sec-WebSocket-Key: <random 16 bytes base64>",
        "Sec-WebSocket-Version: 13",
        "Origin: http://127.0.0.1:" + PORT,
        cookie ? "Cookie: dsh-auth-***=***" : "(no Cookie header)",
      ],
    };
    const key = crypto.randomBytes(16).toString("base64");
    const socket = net.connect({ host: "127.0.0.1", port: PORT });
    let buffer = Buffer.alloc(0);
    let handshakeDone = false;
    let finished = false;
    let closePhase = false;
    const timer = setTimeout(() => finish("timeout"), 7000);
    function finish(error) {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      if (error) result.error = String(error);
      try { socket.destroy(); } catch (e) {}
      resolve(result);
    }
    socket.on("error", (error) => finish(error));
    socket.on("connect", () => {
      const lines = [
        "GET " + wsPath + " HTTP/1.1",
        "Host: 127.0.0.1:" + PORT,
        "Upgrade: websocket",
        "Connection: Upgrade",
        "Sec-WebSocket-Key: " + key,
        "Sec-WebSocket-Version: 13",
        "Origin: http://127.0.0.1:" + PORT,
      ];
      if (cookie) lines.push("Cookie: " + cookie);
      socket.write(lines.join("\r\n") + "\r\n\r\n");
    });
    function sendClose() {
      if (closePhase) return;
      closePhase = true;
      const payload = Buffer.alloc(2);
      payload.writeUInt16BE(1000, 0);
      try { socket.write(encodeWsFrame(0x8, payload)); } catch (e) {}
      setTimeout(() => finish(), 800);
    }
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (!handshakeDone) {
        const index = buffer.indexOf("\r\n\r\n");
        if (index < 0) return;
        handshakeDone = true;
        const head = buffer.slice(0, index).toString("utf8");
        buffer = buffer.slice(index + 4);
        const headLines = head.split("\r\n");
        const statusLine = headLines[0] || "";
        const statusMatch = /^HTTP\/1\.[01]\s+(\d{3})/.exec(statusLine);
        result.status = statusMatch ? Number(statusMatch[1]) : 0;
        result.statusLine = sanitize(statusLine);
        for (const line of headLines.slice(1)) {
          const colon = line.indexOf(":");
          if (colon > 0) result.headers[line.slice(0, colon).trim().toLowerCase()] = sanitize(line.slice(colon + 1).trim());
        }
        if (result.status !== 101) {
          finish();
          return;
        }
        result.upgraded = true;
        try { socket.write(encodeWsFrame(0x9, Buffer.from("recon-ping"))); } catch (e) {}
        try {
          socket.write(encodeWsFrame(0x1, Buffer.from(JSON.stringify({ type: "open", streamId: "recon-1", endpoint: "$events", payload: { args: {} } }))));
        } catch (e) {}
        setTimeout(() => {
          try { socket.write(encodeWsFrame(0x1, Buffer.from(JSON.stringify({ type: "end", streamId: "recon-1" })))); } catch (e) {}
          setTimeout(sendClose, 200);
        }, 2500);
      }
      const decoded = decodeWsFrames(buffer);
      buffer = decoded.rest;
      for (const frame of decoded.frames) {
        const payload = frame.payload;
        result.frames.push({
          opcode: frame.opcode,
          bytes: payload.length,
          text: frame.opcode === 1 || frame.opcode === 2 ? sanitize(payload.toString("utf8").slice(0, 300)) : sanitize(payload.toString("hex").slice(0, 80)),
        });
        if (frame.opcode === 0x9) {
          try { socket.write(encodeWsFrame(0xa, payload)); } catch (e) {}
        }
        if (frame.opcode === 0x8) {
          result.closeCode = payload.length >= 2 ? payload.readUInt16BE(0) : null;
          result.closeReason = payload.length > 2 ? sanitize(payload.slice(2).toString("utf8")) : "";
          setTimeout(() => finish(), 100);
        }
      }
    });
  });
}

function readProcStat(pid) {
  try {
    const stat = fs.readFileSync("/proc/" + pid + "/stat", "utf8");
    const close = stat.lastIndexOf(")");
    const fields = stat.slice(close + 2).split(" ");
    return { pid, state: fields[0], ppid: Number(fields[1]), pgrp: Number(fields[2]), starttime: fields[19] };
  } catch (e) {
    return null;
  }
}
function readProcRssKb(pid) {
  try {
    const status = fs.readFileSync("/proc/" + pid + "/status", "utf8");
    const match = /VmRSS:\s+(\d+)\s+kB/.exec(status);
    return match ? Number(match[1]) : null;
  } catch (e) {
    return null;
  }
}
function processTree(rootPid) {
  const entries = fs.readdirSync("/proc").filter((name) => /^\d+$/.test(name)).map((pid) => readProcStat(Number(pid))).filter(Boolean);
  const byParent = new Map();
  for (const entry of entries) {
    if (!byParent.has(entry.ppid)) byParent.set(entry.ppid, []);
    byParent.get(entry.ppid).push(entry);
  }
  const result = [];
  const walk = (pid, depth) => {
    const rss = readProcRssKb(pid);
    result.push({ pid, depth, rssKb: rss, ...(readProcStat(pid) || {}) });
    for (const child of byParent.get(pid) || []) walk(child.pid, depth + 1);
  };
  walk(rootPid, 0);
  return result;
}
function ssPortLines() {
  try {
    const output = execFileSync("ss", ["-ltnp"], { encoding: "utf8" });
    return output.split("\n").filter((line) => new RegExp(":" + PORT + "\\b").test(line));
  } catch (e) {
    return ["ss failed: " + e.message];
  }
}

function portFree() {
  return new Promise((resolve) => {
    const socket = net.connect({ host: "127.0.0.1", port: PORT });
    let done = false;
    const finish = (free) => {
      if (done) return;
      done = true;
      try { socket.destroy(); } catch (e) {}
      resolve(free);
    };
    socket.setTimeout(300);
    socket.once("connect", () => finish(false));
    socket.once("timeout", () => finish(true));
    socket.once("error", () => finish(true));
  });
}

async function stopInstance(state, file) {
  const started = Date.now();
  if (state.exit) {
    append(file, "process already exited: " + JSON.stringify(state.exit));
    return { stopMs: Date.now() - started, graceful: true };
  }
  try { process.kill(-state.child.pid, "SIGTERM"); } catch (e) { try { state.child.kill("SIGTERM"); } catch (e2) {} }
  const graceful = await Promise.race([state.exited.then(() => true), sleep(3000).then(() => false)]);
  if (!graceful) {
    try { process.kill(-state.child.pid, "SIGKILL"); } catch (e) {}
    await Promise.race([state.exited, sleep(4000)]);
  }
  const stopMs = Date.now() - started;
  let released = false;
  const releaseStarted = Date.now();
  while (Date.now() - releaseStarted < 2000) {
    if (await portFree()) { released = true; break; }
    await sleep(80);
  }
  const lines = ssPortLines();
  append(file, "stop: graceful=" + graceful + " stop_ms=" + stopMs + " port_free=" + released + " ss_lines=" + JSON.stringify(lines.map((line) => sanitize(line))));
  return { stopMs, graceful, released };
}

async function main() {
  note("version", VERSION);
  note("node", NODE);
  note("node_version", execFileSync(NODE, ["-v"], { encoding: "utf8" }).trim());
  note("entry", ENTRY);
  note("entry_bytes", fs.statSync(ENTRY).size);
  note("entry_sha256", crypto.createHash("sha256").update(fs.readFileSync(ENTRY)).digest("hex"));
  note("port", PORT);
  note("run_at", stamp());
  note("ss_before", JSON.stringify(ssPortLines().map((line) => sanitize(line))));

  // ---------------- run 1 ----------------
  const run1 = path.join(ROOT, "runs", "run1");
  append("01-startup.log", "=== run1 start ===");
  const state = await startInstance(run1);
  let ready = false;
  try {
    ready = await waitReady(state, 90000);
  } catch (e) {
    failures.push("run1 ready failed: " + e.message);
    append("01-startup.log", "READY FAILED: " + e.message);
    note("ready", false);
  }
  note("ready_ms", state.readyMs || -1);
  note("ready_statuses", describeStatuses(state.statuses));
  note("token_found", Boolean(state.tokenUri));
  if (!state.tokenUri) failures.push("run1 did not print a valid dsh web bootstrap URI");
  if (!ready) {
    await stopInstance(state, "08-process.txt");
    throw new Error("run1 not ready; cannot continue HTTP/SSE/WS recon");
  }

  // process snapshot
  const tree = processTree(state.child.pid);
  const totalRss = tree.reduce((sum, item) => sum + (item.rssKb || 0), 0);
  append("08-process.txt", "pid=" + state.child.pid + " tree=" + JSON.stringify(tree));
  note("process_count", tree.length);
  note("node_rss_kb", (tree[0] && tree[0].rssKb) || -1);
  note("tree_total_rss_kb", totalRss);
  note("pid_pgrp", (readProcStat(state.child.pid) || {}).pgrp);
  note("pid_starttime", (readProcStat(state.child.pid) || {}).starttime);

  // HTTP probes: anonymous -> bootstrap -> cookie -> page
  const probeLines = [];
  const cookieJar = {};
  const anonymous = await (async () => {
    const res = await httpRequest("/", { cookie: null });
    const body = await res.text();
    probeLines.push("### anonymous root");
    probeLines.push("status=" + res.status);
    probeLines.push(headerDump(res));
    probeLines.push("body_head=" + JSON.stringify(sanitize(body.slice(0, 240).replace(/\s+/g, " "))));
    return { res, body };
  })();
  note("anonymous_root_status", anonymous.res.status);
  note("anonymous_root_content_type", firstHeader(anonymous.res, "content-type"));
  note("anonymous_root_has_auth_required", /authentication required|dsh web/i.test(anonymous.body));

  const invalidToken = await (async () => {
    const res = await httpRequest("/?token=invalid-recon-token", { cookie: null });
    const body = await res.text();
    probeLines.push("### invalid token");
    probeLines.push("status=" + res.status);
    probeLines.push("body_head=" + JSON.stringify(sanitize(body.slice(0, 200).replace(/\s+/g, " "))));
    return res;
  })();
  note("invalid_token_status", invalidToken.status);

  let cookie = "";
  let setCookieParsed = null;
  if (state.tokenUri) {
    const res = await httpRequest(state.tokenUri.pathname + state.tokenUri.search, { cookie: null });
    const body = await res.text();
    probeLines.push("### bootstrap token request");
    probeLines.push("status=" + res.status);
    probeLines.push("location=" + maskLocation(firstHeader(res, "location")));
    const setCookies = res.headers.getSetCookie ? res.headers.getSetCookie() : (firstHeader(res, "set-cookie") ? [firstHeader(res, "set-cookie")] : []);
    setCookies.forEach((raw) => probeLines.push("set-cookie: " + maskSetCookie(raw)));
    probeLines.push("body_bytes=" + Buffer.byteLength(body));
    note("bootstrap_status", res.status);
    note("bootstrap_location", maskLocation(firstHeader(res, "location")));
    note("bootstrap_set_cookie_count", setCookies.length);
    if (setCookies.length > 0) {
      setCookieParsed = parseSetCookie(setCookies[0]);
      cookie = cookieHeaderFrom(setCookieParsed);
      cookieJar.cookie = cookie;
      const attrs = setCookieParsed.attrs;
      note("cookie_name_shape", /^dsh-auth-[A-Za-z0-9]+$/.test(setCookieParsed.name) ? "dsh-auth-<random>" : "other");
      note("cookie_name_suffix_len", (setCookieParsed.name.match(/^dsh-auth-(.+)$/) || [])[1] ? (setCookieParsed.name.match(/^dsh-auth-(.+)$/) || [])[1].length : -1);
      note("cookie_httponly", Boolean(attrs.httponly));
      note("cookie_secure", Boolean(attrs.secure));
      note("cookie_samesite", attrs.samesite || "(absent)");
      note("cookie_path", attrs.path || "(absent)");
      note("cookie_domain", attrs.domain || "(absent)");
      note("cookie_maxage", attrs["max-age"] || "(absent)");
      note("cookie_expires", attrs.expires ? "present" : "(absent)");
      note("cookie_value_len", setCookieParsed.value.length);
      const expectedCookieName = "dsh-auth-" + crypto.createHash("sha256").update("127.0.0.1:" + PORT).digest("base64url");
      note("cookie_name_matches_authority_hash", setCookieParsed.name === expectedCookieName);
      note("cookie_authority", "127.0.0.1:" + PORT);
    }
  }
  if (!cookie) failures.push("bootstrap did not set a dsh-auth cookie");
  writeFile("02-http-probe.txt", probeLines.join("\n") + "\n");

  let html = "";
  let htmlFacts = {};
  let assets = [];
  if (cookie) {
    const res = await httpRequest("/", { cookie });
    html = await res.text();
    htmlFacts = extractHtmlFacts(html);
    note("cookie_root_status", res.status);
    note("cookie_root_content_type", firstHeader(res, "content-type"));
    note("cookie_root_cache_control", firstHeader(res, "cache-control"));
    note("cookie_root_x_frame_options", firstHeader(res, "x-frame-options") || "(absent)");
    note("cookie_root_content_security_policy", firstHeader(res, "content-security-policy") || "(absent)");
    note("html_bytes", htmlFacts.bytes);
    note("html_title", htmlFacts.title);
    note("html_base_href", htmlFacts.baseHref);
    note("html_scripts", JSON.stringify(htmlFacts.scripts));
    note("html_absolute_refs", JSON.stringify(htmlFacts.absoluteRefs));
    note("html_has_token", htmlFacts.hasTokenInHtml);
    note("html_has_manifest", htmlFacts.hasManifest);
    note("html_has_plugin_urls", htmlFacts.hasPluginUrls);
    const htmlText = [
      "title=" + htmlFacts.title,
      "base_href=" + htmlFacts.baseHref,
      "bytes=" + htmlFacts.bytes,
      "scripts=" + JSON.stringify(htmlFacts.scripts),
      "links=" + JSON.stringify(htmlFacts.links),
      "absolute_loopback_refs=" + JSON.stringify(htmlFacts.absoluteRefs),
      "has_token=" + htmlFacts.hasTokenInHtml,
      "has_manifest=" + htmlFacts.hasManifest,
      "has_plugin_urls=" + htmlFacts.hasPluginUrls,
      "--- html (sanitized) ---",
      html.slice(0, 200000),
    ].join("\n");
    writeFile("03-html-analysis.txt", htmlText + "\n");

    // static assets through the same browser-like path
    const assetLines = [];
    for (const ref of htmlFacts.scripts.concat(htmlFacts.links).slice(0, 20)) {
      const url = resolveAssetUrl(ref);
      if (!url) continue;
      const asset = await fetchAsset(url, cookie);
      assets.push(asset);
      assetLines.push(JSON.stringify({ ref, status: asset.status, contentType: asset.contentType, bytes: asset.bytes, error: asset.error, ms: asset.ms }));
    }
    writeFile("04-assets.txt", assetLines.join("\n") + "\n");
    note("asset_statuses", assets.map((a) => (a.error ? "ERR" : a.status) + ":" + a.url).join(","));
    note("asset_all_ok", assets.every((a) => a.status === 200));
    const firstOk = assets.find((item) => item.status === 200 && /\/assets\//.test(item.url)) || assets.find((item) => item.status === 200);
    const extra = await extraHttpChecks(cookie, state.tokenUri.searchParams.get("token"), firstOk ? new URL(firstOk.url).pathname : "");
    writeFile("02b-http-extra.txt", extra.lines.join("\n") + "\n");
    note("extra_http_statuses", JSON.stringify(extra.statuses));
  }

  // JS analysis (runs, WebSocket, SSE paths)
  const jsAssets = assets.filter((asset) => asset.body && (/javascript/i.test(asset.contentType || "") || /\.m?js(?:\?|$)/.test(asset.url)));
  const analysis = jsAnalysis(jsAssets);
  const jsLines = [];
  jsLines.push("js_files=" + JSON.stringify(analysis.files));
  for (const [key, data] of Object.entries(analysis.matches)) {
    jsLines.push("### pattern " + key + " total=" + data.total);
    data.samples.forEach((sample) => jsLines.push("  " + sample));
  }
  jsLines.push("api_paths=" + JSON.stringify([...analysis.apiPaths].slice(0, 100)));
  jsLines.push("sse_paths=" + JSON.stringify([...analysis.ssePaths].slice(0, 40)));
  jsLines.push("ws_paths=" + JSON.stringify([...analysis.wsPaths].slice(0, 40)));
  writeFile("05-js-analysis.txt", jsLines.join("\n") + "\n");
  note("js_file_count", analysis.files.length);
  note("js_patterns_seen", Object.keys(analysis.matches).join(","));
  note("js_api_paths", JSON.stringify([...analysis.apiPaths].slice(0, 60)));
  note("js_sse_paths", JSON.stringify([...analysis.ssePaths].slice(0, 20)));
  note("js_ws_paths", JSON.stringify([...analysis.wsPaths].slice(0, 20)));

  // SSE: rc.3 workbench events travel over the /api/remote.mux WebSocket; the
  // HTTP streaming route is /plugins/events (registered by @deepseek-ai/dsh-client-hmr).
  const sseCandidates = new Set(["/plugins/events", ...analysis.ssePaths]);
  for (const fallback of ["/api/events", "/api/remote/events", "/api/stream"]) if (sseCandidates.size < 3) sseCandidates.add(fallback);
  const sseResults = await sseProbe([...sseCandidates].slice(0, 6), cookie);
  const sseAnonymous = await sseProbe(["/plugins/events"], "");
  writeFile("06-sse.txt", "authenticated:\n" + sseResults.map((item) => JSON.stringify(item)).join("\n") + "\nanonymous:\n" + sseAnonymous.map((item) => JSON.stringify(item)).join("\n") + "\n");
  note("sse_results", JSON.stringify(sseResults.map((item) => ({ path: item.candidate, status: item.status, contentType: item.contentType, chunks: item.chunkCount, chunkTimes: item.chunkTimes, firstChunk: (item.firstChunk || "").slice(0, 160), error: item.error }))));
  note("sse_anonymous", JSON.stringify(sseAnonymous.map((item) => ({ path: item.candidate, status: item.status, contentType: item.contentType, chunks: item.chunkCount, chunkTimes: item.chunkTimes, firstChunk: (item.firstChunk || "").slice(0, 120), error: item.error }))));

  // WebSocket candidates: /api/remote.mux is the known DSH route; verify against the page.
  const wsCandidates = new Set([...analysis.wsPaths]);
  wsCandidates.add("/api/remote.mux");
  const wsResults = [];
  for (const wsPath of [...wsCandidates].slice(0, 4)) {
    const anonymous = await wsProbe(wsPath, "", "anonymous");
    wsResults.push(anonymous);
    if (cookie) {
      const authed = await wsProbe(wsPath, cookie, "authenticated");
      wsResults.push(authed);
      if (authed.upgraded) break;
    }
    if (anonymous.upgraded) break;
  }
  writeFile("07-ws.txt", wsResults.map((item) => JSON.stringify(item)).join("\n") + "\n");
  const upgrades = wsResults.filter((item) => item.upgraded);
  note("ws_results", JSON.stringify(wsResults.map((item) => ({ label: item.label, path: item.path, status: item.status, upgraded: item.upgraded, frames: item.frames.length, closeCode: item.closeCode, error: item.error }))));
  note("ws_upgraded_path", upgrades.length ? upgrades[0].path : "(none)");
  note("ws_authenticated", upgrades.some((item) => item.label === "authenticated"));
  if (upgrades.length) {
    const first = upgrades[0];
    note("ws_upgrade_headers", JSON.stringify(first.headers));
    note("ws_request_headers", JSON.stringify(first.requestHeaders));
    note("ws_subprotocol_offered", "(none)");
    note("ws_subprotocol_accepted", first.headers["sec-websocket-protocol"] || "(none)");
    note("ws_heartbeat_pings", first.frames.filter((frame) => frame.opcode === 9).length);
    note("ws_pong_frames", first.frames.filter((frame) => frame.opcode === 10).length);
    note("ws_frames_received", JSON.stringify(first.frames));
    note("ws_close_code", first.closeCode === undefined ? "(none)" : first.closeCode);
    note("ws_close_reason", first.closeReason || "(none)");
  } else {
    failures.push("no WebSocket path upgraded");
  }
  const wsAnonRejected = wsResults.filter((item) => item.label === "anonymous" && item.status && item.status !== 101);
  note("ws_anonymous_reject_statuses", JSON.stringify(wsAnonRejected.map((item) => ({ path: item.path, status: item.status }))));

  // RSS sample again after load
  const tree2 = processTree(state.child.pid);
  note("node_rss_kb_after", (tree2[0] && tree2[0].rssKb) || -1);
  note("tree_total_rss_kb_after", tree2.reduce((sum, item) => sum + (item.rssKb || 0), 0));

  // graceful stop + port release
  append("08-process.txt", "=== run1 stop ===");
  const stop1 = await stopInstance(state, "08-process.txt");
  note("stop1_ms", stop1.stopMs);
  note("stop1_graceful", stop1.graceful);
  note("stop1_port_released", stop1.released);
  note("ss_after", JSON.stringify(ssPortLines().map((line) => sanitize(line))));
  if (!stop1.released) failures.push("port " + PORT + " not released after stop");
  await sleep(300);

  // ---------------- run 2: repeatability ----------------
  append("09-run2.log", "=== run2 start ===");
  const run2 = path.join(ROOT, "runs", "run2");
  const state2 = await startInstance(run2);
  let ready2 = false;
  try {
    ready2 = await waitReady(state2, 90000);
  } catch (e) {
    failures.push("run2 ready failed: " + e.message);
  }
  note("run2_ready", ready2);
  note("run2_ready_ms", state2.readyMs || -1);
  note("run2_statuses", describeStatuses(state2.statuses));
  note("run2_token_found", Boolean(state2.tokenUri));
  append("09-run2.log", "ready=" + ready2 + " token=" + Boolean(state2.tokenUri));
  if (state2.tokenUri) {
    const res = await httpRequest(state2.tokenUri.pathname + state2.tokenUri.search, { cookie: null });
    const setCookies = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
    note("run2_bootstrap_status", res.status);
    note("run2_bootstrap_set_cookie_count", setCookies.length);
    if (setCookies.length > 0) {
      const parsed = parseSetCookie(setCookies[0]);
      const cookie2 = cookieHeaderFrom(parsed);
      const page = await httpRequest("/", { cookie: cookie2 });
      await page.text();
      note("run2_page_status_with_cookie", page.status);
    }
  }
  const stop2 = await stopInstance(state2, "09-run2.log");
  note("run2_stop_ms", stop2.stopMs);
  note("run2_port_released", stop2.released);
  if (!stop2.released) failures.push("port " + PORT + " not released after run2 stop");

  // ---------------- summary ----------------
  note("failures", JSON.stringify(failures));
  note("result", failures.length === 0 ? "ok" : "failed");
  writeFile("10-summary.txt", findings.join("\n") + "\n");
  console.log(findings.join("\n"));
  if (failures.length) {
    console.error("FAILURES: " + failures.join("; "));
    process.exitCode = 1;
  }
}

main().catch((error) => {
  try {
    writeFile("99-error.txt", String(error && error.stack ? error.stack : error) + "\n");
    note("result", "error");
    note("error", String(error && error.message ? error.message : error));
    writeFile("10-summary.txt", findings.join("\n") + "\n");
  } catch (e) {}
  console.error(error);
  process.exitCode = 1;
});
