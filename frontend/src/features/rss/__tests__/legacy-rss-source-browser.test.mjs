import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createRssStateBrowserFixture } from './rss-item-state-browser.mjs'

test('RSS sources require explicit confirmation before making a local metadata copy', async () => {
  const { server, url } = await createRssStateBrowserFixture({ legacySource: true })
  let browser
  try {
    browser = await chromium.launch()
    const page = await browser.newPage()
    await page.goto(url)
    await page.getByRole('button', { name: 'List view', exact: true }).click()
    await page.getByRole('button', { name: '源 (1)', exact: true }).click()
    const save = page.getByRole('button', { name: 'Save source metadata locally', exact: true })
    const dialog = page.getByRole('dialog')
    const snapshot = () => page.evaluate(async () => {
      const { localDB, requests } = window.rssStateFixture
      const db = localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
      return {
        copies: await db.query("SELECT owner_user_id,ext_id,reset_epoch FROM local_portal_records WHERE kind='source'"),
        outbox: await db.query('SELECT * FROM local_portal_record_outbox'),
        reads: requests.filter(request => request.path.endsWith('/migration-snapshot')).map(request => request.owner),
      }
    })

    await save.click()
    await dialog.getByText(/Legacy feed.*workspace one/).waitFor()
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.deepEqual(await snapshot(), { copies: [], outbox: [], reads: ['A'] })

    await save.click()
    await dialog.waitFor()
    await page.evaluate(() => document.querySelector('#bob').click())
    await dialog.getByRole('button', { name: '确定', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.deepEqual(await snapshot(), { copies: [], outbox: [], reads: ['A', 'A'] })

    await save.click()
    await dialog.getByRole('button', { name: '确定', exact: true }).click()
    await page.getByText('Source metadata saved on this device.', { exact: true }).waitFor()
    assert.deepEqual(await snapshot(), {
      copies: [{ owner_user_id: 'B', ext_id: 'pocketd-source:src_legacy', reset_epoch: null }],
      outbox: [], reads: ['A', 'A', 'B', 'B'],
    })
  } finally {
    await browser?.close()
    await server.close()
  }
})
