import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { parse } from '@vue/compiler-sfc'

for (const file of ['TaskBoardView.vue', 'TaskDetailView.vue']) {
  test(`${file} keeps its last content reachable inside a self-scrolling route`, async () => {
    const descriptor = parse(readFileSync(new URL(`../${file}`, import.meta.url), 'utf8')).descriptor
    const styles = descriptor.styles.map(block => block.content).join('\n')
    const browser = await chromium.launch({ headless: true })
    try {
      const page = await browser.newPage({ viewport: { width: 360, height: 820 } })
      await page.setContent(`<style>
        * { box-sizing: border-box; }
        body { margin: 0; }
        main { height: 650px; padding: 12px; overflow: hidden; }
        .fixture-content { height: 1000px; }
        ${styles}
      </style><main><div class="page">
        <div class="fixture-content">Long run list or node details</div>
        <button id="last-control" onclick="this.dataset.clicked='yes'">Last content</button>
      </div></main>`)
      const dimensions = await page.locator('.page').evaluate(element => ({
        client: element.clientHeight, scroll: element.scrollHeight,
        parent: element.parentElement.clientHeight,
      }))
      assert.ok(dimensions.client <= dimensions.parent, 'content must stay within the route viewport')
      assert.ok(dimensions.scroll > dimensions.client, 'long content must have a scrollable range')
      await page.locator('.page').evaluate(element => { element.scrollTop = element.scrollHeight })
      const control = page.locator('#last-control')
      const target = await control.boundingBox(), viewport = await page.locator('main').boundingBox()
      assert.ok(target.y >= viewport.y && target.y + target.height <= viewport.y + viewport.height)
      await control.click({ timeout: 2000 })
      assert.equal(await control.getAttribute('data-clicked'), 'yes')
    } finally { await browser.close() }
  })
}
