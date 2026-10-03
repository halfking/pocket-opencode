// 探针：能不能从 CDP 拿到 Pinia 实例，从而**直接清空 store 的 state**。
//
// 为什么需要它（2026-10-04 第二次踩坑）：
//   「先导航到 #/ai 再 removeItem」这个修法**前提是错的** —— 导航不销毁 Pinia store。
//   Pinia 的 defineStore 建的是**全局单例**，只要本次 App 会话里挂载过一次
//   （例如上一轮 flow 进过 #/flashcards），内存里的 deckConfigs 就一直活着，
//   之后任何一次 persistCache()（syncFromServer / applyReviewLocally 等）都会写回磁盘。
//   之前那次「正向验证」之所以通过，是因为那会话里 store 压根没被挂载过 ——
//   两个变量一起变了，结论不成立。
//
// ⇒ 要清的是**内存里的 store state**，不是路由、也不是 localStorage。
//    本探针只负责回答「拿不拿得到、store id 叫什么、state 在哪」，不做修改。
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'

const PROBE = `(() => {
  const root = document.querySelector('#app') || document.body.firstElementChild
  const app = root && root.__vue_app__
  if (!app) return JSON.stringify({ ok: false, why: 'no __vue_app__', rootTag: root ? root.tagName : null })
  const pinia = app.config && app.config.globalProperties && app.config.globalProperties.$pinia
  if (!pinia) {
    return JSON.stringify({ ok: false, why: 'no $pinia', globals: Object.keys(app.config.globalProperties || {}) })
  }
  const ids = Array.from(pinia._s instanceof Map ? pinia._s.keys() : [])
  const state = pinia.state && pinia.state.value ? Object.keys(pinia.state.value) : []
  const fc = pinia.state && pinia.state.value && pinia.state.value.flashcards
  return JSON.stringify({
    ok: true,
    storeIds: ids,
    stateKeys: state,
    flashcards: fc ? { deckConfigs: (fc.deckConfigs || []).length, cards: (fc.cards || []).length, notes: (fc.notes || []).length } : null,
  })
})()`

const cdp = await openCdp({ pkg: PKG })
try {
  const out = await cdp.ev(PROBE)
  console.log(typeof out === 'string' ? out : JSON.stringify(out, null, 2))
} finally {
  await cdp.close()
}
