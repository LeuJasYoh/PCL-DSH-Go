#!/usr/bin/env node
// PCL-DSH-Go M0-7 | reusable SSE E2E check for the /workbench proxy (or direct DSH).
//
// Usage:
//   printf '%s' "$DSH_COOKIE" | node sse-e2e.mjs --url http://127.0.0.1:18080/workbench/i-xxx/plugins/events \
//       [--timeout 4000] [--json]
//
// Asserts: HTTP 200, content-type text/event-stream, first chunk starts with the SSE
// comment ": connected", and at least one data frame arrives without buffering.
// Cookie is read from stdin only; output is sanitized. Exit 0 ok, 2 assertion failure.
import http from "node:http";

function parseArgs(argv) {
  const out = { timeout: 4000, json: false };
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--json") { out.json = true; continue; }
    if (!a.startsWith("--")) throw new Error("unexpected argument: " + a);
    const key = a.slice(2);
    const value = argv[++i];
    if (value === undefined) throw new Error("missing value for --" + key);
    out[key] = value;
  }
  return out;
}

function sanitize(text) {
  return String(text)
    .replace(/([?&]token=)[^&\s"'<>]+/gi, "$1***")
    .replace(/dsh-auth-[A-Za-z0-9]+=[^;,\s"']*/g, "dsh-auth-***=***")
    .replace(/sk-[A-Za-z0-9_-]{12,}/g, "***");
}

const args = parseArgs(process.argv);
if (!args.url || !/^https?:\/\//.test(args.url)) {
  console.error("sse-e2e: --url http(s)://... is required");
  process.exit(4);
}
const cookie = await new Promise((resolve) => {
  let data = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => { data += chunk; });
  process.stdin.on("end", () => resolve(data.trim()));
  process.stdin.resume();
});
const timeoutMs = Number(args.timeout || 4000);
const result = {
  url: sanitize(args.url),
  cookieSent: Boolean(cookie),
  status: null,
  contentType: "",
  cacheControl: "",
  headersAtMs: null,
  chunks: [],
  chunkTimes: [],
  error: null,
};
const started = Date.now();

const request = http.get(args.url, {
  headers: Object.assign({ Accept: "text/event-stream" }, cookie ? { Cookie: cookie } : {}),
  timeout: timeoutMs,
}, (response) => {
  result.status = response.statusCode;
  result.contentType = String(response.headers["content-type"] || "");
  result.cacheControl = String(response.headers["cache-control"] || "");
  result.headersAtMs = Date.now() - started;
  response.on("data", (chunk) => {
    const text = sanitize(chunk.toString("utf8"));
    result.chunks.push(text.slice(0, 500));
    result.chunkTimes.push(Date.now() - started);
    if (result.chunks.length >= 4) response.destroy();
  });
  response.on("end", () => finish());
  response.on("error", (error) => finish(error));
});
request.on("timeout", () => { request.destroy(new Error("timeout")); });
request.on("error", (error) => finish(error));
const hardStop = setTimeout(() => { try { request.destroy(); } catch (e) {} finish(); }, timeoutMs + 500);

let done = false;
function finish(error) {
  if (done) return;
  done = true;
  clearTimeout(hardStop);
  try { request.destroy(); } catch (e) {}
  if (error && !result.status) result.error = String(error);
  const joined = result.chunks.join("");
  const failures = [];
  if (result.status !== 200) failures.push("status=" + result.status);
  if (!/text\/event-stream/i.test(result.contentType)) failures.push("content-type=" + result.contentType);
  if (!/^: connected/.test(joined)) failures.push("missing ': connected' comment");
  if (!/data:\s*\{/.test(joined)) failures.push("missing data frame");
  result.failures = failures;
  result.checks = {
    status200: result.status === 200,
    eventStream: /text\/event-stream/i.test(result.contentType),
    connectedComment: /^: connected/.test(joined),
    dataFrame: /data:\s*\{/.test(joined),
  };
  if (args.json) console.log(JSON.stringify(result));
  else {
    console.log("status=" + result.status + " content_type=" + result.contentType + " cache_control=" + result.cacheControl
      + " headers_at_ms=" + result.headersAtMs + " chunks=" + result.chunks.length + " chunk_times=" + JSON.stringify(result.chunkTimes));
    console.log("first_chunk=" + JSON.stringify(result.chunks[0] || ""));
    console.log("checks=" + JSON.stringify(result.checks) + " failures=" + JSON.stringify(result.failures));
  }
  process.exit(failures.length === 0 ? 0 : 2);
}
