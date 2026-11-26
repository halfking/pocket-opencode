import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createNativeRssBrowserFixture } from './rss-native-browser.mjs'

test('new native RSS subscription events reach the portable outbox without enrolling a former discovery owner', async () => {
  const { server, url } = await createNativeRssBrowserFixture()
  let browser
  try {
    browser = await chromium.launch()
    const page = await browser.newPage()
    await page.goto(url)
    const button = name => page.getByRole('button', { name, exact: true })
    const address = page.locator('#rss-add-url')
    const portable = page.locator('.portal-records')

    await address.fill('https://example.test/native')
    await button('发现候选').click()
    await page.locator('.candidates li').filter({ hasText: 'Native A feed' }).click()
    await page.waitForFunction(() => document.querySelector('.manual button')?.disabled === false)
    await button('Pull native changes').click()
    await portable.getByText('Native A feed', { exact: true }).waitFor()
    await page.waitForFunction(() => window.nativeSnapshot?.length === 1)
    const [queued] = await page.evaluate(() => window.nativeSnapshot)
    const item = JSON.parse(queued.item_json)
    assert.equal(item.ext_id, 'pocketd-source:A-1')
    assert.equal(item.epoch, 0)

    await button('Bob').click()
    await page.getByText('No shared RSS records yet', { exact: true }).waitFor()
    assert.doesNotMatch(await page.locator('body').innerText(), /Native A feed/)
    await button('Alice').click()
    await button('Pause discovery').click()
    await address.fill('https://example.test/private-A')
    await button('发现候选').click()
    await page.waitForFunction(() => window.nativeDiscoveryStarted)
    await button('Bob').click()
    await button('Release discovery').click()
    await page.getByText('No shared RSS records yet', { exact: true }).waitFor()
    assert.doesNotMatch(await page.locator('body').innerText(), /private-A|Native A feed/)
    const sent = await page.evaluate(async () => {
      await window.nativeRssFixture.ingest()
      return window.nativeRssFixture.requests.filter(request => request.path === '/api/rss/sources/queued')
        .map(request => request.owner)
    })
    assert.deepEqual(sent, ['A'])
  } finally {
    await browser?.close()
    await server.close()
  }
})
