const path = require('path')
const { chromium } = require('D:/Projects/DSHL/PCL-DSH-Go/web/node_modules/playwright')
async function main() {
  const [base, password, out] = process.argv.slice(2)
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    const context = await browser.newContext({ baseURL: base, locale: 'zh-CN', viewport: { width: 1280, height: 800 } })
    const login = await context.request.post('/api/auth/login', { data: { password } })
    if (login.status() !== 204) throw new Error('login failed: ' + login.status())
    const page = await context.newPage()
    await page.goto('/instances')
    await page.waitForSelector('[data-testid="page-instances"]', { timeout: 15000 })
    await page.waitForFunction(() => document.querySelectorAll('.instance-card').length >= 2, null, { timeout: 15000 })
    await page.waitForTimeout(800)
    await page.screenshot({ path: out })
    const running = await page.getByText('运行中').count()
    if (running < 2) throw new Error('expected >=2 running cards, got ' + running)
    console.log('screenshot ok ' + path.basename(out) + ' running=' + running)
  } finally { await browser.close() }
}
main().catch((error) => { console.error(error); process.exit(1) })
