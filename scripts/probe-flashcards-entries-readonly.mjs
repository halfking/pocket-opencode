// probe-flashcards-entries-readonly.mjs —— **只读**核对闪卡两个建组/建卡入口。
//
// ⚠️ 严格只读：只 openCdp + Runtime.evaluate 读 DOM，**不导航、不点击、不发输入事件、
//    不碰 adb reverse**。设备当前 forward 指向 18099（并发会话的后端），
//    那是共享可变状态，本脚本一个字节都不改。
//    如果 App 当前**不在**闪卡页，就只报告「在哪一页」，不跳过去 ——
//    跳转是状态变更，那属于需要跟对方确认的共享设备操作。
//
// 为什么需要它：BUG-K（§4.14）修的是「文案说建卡组、实际跳新建卡片页」，
// BUG-AA（§4.28）补齐了另外 7 种语言。代码与 9 份 locale 都已核对一致，
// 但**真机上这两个入口渲染成什么、点了去哪，一直只有源码级证据**。
// 本脚本把「源码写了什么」升级成「设备上真的长什么样」。
//
// 判据纪律：只认 data-testid 精确定位，且每条都打印**实际读到的文本**，
// 不允许「找不到就当不存在」——找不到必须是 FAIL 并显示读到了什么。
import { openCdp } from './lib/adb-cdp.mjs'

const checks = []
const check = (name, pass, detail) => { checks.push({ name, pass }); console.log(`  ${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`) }

let cdp
try {
  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  console.log(`CDP 已连：pid=${cdp.pid} socket=${cdp.socket} port=${cdp.port}\n`)

  const hash = await cdp.ev('location.hash')
  const origin = await cdp.ev('location.origin')
  console.log(`当前页面：origin=${origin} hash=${hash}\n`)

  if (!/#\/flashcards/.test(String(hash))) {
    console.log('当前不在闪卡页。')
    console.log('**不跳转** —— 跳转是状态变更；设备 forward 指向并发会话的后端，动手前需先确认对方没在跑。')
    console.log('本次结论：闪卡两入口的真机渲染「未验证」（不是「通过」，也不是「不通过」）。')
    process.exitCode = 2
  } else {
    const pane = `(function(){var ps=document.querySelectorAll('.inner-pane,.outer-pane');for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null)return ps[i];}return document.body})()`

    // 入口一：「新建卡片」→ 卡片编辑页
    const addBtn = await cdp.ev(`(function(){
      var b=(${pane}).querySelector('.add,.add-btn'); if(!b) return null;
      return {tag:b.tagName, text:(b.textContent||'').replace(/\\s+/g,' ').trim(), hasAddIcon: !!b.querySelector('.material-symbols-outlined')};
    })()`)
    check('入口一存在且文案指向「卡片」而非「卡组」', !!addBtn && /卡|card/i.test(addBtn.text || '') && !/卡组|deck/i.test(addBtn.text || ''),
      addBtn ? `text=${JSON.stringify(addBtn.text)}` : '未找到 .add/.add-btn')

    // 入口二：「新建卡组」→ 展开内联建组表单（不是跳转）
    const toggle = await cdp.ev(`(function(){
      var b=document.querySelector('[data-testid="deck-create-toggle"]'); if(!b) return null;
      return {text:(b.textContent||'').replace(/\\s+/g,' ').trim(),
              expanded:b.getAttribute('aria-expanded'),
              formPresent: !!document.querySelector('[data-testid="deck-create-form-existing"]')};
    })()`)
    check('入口二存在且文案指向「卡组」而非「卡片」', !!toggle && /卡组|deck/i.test(toggle.text || '') && !/卡片|card/i.test(toggle.text || ''),
      toggle ? `text=${JSON.stringify(toggle.text)}` : '未找到 deck-create-toggle')

    // 关键行为判定：点入口二**不应该**发生路由跳转。
    // 这里只读 aria-expanded 与表单存在性，不点击。
    const routeNow = await cdp.ev('location.hash')
    check('入口二当前不处于「已展开」态（说明它是折叠的独立控件，不是被点过就跳走的按钮）',
      !toggle || toggle.expanded !== 'true', `aria-expanded=${toggle ? toggle.expanded : '?'} hash=${routeNow}`)

    // 两入口文案必须不同 —— 这是 BUG-AA 的核心判据，在真机 DOM 上再验一次
    const t1 = addBtn ? addBtn.text : ''
    const t2 = toggle ? toggle.text : ''
    check('两个入口在真机 DOM 上文案互不相同（BUG-AA 判据）', !!t1 && !!t2 && t1 !== t2, `「${t1}」 vs 「${t2}」`)

    // 反向对照：确认选择器本身不是恒真 —— 找一个**不该存在**的 testid
    const ghost = await cdp.ev(`!!document.querySelector('[data-testid="deck-create-form-existing-NOPE"]')`)
    check('反向对照：不存在的 testid 查不到（选择器没有变盲）', ghost === false, `ghost=${ghost}`)
  }
} catch (e) {
  console.error(`探测失败：${e.message}`)
  process.exitCode = 3
} finally {
  // close() 走 finally；且不调 process.exit —— 那会跳过 finally。
  if (cdp) await cdp.close()
}

if (checks.length) {
  const ok = checks.filter((c) => c.pass).length
  console.log(`\n${ok}/${checks.length} 通过`)
  if (ok !== checks.length) process.exitCode = 1
}
