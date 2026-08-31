// E2E for OpenCode Pocket SPA — verify AI 网关默认值与 UI 集成
// Backend: http://127.0.0.1:8090, Frontend: http://127.0.0.1:4175
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const DIR = path.join(__dirname, 'shots');
fs.mkdirSync(DIR, { recursive: true });

const shot = async (page, name) => {
  const p = path.join(DIR, name);
  await page.screenshot({ path: p, fullPage: false });
  console.log('shot:', p);
};

(async () => {
  const browser = await chromium.launch({
    args: ['--no-sandbox'],
    executablePath: '/Users/xutaohuang/Library/Caches/ms-playwright/chromium_headless_shell-1228/chrome-headless-shell-mac-arm64/chrome-headless-shell',
  });
  const context = await browser.newContext({
    viewport: { width: 393, height: 852 }, // iPhone 14 Pro size, close to iPhone-Test
    isMobile: true,
    hasTouch: true,
    deviceScaleFactor: 2,
    userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148',
  });
  const page = await context.newPage();
  page.on('pageerror', (e) => console.log('[pageerror]', e.message));
  page.on('console', (m) => m.type() === 'error' && console.log('[console error]', m.text()));

  console.log('→ 1) 打开登录页');
  await page.goto('http://127.0.0.1:4175/#/login', { waitUntil: 'networkidle', timeout: 30000 });
  await shot(page, '01-login.png');

  console.log('→ 2) 填入 admin + dev 密码');
  await page.fill('input[autocomplete="username"], input[type="text"]:first-of-type', 'admin').catch(()=>{});
  // 找密码框
  const pwd = await page.locator('input[type="password"]').first();
  await pwd.fill('d18db57a2e35e792b5223e562be2c3ea');
  await shot(page, '02-login-filled.png');

  console.log('→ 3) 提交登录');
  await page.locator('button:has-text("登录")').first().click();
  await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(()=>{});
  await page.waitForTimeout(1500);
  await shot(page, '03-home-after-login.png');

  // dump current URL
  console.log('   url =', page.url());

  console.log('→ 4) 等首页渲染，进入 AI 工具');
  // Try to find menu trigger or AI tools entry
  // Many routes: /workspace, /tasks, /chat etc. Open menu first.
  // Some UIs use a hamburger ≡.
  const menu = page.locator('[aria-label*="菜单"], [aria-label*="menu"]').first();
  if (await menu.count() > 0) {
    await menu.click();
    await page.waitForTimeout(800);
    await shot(page, '04-menu-open.png');
  }

  console.log('→ 5) 导航到 /settings/llm-gateway');
  await page.goto('http://127.0.0.1:4175/#/settings/llm-gateway', { waitUntil: 'networkidle', timeout: 20000 });
  await page.waitForTimeout(2500); // wait for ai-gateway GET config
  await shot(page, '05-settings-llm-gateway.png');

  console.log('→ 6) 抓取页面文字确认 baseURL/apikey/preferred 已注入');
  const html = await page.content();
  const ob = {
    hasBaseURL: html.includes('llm.kxpms.cn'),
    hasApiKeyMask: html.includes('sk-6****51YV'),
    apiKeyFormFilled: await page.locator('#gateway-api-key').inputValue().catch(()=>null),
    baseURLFormValue: await page.locator('#gateway-base-url').inputValue().catch(()=>null),
    formatValue: await page.locator('#gateway-format').inputValue().catch(()=>null),
    modelsCount: await page.evaluate(() => document.querySelectorAll('.pref-chip').length),
    preferredChecked: await page.evaluate(() => {
      const chips = Array.from(document.querySelectorAll('.pref-chip[aria-pressed="true"]'));
      return chips.map(c => c.textContent.trim()).filter(Boolean);
    }),
  };
  console.log('  observed:', JSON.stringify(ob, null, 2));

  await page.waitForTimeout(800);
  await shot(page, '06-settings-llm-gateway-fully-rendered.png');

  console.log('→ 7) 触发"测试连接"验证 baseURL 可达');
  const testBtn = page.locator('button:has-text("测试连接")').first();
  if (await testBtn.count() > 0 && !(await testBtn.isDisabled())) {
    await testBtn.click();
    await page.waitForTimeout(8000); // 让它做完 GET {baseURL}/v1/models
    await shot(page, '07-test-connection.png');
  } else {
    console.log('   跳过测试连接（按钮 disabled）');
  }

  console.log('→ 8) 滚到表单底部看 preferred 模型目录');
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await page.waitForTimeout(1000);
  await shot(page, '08-preferred-bottom.png');

  console.log('→ 9) 进 AI 工具测试一条短 prompt');
  await page.goto('http://127.0.0.1:4175/#/ai-tools', { waitUntil: 'networkidle', timeout: 20000 }).catch(()=>{});
  await page.waitForTimeout(2000);
  await shot(page, '09-ai-tools.png');

  console.log('完成。');
  await browser.close();
})().catch((e) => {
  console.error('FATAL:', e.stack || e.message);
  process.exit(1);
});
