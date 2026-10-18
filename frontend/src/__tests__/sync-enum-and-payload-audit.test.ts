// sync-enum-and-payload-audit.test.ts — §859：跨边界枚举未校验、同一个解析失败两种待遇、
// 以及 §858/D21 硬化了**读侧**却漏掉的**写侧**。
//
// 三处缺陷的背景：
//
//   D22 native/config-sync/apply.ts::applyAppPrefs
//     远端下发的 app_prefs 里 theme / deviceTier / sttPref 三个枚举字段
//     **未校验就落盘**（`localStorage.setItem('app_theme', prefs.theme)`）。
//     ⭐ 这是**同一个函数里的同族漏网**：applyRemoteLocale 的注释明写着
//     「未规范化的字符串被直接写进 app_locale……现在：只在能匹配到受支持语言时
//      才落盘并切换，匹配不到则完全忽略」——locale 已经因为完全相同的原因修过。
//     配套：stores/device.ts 读盘只 `as` 断言不校验，而 theme.ts / locale.ts
//     早已是白名单形态 ⇒ 「本仓的正确写法就在隔壁」。
//
//   D23 ScheduledTaskEditView::save
//     同一个 parsePayloadText()，两个分支两种待遇：
//       showPrompt 分支 `parsePayloadText() ?? {}` ⇒ JSON 非法时**静默丢弃用户参数**；
//       另一分支会报「任务参数必须是合法 JSON」。
//     而 JSON 文本框在 showPrompt 为真时被 `v-if="!showPrompt"` 隐藏（还在 details 里）
//     ⇒ 用户连自己丢了什么都看不见，任务照样保存成功。
//
//   D24 native/config-sync/apply.ts::applyChatSettings
//     `JSON.parse(localStorage.getItem(key) || '{}')` 解析失败被外层
//     `catch { /* ignore */ }` 整个吞掉 ⇒ **整份远端聊天设置被静默丢弃**。
//     ⭐ 同一个 key（pocket:ai-chat:settings:v2:<scope>）的**读侧**
//     已在 §858/D21 硬化（留证 + 置位 + 如实告知），写侧却还在静默。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

/** 先 strip 注释：判据里的锚点必须落在真代码上，不能落在自己写的说明文字上。 */
const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

const APPLY = read('native/config-sync/apply.ts')
const DEVICE = read('stores/device.ts')
const TASK_VIEW = read('features/scheduled-tasks/ScheduledTaskEditView.vue')
const TASK_TYPES = read('features/scheduled-tasks/types.ts')
const PRESERVE = read('utils/corrupt-preserve.ts')

/** 取函数体：先 strip 注释，再从形参 `(` 起做圆括号配平，再对花括号配平。
 *  ⚠ 名字后面可能带泛型参数（`function readEnum<T extends string>(`）——
 *    少写这一段就会取到空串 ⇒ 断言在**空对象**上跑并报「没有白名单」（本节第一版）。 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(`(?:async\\s+)?function\\s+${name}\\s*(?:<[^>]*>)?\\s*\\(`).exec(code)
  if (!m) return ''
  let d = 0
  let after = -1
  for (let i = m.index + m[0].length - 1; i < code.length; i++) {
    if (code[i] === '(') d++
    else if (code[i] === ')') { d--; if (d === 0) { after = i + 1; break } }
  }
  if (after < 0) return ''
  const start = code.indexOf('{', after)
  if (start < 0) return ''
  let depth = 0
  for (let i = start; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') { depth--; if (depth === 0) return code.slice(start, i + 1) }
  }
  return ''
}

/** 逐个取出函数体里的 `catch { … }` 块（按花括号配平，不用正则去猜嵌套）。
 *  ⚠ 嵌套 catch 的块体里有 `{`，用 `/catch\s*\{([^}]*)\}/` 只会取到第一层 ⇒
 *    「内层 catch 里没有 return」这条断言永远看不到真正的块体（本节第一版）。 */
function catchBlocks(body: string): string[] {
  const out: string[] = []
  const re = /catch\s*(?:\([^)]*\))?\s*\{/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body))) {
    const start = m.index + m[0].length - 1
    let depth = 0
    for (let i = start; i < body.length; i++) {
      if (body[i] === '{') depth++
      else if (body[i] === '}') { depth--; if (depth === 0) { out.push(body.slice(start + 1, i)); break } }
    }
  }
  return out
}

