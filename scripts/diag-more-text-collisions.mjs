// 查首页上「更多」这个文案到底出现在几个可点元素上 —— Maestro 的 tapOn{text} 取第一个匹配。
//
// more-grid-reach.yaml 点「更多|More」没进到 #/more，但 adb 按坐标点是通的
// （diag-bottom-nav-taps.mjs 实测：更多 → hash=#/more ✅）。
// ⇒ 差别落在**文本匹配**上：要么首页还有别处写着「更多」被优先匹配到，
//   要么这个 tab 的可访问性文本不是「更多」。
//
// 只读。
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const cdp = await openCdp({ pkg: PKG })
try {
  await cdp.ev(`location.hash = '#/ai'`)
  await sleep(2500)
  const out = await cdp.ev(`(function(){
    var hits = [];
    var all = document.querySelectorAll('*');
    for (var i = 0; i < all.length; i++) {
      var e = all[i];
      // 只看自身文本节点（不含子孙），否则父容器会因为聚合了子文本而全部命中
      var own = '';
      for (var j = 0; j < e.childNodes.length; j++) {
        if (e.childNodes[j].nodeType === 3) own += e.childNodes[j].nodeValue;
      }
      own = own.replace(/\\s+/g, ' ').trim();
      if (!own) continue;
      if (!/更多|More/.test(own)) continue;
      var r = e.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) continue;
      var clickable = e.closest('a,button,[role="button"],[role="tab"]');
      hits.push({
        tag: e.tagName, text: own.slice(0, 24),
        rect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)],
        clickableTag: clickable ? clickable.tagName : null,
        clickableText: clickable ? (clickable.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 20) : null,
      });
    }
    return JSON.stringify({ hash: location.hash, hits: hits });
  })()`)
  const { hash, hits } = JSON.parse(out)
  console.log(`hash=${hash}，含「更多/More」的元素共 ${hits.length} 个：`)
  for (const h of hits) {
    console.log(`  <${h.tag}> "${h.text}" rect=${JSON.stringify(h.rect)}`)
    console.log(`      可点祖先：${h.clickableTag ? `<${h.clickableTag}> "${h.clickableText}"` : '（无 —— 不可点）'}`)
  }
  const clickable = hits.filter((h) => h.clickableTag)
  console.log('')
  console.log(clickable.length === 1
    ? '✅ 只有一个可点的「更多」，Maestro 不该匹配错 —— 失败另有原因。'
    : `⚠️ 有 ${clickable.length} 个可点的「更多」：\`tapOn{text:"更多|More"}\` 的命中对象不确定。`)
} finally {
  await cdp.close()
}
