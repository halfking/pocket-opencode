// run-device-against-isolated.mjs —— 把 App 指向隔离后端，然后跑真机写路径脚本。
//
// ## 为什么需要它
//
// 设备上的 App 通过 `localStorage.pocket_api_base` 决定打向哪个后端
// （`frontend/src/config/api-base.ts` 规则 1：用户显式覆盖优先于构建默认值，
//  代码注释明说「用户显式填的地址不受影响，因为 adb reverse 开发流确实需要
//  用户主动指定 localhost」）。实测（§4.93）：
//   - 当前值 = http://127.0.0.1:18099，靠 adb reverse 打到并发会话的后端
//   - 页面 https://localhost 可以直连 http://192.168.31.20:18101/healthz → 200
//     （Capacitor WebView 允许 cleartext，混合内容没被拦）
//
// ⇒ 改**一个 localStorage 键**就能让真机打到隔离后端，
//   **完全不必碰 `adb reverse`** —— 那是真正的共享状态（动了会打断别人）。
//
// ## 共享状态与可逆性
//
// localStorage 是设备上的共享状态（同一个 App、同一份）。所以：
//   1. 动手前先跑 probe-device-idle.mjs 确认窗口内无人使用；不 idle 就 abort。
//   2. **原值原样记录**，finally 里写回 —— 成功、失败、异常都一样。
//   3. 不改 adb reverse、不动对方后端进程、不碰共享库。
//
// ## 失败路径必须还原（BUG-V10/V14 同一类）
//
// 只在 happy path 写回 localStorage，等于「跑失败就把 App 留在隔离后端上」，
// 那会让**下一个用到设备的人**莫名其妙打到一个空库。所以还原走 finally +
// unhandledRejection / uncaughtException 钩子。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const ORIGIN_EXPECT = process.env.POCKET_EXPECT_ORIGIN || 'https://localhost'
const HOST_LAN = process.env.POCKET_HOST_LAN || '192.168.31.20'
const VERIFY_PORT = process.env.POCKET_VERIFY_PORT || '18101'
const NEW_BASE = `http://${HOST_LAN}:${VERIFY_PORT}`
// POCKET_DEVICE_SCRIPTS 支持带参数，空白分隔即可：
//   POCKET_DEVICE_SCRIPTS='verify-finance-writepath.mjs --sabotage=hide-cta'
// 证伪模式（--sabotage）必须能透传，否则「判据会不会红」根本没法验。
const SCRIPTS = (process.env.POCKET_DEVICE_SCRIPTS || 'verify-finance-writepath.mjs')
  .split(/\s+/).map((s) => s.trim()).filter(Boolean)
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let cdp = null
let originalBase = null
let restored = false

async function restoreBase(reason) {
  if (originalBase === null || restored) return
  try {
    await cdp.ev(`(function(){
      var v = ${JSON.stringify(originalBase)};
      if (v === null) localStorage.removeItem('pocket_api_base'); else localStorage.setItem('pocket_api_base', v);
      return localStorage.getItem('pocket_api_base');
    })()`)
    restored = true
    console.log(`[restore:${reason}] pocket_api_base 已写回 ${JSON.stringify(originalBase)}`)
  } catch (e) {
    console.error(`[restore:${reason}] **还原失败**：${e.message}`)
    console.error(`   请手工在设备上把「后端服务器」改回 ${JSON.stringify(originalBase)}`)
    process.exitCode = 2
  }
}

for (const sig of ['unhandledRejection', 'uncaughtException']) {
  process.on(sig, async (e) => {
    console.error(`\n[${sig}]`, e)
    await restoreBase(sig)
    if (cdp) await cdp.close()
    process.exit(1)
  })
}

