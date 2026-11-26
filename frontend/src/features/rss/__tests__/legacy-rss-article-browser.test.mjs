import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createRssStateBrowserFixture } from './rss-item-state-browser.mjs'

test('article detail confirms a local archive while preserving its visible native body and shared-copy entry', async () => {
  const { server, url } = await createRssStateBrowserFixture({ legacyArticle: true })
  let browser
  try {
    browser = await chromium.launch()
    const page = await browser.newPage()
    await page.goto(url)
    await page.getByText('PRIVATE BODY A', { exact: true }).waitFor()
    const save = page.getByRole('button', { name: 'Save article metadata locally', exact: true })
    const dialog = page.getByRole('dialog')
    const confirm = dialog.getByRole('button', { name: '确定', exact: true })
    const localMetadata = () => page.evaluate(async () => {
      const { localDB, requests } = window.rssStateFixture
      const db = localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
      return {
        rows: await db.query("SELECT owner_user_id,kind,ext_id,item_json FROM local_portal_records WHERE module='rss'"),
        outbox: await db.query('SELECT * FROM local_portal_record_outbox'),
        requests: requests.filter(request => request.path.endsWith('/migration-snapshot')).map(request => request.owner),
      }
    })
    await save.click()
    await dialog.getByText(/Article A.*workspace one/).waitFor()
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.deepEqual(await localMetadata(), { rows: [], outbox: [], requests: ['A'] })

    await save.click()
    await dialog.waitFor()
    await page.evaluate(() => document.querySelector('#bob').click())
    await confirm.click()
    await dialog.waitFor({ state: 'hidden' })
    assert.deepEqual(await localMetadata(), { rows: [], outbox: [], requests: ['A', 'A'] })

    await save.click()
    await confirm.click()
    await page.getByText('Article metadata saved on this device.', { exact: true }).waitFor()
    await page.getByText('PRIVATE BODY B', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Save a shared record', exact: true }).waitFor()
    const result = await localMetadata()
    assert.deepEqual(result.requests, ['A', 'A', 'B', 'B'])
    assert.deepEqual(result.outbox, [])
    assert.equal(result.rows.length, 1)
    assert.equal(result.rows[0].owner_user_id, 'B')
    assert.equal(result.rows[0].kind, 'item')
    assert.equal(result.rows[0].ext_id, 'rss-item:pocketd-item:article')
    const item = JSON.parse(result.rows[0].item_json)
    assert.deepEqual(item.payload, { source: 'source', url: 'https://example.test/article', summary: 'Portable summary' })
  } finally {
    await browser?.close()
    await server.close()
  }
})
