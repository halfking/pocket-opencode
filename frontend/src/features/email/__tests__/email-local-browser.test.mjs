import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { chromium } from 'playwright'
import { createPrivacyBrowserFixture, pixel } from './fixtures/email-privacy-browser.mjs'


let fixture, browser
before(async () => {
  fixture = await createPrivacyBrowserFixture()
  browser = await chromium.launch()
})
after(async () => { await browser?.close(); await fixture?.server.close() })

test('local rendering blocks every CSS resource grammar before the browser sees it', async () => {
  const page = await browser.newPage()
  const resources = []
  page.on('request', request => { if (request.url().includes('__privacy_resource__')) resources.push(request.url()) })
  await page.goto(`${fixture.url}?mode=local`)
  await page.getByText('保留本地邮件原文', { exact: true }).waitFor()
  await page.waitForLoadState('networkidle')
  assert.deepEqual(resources, [], 'local CSS/image rendering must cause zero resource requests')
  assert.match(await page.locator('.privacy-notice').innerText(), /仅本地/)
  assert.equal(await page.locator('img[alt="embedded"]').getAttribute('src'), pixel)
  assert.equal(await page.getByText('保留本地邮件原文', { exact: true }).evaluate(el => el.style.padding), '7px')
  assert.equal(await page.getByText('保留本地邮件原文', { exact: true }).evaluate(el => el.style.color), 'rgb(1, 2, 3)')
  assert.match(await page.getByText('用户链接', { exact: true }).getAttribute('href'), /\/__privacy_resource__\/explicit$/)
  await page.close()
})

test('switching local never replays preloads from the previous cloud cache', async () => {
  const page = await browser.newPage()
  await page.goto(`${fixture.url}?mode=cloud`)
  await page.getByText('保留本地邮件原文', { exact: true }).waitFor()
  await page.waitForLoadState('networkidle')
  const before = await page.evaluate(() => window.privacyHarness.calls.length)
  assert.ok(before > 0, 'cloud mode must exercise the actual preload boundary')
  await page.getByRole('button', { name: '切换本地' }).click()
  await page.waitForFunction(() => window.privacyHarness.policy.modules.email === 'local')
  await page.waitForLoadState('networkidle')
  assert.deepEqual(await page.evaluate(offset => window.privacyHarness.calls.slice(offset), before), [], 'sync local watcher must not fetch against the old cloud cache')
  assert.equal(await page.locator('.body.html [style*="__privacy_resource__"]').count(), 0)
  await page.getByRole('button', { name: '切换云端' }).click()
  await page.waitForFunction(() => window.privacyHarness.policy.modules.email === 'cloud')
  await page.waitForLoadState('networkidle')
  assert.ok(await page.evaluate(offset => window.privacyHarness.calls.length > offset, before), 'cloud confirmation restores images')
  await page.close()
})
