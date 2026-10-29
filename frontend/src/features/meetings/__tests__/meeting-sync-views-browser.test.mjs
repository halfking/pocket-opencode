import test from 'node:test'
import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { createMeetingViewsFixture } from './fixtures/meeting-sync-views-browser.mjs'

test('actual cached meeting list expires owners and reflects remote edits without destroying other drafts', async () => {
  const fixture = await createMeetingViewsFixture(),
    browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.goto(fixture.url)
    await page.getByText('A:one private meeting', { exact: true }).waitFor()
    await page.getByTestId('portal-meetings-sync-retry').click()
    await page.waitForFunction(() => document.querySelector('#retry-count').textContent === '1')
    await page.locator('#other').click()
    await page.locator('#other-draft').fill('Unrelated recording draft')
    await page.locator('#account').click()
    await page.locator('#meetings').click()
    await page.getByText('B:one private meeting', { exact: true }).waitFor()
    assert.equal(await page.getByText('A:one private meeting', { exact: true }).count(), 0)
    await page.locator('#remote').click()
    await page.getByText('B:one REMOTE meeting', { exact: true }).waitFor()
    await page.locator('#other').click()
    assert.equal(await page.locator('#other-draft').inputValue(), 'Unrelated recording draft')
    await page.locator('#delete').click()
    await page.locator('#meetings').click()
    await page.getByText('还没有会议记录', { exact: true }).waitFor()
    await page.locator('#workspace').click()
    await page.getByText('B:two private meeting', { exact: true }).waitFor()
    await page.locator('#resolve').click()
    assert.equal(await page.getByTestId('portal-meetings-sync-error').count(), 0)
  } finally {
    await browser.close()
    await fixture.server.close()
  }
})
