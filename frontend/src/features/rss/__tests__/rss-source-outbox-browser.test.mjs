import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createRssStateBrowserFixture } from './rss-item-state-browser.mjs'

test('add a feed without discovery while offline and create it on reconnect after reload', async () => {
  const { server, url } = await createRssStateBrowserFixture({ queuedSource: true })
  let browser
  try {
    browser = await chromium.launch()
    const page = await browser.newPage()
    const feed = 'https://example.test/reconnect-feed'
    await page.goto(url)
    await page.locator('#rss-add-url').fill(feed)
    await page.getByRole('button', { name: 'Add feed', exact: true }).click()
    const queue = page.getByRole('region', { name: 'Pending subscriptions' })
    await queue.getByText(feed, { exact: true }).waitFor()
    const intent = () => page.evaluate(async () => {
      const { localDB } = window.rssStateFixture
      const db = localDB.bindOperation(localDB.captureOperation({kind:'local-edit'}))
      return (await db.queryOne('SELECT request_json FROM local_rss_source_outbox'))?.request_json
    })
    const original = await intent()
    assert.equal(JSON.parse(original).url, feed)
    await page.reload()
    await queue.getByText(feed, { exact: true }).waitFor()
    await page.waitForFunction(() => !!window.rssStateFixture)
    assert.equal(await intent(), original)
    await page.getByRole('button', { name: 'Online', exact: true }).click()
    await queue.waitFor({ state: 'hidden' })
    await page.getByRole('button', { name: '源 (1)', exact: true }).click()
    await page.locator('.source-list .src-url').getByText(feed, { exact: true }).waitFor()
    assert.equal(await intent(), undefined)
    const posts = await page.evaluate(() => window.rssStateFixture.requests.filter(request => request.path.endsWith('/sources/queued')))
    assert.ok(posts.length > 0)
    assert.ok(posts.every(request => request.body === original))
  } finally {
    await browser?.close()
    await server.close()
  }
})
