import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createPortalMailBrowserFixture } from './fixtures/portal-mail-browser.mjs'

test('mail metadata UI preserves native mail, isolates owners, and refreshes remote changes', async () => {
  const fixture = await createPortalMailBrowserFixture(), browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.goto(fixture.url)
    await page.locator('summary').click()
    await page.getByRole('button', { name: 'Create local mail', exact: true }).click()
    const summary = page.locator('.portal-mail li').filter({ hasText: 'Original mail' })
    await summary.getByText('Original summary', { exact: true }).waitFor()
    await summary.getByRole('button', { name: 'Edit', exact: true }).click()
    await page.getByLabel('Summary', { exact: true }).fill('  Edited summary\n')
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    await summary.getByText('Edited summary', { exact: true }).waitFor()
    assert.equal(await page.locator('.portal-mail form').count(), 0, 'Successful save must close editor')
    assert.equal(await summary.getByRole('button', { name: 'Edit', exact: true }).isEnabled(), true, 'Successful save must release busy state')
    page.once('dialog', dialog => dialog.accept())
    await page.locator('.portal-mail li').filter({ hasText: 'Mailbox A' }).getByRole('button', { name: 'Delete', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('.portal-mail li').length === 1)
    assert.equal(await summary.getByRole('button', { name: 'Edit', exact: true }).isEnabled(), true, 'Successful delete must release busy state')
    assert.match(await page.locator('#source').innerText(), /PRIVATE BODY/)
    await page.getByRole('button', { name: 'Bob', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('.portal-mail li').length === 0)
    await page.getByRole('button', { name: 'Other workspace', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('.portal-mail li').length === 0)
    await page.getByRole('button', { name: 'Alice', exact: true }).click()
    await summary.getByText('Edited summary', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Remote update', exact: true }).click()
    await page.getByText('REMOTE SUMMARY', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Remote delete', exact: true }).click()
    await page.waitForFunction(() => !document.querySelector('.portal-mail ul')?.textContent?.includes('REMOTE SUMMARY'))
    assert.match(await page.locator('#source').innerText(), /Original mail/)
    assert.match(await page.locator('#source').innerText(), /PRIVATE BODY/)
  } finally { await browser.close(); await fixture.server.close() }
})
