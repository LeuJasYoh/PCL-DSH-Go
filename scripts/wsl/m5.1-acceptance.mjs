import fs from 'node:fs'
import path from 'node:path'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO = path.resolve(HERE, '..', '..')
const RELEASE = process.env.M52_RELEASE || path.join(REPO, 'release', 'pcl-dsh-go-0.2.1-linux-amd64')
const BIN = path.join(RELEASE, 'bin', 'pcl-dsh-go')
const TOOLS = path.join(RELEASE, 'tools')
const STATIC = path.join(RELEASE, 'static')
const NODE = path.join(TOOLS, 'node', 'bin', 'node')
const VERSION = '0.2.1-rc.3'
const RUNTIME_SRC = '/var/lib/pcl-dsh-go/runtimes/' + VERSION
const stamp = new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)
const SCENARIO = '/tmp/pcl-dsh-go-test/m5.2-scenario-' + stamp
const EVIDENCE = path.join(REPO, 'docs', 'evidence', 'm5')
const LOG = path.join(EVIDENCE, 'm5.2-acceptance-' + stamp + '.log.txt')
const managers = []
let pass = 0
let fail = 0
const lines = []
function log(text) { lines.push(text); console.log(text) }
function check(name, ok, detail) { if (ok) { pass++; log('PASS  ' + name + '  ' + (detail || '')) } else { fail++; log('FAIL  ' + name + '  ' + (detail || '')) } }
function san(text) { return String(text).replace(/token=[^&"\s]+/g, 'token=***').replace(/(JSESSIONID|XSRF-TOKEN)=[^;,"\s]+/g, '$1=***').replace(/("[A-Za-z]*[Pp]assword"\s*:\s*")[^"]*"/g, '$1"***"') }
function sleep(ms) { return new Promise((resolve) => setTimeout(resolve, ms)) }
function fetchWithTimeout(url, options, ms) { return fetch(url, { ...(options || {}), signal: AbortSignal.timeout(ms || 30000) }) }
function portBusy(port) { try { const out = fs.readFileSync('/proc/net/tcp', 'utf8'); const hex = (port).toString(16).toUpperCase().padStart(4, '0'); return out.split('\n').some((line) => line.includes(':' + hex) && line.includes('0A')) } catch { return false } }
class Jar {
  constructor() { this.map = new Map() }
  absorb(response) {
    const raw = typeof response.headers.getSetCookie === 'function' ? response.headers.getSetCookie() : []
    for (const item of raw) { const pair = item.split(';')[0]; const index = pair.indexOf('='); if (index > 0) this.map.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim()) }
  }
  header() { return Array.from(this.map.entries()).map((entry) => entry[0] + '=' + entry[1]).join('; ') }
}
async function req(base, jar, method, p, body, csrf) {
  const headers = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (jar.header()) headers.Cookie = jar.header()
  if (csrf) headers['X-XSRF-TOKEN'] = csrf
  const response = await fetchWithTimeout(base + p, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'manual' }, 60000)
  jar.absorb(response)
  const text = await response.text()
  let json = null
  try { json = JSON.parse(text) } catch { json = null }
  return { status: response.status, text, json, headers: response.headers }
}
async function waitHealth(port, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try { const response = await fetchWithTimeout('http://127.0.0.1:' + port + '/api/health', {}, 3000); if (response.ok) return true } catch {}
    await sleep(300)
  }
  return false
}
async function startManager(port, data, extra) {
  fs.mkdirSync(data, { recursive: true })
  const fd = fs.openSync(path.join(SCENARIO, 'manager-' + port + '.log'), 'a')
  const args = ['--pcl.data-root=' + data, '--pcl.tool-root=' + TOOLS, '--server.address=127.0.0.1', '--server.port=' + String(port), '--server.static-dir=' + STATIC, ...extra]
  const child = spawn(BIN, args, { detached: true, stdio: ['ignore', fd, fd] })
  child.unref()
  managers.push(child.pid)
  const ok = await waitHealth(port, 45000)
  if (!ok) throw new Error('manager on ' + port + ' not healthy')
  log('manager port=' + port + ' pid=' + child.pid + ' args=' + JSON.stringify(extra))
  return child.pid
}
async function stopManagers() {
  for (const pid of managers.splice(0)) { try { process.kill(-pid, 'SIGTERM') } catch { try { process.kill(pid, 'SIGTERM') } catch {} } }
  await sleep(1500)
}
function prepareData(data) {
  fs.rmSync(data, { recursive: true, force: true })
  fs.mkdirSync(path.join(data, 'runtimes'), { recursive: true, mode: 0o700 })
  const runtimeFile = path.join(RUNTIME_SRC, 'node_modules', '@deepseek-ai', 'dsh', 'lib', 'bin.js')
  if (!fs.existsSync(runtimeFile)) throw new Error('runtime missing: ' + RUNTIME_SRC)
  fs.symlinkSync(RUNTIME_SRC, path.join(data, 'runtimes', VERSION))
}
async function pollJob(base, jar, csrf, id) {
  const deadline = Date.now() + 300000
  while (Date.now() < deadline) {
    const response = await req(base, jar, 'GET', '/api/jobs/' + id, undefined, csrf)
    const state = response.json && response.json.state
    if (state === 'SUCCEEDED' || state === 'FAILED' || state === 'CANCELLED') return { state, body: response.json }
    await sleep(500)
  }
  return { state: 'TIMEOUT', body: null }
}
async function authFlow(base, jar, data) {
  const initial = fs.readFileSync(path.join(data, 'initial-password'), 'utf8').trim()
  let response = await req(base, jar, 'POST', '/api/auth/login', { password: initial })
  check('login initial 204', response.status === 204, 'status=' + response.status)
  response = await req(base, jar, 'GET', '/api/auth/me')
  const csrf = response.json && response.json.csrfToken
  const newPassword = 'M51qa-' + Math.random().toString(36).slice(2, 11)
  response = await req(base, jar, 'PUT', '/api/auth/password', { oldPassword: initial, newPassword }, csrf)
  check('first-login change password 204', response.status === 204, 'status=' + response.status)
  jar.map.clear()
  response = await req(base, jar, 'POST', '/api/auth/login', { password: newPassword })
  check('login new password 204', response.status === 204, 'status=' + response.status)
  response = await req(base, jar, 'GET', '/api/auth/me')
  return { csrf: response.json && response.json.csrfToken, password: newPassword }
}
async function instanceField(base, jar, csrf, id, fieldName) {
  const response = await req(base, jar, 'GET', '/api/instances', undefined, csrf)
  const item = Array.isArray(response.json) ? response.json.find((entry) => entry.id === id) : null
  return item ? item[fieldName] : ''
}
async function runFlow(port, data, extra, label, mode) {
  prepareData(data)
  const base = 'http://127.0.0.1:' + port
  const jar = new Jar()
  await startManager(port, data, extra)
  const auth = await authFlow(base, jar, data)
  const csrf = auth.csrf
  async function create(name, instancePort) {
    const response = await req(base, jar, 'POST', '/api/instances', { name, version: VERSION, profile: 'web', port: instancePort }, csrf)
    check(label + ' create ' + name + ' port ' + instancePort, response.status === 201, 'status=' + response.status + ' code=' + (response.json && response.json.code) + ' message=' + (response.json && response.json.message))
    if (response.status !== 201) throw new Error('create instance failed: ' + response.status + ' ' + response.text.slice(0, 200))
    return response.json.id
  }
  async function launch(id, title, expected) {
    const response = await req(base, jar, 'POST', '/api/instances/' + id + '/launch', undefined, csrf)
    if (expected === 409) {
      check(label + ' ' + title + ' 409 CONFLICT_RUNNING_INSTANCE', response.status === 409 && response.json && response.json.code === 'CONFLICT_RUNNING_INSTANCE', 'status=' + response.status + ' code=' + (response.json && response.json.code) + ' message=' + (response.json && response.json.message))
      return response.json && response.json.message
    }
    check(label + ' ' + title + ' launch 202', response.status === 202, 'status=' + response.status)
    const job = await pollJob(base, jar, csrf, response.json.id)
    check(label + ' ' + title + ' launch job SUCCEEDED', job.state === 'SUCCEEDED', 'state=' + job.state)
    return job.state
  }
  async function stopInstance(id, title) {
    // POST /api/instances/{id}/stop is synchronous: 200 + updated instance DTO.
    const response = await req(base, jar, 'POST', '/api/instances/' + id + '/stop', undefined, csrf)
    check(label + ' ' + title + ' stop 200', response.status === 200, 'status=' + response.status)
    await sleep(500)
    const status = await instanceField(base, jar, csrf, id, 'status')
    check(label + ' ' + title + ' stopped', !String(status).includes('运行中'), 'status=' + status)
  }
  async function workbench(id, title) {
    let url = base + '/workbench/' + id + '/'
    let final = null
    const managerCookie = jar.header()
    const dshCookies = new Map()
    const dshHeader = () => Array.from(dshCookies.entries()).map((entry) => entry[0] + '=' + entry[1]).join('; ')
    for (let index = 0; index < 5; index += 1) {
      const combined = managerCookie + (dshCookies.size ? '; ' + dshHeader() : '')
      const response = await fetchWithTimeout(url, { headers: { Cookie: combined }, redirect: 'manual' }, 60000)
      const raw = typeof response.headers.getSetCookie === 'function' ? response.headers.getSetCookie() : []
      for (const item of raw) { const pair = item.split(';')[0]; const eq = pair.indexOf('='); if (eq <= 0) continue; const name = pair.slice(0, eq).trim(); const value = pair.slice(eq + 1).trim(); if (name !== 'JSESSIONID' && name !== 'XSRF-TOKEN') dshCookies.set(name, value) }
      if (response.status >= 300 && response.status < 400) { const location = response.headers.get('location'); if (!location) break; url = new URL(location, url).href; continue }
      final = response
      break
    }
    if (!final) { check(label + ' ' + title + ' workbench 200', false, 'no final response'); return }
    const text = await final.text()
    check(label + ' ' + title + ' workbench 200', final.status === 200, 'status=' + final.status)
    check(label + ' ' + title + ' workbench title DSH', text.toLowerCase().includes('<title>deepseek harness</title>'))
  }
  if (mode === 'limit1') {
    // Explicit --pcl.max-running-instances=1 keeps the upper-bound semantics.
    const b1 = await create('M5.2 上限1 甲', 3290)
    const b2 = await create('M5.2 上限1 乙', 3291)
    await launch(b1, 'limit1-甲')
    const message = await launch(b2, 'limit1-乙', 409)
    check(label + ' explicit limit=1 second launch 409 message contains 1', String(message).includes('1'), 'message=' + message)
    await stopInstance(b1, 'limit1-甲')
    return
  }
  // Default (no flag): --pcl.max-running-instances defaults to 0 (unlimited) in M5.2.
  const id1 = await create('M5.2 默认甲', 3280)
  const id2 = await create('M5.2 默认乙', 3281)
  const id3 = await create('M5.2 默认丙', 3282)
  await launch(id1, '默认甲')
  await launch(id2, '默认乙')
  await launch(id3, '默认丙')
  const statuses = []
  for (const id of [id1, id2, id3]) statuses.push(await instanceField(base, jar, csrf, id, 'status'))
  check(label + ' 默认甲 运行中', String(statuses[0]).includes('运行中'), 'status=' + statuses[0])
  check(label + ' 默认乙 运行中', String(statuses[1]).includes('运行中'), 'status=' + statuses[1])
  check(label + ' 默认丙 运行中', String(statuses[2]).includes('运行中'), 'status=' + statuses[2])
  await workbench(id1, '默认甲')
  await workbench(id2, '默认乙')
  const ps = await new Promise((resolve) => { const child = spawn('ps', ['-eo', 'pid,rss,args']); let out = ''; child.stdout.on('data', (chunk) => { out += chunk }); child.on('close', () => resolve(out)) })
  log('rss snapshot:\n' + ps.split('\n').filter((line) => line.includes('pcl-dsh-go') || line.includes('dsh/lib/bin')).slice(0, 8).join('\n'))
  const shotScript = path.join(REPO, 'scripts', 'wsl', 'm5-shot.cjs')
  const winNode = '/mnt/d/DevelopSoftware/nodejs/node.exe'
  if (fs.existsSync(winNode)) {
    const winPath = (p) => p.replace(/^\/mnt\/d\//, 'D:/')
    await new Promise((resolve) => { const child = spawn(winNode, [winPath(shotScript), base, auth.password, winPath(path.join(EVIDENCE, 'm5.2-two-instances.png'))], { stdio: ['ignore', fs.openSync(path.join(SCENARIO, 'shot.log'), 'a'), fs.openSync(path.join(SCENARIO, 'shot.log'), 'a')] }); child.on('close', resolve) })
    check(label + ' default-unlimited two-instances screenshot', fs.existsSync(path.join(EVIDENCE, 'm5.2-two-instances.png')))
  } else { log('INFO  Windows node missing; screenshot skipped') }
}

async function main() {
  fs.mkdirSync(EVIDENCE, { recursive: true })
  fs.mkdirSync(SCENARIO, { recursive: true })
  log('=== M5.2 multi-instance acceptance (default unlimited) ' + stamp + ' ===')
  log('release=' + RELEASE + ' version=' + fs.readFileSync(path.join(RELEASE, 'VERSION'), 'utf8').trim())
  log('bin_sha256=' + (await import('node:crypto')).createHash('sha256').update(fs.readFileSync(BIN)).digest('hex'))
  log('runtime=' + RUNTIME_SRC)
  try {
    await runFlow(18086, path.join(SCENARIO, 'data-unlimited'), [], 'default-unlimited', 'unlimited')
    await stopManagers()
    await sleep(1000)
    await runFlow(18087, path.join(SCENARIO, 'data-limit1'), ['--pcl.max-running-instances', '1'], 'explicit-limit1', 'limit1')
  } catch (error) {
    console.error(error)
    log('FATAL: ' + (error && error.message ? error.message : String(error)))
    fail++
  } finally {
    await stopManagers()
    log('checks_passed=' + pass + ' checks_failed=' + fail + ' scenario=' + SCENARIO)
    fs.mkdirSync(EVIDENCE, { recursive: true })
    fs.writeFileSync(LOG, san(lines.join('\n')) + '\nEXIT_CODE=' + (fail === 0 ? 0 : 1) + '\n')
    if (fs.existsSync(SCENARIO)) {
      for (const file of fs.readdirSync(SCENARIO)) {
        if (!file.startsWith('manager-')) continue
        const raw = fs.readFileSync(path.join(SCENARIO, file), 'utf8')
        fs.writeFileSync(path.join(EVIDENCE, 'm5.2-acceptance-' + stamp + '-' + file) + '.txt', san(raw))
      }
    }
  }
  if (fail > 0) process.exitCode = 1
}
main().catch((error) => { console.error(error); log('FATAL: ' + error.message); fs.writeFileSync(LOG, san(lines.join('\n'))); process.exit(1) })
