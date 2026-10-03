// 探针：确认 vue-i18n 到底在**哪一步**报 `Not allowed nest placeholder`。
// 依次在三个点上抓 console.error：createI18n / t() / 其它。
// 用途是给 check-i18n-nested-placeholder.mjs 选对判据落点 ——
// 前两版分别判 t() 抛错、判 createI18n 打 console，**两次都判据失明**。
import { createI18n } from 'vue-i18n'

const MSG = 'Use {{c1::a}} or {{c1::b}} x.'

const grab = (label, fn) => {
  const hits = []
  const orig = console.error
  const origWarn = console.warn
  console.error = (...a) => hits.push('ERR ' + a.map(String).join(' ').split('\n')[0])
  console.warn = (...a) => hits.push('WARN ' + a.map(String).join(' ').split('\n')[0])
  let ret
  let threw = null
  try {
    ret = fn()
  } catch (e) {
    threw = e.message
  } finally {
    console.error = orig
    console.warn = origWarn
  }
  console.log(`\n[${label}]`)
  console.log('  threw:', threw)
  console.log('  ret  :', JSON.stringify(ret))
  console.log('  console 命中:', hits.length)
  for (const h of hits.slice(0, 4)) console.log('    ', h)
}

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { k: MSG } } })
console.log('=== 探针 1: t() ===')
grab('t()', () => i18n.global.t('k'))

console.log('\n=== 探针 2: 挂到组件渲染（te()）===')
try {
  const { createSSRApp, h } = await import('vue')
  const { useI18n } = await import('vue-i18n')
  const app = createSSRApp({
    setup() {
      const { t } = useI18n()
      return () => h('div', t('k'))
    },
  })
  const { renderToString } = await import('@vue/server-renderer')
  grab('SSR render', () => renderToString(app))
} catch (e) {
  console.log('  SSR 探针不可用：', e.message.split('\n')[0])
}
