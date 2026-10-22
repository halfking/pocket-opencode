/**
 * 设备级「Android 无障碍树抖动」判定 —— 只用它决定**要不要重跑整条 flow**。
 *
 * ## 现象（2026-10-22 round51 实测）
 * 本机 WebView 113 的 Android 无障碍树在**增量更新**时会整片丢掉某个容器的子树：
 * 画面上元素明明在（同一瞬间的 Maestro 截图实证），树里却查不到。
 * 实测 8 次观测红 1 次；更关键的是 —— **完全相同的命令序列**有时绿有时红
 * （诊断探针跑：三条探针跑的是同一串命令，两绿一红），所以这是**设备/驱动层的竞态**，
 * 不是产品缺陷，也不是 flow 写错了。
 *
 * ## 为什么不改断言、不改产品
 * 画面是对的 ⇒ 产品是对的；只有「测试装置读不到」是不对的。
 * 把断言放宽（optional / 改成查 DOM / 改成看截图）就是「让测试迁就错误环境」，
 * 那是本项目明令禁止的。这里的做法是**重跑同一条 flow、同样的断言**：
 * 断言一条不少地再跑一遍，设备这次把树填上了就是绿；填不上就是红。
 *
 * ## 护栏（宁可漏判，不可误判）
 * ① 失败文本必须**精确命中** A11Y_GLITCH_SYMPTOMS 里的一条；
 * ② 且该断言是**超时**（跑满了 timeout）而不是「立刻判不成立」——
 *    「元素根本没进树」才会耗满 timeout；「元素在树里但不满足条件」是瞬时的。
 * 两条都满足才重试；否则**立即照实报红**，绝不重试。
 *
 * 本模块刻意**不做**任何通用化推断：症状表是白名单，不是启发式。
 * 新增症状必须先有现场证据（截图 + 原始 hierarchy JSON）再往里加。
 */

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { homedir } from 'node:os'

export const MAESTRO_TESTS_DIR = join(homedir(), '.maestro', 'tests')

/**
 * 已知「设备级无障碍树抖动」症状白名单。
 *
 * ⚠️ 这里**刻意用白名单而不是泛化规则**，理由不是偷懒，是**泛化会制造假绿**：
 *   「先创建再断言存在」的 flow（如 notes-crud 建完 PKM 笔记再断言列表可见）
 *   若首轮创建成功、后面才失败，重试时那份**残留**会让第二轮的「列表可见」白过
 *   ⇒ 断言看起来绿了，产品其实是坏的。
 *   泛化成「任何超时的 assertVisible 都重试」就会踩这个坑。
 *   notes-crud 自己的收尾 `assertNotVisible` 能挡住这种假绿，但那是**那条 flow 的
 *   取舍**，不该由 harness 替所有 flow 兜底。
 *
 * ⇒ 所以：**每加一条都必须先有现场证据**（同一瞬间的截图 + 原始 hierarchy JSON，
 *   证明元素在画面上、却不在树里），再把它的失败文本精确写进来。
 *
 * 已知症状（2026-10-22 实测）：
 *   1. `notes-crud` → `_goto-pkm.yaml` 的 `assertVisible: "打开 PKM|Open PKM"`
 *      —— 「Open PKM」卡片在截图里明明在（带「2」和 ›），树里整片没有。
 *   2. `email-accounts` → `assertVisible: "邮箱设置" is visible`
 *      —— 更多菜单**开着**（截图里 Daily digest / 发票整理 / 清理垃圾 / 邮件目录 /
 *      智能整理 / 邮箱设置 / 批量操作 全在），树里一个菜单项都没有。
 *   两者同源：WebView 113 把**新插入的 DOM 子树**漏给 Android 无障碍服务。
 */
export const A11Y_GLITCH_SYMPTOMS = [
  'Assert that "打开 PKM|Open PKM" is visible FAILED',
  'Assert that "邮箱设置" is visible FAILED',
]

