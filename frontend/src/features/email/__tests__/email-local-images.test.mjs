import assert from 'node:assert/strict'
import { test } from 'node:test'
import { JSDOM } from 'jsdom'
import { hideRemoteImages, preloadRemoteImages } from '../email-image-preload.ts'

globalThis.document = new JSDOM('').window.document

test('local HTML keeps only resource-free presentation declarations and data images', () => {
  const out = hideRemoteImages(`<div style="color:red;padding:7px;background-color:blue;--resource:url(https://tracker.example/custom);background-image:image-set('https://tracker.example/set' 1x);cursor:url(https://tracker.example/cursor),auto">Text
    <style>@import 'https://tracker.example/import';p{color:green}</style>
    <img src="/tracker-relative"><img src="data:image/png;base64,AA"></div>`)
  const template = document.createElement('template')
  template.innerHTML = out
  const div = template.content.querySelector('div')
  assert.equal(div.style.color, 'red')
  assert.equal(div.style.padding, '7px')
  assert.equal(div.style.backgroundColor, 'blue')
  assert.equal(div.style.backgroundImage, '')
  assert.equal(div.style.cursor, '')
  assert.equal(div.style.getPropertyValue('--resource'), '')
  assert.equal(template.content.querySelectorAll('style').length, 0)
  assert.ok(out.includes('Text'))
  assert.ok(out.includes('data:image/png;base64,AA'))
  assert.ok(!out.includes('tracker'))
})
const mail = '<p>Private email stays readable</p><img src="https://tracker.example/open"><div style="background-image:url(//tracker.example/bg)">Background</div><img src="data:image/png;base64,AA">'

test('local email images never fetch and returned HTML cannot trigger browser tracking', async () => {
  let requests = 0
  const out = await preloadRemoteImages(mail, { allowRemote: () => false, fetchImpl: async () => { requests++; return new Response('pixel', { headers: { 'Content-Type': 'image/png' } }) } })
  assert.equal(requests, 0)
  assert.ok(out.includes('Private email stays readable'))
  assert.ok(out.includes('data:image/png;base64,AA'))
  assert.ok(!out.includes('tracker.example'))
})

test('switching local during preload prevents later requests and removes unresolved sources', async () => {
  let allowed = true
  let requests = 0
  const out = await preloadRemoteImages('<p>Original text</p>' + Array.from({ length: 6 }, (_, i) => `<img src="https://tracker.example/${i}">`).join(''), {
    allowRemote: () => allowed,
    fetchImpl: async () => { requests++; allowed = false; return new Response('', { status: 500 }) },
  })
  assert.equal(requests, 1)
  assert.ok(out.includes('Original text'))
  assert.ok(!out.includes('tracker.example'))
})
