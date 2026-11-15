import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { chromium } from 'playwright'
import { parse } from '@vue/compiler-sfc'

const component = parse(readFileSync(new URL('../MessagesHubView.vue', import.meta.url), 'utf8')).descriptor
const style = component.styles.map(block => block.content).join('\n')

test('the message timeline scrolls its mail entry into the usable route viewport', async () => {
  const browser = await chromium.launch({ headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 360, height: 820 } })
    await page.setContent(`
      <style>
        * { box-sizing: border-box; }
        :root { --space-3: 12px; --space-6: 24px; }
        body { margin: 0; }
        main { height: 650px; overflow-y: hidden; padding: 12px; }
        .fixture-messages { flex-shrink: 0; height: 1000px; }
        ${style}
      </style>
      <main><div class="msg-hub">
        <div class="fixture-messages">Message timeline</div>
        <nav class="see-all"><button class="see-all-btn" id="open-mail">打开邮箱</button></nav>
      </div></main>
    `)
    const before = await page.locator('.msg-hub').evaluate(element => ({
      clientHeight: element.clientHeight, scrollHeight: element.scrollHeight,
      overflow: getComputedStyle(element).overflowY,
    }))
    assert.equal(before.overflow, 'auto')
    assert.ok(before.scrollHeight > before.clientHeight)
    await page.locator('.msg-hub').evaluate(element => { element.scrollTop = element.scrollHeight })
    const entry = await page.locator('#open-mail').boundingBox()
    const viewport = await page.locator('main').boundingBox()
    assert.ok(entry.y >= viewport.y && entry.y + entry.height <= viewport.y + viewport.height)
    await page.locator('#open-mail').click({ timeout: 2000 })
  } finally {
    await browser.close()
  }
})