describe('§859 · 尺子自证', () => {
  it('★ 五个文件都读到了（路径写错会让后面全是绿灯假象）', () => {
    assert.ok(APPLY.includes('applyAppPrefs'), '没读到 applyAppPrefs')
    assert.ok(APPLY.includes('applyChatSettings'), '没读到 applyChatSettings')
    assert.ok(DEVICE.includes('useDeviceStore'), '没读到 device store')
    assert.ok(TASK_VIEW.includes('parsePayloadText'), '没读到 ScheduledTaskEditView')
    assert.ok(TASK_TYPES.includes('formatPayload'), '没读到 formatPayload')
    assert.ok(PRESERVE.includes('preserveCorruptRaw'), '没读到 corrupt-preserve')
  })
})

describe('§859 · D22 跨边界枚举必须白名单校验（与 applyRemoteLocale 同构）', () => {
  it('★★★ 三个字段都必须有白名单，且落盘的值不是原始 payload 字段', () => {
    const body = fnBody(APPLY, 'applyAppPrefs')
    assert.ok(body, '找不到 applyAppPrefs')
    for (const setName of ['APP_THEMES', 'APP_DEVICE_TIERS', 'APP_STT_PREFS']) {
      assert.ok(body.includes(setName),
        `${setName} 没被 applyAppPrefs 用到 ⇒ 有一个字段仍在无校验落盘`)
    }
    // ⭐ 判据钉的是「**写进去的值**」而不是「有没有 setItem」——
    //   校验了但又把原始字段写回去，是这一族最典型的假修。
    for (const [key, field] of [
      ['app_theme', 'prefs.theme'],
      ['pocket_device_tier', 'prefs.deviceTier'],
      ['pocket_stt_pref', 'prefs.sttPref'],
    ] as const) {
      const re = new RegExp(`setItem\\(\\s*'${key}'\\s*,\\s*([^)]+)\\)`)
      const m = re.exec(body)
      assert.ok(m, `没找到 setItem('${key}', …)`)
      assert.notEqual(m![1].trim(), field,
        `'${key}' 直接落盘了未校验的 ${field} —— 远端下发什么就存什么`)
    }
  })

  it('★★ theme 的 DOM 生效也必须用校验过的值', () => {
    const body = fnBody(APPLY, 'applyAppPrefs')
    const setAttr = /setAttribute\(\s*'data-theme'\s*,\s*([^)]+)\)/.exec(body)
    assert.ok(setAttr, '没找到 setAttribute(\'data-theme\', …)')
    assert.notEqual(setAttr![1].trim(), 'prefs.theme',
      'data-theme 上写的是未校验的 prefs.theme ⇒ 会往 <html> 上塞非法属性值')
  })

  it('★★★ stores/device.ts 读盘必须白名单，不能只靠 as 断言', () => {
    const code = strip(DEVICE)
    assert.doesNotMatch(code, /getItem\(\s*'pocket_device_tier'\s*\)\s+as\s+DeviceTier/,
      '读盘仍然只做 as 断言：`as` 只对编译器说话，运行期什么字符串都能进来')
    assert.doesNotMatch(code, /getItem\(\s*'pocket_stt_pref'\s*\)\s+as\s+/,
      '读盘仍然只做 as 断言')
    assert.ok(/function\s+readEnum\s*</.test(code), '找不到白名单读盘函数 readEnum')
    const body = fnBody(DEVICE, 'readEnum')
    assert.ok(body.includes('allowed.has'), 'readEnum 没有用白名单过滤')
  })
})

