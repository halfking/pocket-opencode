// 量闪卡建卡页**所有**保存入口的位置与可点性。
//
// 为什么单独量：FlashcardEditView.vue 里有两个都叫「保存」的按钮——
//   :8   header 的 <button class="save-link">
//   :175 底部 <button class="primary" type="submit">
// flow 现在写的是 `tapOn: { text: "保存|Save", enabled: true }`，
// 文本相同 ⇒ 命中哪一个取决于可访问性树顺序，不是显式指定。
// 而底部那个正好落在软键盘的遮挡区（键盘占屏 970..1640 = 41%）。
//
// 判据要回答的是「键盘开着时哪一个真的能点」，所以同时给出：
//   屏幕百分比、IME 顶边百分比、是否落在 IME 覆盖区内。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'

const imeTopPct = () => {
  const out = execFileSync(adbBin, ['-s', S, 'shell', 'dumpsys', 'window'], { encoding: 'utf8', maxBuffer: 33554432 })
  const line = out.split(/\r?\n/).find((l) => /type=ime/.test(l) && /visible=true/.test(l)) || ''
  const f = /frame=\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(line)
  return f ? Math.round((Number(f[2]) / 1640) * 100) : null
}

const READ = `(() => {
  const rows = Array.from(document.querySelectorAll('button, a[role="button"], input[type="submit"]')).map((b, i) => {
    const r = b.getBoundingClientRect()
    return {
      i,
      tag: b.tagName,
      cls: (b.className || '').toString(),
      text: (b.textContent || b.value || '').trim().slice(0, 12),
      disabled: b.disabled === true,
      rect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)],
      pct: [Math.round((r.x + r.width / 2) / window.innerWidth * 100),
            Math.round((r.y + r.height / 2) / window.innerHeight * 100)],
    }
  })
  return JSON.stringify({ hash: location.hash, imeTopPct: ${imeTopPct()},
    scrollY: Math.round(window.scrollY),
    scrollH: document.documentElement.scrollHeight,
    innerH: window.innerHeight,
    buttons: rows })
})()`

const cdp = await openCdp({ pkg: PKG })
try {
  const out = JSON.parse(await cdp.ev(READ))
  console.log(`hash = ${out.hash}`)
  console.log(`IME 顶边 = ${out.imeTopPct === null ? '未显示' : out.imeTopPct + '%'}（该线以下点不到）`)
  console.log(`滚动：scrollY=${out.scrollY} scrollHeight=${out.scrollH} innerHeight=${out.innerH}`)
  const saves = out.buttons.filter((b) => /保存|Save/.test(b.text) || /save|primary/.test(b.cls))
  if (!saves.length) {
    console.log('❌ 页面上没有保存类按钮 —— 组件可能没渲染出来，先别改坐标。')
    process.exitCode = 1
  }
  for (const b of saves) {
    const occluded = out.imeTopPct !== null && b.pct[1] > out.imeTopPct
    console.log(`  [${b.i}] <${b.tag} class="${b.cls}"> "${b.text}" disabled=${b.disabled} ` +
      `rect=${JSON.stringify(b.rect)} 中心=${b.pct[0]}%,${b.pct[1]}%  ${occluded ? '❌ 被键盘遮挡' : '✅ 可点'}`)
  }
  // 命中歧义诊断：同名保存入口 >1 个时，文本选择器就是掷骰子。
  if (saves.length > 1) {
    console.log(`\n⚠️ 有 ${saves.length} 个保存入口，\`tapOn {text:"保存|Save"}\` 的命中对象不确定。`)
  }
} finally {
  await cdp.close()
}
