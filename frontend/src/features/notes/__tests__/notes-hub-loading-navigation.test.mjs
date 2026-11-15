import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { parse } from '@vue/compiler-sfc'

const source = readFileSync(new URL('../NotesHubView.vue', import.meta.url), 'utf8')
const template = parse(source).descriptor.template.content
const translations = JSON.parse(readFileSync(new URL('../../../locales/zh-CN.json', import.meta.url), 'utf8'))
const slotContainer = defineComponent({ setup: (props, { slots }) => () => h('div', slots.default?.()) })

async function renderHub(dbNotReady = false) {
  return renderToString(createSSRApp({
    template,
    components: {
      HeaderActionsPortal: slotContainer,
      ScrollChromePortal: slotContainer,
      SourceFilterBar: defineComponent({ render: () => h('div', 'filters') }),
      Skeleton: defineComponent({ render: () => h('div', 'loading-records') }),
      EmptyState: defineComponent({ render: () => h('div', 'empty-records') }),
      DbLockedState: defineComponent({ render: () => h('div', 'database-locked') }),
    },
    setup: () => ({
      dbNotReady, loading: true, source: 'all', sourceOptions: [], rows: [],
      counts: { total: 0, note: 0, meeting: 0 },
      t: key => key.split('.').reduce((value, part) => value?.[part], translations) ?? key,
      go: () => {}, router: { push: () => {} },
    }),
  }))
}

test('PKM and voice note tools remain available while list sources are pending', async () => {
  const html = await renderHub()
  assert.match(html, /loading-records/)
  assert.match(html, /打开 PKM/)
  assert.match(html, /全部笔记/)
})

test('the locked database screen still gates note tools', async () => {
  const html = await renderHub(true)
  assert.match(html, /database-locked/)
  assert.doesNotMatch(html, /打开 PKM|全部笔记/)
})
