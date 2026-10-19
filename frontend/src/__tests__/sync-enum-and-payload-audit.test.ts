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
    // ⚠⚠§869：原写法只认 `setItem('key', X)` 这一个字面形状，而本节把写入
    //   收拢到逐字段的 `persist('key', X, 标签)` 里（为了让一个字段写失败
    //   **不连坐**其余字段）⇒ 旧形状匹配不到、门禁转红。
    //   这里**只放宽形状、绝不放宽语义**：仍然要求「该 key 落盘的值不是原始
    //   payload 字段」。按惯例，撞门禁只改自己措辞、不动门禁语义。
    for (const [key, field] of [
      ['app_theme', 'prefs.theme'],
      ['pocket_device_tier', 'prefs.deviceTier'],
      ['pocket_stt_pref', 'prefs.sttPref'],
    ] as const) {
      const re = new RegExp(`(?:setItem|persist)\\(\\s*'${key}'\\s*,\\s*([^,)]+)`)
      const m = re.exec(body)
      assert.ok(m, `没找到 '${key}' 的落盘点`)
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
    // ⚠§862/D26 起留证函数多了「命名空间」实参（AI 对话侧与 crypto 侧各一个 ns），
    //   所以这里不能钉死第一个实参 —— 判据要钉的是「**留证的是 settings**」这件事本身。
    assert.match(body, /preserveCorruptRaw\(\s*'[^']+'\s*,\s*'settings'/,
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

describe('§859 · D25 同一个 key 的**第三个**消费者（迁移路径）', () => {
  // ⭐ 这条断言的由来是本仓**同族第三次漏网**：
  //   §838/D19（onArchive/onRestore/onDelete 三个处理器都修过、startNewMeeting 漏网）
  //   §859/D24（同一个 key 的读侧 §858 修过、写侧漏网）
  //   §859/D25（同一个 key 的读侧 + 写侧都修过、**一次性迁移路径**漏网）
  // ⇒ 只钉「已知的两个兄弟」必然漏掉第三个 ⇒ 判据必须**枚举全部消费者**再逐个要求。

  it('★★★ 三个消费者一个都不能少（少一个就说明有人加了新消费者而没人点名）', () => {
    const store = strip(read('features/ai-chat/aiChatStore.ts'))
    const runtime = strip(read('native/config-sync/runtime.ts'))
    // ① 读侧
    assert.match(store, /loadSettings/,
      'aiChatStore::loadSettings 不见了 —— 若它改名，请同步更新这条枚举而不是删掉它')
    // ② 写侧
    assert.ok(APPLY.includes('applyChatSettings'), 'applyChatSettings 不见了')
    // ③ 迁移侧（第三个消费者）
    assert.match(runtime, /importLegacyPrefsIfMissing/,
      '一次性旧偏好迁移函数不见了；若它被删/改名，这条枚举需要重新点名')
  })

  it('★★★ 解析失败的那个 catch 必须说话（外层那个不管）', () => {
    const runtime = strip(read('native/config-sync/runtime.ts'))
    const body = fnBody(runtime, 'importLegacyPrefsIfMissing')
    assert.ok(body, '找不到 importLegacyPrefsIfMissing')
    // ⚠ 只钉「紧跟 JSON.parse 的那一个 catch」：
    //   这个函数**外层**的 `catch { /* ignore */ }` 保护的是 getLocalSetting /
    //   writeLocalSetting 这类 DB 调用，失败时「跳过这次迁移」本来就是正确行为，
    //   要求它说话属于越界。第一版断言写成了「函数里不许有静默 catch」⇒ 立刻误报。
    const parseAt = body.indexOf('JSON.parse')
    assert.ok(parseAt > 0, '这个函数里已经找不到 JSON.parse ⇒ 若迁移改写了，请重新点名')
    const rest = body.slice(parseAt)
    const firstCatch = catchBlocks(rest)[0]
    assert.ok(firstCatch, 'JSON.parse 之后没有任何 catch ⇒ 解析失败会直接冒出去')
    assert.match(firstCatch, /preserveCorruptRaw|console\.|throw\s/,
      '解析失败后的那个 catch 什么都不说 ⇒ 旧 blob 损坏时这次迁移会无声跳过，'
      + '用户的 AI 聊天设置从此只留在这一台设备上')
  })

  it('★★ 迁移路径必须复用共用留证落点（不得自己拼键名）', () => {
    const runtime = strip(read('native/config-sync/runtime.ts'))
    assert.doesNotMatch(runtime, /corrupt:[^`]*\$\{/,
      'runtime.ts 自己拼了 corrupt 键名')
    assert.match(runtime, /from '\.\.\/\.\.\/utils\/corrupt-preserve'/,
      'runtime.ts 没有复用共用留证落点')
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

// ============================================================================
// §862/D26：加密配置读失败被谎报成「从来没有主密码」
//
// 原文：stores/crypto-config.ts::loadConfig 的 catch 是 `return { ...DEFAULT_CONFIG }`
//   ⇒ 「读不出来」与「首次安装」是同一个值，而 hasMasterPassword 决定：
//     · 登录后是否弹主密码对话框（LoginView / RegisterView）
//     · 弹窗标题（mode="create" ⇒ **「创建主密码」**）
//     · 新笔记是否做字段加密（shouldEncryptField()）
// ⇒ 明明早就设过主密码的用户，会被困在「创建 → 主密码不正确 → 再创建」的循环里。
// ============================================================================

const CRYPTO = read('stores/crypto-config.ts')
const DIALOG = read('features/auth/MasterPasswordDialog.vue')
const DIALOG_TPL = strip(/<template>([\s\S]*)<\/template>/.exec(DIALOG)?.[1] ?? '')
const LOGIN = read('features/auth/LoginView.vue')
const REGISTER = read('features/auth/RegisterView.vue')

describe('§862 · D26 读失败不得谎报成「没有主密码」', () => {
  it('★★★ catch 里必须留证（否则 setMasterPassword 会覆盖掉唯一的证据）', () => {
    const body = fnBody(CRYPTO, 'loadConfig')
    assert.ok(body, '找不到 loadConfig')
    assert.match(body, /preserveCorruptRaw\(\s*'pocket:crypto:corrupt'/,
      '解析失败没有留证 ⇒ 用户输入主密码后 save() 会把这串损坏内容覆盖掉')
  })

  it('★★★ catch 里必须置位 configRecovered（否则界面无从告知）', () => {
    const body = fnBody(CRYPTO, 'loadConfig')
    assert.match(body, /configRecovered\.value = true/,
      '没有置位 ⇒ 弹窗不知道该说「解锁」而不是「创建」')
  })

  it('★★★ 失败分支不得把 hasMasterPassword 谎报成 false', () => {
    const body = fnBody(CRYPTO, 'loadConfig')
    const tail = body.slice(body.lastIndexOf('catch'))
    assert.doesNotMatch(tail, /hasMasterPassword:\s*false/,
      '失败分支仍然说「没有主密码」⇒ 用户会被要求重新创建，且 verifyMasterPassword 会拒')
    assert.match(tail, /hasMasterPassword:\s*true/,
      '失败分支应按「锁定」处理：raw 存在说明这台机器曾经有过一份加密配置')
  })

  it('★★ 字段级校验不得省略（`as CryptoConfig` 只对编译器说话）', () => {
    const body = fnBody(CRYPTO, 'loadConfig')
    assert.doesNotMatch(body, /\.\.\.JSON\.parse\(/,
      '仍是 `{ ...DEFAULT, ...JSON.parse(raw) }` ⇒ 一个合法 JSON 里的任意字段类型都能进来')
    assert.match(body, /fieldEncryption\s*===\s*'enabled'\s*\?\s*'enabled'\s*:\s*'disabled'/,
      'fieldEncryption 没有按枚举校验')
    assert.match(body, /hasMasterPassword\s*===\s*true/,
      'hasMasterPassword 没有按布尔校验')
  })

  it('★★ configRecovered 必须导出', () => {
    const ret = /useCryptoConfig[\s\S]*?\n  return \{[\s\S]*?\n  \}/.exec(strip(CRYPTO))?.[0] ?? ''
    assert.match(ret, /configRecovered/, 'store 没导出 configRecovered ⇒ 视图拿不到')
  })
})

describe('§862 · D26 界面必须说**实话**（损坏时不能说「创建主密码」）', () => {
  it('★★★ 弹窗必须有 unlock 形态，且它的标题不得是「创建主密码」', () => {
    assert.match(DIALOG, /'create'\s*\|\s*'change'\s*\|\s*'unlock'/,
      '弹窗没有 unlock 模式 ⇒ 配置损坏时用户会被要求「创建」一个他早就有的主密码')
    assert.match(DIALOG_TPL, /\{\{\s*title\s*\}\}/, '标题没有随 mode 变化')
    assert.match(strip(DIALOG), /'输入主密码解锁'/,
      'unlock 模式的标题缺失')
  })

  it('★★★ unlock 形态必须说清「是读取失败，不是你忘了设」', () => {
    assert.match(DIALOG_TPL, /v-if="mode === 'unlock'"/,
      'unlock 形态没有独立的告知行')
    assert.match(strip(DIALOG), /检测到本机加密配置损坏/,
      '没有告诉用户真实原因 ⇒ 他只会以为自己从来没设过')
  })

  it('★★ 两个入口都必须按 configRecovered 选模式', () => {
    for (const [name, code] of [['LoginView', LOGIN], ['RegisterView', REGISTER]] as const) {
      assert.match(code, /:mode="cryptoConfig\.configRecovered \? 'unlock' : 'create'"/,
        `${name} 没有按 configRecovered 选模式`)
    }
  })

  it('★ computed 不能叫 hint（弹窗里已有一个 hint ref）', () => {
    // 第一版把提示文案的 computed 命名为 `hint`，与密码提示输入框的 ref 同名
    // ⇒ typecheck 报重复标识符。判据钉住「两处不能同名」。
    const hintDecls = (strip(DIALOG).match(/\bconst hint\b/g) ?? []).length
    assert.equal(hintDecls, 1, `弹窗里出现了 ${hintDecls} 个 const hint（应为 1：只有输入框那个 ref）`)
  })
})

describe('§862 · 变异负控', () => {
  it('把 catch 改回「没有主密码」⇒ 会被 D26 那组抓住', () => {
    const mutated = CRYPTO.replace(
      /return \{ \.\.\.DEFAULT_CONFIG, hasMasterPassword: true \}/,
      'return { ...DEFAULT_CONFIG }')
    assert.notEqual(mutated, CRYPTO, '变异没生效（锚点与真源码不匹配 ⇒ 判据与变异没共用一把尺子）')
    // ⚠ 只能在 **loadConfig 的函数体**里查，不能查整份文件：
    //   `setMasterPassword()` 里本来就有 `cfg.value.hasMasterPassword = true`
    //   —— 第一版查了整份文件，于是这条负控恒红（是量具坏了，不是被测代码坏了）。
    const body = fnBody(mutated, 'loadConfig')
    assert.doesNotMatch(body.slice(body.lastIndexOf('catch')), /hasMasterPassword:\s*true/,
      '守卫样本与真源码不匹配')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §878：读 SQLite「一列 JSON」这件事此前有**三份实现、三种降级口径**，全零留证。
//
//   ① native/outboxStore.ts          裸 JSON.parse → 直接抛（一条坏行永久堵死整个队列）
//   ② config-sync/settings-store.ts  catch { payload = {} } → 谎报成空（定时任务列表出现空条目）
//   ③ config-sync/outbox.ts          同上，但这份是**推送队列** ⇒ 空 payload 会被真的发出去
//   ④ native/approvalStore.ts        同上，但**唯一非破坏**的一处（展示只看 kind/state）
//
// ⚠ 这 4 处是**按能力枚举**出来的（扫 `JSON.parse` 落在 DB 行字段上），
//   不是按调用点一个个撞出来的 —— 只查 outbox 会漏掉另外三处。
// ⚠ 行为那一层（真 SQLite + 真 drain）在
//   `native/__tests__/outboxDrain.test.mjs`；这里是**形状层**，只保证口径没退回三份。
describe('§878 · SQLite JSON 列的解码口径必须只有一个落点', () => {
  /** 4 个「读 DB 行上的 JSON 列」的文件。数量被钉住 —— 新增落点必须是一个有意识的决定。 */
  const DECODE_SITES = [
    'native/outboxStore.ts',
    'native/approvalStore.ts',
    'native/config-sync/settings-store.ts',
    'native/config-sync/outbox.ts',
  ]

  it('★ 4 个落点全部走同一个解码器', () => {
    for (const rel of DECODE_SITES) {
      const code = strip(read(rel))
      // ⚠ 正则必须认 `readJsonColumn<...>(`：调用点都带泛型实参，
      //   写成 `readJsonColumn\(` 会在**真源码**上 0 命中（第一版就栽在这，
      //   又一次「量具坏了」而不是「代码坏了」）。
      assert.match(code, /readJsonColumn[<(]/,
        `${rel} 没有走共用解码器 ⇒ 解码口径又各自漂移了（§878.1 的起点）`)
      assert.doesNotMatch(code, /JSON\.parse\(\s*String\(\s*\w+\s*\.\s*payload/,
        `${rel} 又自己解析 payload 列了 ⇒ 多了一份实现就会各自漂移`)
    }
  })

  it('★ 解码器全仓只有一个定义点', () => {
    const defs = strip(read('native/sqlDb.ts')).match(/export function readJsonColumn/g) ?? []
    assert.equal(defs.length, 1, `readJsonColumn 定义了 ${defs.length} 次 —— 必须只有一个落点`)
  })

  it('★ 「谎报成空」的降级不得回来（那两处是 §878.2 的缺陷形态）', () => {
    for (const rel of ['native/config-sync/settings-store.ts', 'native/config-sync/outbox.ts']) {
      const code = strip(read(rel))
      assert.doesNotMatch(code, /payload\s*=\s*\{\}/,
        `${rel} 又把损坏的 payload 降级成 {} 了 ⇒ 列表里会出现既没标题也没 id 的空条目`)
      assert.match(code, /continue|filter\(/,
        `${rel} 必须显式跳过损坏的行，而不是把它换成一个空对象`)
    }
  })

  it('★ 唯一允许降级成空的那一处必须写明「为什么它非破坏」', () => {
    const raw = read('native/approvalStore.ts')
    const body = fnBody(raw, 'toApprovalRow')
    assert.ok(body, '取不到 toApprovalRow 的函数体')
    assert.match(body, /readJsonColumn[<(]/, '它也必须走共用解码器（只是决定不同）')
    // ⚠ 这条断言的是**理由必须在场**，不是「注释里提到就算数」——
    //   它只要求注释存在且与 kind/state 有关，是「人写给人看的」那条约定。
    assert.match(raw, /kind[\s\S]{0,200}state|state[\s\S]{0,200}kind/,
      'approvalStore 的降级理由（展示只依赖 kind/state）不见了 —— 那时降级就不再是非破坏的')
  })

  it('★ 点查与批量读的口径差异必须显式（一个抛、一个跳过）', () => {
    const code = strip(read('native/outboxStore.ts'))
    // 批量读：跳过 + 过滤
    assert.match(code, /if\s*\(rec\s*!==\s*null\)\s*out\.push\(rec\)/,
      'listReady 必须逐行跳过损坏的记录，不能整批 reject')
    // 点查：抛，且带可被断言的标记
    assert.match(code, /throw new Error\(\s*`\[outbox_corrupt\]/,
      'get() 必须抛可识别的错误 —— 返回 null 就是把损坏报成「不存在」（§876/D45）')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §880：一次操作里的**多次写**普查（阴性结论 + 常驻门）。
//
// 普查口径：按**函数体**取（不是按文本窗口），凡函数体内有 ≥2 处
// `localDB.run(` / `db.run(` 且没有 `runInTransaction` 的，列进来。
//
// ⚠ 普查结果（2026-10-18）：**9 处**。逐条看过执行路径后，只有 1 处
//   「一次调用必写两次」（`chat-store.saveMessage`），而它所在的模块
//   **整模块零引用** —— 已被仓里既有的 `audit-dead-features` 报出并在基线里。
//   ⇒ **本轴在生产路径上是干净的**：SQLite 的写原子性没有漏网的真实缺陷。
//   ⇒ 但「干净」是**今天**的干净，所以把普查钉成门：新增第 10 处会红，
//     逼出一次有意识的决定，而不是等下一次审计重新普查一遍。
import { readdirSync, statSync } from 'node:fs'

const SCAN_ROOTS = ['native', 'stores', 'features', 'api', 'composables', 'utils'].map((r) => join(SRC, r))
const RUN_RE = /\b(?:localDB|this\.db|db)\.run\(/g

/**
 * 取函数体：先剥注释，再定位声明 → **括号配平**扫参数表 → 花括号配平扫函数体。
 *
 * ⚠⚠⚠ 这段解析器返工了三代，全部记在这里（它们是同一个错误的三种形状）：
 *   v1 用正则 `[^)]*` 匹配参数表 —— 会**跨行**，某处缩进的 `foo(a)` 一路吃到下面的
 *       `export async function handleServerEvent(`，真函数被整段吞掉（少认，且不报错）。
 *   v2 改成 `[^\n)]*` 只吃单行 —— 修好了上面那个，却**漏掉多行签名**：
 *       `runNotesIndexBackfill(db: NotesIndexDb,\n decrypt: ...)` 直接消失。
 *   v3（现在）不用正则碰括号，改为**逐字符配平**：定位声明头 → 跳泛型 → 扫到配平的
 *       `(` … `)` → 扫返回类型（到 `{` 或 `;` 为止）→ 花括号配平取体。
 *       顺带排除 `if/for/catch/...` 控制流关键字（形状与方法相同）。
 *
 * ⭐ 教训：**正则一旦需要"跨结构"，就会在某一个真实文件上悄悄漏掉，且不报错。**
 *   门禁最容易在这种地方变成「永远绿的装饰」—— 抓它的是「反向断言」
 *   （允许清单不得有过期条目）与「合成输入自证」，两者都在下面。
 */
const CONTROL_KEYWORDS = new Set([
  'if', 'for', 'while', 'switch', 'catch', 'return', 'function', 'constructor',
  'do', 'else', 'try', 'finally', 'new', 'typeof', 'await',
])

/** 从 `from` 开始找配平的 `(`…`)`；返回 `)` 之后的下标，找不到返回 -1。 */
function skipBalancedParens(src: string, from: number): number {
  while (from < src.length && /\s/.test(src[from])) from++
  if (src[from] !== '(') return -1
  let depth = 0
  for (let i = from; i < src.length; i++) {
    if (src[i] === '(') depth++
    else if (src[i] === ')') {
      depth--
      if (depth === 0) return i + 1
    }
  }
  return -1
}

/** 跳到函数体的 `{`（跳过泛型实参与返回类型），遇到 `;` 说明这不是函数声明。 */
function findBodyBrace(src: string, from: number): number {
  // ⚠⚠ 必须设**搜索窗口**。否则「接口里的方法签名」会被当成函数声明：
  //   `interface VectorDb { run(sql, params): Promise<SqlRow[]>; … }` 里的 `run(` 后面
  //   **没有** `{`，扫描会一路往后找，直到撞上几百行之后的类体 ⇒ 整个类被算成
  //   「函数 run 的函数体」⇒ 凭空报出一个不存在的条目。
  //   一次函数声明的 `{` 距离 `)` 不会超过返回类型的几个词，400 字符足够宽。
  const LIMIT = 400
  let i = from
  while (i < src.length && /\s/.test(src[i])) i++
  if (src[i] === '<') {
    let depth = 0
    for (; i < src.length && i - from < LIMIT; i++) {
      if (src[i] === '<') depth++
      else if (src[i] === '>') { depth--; if (depth === 0) { i++; break } }
    }
  }
  const afterParen = skipBalancedParens(src, i)
  if (afterParen < 0) return -1
  i = afterParen
  while (i < src.length && /\s/.test(src[i])) i++
  // ⚠⚠ 返回类型**不许跨行**。这不是风格要求，而是「这不是函数声明」的可靠信号：
  //   TypeScript 的 interface 成员**可以不带分号**（`run(): Promise<number>` 合法），
  //   所以拿 `;` 当终止符根本挡不住接口方法签名 —— 实测它照样一路找到几百行后的类体。
  //   真实的函数声明，返回类型与 `{` 永远在同一行。
  if (src[i] === ':') {
    const lineEnd = src.indexOf('\n', i)
    for (; i < src.length && i < lineEnd && i - from < LIMIT; i++) {
      if (src[i] === '{') return i
    }
    return -1
  }
  if (src[i] !== '{') return -1
  if (i - from >= LIMIT) return -1
  return i
}

function functionBodies(code: string): { name: string; body: string }[] {
  const src = strip(code)
  const out: { name: string; body: string }[] = []
  const heads = [
    ...src.matchAll(/(?:^|\n)\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)/g),
    ...src.matchAll(
      /(?:\n)\s{2,}(?:(?:public|private|protected|static|async|get|set)\s+)*([A-Za-z_]\w*)\s*(?=\()/g,
    ),
  ]
  for (const h of heads) {
    const name = h[1]
    if (!name || CONTROL_KEYWORDS.has(name)) continue
    const open = findBodyBrace(src, h.index + h[0].length)
    if (open < 0) continue
    let depth = 0
    for (let i = open; i < src.length; i++) {
      if (src[i] === '{') depth++
      else if (src[i] === '}') {
        depth--
        if (depth === 0) { out.push({ name, body: src.slice(open, i + 1) }); break }
      }
    }
  }
  return out
}

/**
 * 递归收集源码文件。
 *
 * ⚠⚠ **必须递归**：第一版只 `readdirSync(root)` 一层，于是
 *   `features/notes/notes-store.ts`、`features/email/*.ts` 这些**两级目录**
 *   整个没被扫到 ⇒ 普查只认出 4 处而不是 10 处，而且**完全不报错**。
 *   抓住它的是「允许清单不得有过期条目」那条**反向**断言 ——
 *   正向断言（新条目会红）在漏扫时永远是绿的，**只有反向断言会喊**。
 */
function collectSources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name)
    let st
    try { st = statSync(full) } catch { continue }
    if (st.isDirectory()) {
      if (name === '__tests__' || name === 'node_modules') continue
      collectSources(full, out)
    } else if (/\.(ts|vue)$/.test(name) && !name.includes('.test.')) {
      out.push(full)
    }
  }
  return out
}

/** 全仓枚举「一次操作里写两次以上」的函数。 */
function multiWriteFunctions(): { file: string; fn: string; runs: number }[] {
  const found: { file: string; fn: string; runs: number }[] = []
  for (const full of SCAN_ROOTS.flatMap((root) => collectSources(root))) {
    for (const f of functionBodies(readFileSync(full, 'utf8'))) {
      const runs = (f.body.match(RUN_RE) ?? []).length
      if (runs >= 2 && !f.body.includes('runInTransaction')) {
        found.push({ file: full.replace(SRC + '/', ''), fn: f.name, runs })
      }
    }
  }
  return found
}

describe('§880 · 一次操作里的多次写（普查门）', () => {
  /**
   * ⚠ 允许清单**逐条写明理由**，不是「这 9 处没关系」的笼统放行。
   *   新增条目必须在这里出现并写清「为什么它不是半个操作」。
   */
  const ALLOW = new Map<string, { why: string; runs: number }>([
    ['native/outboxStore.ts::put', { runs: 2, why: 'INSERT 撞幂等键才走 UPDATE，两条互斥（不是一次调用写两次）' }],
    ['native/asset-store.ts::upsert', { runs: 2, why: '先查存在性再 INSERT 或 UPDATE，两条互斥' }],
    ['native/approvalStore.ts::upsertSnapshot', { runs: 2, why: 'if/else 二选一，两条互斥' }],
    ['features/notes/notes-store.ts::handleServerEvent', { runs: 2, why: 'if(existing) UPDATE else INSERT，两条互斥' }],
    ['features/notes/notes-persist.ts::createNote', { runs: 2, why: 'INSERT + 回填 UPDATE，顺序由外键约束固定（见 note-persist-fk-order 门）' }],
    ['features/notes/notes-fts-backfill.ts::runNotesIndexBackfill', { runs: 2, why: '两条是 UPDATE search_text 与 INSERT _schema_migrations；FTS 那两条是 execute 不计入，合事务与否见简报 100' }],
    ['features/email/invoices-store.ts::remapLocalId', { runs: 2, why: 'if(existing) DELETE else UPDATE，两条互斥' }],
    ['features/email/email-folders-store.ts::recordOpsEntry', { runs: 2, why: 'UPDATE 分支与 INSERT 分支互斥' }],
    ['features/chat/chat-store.ts::saveMessage', { runs: 2, why: '⚠ 真的连写两次，但整个模块零引用（audit-dead-features 已报）——死代码，不装修' }],
    ['features/meetings/voiceprints-store.ts::saveVoiceprint', { runs: 2, why: 'planVoiceprintWrite 分成 rename/insert 两条互斥分支（多行签名，曾被 v2 解析器漏掉）' }],
    ['features/email/email-folders-store.ts::flushEmailOps', { runs: 2, why: '逐行标 pushed；中途失败 ⇒ 部分行仍 pending ⇒ 下轮重推，但 pushOps 带幂等键，重复推送会被去重（有意为之）' }],
  ])

  it('★ 普查结果与允许清单逐条一致（新增第 N+1 处、或已有处多写一次，都会红）', () => {
    const found = multiWriteFunctions()
    const keys = found.map((f) => `${f.file}::${f.fn}`)
    const missing = keys.filter((k) => !ALLOW.has(k))
    assert.deepEqual(missing, [],
      `新出现「一次操作里写两次」的函数，必须在 §880 允许清单里写清理由：\n  - ${missing.join('\n  - ')}`)
    // ⚠⚠ 必须**同时**钉住写次数。第一版只比函数名，结果在已放行的函数里
    //   多加一句 `db.run(...)` 时全绿 —— 而那恰恰是最该抓的改动。
    const drifted = found
      .map((f) => ({ k: `${f.file}::${f.fn}`, runs: f.runs, want: ALLOW.get(`${f.file}::${f.fn}`)?.runs }))
      .filter((x) => x.want !== undefined && x.want !== x.runs)
    assert.deepEqual(drifted, [],
      `已放行的函数改动过写次数：\n${drifted.map((x) => `  - ${x.k}: 期望 ${x.want} 次，实得 ${x.runs} 次`).join('\n')}`)
  })

  it('★ 允许清单不得有「已不存在」的条目（清单会悄悄过期）', () => {
    const keys = new Set(multiWriteFunctions().map((f) => `${f.file}::${f.fn}`))
    const stale = [...ALLOW.keys()].filter((k) => !keys.has(k))
    assert.deepEqual(stale, [],
      `允许清单里有条目已不成立（函数被重写或删除）⇒ 清单会骗人，请同步删：\n  - ${stale.join('\n  - ')}`)
  })

  it('★ 每条放行都必须写明理由与读数（不许出现空理由 / 0 次写）', () => {
    for (const [k, v] of ALLOW) {
      assert.ok(v.why.trim().length > 0, `${k} 的放行理由是空的`)
      assert.ok(v.runs >= 2, `${k} 的读数是 ${v.runs} —— 少于 2 次就不该出现在清单里`)
    }
  })

  it('★ 负控：分类器在合成输入上认得出「一次写两次」', () => {
    // ⚠⚠ 自证的**只有计数**。本分类器是**纯文本**的，它不懂分支 ——
    //   「这两处 run 是否互斥」是**人工**判定的，结论写在 ALLOW 的理由里。
    //   早先一版把「互斥分支只该数出 1 次」也写进负控，红了 ——
    //   那说明把「人的判断」当成了「量具的能力」。写在这里免得下一个人也这么写。
    const fake = `
      async function demoTwoWrites(db) {
        await db.run('UPDATE a SET x = ?', [1])
        await db.run('UPDATE b SET y = ?', [2])
      }
      async function demoOneWrite(db) {
        await db.run('UPDATE a SET x = ?', [1])
      }
    `
    const bodies = functionBodies(fake)
    // ⚠ 前提自证：分类器确实认得这两个名字，否则下面两条都在「没扫到」上假绿。
    assert.deepEqual(bodies.map((b) => b.name).sort(), ['demoOneWrite', 'demoTwoWrites'])
    assert.ok(
      (bodies.find((b) => b.name === 'demoTwoWrites')!.body.match(RUN_RE) ?? []).length >= 2,
      '分类器在合成输入上认不出「一次写两次」⇒ 主判据可能恒空转',
    )
    assert.equal((bodies.find((b) => b.name === 'demoOneWrite')!.body.match(RUN_RE) ?? []).length, 1)
  })
})
