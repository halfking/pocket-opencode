import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createEnexPortalBrowserFixture } from './enex-portal-browser.mjs'

test('real PKM route redirects imported Markdown to the notes renderer and preserves attachments',async()=>{
  const fixture=await createEnexPortalBrowserFixture(),browser=await chromium.launch()
  try{
    const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message))
    await page.goto(fixture.url);await page.getByText('No active note',{exact:true}).waitFor()
    await page.getByRole('button',{name:'Import Markdown',exact:true}).click()
    await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('"attachments":1'))
    assert.match(await page.locator('#status').innerText(),/"format":"markdown"/)
    assert.doesNotMatch(await page.locator('#status').innerText(),/UFJJVkFURSBBVFRBQ0hNRU5U/)
    await page.getByRole('button',{name:'Open through PKM',exact:true}).click()
    await page.locator('article.markdown-body h1').getByText('Original Markdown',{exact:true}).waitFor()
    assert.equal(await page.locator('.tiptap').count(),0)
    await page.getByRole('button',{name:'Remote edit',exact:true}).click()
    await page.locator('article.markdown-body h1').getByText('Remote Markdown',{exact:true}).waitFor()
    assert.match(await page.locator('#status').innerText(),/"attachments":1/)
    await page.getByRole('button',{name:'Bob',exact:true}).click()
    await page.getByText('No active note',{exact:true}).waitFor()
    assert.doesNotMatch(await page.locator('#status').innerText(),/Exact|Original Markdown/)
    await page.getByRole('button',{name:'Alice',exact:true}).click()
    await page.getByRole('button',{name:'Open through PKM',exact:true}).click()
    await page.locator('article.markdown-body h1').getByText('Remote Markdown',{exact:true}).waitFor()
    await page.getByRole('button',{name:'Remote delete',exact:true}).click()
    await page.waitForFunction(()=>!document.querySelector('article.markdown-body'))
    assert.match(await page.locator('#status').innerText(),/"attachments":1/)
    assert.deepEqual(errors,[])
  }finally{await browser.close();await fixture.server.close()}
})
