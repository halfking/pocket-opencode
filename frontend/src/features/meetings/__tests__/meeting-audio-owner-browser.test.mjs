import test from 'node:test'
import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { createMeetingAudioFixture } from './fixtures/meeting-audio-browser.mjs'

test('real IndexedDB audio isolates identical IDs, retains unclaimed legacy keys and rejects stale opens', async () => {
  const fixture = await createMeetingAudioFixture(),
    browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.goto(fixture.url)
    await page.locator('#ready').getByText('ready').waitFor()
    const click = async (id, text) => {
      await page.locator('#result').evaluate((e) => (e.textContent = ''))
      await page.locator(id).click()
      if (text) await page.waitForFunction((t) => document.querySelector('#result').textContent === t, text)
    }
    await click('#legacy', 'legacy retained')
    await click('#read', 'empty')
    await click('#save', 'saved A')
    await click('#read', 'A')
    await click('#account', 'switched')
    await click('#read', 'empty')
    await click('#save', 'saved B')
    await click('#read', 'B')
    await click('#remove', 'deleted')
    await click('#read', 'empty')
    await click('#account', 'switched')
    await click('#read', 'A')
    await click('#workspace', 'switched')
    await click('#read', 'empty')
    await click('#workspace', 'switched')
    await click('#legacy', 'legacy retained')
    await click('#read', 'A')
    assert.equal(await page.evaluate(() => window.audioFixture.legacyExists()), true)
    await click('#pause', 'pause armed')
    await page.locator('#save').click()
    await page.locator('#waiting').getByText('waiting').waitFor()
    await click('#account', 'switched')
    await page.locator('#release').click()
    await page.waitForFunction(() => document.querySelector('#result').textContent.startsWith('AbortError:'))
    await click('#read', 'empty')
    await click('#account', 'switched')
    await click('#read', 'A')
  } finally {
    await browser.close()
    await fixture.server.close()
  }
})
