#!/usr/bin/env node
// PCL-DSH-Go M1 | generic Job SSE client for contract/E2E assertions.
//
// Usage:
//   printf '%s' "$JSESSIONID_COOKIE" | node job-sse.mjs --url http://127.0.0.1:18080/api/jobs/j-xxx/events \
//       [--timeout-ms 300000] [--last-event-id N] [--json-out file]
//
// Reads the SSE stream, records id/event/data, detects the terminal state, counts
// ": ping" heartbeats and reports monotonic ids. Supports Last-Event-ID replay.
// Cookie is stdin-only; output is sanitized (token=/dsh-auth/session cookies -> ***).
import http from "node:http";
import fs from "node:fs";
import path from "node:path";

function parseArgs(argv) {
  const out = { "timeout-ms": 300000, "expect-terminal": true };
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
function sanitize(text) {
  return String(text)
    .replace(/([?&]token=)[^&\s"'<>]+/gi, "$1***")
    .replace(/dsh-auth-[A-Za-z0-9]+=[^;,\s"']*/g, "dsh-auth-***=***")
    .replace(/(JSESSIONID|XSRF-TOKEN)=[^;,\s"']+/g, "$1=***")
    .replace(/\bsk-[A-Za-z0-9_-]{12,}/g, "***");
}
const args = parseArgs(process.argv);
const url = new URL(args.url);
if (!/^https?:$/.test(url.protocol)) throw new Error("--url must be http(s)");
const timeoutMs = Number(args["timeout-ms"] || 300000);
const expectTerminal = String(args["expect-terminal"]) !== "false";
const cookie = await new Promise((resolve) => {
  let data = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => { data += chunk; });
  process.stdin.on("end", () => resolve(data.trim()));
  process.stdin.resume();
});
const result = {
  url: sanitize(args.url),
  lastEventIdRequested: args["last-event-id"] === undefined ? null : Number(args["last-event-id"]),
  status: null,
  contentType: "",
  cacheControl: "",
  events: [],
  eventNames: [],
  pingCount: 0,
  idsMonotonic: true,
  terminalState: "",
  terminalEvent: "",
  resultPayload: null,
  errorPayload: null,
  durationMs: 0,
  failures: [],
};
const started = Date.now();

function finish(error) {
  result.durationMs = Date.now() - started;
  if (error) result.failures.push(String(error));
  if (expectTerminal && !result.terminalState) result.failures.push("no terminal state observed");
  if (!result.idsMonotonic) result.failures.push("event ids are not monotonic");
  if (result.lastEventIdRequested && result.lastEventIdRequested > 0 && result.events.length > 0 && result.events[0].seq <= result.lastEventIdRequested) {
    result.failures.push("replay did not start after Last-Event-ID");
  }
  const json = JSON.stringify(result, null, 2) + "\n";
  if (args["json-out"]) {
    fs.mkdirSync(path.dirname(args["json-out"]), { recursive: true });
    fs.writeFileSync(args["json-out"], sanitize(json), { mode: 0o600 });
  }
  console.log("job-sse: status=" + result.status + " content_type=" + result.contentType + " cache_control=" + result.cacheControl);
  console.log("job-sse: events=" + result.events.length + " names=" + JSON.stringify(result.eventNames.slice(0, 12)) + " ping=" + result.pingCount + " terminal=" + result.terminalState + " duration_ms=" + result.durationMs);
  if (result.terminalEvent === "result") console.log("job-sse: result=" + sanitize(JSON.stringify(result.resultPayload)));
  if (result.terminalEvent === "error") console.log("job-sse: error=" + sanitize(JSON.stringify(result.errorPayload)));
  console.log("job-sse: failures=" + JSON.stringify(result.failures));
  process.exit(result.failures.length === 0 ? 0 : 2);
}

const headers = { Accept: "text/event-stream" };
if (cookie) headers.Cookie = cookie;
if (args["last-event-id"] !== undefined) headers["Last-Event-ID"] = String(args["last-event-id"]);
const req = http.get(args.url, { headers }, (res) => {
  result.status = res.statusCode;
  result.contentType = String(res.headers["content-type"] || "");
  result.cacheControl = String(res.headers["cache-control"] || "");
  if (res.statusCode !== 200) {
    let body = "";
    res.setEncoding("utf8");
    res.on("data", (c) => { body += c; if (body.length < 4000) return; });
    res.on("end", () => finish("http status " + res.statusCode + " body=" + sanitize(body.slice(0, 500))));
    return;
  }
  res.setEncoding("utf8");
  let buffer = "";
  let current = { id: undefined, name: "", data: [] };
  let lastSeq = 0;
  const handleLine = (line) => {
    if (line === "") {
      if (current.id !== undefined || current.name !== "" || current.data.length > 0) {
        const seq = current.id === undefined ? null : Number(current.id);
        let payload = current.data.join("\n");
        let parsed = null;
        try { parsed = payload === "" ? null : JSON.parse(payload); } catch (e) { parsed = payload; }
        const entry = { seq, name: current.name, atMs: Date.now() - started, data: parsed };
        if (seq !== null) {
          if (seq <= lastSeq) result.idsMonotonic = false;
          lastSeq = seq;
        }
        result.events.push(entry);
        result.eventNames.push(current.name);
        if (current.name === "result") {
          result.terminalEvent = "result";
          result.resultPayload = parsed;
          if (parsed && parsed.state) result.terminalState = parsed.state;
        } else if (current.name === "error") {
          result.terminalEvent = "error";
          result.errorPayload = parsed;
          if (parsed && parsed.state) result.terminalState = parsed.state;
        } else if (current.name === "state" && parsed && ["SUCCEEDED", "FAILED", "CANCELLED"].includes(parsed.state)) {
          result.terminalState = parsed.state;
        }
        current = { id: undefined, name: "", data: [] };
      }
      return;
    }
    if (line.startsWith(":")) { if (/^:\s*ping/.test(line)) result.pingCount++; return; }
    if (line.startsWith("id:")) { current.id = line.slice(3).trim(); return; }
    if (line.startsWith("event:")) { current.name = line.slice(6).trim(); return; }
    if (line.startsWith("data:")) { current.data.push(line.slice(5).replace(/^ /, "")); return; }
  };
  res.on("data", (chunk) => {
    buffer += chunk;
    let idx;
    while ((idx = buffer.indexOf("\n")) >= 0) {
      let line = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 1);
      if (line.endsWith("\r")) line = line.slice(0, -1);
      handleLine(line);
    }
  });
  res.on("end", () => { handleLine(""); finish(null); });
  res.on("error", (error) => finish(error));
});
req.on("error", (error) => finish(error));
req.setTimeout(timeoutMs, () => { req.destroy(new Error("timeout after " + timeoutMs + "ms")); });
