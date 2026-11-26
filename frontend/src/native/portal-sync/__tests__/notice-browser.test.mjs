import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createNotesSyncNoticeFixture } from './notice-browser.mjs'

test('clock resubmission is separate from ordinary retry and requires available confirmation', async () => {
  const fixture = await createNotesSyncNoticeFixture({ clockPending: 2 })
  try {
    const browser = await chromium.launch()
    try {
      const page = await browser.newPage()
      await page.goto(fixture.url)
      await page.getByTestId('portal-notes-sync-retry').click()
      assert.equal(await page.locator('#confirmed').innerText(), '0')
      await page.getByTestId('portal-notes-clock-retry').click()
      await page.getByText('请先校正设备时间。是否以当前设备时间重新提交 2 项待处理的本地更改？云端较早的版本可能被替换。').waitFor()
      await page.getByRole('button', { name: '取消', exact: true }).click()
      assert.equal(await page.locator('#confirmed').innerText(), '0')
      await page.getByTestId('portal-notes-clock-retry').click()
      await page.getByRole('button', { name: '确定', exact: true }).click()
      assert.equal(await page.locator('#confirmed').innerText(), '1')
    } finally { await browser.close() }
  } finally { await fixture.server.close() }
})
