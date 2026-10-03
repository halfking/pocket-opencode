// 探测：CDP 设 location.hash 能否真正驱动路由。
//
// 背景（.maestro/_goto-pkm.yaml 的注释里有两次实测记录）：
//   · tapOn: {point: "88%,95%"}  两次都没命中，Maestro 报 COMPLETED 但页面不动
//   · evalScript: ${location.hash = '#/more'} → TypeError: Cannot set property
//     'hash' of undefined（evalScript 不在 WebView 的 JS 上下文里跑）
//
// 而 harness 的 CDP 通道（scripts/lib/adb-cdp.mjs 的 ev）**是**在 WebView 上下文里的。
// 若 CDP 设 hash 能驱动路由，底部导航就可以彻底绕开 tapOn。
//
// 这条探测只读 + 改一次 hash，不点 UI、不写业务数据。

import { openCdp } from './lib/adb-cdp.mjs'

const sleep = (ms) => new Promise((s) => setTimeout(s, ms))
const flat = (s) => String(s ?? '').replace(/\s+/g, ' ').trim()

const cdp = await openCdp()
try {
  const read = async () => ({
    hash: flat(await cdp.ev('location.hash')),
    path: flat(await cdp.ev('location.pathname')),
    body: flat(await cdp.ev('document.body.innerText')).slice(0, 260),
  })

  const before = await read()
  console.log('=== 起点 ===')
  console.log('  hash =', before.hash)
  console.log('  body =', before.body)

  // 判据：body 里出现「更多」的二级入口文案，才算真的切过去了。
  const MORE_MARKERS = /更多功能|邮箱|闪卡|订阅|全部已读/
  const isMore = (b) => MORE_MARKERS.test(b)

  // ⚠️ 阳性对照：标记集必须**在起点就不命中**。
  // 没有这道对照，一个写得足够松的标记集会让本探测在任何页面都报 YES，
  // 于是「CDP 能驱动路由」变成一句永远为真的废话 —— 而这正是判据最常见的
  // 失效形态：判据恒真。marker 漏字、App 改文案，都会这样静默退化。
  if (isMore(before.body)) {
    console.error('❌ 阳性对照失败：标记集在**起点页**就已命中 ⇒ 判据恒真，本探测无意义。')
    console.error(`   起点 body = ${before.body}`)
    process.exitCode = 1
    throw new Error('MORE_MARKERS matches the starting page; the probe cannot discriminate')
  }
  console.log('  阳性对照：标记集在起点**不**命中 ✅（判据不是恒真）')

  console.log('\n=== 通过 CDP 设 location.hash = "#/more" ===')
  const ret = await cdp.ev("(() => { location.hash = '#/more'; return 'set'; })()", 5000)
  console.log('  eval 返回 =', JSON.stringify(ret))
  await sleep(3000)
  const after = await read()
  console.log('  hash =', after.hash)
  console.log('  body =', after.body)
  console.log('  页面到了「更多」？', isMore(after.body) ? 'YES' : 'NO')

  console.log('\n=== 对照：再用 Vue Router 自己的 API 推一次 ===')
  const r2 = await cdp.ev(`(() => {
    const h = window.location.hash
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    return { hash: h, hasVueRouter: !!(window.__VUE_DEVTOOLS_GLOBAL_HOOK__ || document.querySelector('#app')) }
  })()`, 5000)
  console.log('  ', JSON.stringify(r2))
  await sleep(1500)
  const after2 = await read()
  console.log('  body =', after2.body)
  console.log('  到了「更多」？', isMore(after2.body) ? 'YES' : 'NO')

  console.log('\n=== 结论 ===')
  if (isMore(after.body) || isMore(after2.body)) {
    console.log('  CDP 改 hash 能驱动路由 ⇒ 底部导航**可以**改走 CDP 绕开 tap。')
  } else {
    console.log('  CDP 改 hash **不能**驱动路由（hash 变了但内容没变）')
    console.log('  ⇒ 大概率是 App 的路由只监听 popstate/自己的一套导航，')
    console.log('     或 hash 相同不触发重算（需要先跳去别处再跳回来）。')
  }
  console.log('\n  ⚠️ 别把这个结论读成「MIUI 吞 tap」的证据。')
  console.log('    2026-10-04 的对照实验：关掉本机制、清掉首页 7 条自造测试探针任务')
  console.log('    （它们曾把首页「需要你介入」面板占满）后，email-accounts 不带任何')
  console.log('    绕过手段就 2/2 通过 ⇒ 当时的红是**测试残留数据盖住了导航区**，')
  console.log('    tap 落在面板上，不是被系统吞掉。')
  console.log('    ⇒ 本探测的用途是「区分导航没发生 vs 页面坏了」，不是「证明设备有 bug」。')
} finally {
  await cdp.close()
}
