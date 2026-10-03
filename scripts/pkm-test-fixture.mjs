// pkm-test-fixture — 清掉 notes-crud.yaml 产生的测试残留，让每次 run 的断言都真的在验当次结果。
//
// 为什么必须清：flow 的收尾断言是「列表里能看到 MaestroPKM笔记」。如果不清理，
// 上一轮跑出来的同名笔记会一直躺在列表里，于是**即使功能彻底坏掉，断言照样通过**——
// 判据失去区分能力。清理后，「功能坏」= 列表为空 = 断言必红，这才有意义。
//
// 只删 notes-crud.yaml 会产生的两类行（标题「无标题」或以 Maestro 开头），
// 不碰其它数据；每次删完打印被删的 id，便于核对。
//
// 用法：node scripts/pkm-test-fixture.mjs [--dry]
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const DRY = process.argv.includes('--dry')

// CDP 通道走共享 helper（2026-10-02）：端口由 adb 分配（tcp:0），不再硬绑 9418。
// 硬编码端口是**同机所有会话共享**的状态，撞上就抛 10048 而报错指向装置。
// 这段逻辑在仓库里被复制了一百多处，bug 也被复制了一百多次；
// 债务量用 `node scripts/check-fixed-cdp-ports.mjs` 复测，别信注释里的旧数。
let cdp
try {
  cdp = await openCdp({ pkg: PKG })
} catch (e) {
  // 保留原有退出码契约：App 没跑 = 2（文档 handoff 里写着这个语义），其它 = 1。
  const m = String(e?.message || e)
  console.log(m)
  process.exit(m.startsWith('APP_NOT_RUNNING') ? 2 : 1)
}

const ev = async (x, ms = 20000) => {
  try {
    return { value: await cdp.ev(x, ms), err: '' }
  } catch (e) {
    return { value: undefined, err: String(e?.message || e).slice(0, 300) }
  }
}

try {
  const { value, err } = await ev(`(async () => {
  const app = document.querySelector('#app').__vue_app__
  const pinia = app.config.globalProperties.$pinia
  const db = pinia._s.get('connectivity').runtime.deps.db()
  if (!db) return 'DB_NOT_READY'
  const before = await db.all("SELECT id, workspace_id, title FROM local_assets WHERE kind='note' AND (title = '无标题' OR title LIKE 'Maestro%')")
  if (${DRY ? 'true' : 'false'}) return JSON.stringify({ dry: true, matched: before })
  for (const r of before) {
    await db.run("DELETE FROM local_assets WHERE id = ?", [r.id])
  }
  const after = await db.all("SELECT id FROM local_assets WHERE kind='note' AND (title = '无标题' OR title LIKE 'Maestro%')")
  return JSON.stringify({ deleted: before.map(r => ({ id: r.id, ws: r.workspace_id, title: r.title })), remaining: after.length })
})()`)

  if (err) { console.log('ERR: ' + err); process.exitCode = 1 }
  else console.log(String(value))
} finally {
  // 失败路径也必须走清理：原来这里是 `ws.close(); process.exit(0)`，
  // 出错分支直接 exit(1) 把 adb forward 留在机上——和 BUG-V10 的
  // 「失败路径不还原覆盖值」是同一类：残留比报错本身更难查。
  // 另注：process.exit() 不会跑 finally，所以上面只设 exitCode，退出放到块外。
  await cdp.close()
}