describe('§859 · D23 同一个解析失败只能有一种待遇', () => {
  it('★★★ 解析一次、判定一次，且必须早于 applyPrompt', () => {
    const body = fnBody(TASK_VIEW, 'save')
    assert.ok(body, '找不到 save')
    const checkAt = body.indexOf('=== null')
    const applyAt = body.indexOf('applyPrompt(')
    assert.ok(checkAt > 0, 'save() 里没有对解析失败的判定 ⇒ 非法 JSON 又会被静默吞掉')
    assert.ok(applyAt > 0, 'save() 里找不到 applyPrompt')
    assert.ok(checkAt < applyAt,
      '判定出现在 applyPrompt 之后 ⇒ 参数已经被丢进 {} 了，判定形同虚设')
  })

  it('★★★ 提示词分支不得再用 ?? {} 兜底', () => {
    const body = fnBody(TASK_VIEW, 'save')
    assert.doesNotMatch(body, /applyPrompt\(\s*form\.kind\s*,\s*parsePayloadText\(\)\s*\?\?/,
      'applyPrompt 的基底又变回 parsePayloadText() ?? {} ⇒ 非法 JSON 会被静默丢弃')
  })

  it('★★ 用户输入被丢弃时必须有话说（沿用既有的错误文案）', () => {
    const body = fnBody(TASK_VIEW, 'save')
    assert.match(body, /error\.value\s*=\s*'任务参数必须是合法 JSON'/,
      '判定存在但没有告知用户 —— 那是 §847 型的「失败零反馈」')
  })

  it('★★ formatPayload 必须始终产出可解析文本（否则修复会卡住编辑旧任务）', () => {
    const body = fnBody(TASK_TYPES, 'formatPayload')
    assert.ok(body, '找不到 formatPayload')
    assert.doesNotMatch(body, /typeof\s+payload\s*===\s*'string'\s*\)\s*return\s+payload/,
      '字符串 payload 被原样返回 ⇒ 文本框里是无法解析的裸串，save() 会被自己的校验卡住')
    assert.match(body, /JSON\.stringify\(/, 'formatPayload 不再走 JSON.stringify')
  })
})

describe('§859 · D24 写侧不能静默丢掉远端更新（与 §858 读侧对称）', () => {
  it('★★★ 解析失败必须留证', () => {
    const body = fnBody(APPLY, 'applyChatSettings')
    assert.ok(body, '找不到 applyChatSettings')
    assert.match(body, /preserveCorruptRaw\(\s*'settings'/,
      'applyChatSettings 解析失败没有留证 ⇒ 又一次把唯一的排查证据扔掉')
  })

  it('★★ 解析失败不得 early return（远端才是这次的数据源）', () => {
    const body = fnBody(APPLY, 'applyChatSettings')
    // 判据钉的是「留证的那一个 catch 里不许 return」——
    // 若有人图省事把留证和早退写在一起，远端更新就又丢了。
    const preserveCatch = catchBlocks(body).find((c) => c.includes('preserveCorruptRaw'))
    assert.ok(preserveCatch, '找不到含留证的 catch 块')
    assert.doesNotMatch(preserveCatch, /\breturn\b/,
      '留证之后紧接着 return ⇒ 远端更新被整份丢弃（本节要修的正是这一条）')
    // 并且落盘必须发生在这个 catch 之后（否则等于「不应用」）
    const setAt = body.indexOf('setItem(key')
    assert.ok(setAt > 0 && setAt > body.indexOf('preserveCorruptRaw'),
      '落盘发生在留证之前 ⇒ 解析失败时仍会用 {} 覆盖掉原始串')
  })

  it('★★ 不允许再用「一行 parse 包在外层 try 里」的旧写法', () => {
    const body = fnBody(APPLY, 'applyChatSettings')
    assert.doesNotMatch(body, /JSON\.parse\(\s*localStorage\.getItem\(/,
      '又是内联 parse：它抛出的异常只能落到外层的 catch { /* ignore */ }')
  })

  it('★ 留证落点只有一个（键名约定不允许有两份实现）', () => {
    // aiChatStore 已在 §859 改为复用 utils/corrupt-preserve.ts。
    const store = read('features/ai-chat/aiChatStore.ts')
    assert.doesNotMatch(strip(store), /setItem\(\s*`\$\{[^`]*corrupt/,
      'aiChatStore 又自己拼了一次 corrupt 键名 ⇒ 与 utils/corrupt-preserve 会各自漂移')
    assert.match(strip(store), /preserveCorruptRaw\(/, 'aiChatStore 没有复用共用留证落点')
    assert.match(strip(APPLY), /from '\.\.\/\.\.\/utils\/corrupt-preserve'/, 'apply.ts 没有复用共用留证落点')
  })
})

describe('§859 · 变异负控（这些形态出现时判据必须转红）', () => {
  it('把 deviceTier 改回无校验落盘 ⇒ 会被 D22 那条抓住', () => {
    // ⚠⚠必须 strip 注释再匹配：本文件的说明文字里**故意引用了**缺陷原句
    //   （「此前是 localStorage.setItem('app_theme', prefs.theme)」），
    //   不 strip 的话这条负控自己就会红 —— 探针自伤（第 11 次）：
    //   **判据文本里引用代码字面量，就等于把字面量写进了被扫描的语料**。
    const bad = "localStorage.setItem('pocket_device_tier', prefs.deviceTier)"
    assert.doesNotMatch(strip(PRESERVE + APPLY), new RegExp(bad.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')),
      '负控样本写成了守卫本身的样子')
  })

  it('把 applyPrompt 的基底改回 ?? {} ⇒ 会被 D23 那条抓住', () => {
    const body = /applyPrompt\(\s*form\.kind\s*,\s*parsePayloadText\(\)\s*\?\?\s*\{\}/.test(
      'applyPrompt(form.kind, parsePayloadText() ?? {}, form.prompt)')
    assert.equal(body, true, '负控样本没匹配上 D23 那条守卫的表达式')
  })
})