// 「超时」的判定用**该断言自己的 timeout** 做相对阈值，不用绝对毫秒数：
// 断言既没进树、也没满足条件时，Maestro 会一直等到 timeout 耗尽；
// 而「元素在树里但不满足条件」是**瞬时**失败的（毫秒级）。
// 留 20% 余量给机器抖动。
const A11Y_GLITCH_TIMEOUT_RATIO = 0.8

function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** HH:MM:SS.mmm → 毫秒差（同一天内；跨零点会得到负数，但那种情况不可能是本症状）。 */
export function clockDiffMs(a, b) {
  const toMs = (t) => {
    const [h, m, rest] = t.split(':')
    const [s, ms] = rest.split('.')
    return ((+h * 60 + +m) * 60 + +s) * 1000 + +(ms || 0)
  }
  return toMs(b) - toMs(a)
}

function listRunDirs() {
  try {
    return readdirSync(MAESTRO_TESTS_DIR, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name)
      .sort()
  } catch {
    return []
  }
}

/** 某次 Maestro 跑批的全部 maestro.log 候选路径。 */
function logCandidates(runDir) {
  const out = [join(MAESTRO_TESTS_DIR, runDir, 'maestro.log')]
  try {
    for (const e of readdirSync(join(MAESTRO_TESTS_DIR, runDir), { withFileTypes: true })) {
      if (e.isDirectory()) out.push(join(MAESTRO_TESTS_DIR, runDir, e.name, 'logs', 'maestro.log'))
    }
  } catch {
    /* 目录读不到就只用顶层那份 */
  }
  return out
}

/**
 * 在**指定**的跑批目录里找「无障碍树抖动」症状。
 * @param {string} runDir 形如 2026-10-22_073952
 * @returns {{symptom:string, elapsed:number, timeout:number}|null}
 */
export function findA11yGlitchInRun(runDir) {
  for (const p of logCandidates(runDir)) {
    let text
    try {
      text = readFileSync(p, 'utf8')
    } catch {
      continue
    }
    for (const symptom of A11Y_GLITCH_SYMPTOMS) {
      const label = symptom.replace(/ (?:FAILED|COMPLETED)$/, '')
      const re = new RegExp(
        String.raw`(\d{2}:\d{2}:\d{2}\.\d{3})[^\n]*${escapeRegExp(label)} RUNNING` +
          String.raw`([\s\S]*?)` +
          String.raw`(\d{2}:\d{2}:\d{2}\.\d{3})[^\n]*${escapeRegExp(label)} FAILED`,
      )
      const m = text.match(re)
      if (!m) continue
      const elapsed = clockDiffMs(m[1], m[3])
      if (elapsed == null || elapsed < 0) continue
      // 该断言自己的 timeout（Maestro 的 metadata 行里有 timeout=N）
      const tm = m[2].match(/timeout=(\d+)/)
      const timeout = tm ? +tm[1] : null
      // 没有 timeout 字段（默认断言）时无法判断是不是超时 ⇒ 不当作抖动。
      if (!timeout) continue
      if (elapsed < timeout * A11Y_GLITCH_TIMEOUT_RATIO) continue
      return { symptom, elapsed, timeout }
    }
  }
  return null
}

/**
 * 在**本次跑批新建的**产物目录里找症状。
 *
 * 只看本次新建的目录（而不是「全局最新」）：一条 flow 可能在 preflight 阶段
 * 就失败、根本没建新目录，这时去读上一条 flow 的现场会误判。
 *
 * @param {string[]} runDirs 本次新建的目录名
 * @returns {{symptom:string, elapsed:number, timeout:number, dir:string}|null}
 */
export function readA11yGlitchFromDirs(runDirs) {
  for (const dir of runDirs) {
    const hit = findA11yGlitchInRun(dir)
    if (hit) return { ...hit, dir }
  }
  return null
}

/**
 * 在**最新一次**跑批目录里找症状（没有本次目录信息时的退路）。
 * @returns {{symptom:string, elapsed:number, timeout:number, dir:string}|null}
 */
export function readLatestMaestroFailure() {
  const dirs = listRunDirs()
  if (!dirs.length) return null
  const latest = dirs[dirs.length - 1]
  const hit = findA11yGlitchInRun(latest)
  if (!hit) return null
  return { ...hit, dir: latest }
}
