const { chromium } = require('playwright-core');
const BASE = 'http://127.0.0.1';
const PASSWORD = 'PclM0!234567';
const OUT = 'D:/Projects/DSHL/PCL-DSH-Linux/.build-tools/nginx-prod.png';
(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1180, height: 760 } });
  const page = await context.newPage();
  const consoleErrors = []; const pageErrors = []; const failed = []; const sockets = []; const badResponses = [];
  page.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text().slice(0, 300)); });
  page.on('pageerror', e => pageErrors.push(String(e).slice(0, 300)));
  page.on('requestfailed', r => { if (!r.url().includes('favicon')) failed.push(r.url().slice(0, 200)); });
  page.on('websocket', ws => sockets.push(ws.url()));
  page.on('response', r => { if (r.status() >= 400) badResponses.push(r.status() + ' ' + r.url().slice(0, 200)); });
  const result = {};
  try {
    await page.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 30000 });
    await page.waitForTimeout(1500);
    result.firstUrl = page.url();
    if (page.url().includes('/login')) {
      await page.fill('input[type=password]', PASSWORD);
      await page.click('button[type=submit]');
      await page.waitForURL(u => !u.toString().includes('/login'), { timeout: 20000 });
    }
    await page.waitForTimeout(2000);
    result.afterLoginUrl = page.url();
    result.homeText = (await page.locator('body').innerText()).slice(0, 300).replace(/\s+/g, ' ');
    const api = context.request;
    const instance = (await (await api.get(BASE + '/api/instances')).json())[0];
    result.instance = instance ? { id: instance.id, status: instance.status } : null;
    if (instance && instance.status !== '运行中') {
      const me = await (await api.get(BASE + '/api/auth/me')).json();
      const launch = await api.post(BASE + '/api/instances/' + instance.id + '/launch', { headers: { 'X-XSRF-TOKEN': me.csrfToken } });
      result.launchStatus = launch.status();
      const jobId = (await launch.json()).id;
      for (let i = 0; i < 150; i++) {
        await page.waitForTimeout(2000);
        const job = await (await api.get(BASE + '/api/jobs/' + jobId)).json();
        if (job.state !== 'RUNNING' && job.state !== 'PENDING') { result.job = { state: job.state, error: job.error }; break; }
      }
    }
    await page.goto(BASE + '/workbench/' + instance.id + '/', { waitUntil: 'domcontentloaded', timeout: 60000 });
    await page.waitForTimeout(9000);
    result.workbenchUrl = page.url();
    result.workbenchTitle = await page.title();
    result.workbenchText = (await page.locator('body').innerText()).slice(0, 400).replace(/\s+/g, ' ');
    result.websockets = sockets;
    result.consoleErrors = consoleErrors;
    result.pageErrors = pageErrors;
    result.failedRequests = failed;
    result.badResponses = badResponses;
    await page.screenshot({ path: OUT, fullPage: false });
    result.screenshot = OUT;
    try {
      const me2 = await (await api.get(BASE + '/api/auth/me')).json();
      await api.post(BASE + '/api/instances/' + instance.id + '/stop', { headers: { 'X-XSRF-TOKEN': me2.csrfToken } });
      result.stopped = true;
    } catch (e) { result.stopped = String(e); }
  } catch (e) {
    result.error = String(e).slice(0, 800);
    try { await page.screenshot({ path: OUT }); } catch (_) {}
  } finally {
    await browser.close();
    console.log(JSON.stringify(result, null, 2));
  }
})();
