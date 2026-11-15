import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { parse } from '@vue/compiler-sfc'

for (const [file, prefix] of [['ApprovalModal.vue', 'am'], ['HandoffDialog.vue', 'hd']]) {
  test(`${file} keeps its actions reachable above the keyboard`, async () => {
    const component = parse(readFileSync(new URL(`../${file}`, import.meta.url), 'utf8')).descriptor
    const styles = component.styles.map(block => block.content).join('\n')
    const browser = await chromium.launch({ headless: true })
    try {
      const page = await browser.newPage({ viewport: { width: 360, height: 820 } })
      await page.setContent(`<style>
        * { box-sizing: border-box; }
        :root { --kb-inset: 260px; --z-dialog: 1400; }
        ${styles}
      </style><div class="${prefix}-overlay"><section class="${prefix}-card">
        <input aria-label="Comment"><div style="height:1000px;flex-shrink:0">Form content</div>
        <footer><button onclick="this.dataset.clicked='yes'">Confirm</button></footer>
      </section></div>`)
      const overlay = await page.locator(`.${prefix}-overlay`).boundingBox()
      assert.ok(overlay.y + overlay.height <= 560)
      const button = page.getByRole('button', { name: 'Confirm' })
      await button.scrollIntoViewIfNeeded()
      const bounds = await button.boundingBox()
      assert.ok(bounds.y >= 0 && bounds.y + bounds.height <= 560)
      await button.click()
      assert.equal(await button.getAttribute('data-clicked'), 'yes')
    } finally {
      await browser.close()
    }
  })
}
