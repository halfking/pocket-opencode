import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createEmailOwnershipBrowserFixture } from './fixtures/email-ownership-browser.mjs'

test('inactive mail views expire on identity changes while another cached draft survives', async () => {
  const fixture = await createEmailOwnershipBrowserFixture()
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.goto(fixture.url)
    await page.getByRole('button', { name: 'Create Alice mail', exact: true }).click()
    await page.locator('#cached-mail-rows').filter({ hasText: 'Alice private mail' }).waitFor()
    await page.locator('#select-preview').click()
    assert.match(await page.locator('#cached-mail-selection').innerText(), /Alice private/)
    await page.locator('#route-invoice').click()
    await page.locator('#cached-mail-rows').filter({ hasText: 'Alice private invoice' }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('#cached-mail-rows').length === 1)
    await page.locator('#select-preview').click()
    await page.locator('#route-other').click()
    await page.locator('#other-draft').fill('UNSAVED RECORDING DRAFT')
    await page.getByRole('button', { name: 'Bob', exact: true }).click()
    assert.equal(await page.locator('#other-draft').inputValue(), 'UNSAVED RECORDING DRAFT')
    await page.locator('#route-mail').click()
    await page.waitForFunction(() => document.querySelectorAll('#cached-mail-rows').length === 1 && document.querySelector('#cached-mail-rows')?.textContent === '[]')
    assert.equal(await page.locator('#cached-mail-selection').innerText(), '')
    await page.locator('#route-invoice').click()
    await page.waitForFunction(() => document.querySelectorAll('#cached-mail-rows').length === 1 && document.querySelector('#cached-mail-rows')?.textContent === '[]')
    assert.equal(await page.locator('#cached-mail-selection').innerText(), '')
    await page.getByRole('button', { name: 'Alice', exact: true }).click()
    await page.locator('#cached-mail-rows').filter({ hasText: 'Alice private invoice' }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('#cached-mail-rows').length === 1)
    assert.equal(await page.locator('#cached-mail-selection').innerText(), '')
    await page.locator('#route-other').click()
    assert.equal(await page.locator('#other-draft').inputValue(), 'UNSAVED RECORDING DRAFT')
  } finally { await browser.close(); await fixture.server.close() }
})
