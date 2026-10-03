// 诊断：preflight 报「登录后仍停在登录页，last hash=(超时)」时，App 到底是什么状态。
//
// 为什么要单独查：上一条 flow（flashcards-write）用同一套 preflight 是绿的，
// 这一条挂了 ⇒ 不是「preflight 坏了」，是**这一次**有什么不同。
// 而且 last hash 连读都读不到，说明不只是路由没跳，可能是 App/CDP 通道本身有问题。
//
// 要区分的三种情况（处置完全不同，不能靠重试碰）：
//   A. App 进程活着但页面无响应 / CDP 读不动 → 通道问题
//   B. App 活着、CDP 正常、hash 停在 #/login → 登录真没成功
//   C. 停在一层遮罩上（主密码弹窗 / 系统权限弹窗），路由被盖住 → 又是误报
//      （8a504202 修过一次同样的误报：token 291 字符、/api/auth/login 实测 200，
//        只是弹窗挡住了路由。）
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 20000) => {
  try { return execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 }).trim() }
  catch (e) { return `<ERR ${String(e?.message || e).split('\n')[0].slice(0, 80)}>` }
}

const pid = adb(['shell', `pidof ${PKG}`])
console.log(`① 进程：pid=${JSON.stringify(pid)}`)
console.log(`② 前台窗口：${adb(['shell', 'dumpsys window | grep mCurrentFocus'])}`)

let cdp = null
try {
  cdp = await openCdp({ pkg: PKG })
  console.log(`③ CDP 已连上（socket=${cdp.socket}，目标 pid=${cdp.pid}）`)
} catch (e) {
  console.log(`❌ CDP 连不上：${String(e?.message || e).split('\n').slice(0, 3).join(' | ')}`)
  console.log('   ⇒ 情况 A：通道问题，不是登录问题。重试有可能，但先确认不是被别的会话占了设备。')
  process.exit(2)
}

try {
  // 逐项独立读，任何一项失败都单独标出来 —— 不能因为一项超时就丢掉其余证据
  const probe = async (label, expr, ms = 8000) => {
    const t0 = Date.now()
    try {
      const v = await cdp.ev(expr, ms)
      console.log(`   ${label.padEnd(22)} = ${JSON.stringify(v)}  (+${Date.now() - t0}ms)`)
      return v
    } catch (e) {
      console.log(`   ${label.padEnd(22)} ✗ ${String(e?.message || e).split('\n')[0].slice(0, 90)}  (+${Date.now() - t0}ms)`)
      return null
    }
  }
  console.log('④ 页面探针：')
  const hash = await probe('location.hash', 'location.hash')
  const title = await probe('document.title', 'document.title')
  await probe('readyState', 'document.readyState')
  await probe('token 长度', `(localStorage.getItem('pocket_token')||'').length`)
  const mainKids = await probe('<main> 子节点数', `(document.querySelector('main')||{children:[]}).children.length`)
  await probe('textarea/input 数', `document.querySelectorAll('textarea,input').length`)
  const dlg = await probe('可见 dialog 文本', `Array.from(document.querySelectorAll('[role="dialog"],dialog,.modal')).map(e=>(e.textContent||'').trim().slice(0,60)).join(' || ')||'(无)'`)
  await probe('body 文本前 120 字', `(document.body.innerText||'').replace(/\\s+/g,' ').trim().slice(0,120)`)

  console.log('')
  const stuck = hash != null && /#\/login/.test(String(hash))
  if (stuck && (mainKids ?? 0) > 0 && (dlg ?? '(无)') !== '(无)') {
    console.log('⇒ 情况 C：路由没跳，但页面上有东西盖着 —— 又是一次「弹窗遮住路由」的误报形态。')
  } else if (stuck) {
    console.log('⇒ 情况 B：CDP 正常、hash 真的停在 #/login ⇒ 登录本身没成功，要查后端返回。')
  } else if (hash == null) {
    console.log('⇒ 又一种情况：连 hash 都读不出，但通道是通的 —— 页面 JS 线程可能被卡死。')
  } else {
    console.log(`⇒ 当前 hash=${JSON.stringify(hash)}，并非停在登录页。`)
  }
} finally {
  await cdp.close()
}
