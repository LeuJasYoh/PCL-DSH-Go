#!/usr/bin/env node
// PCL-DSH-Go M0-7 | reusable WebSocket E2E client for the DSH /workbench proxy
// (or a direct DSH instance). Node's global WebSocket cannot send a Cookie header,
// so this implements the RFC 6455 client handshake/frames with node:net + crypto.
//
// Usage:
//   printf '%s' "$DSH_COOKIE" | node ws-e2e.mjs --url ws://127.0.0.1:18080/workbench/i-xxx/api/remote.mux \
//       --origin http://127.0.0.1:18080 [--endpoint '$events'] [--timeout 10000] [--json]
//
// Cookie is read from stdin (never argv/env/files). Output is sanitized: the cookie
// value and any token= query are replaced with ***. Exit codes: 0 ok, 2 handshake/
// protocol failure, 3 timeout, 4 bad arguments.
import net from "node:net";
import crypto from "node:crypto";

function parseArgs(argv) {
  const out = { endpoint: "$events", timeout: 10000, json: false, expectMessage: true };
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
if (!args.url || !/^ws:\/\//.test(args.url)) {
  console.error("ws-e2e: --url ws://... is required");
  process.exit(4);
}
const timeoutMs = Number(args.timeout || 10000);
const cookie = (await new Promise((resolve) => {
  let data = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => { data += chunk; });
  process.stdin.on("end", () => resolve(data.trim()));
  process.stdin.resume();
}));
const url = new URL(args.url);
const host = url.hostname;
const port = Number(url.port || 80);
const wsPath = url.pathname + (url.search || "");
const origin = args.origin || (url.protocol === "ws:" ? "http://" + url.host : "https://" + url.host);