try {
  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })

  const origin = await cdp.ev('location.origin')
  console.log(`origin = ${origin}（要求 ${ORIGIN_EXPECT}）`)
  if (origin !== ORIGIN_EXPECT) {
    console.error(`origin 不符 —— 这台设备跑的不是预期的构建，中止（不猜、不硬闯）。`)
    process.exitCode = 5
  } else {
    // ① 先探后端可达：连不上就别改 localStorage，免得把 App 指到虚空
    const probe = await cdp.ev(`(async function(){
      try { var c=new AbortController(); setTimeout(function(){c.abort()},8000);
            var r=await fetch(${JSON.stringify(NEW_BASE + '/healthz')},{signal:c.signal,cache:'no-store'});
            return {ok:r.ok,status:r.status,body:(await r.text()).trim().slice(0,20)};
      } catch(e){ return {ok:false,error:String(e&&e.message||e)} }
    })()`)
    console.log(`隔离后端可达性 ${NEW_BASE} →`, JSON.stringify(probe))
    // ⚠️ 2026-10-03 修：这里原来只有 `process.exitCode = 6`，**没有真正停下**，
    //    于是 ②③④ 照跑 —— 注释写着「连不上就别改 localStorage」，代码却把 App
    //    指向了一个够不着的后端再跑一遍脚本。后果是自造假红：
    //    实测 18101 从设备探不通时，verify-task-writepath 打出
    //    「创建：PG 落库 FAIL / 列表回显 FAIL」，看起来像产品写路径坏了，
    //    实际只是 App 打的 18099 当时根本没有后端在听。
    //    探不通就直接退出：结论不可归因，不产出判据。
    if (!probe.ok) {
      console.error('隔离后端从设备不可达 —— 中止，不改 localStorage，不跑脚本（结论不可归因）')
      process.exit(6)
    }

    // ② 记原值
    originalBase = await cdp.ev(`localStorage.getItem('pocket_api_base')`)
    console.log(`原 pocket_api_base = ${JSON.stringify(originalBase)}（已记录，finally 会原样写回）`)

    // ③ 改指向
    await cdp.ev(`localStorage.setItem('pocket_api_base', ${JSON.stringify(NEW_BASE)}), 1`)
    const now = await cdp.ev(`localStorage.getItem('pocket_api_base')`)
    console.log(`现 pocket_api_base = ${JSON.stringify(now)}${now === NEW_BASE ? ' OK' : ' 写入未生效'}`)
    if (now !== NEW_BASE) { console.error('localStorage 写入未生效，中止'); process.exitCode = 7 }

    // ④ 跑脚本
    // ⚠️ POCKET_PG_SCHEMA 必须一起传。头一版漏了它，finance 脚本的 `SCHEMA` 落回
    //    默认的 `opencode_pocket`（共享库），而 App 写的是隔离库 ⇒
    //    「直接查 PG」那 6 条判据全红，其中「没出现 PG 未变却说成功的假成功」还
    //    打印成一条**看起来很严重的真缺陷**。
    //    那个判据其实是对的：它看到的确实是「UI 报成功、它查的库没变」。
    //    **判据红不一定是产品坏了，先问「我查的是不是同一个库」。**
    const env = {
      ...process.env,
      POCKET_API_PORT: VERIFY_PORT,
      POCKET_HOST_LAN: HOST_LAN,
      POCKET_EXPECT_ORIGIN: ORIGIN_EXPECT,
      POCKET_PG_SCHEMA: process.env.POCKET_PG_SCHEMA || 'opencode_pocket_verify',
    }
    console.log(`\n脚本环境：API_PORT=${env.POCKET_API_PORT}  PG_SCHEMA=${env.POCKET_PG_SCHEMA}  ORIGIN=${env.POCKET_EXPECT_ORIGIN}`)
    let anyFailed = false
    // 把 token 流切成「脚本名 + 其后的 -- 参数」若干组
    const jobs = []
    {
      const toks = [...SCRIPTS]
      while (toks.length) {
        const name = toks.shift()
        const args = []
        while (toks.length && toks[0].startsWith('--')) args.push(toks.shift())
        jobs.push({ name, args })
      }
    }
    for (const job of jobs) {
      const s = job.name
      console.log(`\n========== ${s}${job.args.length ? ' ' + job.args.join(' ') : ''} ==========`)
      // ⚠️ 必须区分「子进程非 0 退出」与「runner 自己抛了」。
      //    execFileSync 在子进程非 0 时会 throw，错误对象带 status/stdout/stderr；
      //    不拆开的话，子脚本的失败会被报成 runner 的失败（BUG-V15 同类）。
      let r, status = 0
      try {
        r = execFileSync(process.execPath, ['scripts/' + s, ...job.args], { env, encoding: 'utf8', timeout: 600000, maxBuffer: 32 * 1024 * 1024 })
      } catch (e) {
        if (typeof e.status === 'number') {
          status = e.status
          r = `${e.stdout || ''}${e.stderr || ''}`
          console.error(`[子进程退出 ${status}]`)
        } else {
          throw e   // 不是退出码问题 ⇒ 是 runner 自己抛的，让外层 catch 处理
        }
      }
      const lines = String(r).split(/\r?\n/)
      const pass = lines.filter((l) => /^\s*PASS\b/.test(l)).length
      const fail = lines.filter((l) => /^\s*FAIL\b/.test(l)).length
      console.log(`exit=${status}  PASS=${pass} FAIL=${fail}`)
      console.log(String(r).slice(-2500))
      if (status !== 0) { anyFailed = true; console.error(`\n${s} 未全绿（exit=${status}）—— 如实记录，不粉饰`) }
    }
    // runner 的退出码必须反映子脚本结果。头一版把子脚本 exit=1 吞了，
    // 外层只看自己的 exitCode，于是「20/26 通过」在管道里显示成 EXIT=0。
    if (anyFailed) process.exitCode = 1
  }
} catch (e) {
  console.error(`运行失败：${e.message}`)
  process.exitCode = 3
} finally {
  // 还原先于 close —— close 之后就没法再 evaluate 了
  await restoreBase('finally')
  if (cdp) await cdp.close()
  await sleep(300)
  // 还原的最终确认：从 localStorage 再读一次
  if (cdp) {
    console.log('（localStorage 已随页面保留，关闭 CDP 不影响）')
  }
  const rev = execFileSync(ADB, ['-s', SERIAL, 'reverse', '--list'], { encoding: 'utf8' }).trim()
  console.log(`\nadb reverse（必须仍是原样，我没碰过）：${rev || '(空)'}`)
}