function encodeFrame(opcode, payload) {
  const data = Buffer.isBuffer(payload) ? payload : Buffer.from(String(payload));
  const mask = crypto.randomBytes(4);
  let header;
  if (data.length < 126) header = Buffer.from([0x80 | opcode, 0x80 | data.length]);
  else if (data.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x80 | opcode; header[1] = 0x80 | 126; header.writeUInt16BE(data.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x80 | opcode; header[1] = 0x80 | 127; header.writeBigUInt64BE(BigInt(data.length), 2);
  }
  const masked = Buffer.alloc(data.length);
  for (let i = 0; i < data.length; i++) masked[i] = data[i] ^ mask[i % 4];
  return Buffer.concat([header, mask, masked]);
}

function decodeFrames(buffer) {
  const frames = [];
  let offset = 0;
  while (buffer.length - offset >= 2) {
    const b0 = buffer[offset], b1 = buffer[offset + 1];
    const opcode = b0 & 0x0f;
    const masked = (b1 & 0x80) !== 0;
    let length = b1 & 0x7f;
    let cursor = offset + 2;
    if (length === 126) {
      if (buffer.length < cursor + 2) break;
      length = buffer.readUInt16BE(cursor); cursor += 2;
    } else if (length === 127) {
      if (buffer.length < cursor + 8) break;
      length = Number(buffer.readBigUInt64BE(cursor)); cursor += 8;
    }
    let maskKey = null;
    if (masked) {
      if (buffer.length < cursor + 4) break;
      maskKey = buffer.slice(cursor, cursor + 4); cursor += 4;
    }
    if (buffer.length < cursor + length) break;
    const payload = Buffer.from(buffer.slice(cursor, cursor + length));
    if (maskKey) for (let i = 0; i < payload.length; i++) payload[i] ^= maskKey[i % 4];
    frames.push({ opcode, payload });
    offset = cursor + length;
  }
  return { frames, rest: buffer.slice(offset) };
}

const result = {
  url: sanitize(args.url),
  origin,
  status: null,
  upgraded: false,
  subprotocol: "(none)",
  cookieSent: Boolean(cookie),
  sentOpen: false,
  frames: [],
  closeCode: null,
  closeReason: "",
  error: null,
};

function finish(code, error) {
  if (error) result.error = String(error);
  if (result.error) code = code || 2;
  if (args.json) console.log(JSON.stringify(result));
  else {
    console.log("status=" + result.status + " upgraded=" + result.upgraded + " subprotocol=" + result.subprotocol + " cookie_sent=" + result.cookieSent);
    for (const frame of result.frames) console.log("frame opcode=" + frame.opcode + " bytes=" + frame.bytes + (frame.text ? " text=" + JSON.stringify(frame.text) : ""));
    console.log("close_code=" + result.closeCode + " close_reason=" + JSON.stringify(result.closeReason) + " error=" + JSON.stringify(result.error));
  }
  process.exit(code);
}

const key = crypto.randomBytes(16).toString("base64");
const socket = net.connect({ host, port });
let buffer = Buffer.alloc(0);
let handshakeDone = false;
let closing = false;
const timer = setTimeout(() => finish(3, "timeout after " + timeoutMs + "ms"), timeoutMs);

function sendClose() {
  if (closing) return;
  closing = true;
  const payload = Buffer.alloc(2);
  payload.writeUInt16BE(1000, 0);
  try { socket.write(encodeFrame(0x8, payload)); } catch (e) {}
  setTimeout(() => finish(result.upgraded && hasMessage ? 0 : 2, hasMessage ? null : "no application message before close"), 800);
}
let hasMessage = false;

socket.on("error", (error) => { clearTimeout(timer); finish(2, error); });
socket.on("connect", () => {
  const lines = [
    "GET " + wsPath + " HTTP/1.1",
    "Host: " + host + ":" + port,
    "Upgrade: websocket",
    "Connection: Upgrade",
    "Sec-WebSocket-Key: " + key,
    "Sec-WebSocket-Version: 13",
    "Origin: " + origin,
  ];
  if (cookie) lines.push("Cookie: " + cookie);
  socket.write(lines.join("\r\n") + "\r\n\r\n");
});
socket.on("data", (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  if (!handshakeDone) {
    const index = buffer.indexOf("\r\n\r\n");
    if (index < 0) return;
    handshakeDone = true;
    const head = buffer.slice(0, index).toString("utf8");
    buffer = buffer.slice(index + 4);
    const statusMatch = /^HTTP\/1\.[01] (\d{3})/.exec(head.split("\r\n")[0] || "");
    result.status = statusMatch ? Number(statusMatch[1]) : 0;
    const headers = {};
    for (const line of head.split("\r\n").slice(1)) {
      const colon = line.indexOf(":");
      if (colon > 0) headers[line.slice(0, colon).trim().toLowerCase()] = line.slice(colon + 1).trim();
    }
    result.subprotocol = headers["sec-websocket-protocol"] || "(none)";
    if (result.status !== 101) { clearTimeout(timer); finish(2, "handshake status " + result.status); return; }
    result.upgraded = true;
    try { socket.write(encodeFrame(0x9, "e2e-ping")); } catch (e) {}
    const message = JSON.stringify({ type: "open", streamId: "e2e-1", endpoint: args.endpoint, payload: { args: {} } });
    result.sentOpen = true;
    result.frames.push({ opcode: 1, bytes: Buffer.byteLength(message), text: sanitize(message.slice(0, 300)) });
    try { socket.write(encodeFrame(0x1, message)); } catch (e) {}
    setTimeout(() => {
      try { socket.write(encodeFrame(0x1, JSON.stringify({ type: "end", streamId: "e2e-1" }))); } catch (e) {}
      setTimeout(sendClose, 150);
    }, 1500);
  }
  const decoded = decodeFrames(buffer);
  buffer = decoded.rest;
  for (const frame of decoded.frames) {
    const payload = frame.payload;
    const entry = { opcode: frame.opcode, bytes: payload.length };
    if (frame.opcode === 1 || frame.opcode === 2) {
      entry.text = sanitize(payload.toString("utf8").slice(0, 400));
      if (/\"item\"|\"error\"|\"end\"/.test(entry.text)) hasMessage = true;
    } else if (frame.opcode === 8) {
      result.closeCode = payload.length >= 2 ? payload.readUInt16BE(0) : null;
      result.closeReason = payload.length > 2 ? sanitize(payload.slice(2).toString("utf8")) : "";
      clearTimeout(timer);
      setTimeout(() => finish(result.upgraded && (hasMessage || !args.expectMessage) ? 0 : 2, hasMessage || !args.expectMessage ? null : "no application message received"), 100);
    }
    if (frame.opcode === 9) { try { socket.write(encodeFrame(0xa, payload)); } catch (e) {} }
    result.frames.push(entry);
  }
});
