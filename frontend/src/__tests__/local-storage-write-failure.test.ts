// local-storage-write-failure.test.ts — §866：本地**写**失败必须与「保存成功」区分开。
//
// 这是 §858/D21（读失败被谎报成「从来没有过」）的**写侧镜像**，同一族：
//   读侧原文：catch { return null }        ⇒ 谎报成「从来没有过对话」
//   写侧原文：catch { /* 容量超限等忽略 */ } ⇒ 谎报成「已经存好了」
//
// 缺陷背景：
//   D30 aiChatStore.persist()
//     存储写不进去时，catch 空转 ⇒ 应用继续表现得一切正常，
//     而用户这轮对话**根本没有落盘** —— 退出/刷新后凭空消失，界面零提示。
//     ⚠ 触发条件不是理论：features/email/email-body-cache.ts 的文件头
//       明写「localStorage 在 Android WebView 里总配额约 5–10MB，一旦写超，
//         snapshot-store 的 saveNS 会静默吞掉」—— 而 AI 对话历史装的是
//       **完整对话全文**（MAX_HISTORY = 40 轮），是最容易撑爆配额的那类数据。
//   D31 crypto-config.save()
//     **没有 try/catch** ⇒ 配额满时抛错，而它唯一的调用者之一是
//     MasterPasswordDialog::submit ⇒ initLobster 已成功后抛 ⇒ 界面显示
//     「本地数据初始化失败」—— **但初始化其实成功了**，真正失败的是「没存住」；
//     而且 hasMasterPassword 没落盘 ⇒ 下次启动回到 §862/D26 那个「创建主密码」对话框。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

const STORE = read('features/ai-chat/aiChatStore.ts')
const CHAT_VIEW = read('features/ai-chat/AIChatView.vue')
const CHAT_TPL = strip(/<template>([\s\S]*)<\/template>/.exec(CHAT_VIEW)?.[1] ?? '')
const CRYPTO = read('stores/crypto-config.ts')
const DIALOG = strip(read('features/auth/MasterPasswordDialog.vue'))
const HUB = strip(read('features/opencode/OpenCodeHub.vue'))
const SESSIONS = strip(read('features/opencode/SessionListView.vue'))

/** 取函数体：strip 注释后按圆/花括号配平（泛型参数要允许）。 */
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

describe('§866 · 尺子自证', () => {
  it('★ 五个文件都读到了', () => {
    assert.ok(STORE.includes('function persist()'), '没读到 aiChatStore.persist')
    assert.ok(CHAT_TPL.includes('</template>') || CHAT_TPL.length > 0, '没读到 AIChatView 模板')
    assert.ok(CRYPTO.includes('function save()'), '没读到 crypto-config.save')
    assert.ok(DIALOG.includes('setMasterPassword'), '没读到主密码对话框')
    assert.ok(HUB.includes('function selectInstance('), '没读到 OpenCodeHub.selectInstance')
    assert.ok(SESSIONS.includes('selected_opencode_instance'), '没读到 SessionListView 的恢复分支')
  })
})

/** D30 判据同样抽成函数，供真实源码与变异体共用。 */
function storeTellsUserTheTruth(src: string): { ok: boolean; why?: string } {
  const body = fnBody(src, 'persist')
  if (!body) return { ok: false, why: '找不到 persist' }
  const catchAt = body.indexOf('catch')
  if (catchAt <= 0) return { ok: false, why: 'persist 没有 catch' }
  const tail = body.slice(catchAt)
  if (!/persistFailed\.value = true/.test(tail)) return { ok: false, why: '写失败没有置位 ⇒ 界面无从区分「保存成功」与「根本没存下」' }
  if (!/console\.warn/.test(tail)) return { ok: false, why: '写失败没有任何日志 ⇒ 事后无法判断这条路是否已被存储容量关掉' }
  if (body.indexOf('persistFailed.value = false') <= 0) return { ok: false, why: '写成功后不复位标志 ⇒ 一次偶发失败会让提示永久显示' }
  return { ok: true }
}

describe('§866 · D30 AI 对话历史写失败必须如实告知', () => {
  it('★★★ persist 的 catch 不得空转（空转 = 谎报「已保存」）', () => {
    const body = fnBody(STORE, 'persist')
    assert.ok(body, '找不到 persist')
    const catchAt = body.indexOf('catch')
    assert.ok(catchAt > 0, 'persist 没有 catch')
    const tail = body.slice(catchAt)
    assert.match(tail, /persistFailed\.value = true/,
      '写失败没有置位 ⇒ 界面无从区分「保存成功」与「根本没存下」')
    assert.match(tail, /console\.warn/,
      '写失败没有任何日志 ⇒ 事后无法判断这条路是否已被存储容量关掉')
  })

  it('★★★ 写成功后必须把标志复位（否则一次偶发失败会永久挂着警告）', () => {
    const body = fnBody(STORE, 'persist')
    const okAt = body.indexOf('persistFailed.value = false')
    assert.ok(okAt > 0, 'persist 成功后不复位标志 ⇒ 一次偶发失败会让提示永久显示')
  })

  it('★★★ 标志必须导出（否则视图拿不到）', () => {
    const ret = /useAIChatStore[\s\S]*?\n  return \{[\s\S]*?\n  \}/.exec(strip(STORE))?.[0] ?? ''
    assert.match(ret, /persistFailed/, 'store 没导出 persistFailed')
  })

  it('★★ 模板必须有出口，且**不得**出现任何暗示「已保存」的措辞', () => {
    assert.match(CHAT_TPL, /v-if="store\.persistFailed"/, '模板缺少写失败提示的出口')
    const notice = /v-if="store\.persistFailed"[\s\S]{0,400}?<\/div>/.exec(CHAT_TPL)?.[0] ?? ''
    assert.ok(notice, '取不到写失败提示块')
    assert.match(notice, /不会.{0,4}保存|保存不了|写不进去/,
      '提示没有说清「这轮不会被保存」')
    assert.doesNotMatch(notice, /已保存|已存储|保存成功/,
      '提示里出现了暗示「已经保存」的措辞 ⇒ 那正是本节要消灭的那种撒谎')
  })

  it('★★ 提示必须在所有空态分支之外（否则下一次成功渲染会把它吞掉）', () => {
    const at = CHAT_TPL.indexOf('store.persistFailed')
    const chainAt = CHAT_TPL.indexOf('turns.length === 0')
    assert.ok(at > 0 && chainAt > 0, '模板结构变了')
    assert.ok(at < chainAt, '写失败提示落在空态互斥链之内 ⇒ 下一次渲染可能整块消失')
  })
})

/**
 * D31 弹窗判据抽成函数：真实源码与变异体**必须跑同一把尺子**。
 * ⚠ 曾在这里翻车：负控原本用 `[^\n]*` 匹配整行，连 `error.value = '` 前缀
 *   一起替换掉 ⇒ 变异后的源码里根本没有 `error.value =`，断言恒红，
 *   看起来像「判据抓到了什么」，其实是量具自己坏了。变异只能改**字符串字面量**。
 */
function dialogTellUserTheTruth(src: string): { ok: boolean; why?: string } {
  if (!/persistFailed/.test(src)) return { ok: false, why: '弹窗不看 persistFailed ⇒ 用户仍然只看到「初始化失败」这种不属实的措辞' }
  // ⚠ 必须连**字符串内容**一起捕获。早先只截到 `error.value =` 就停，
  //   于是无论文案写什么都判绿 —— 变异体把文案改成「初始化失败」也一样绿。
  const m = /persistFailed[\s\S]{0,300}?error\.value\s*=\s*(['"][^'"\n]*['"])/.exec(src)
  if (!m) return { ok: false, why: '弹窗没有在 persistFailed 分支里给出错误文案' }
  if (/初始化失败/.test(m[1]!)) return { ok: false, why: '这条文案不得复用「初始化失败」—— 初始化其实已经成功了' }
  return { ok: true }
}

describe('§866 · D31 加密配置写失败不得冒泡成「初始化失败」', () => {
  it('★★★ save 必须自己承担写失败', () => {
    const body = fnBody(CRYPTO, 'save')
    assert.ok(body, '找不到 save')
    const catchAt = body.indexOf('catch')
    assert.ok(catchAt > 0, 'save 仍然没有 catch ⇒ 配额满时抛给一个与它无关的错误出口')
    const tail = body.slice(catchAt)
    assert.match(tail, /persistFailed\.value = true/, '写失败没有置位')
    assert.match(tail, /console\.warn/, '写失败没有任何日志')
  })

  it('★★ 弹窗必须把「解锁成功但没存住」与「初始化失败」分开说', () => {
    const v = dialogTellUserTheTruth(DIALOG)
    assert.ok(v.ok, v.why!)
  })
})

/** D32 判据：缓存写入不得阻断用户已点下的动作。 */
function cacheWriteNeverBlocks(src: string): { ok: boolean; why?: string } {
  const body = fnBody(src, 'selectInstance')
  if (!body) return { ok: false, why: '找不到 OpenCodeHub.selectInstance' }
  const at = body.indexOf('localStorage.setItem')
  if (at < 0) return { ok: false, why: 'selectInstance 里没有写入' }
  const head = body.slice(0, at)
  if (!/try\s*\{/.test(head)) return { ok: false, why: '缓存写入裸奔 ⇒ 配额满时会把「选中 + 跳转」两步一起取消' }
  const tail = body.slice(at)
  if (!/catch/.test(tail)) return { ok: false, why: '写入没有 catch ⇒ 失败会冒泡并中止后续动作' }
  if (!/console\.warn/.test(tail)) return { ok: false, why: '失败没有日志 ⇒ 事后无法判断这条路是否已被存储容量关掉' }
  if (!/router\.push/.test(tail)) return { ok: false, why: '跳转不在写入之后（本次改动只讨论顺序不变）' }
  return { ok: true }
}

/** D32 读侧：恢复失败不得顶掉整个 onMounted（§860 族的读侧形态）。 */
function restoreNeverBreaksMount(src: string): { ok: boolean; why?: string } {
  const at = src.indexOf('selected_opencode_instance')
  if (at < 0) return { ok: false, why: '找不到恢复分支' }
  const head = src.slice(0, at)
  const tail = src.slice(at, at + 600)
  if (!/try\s*\{/.test(head.slice(-400))) return { ok: false, why: '恢复分支的 JSON.parse 没有 try ⇒ 值损坏时抛错会顶掉整个 onMounted' }
  if (!/catch/.test(tail)) return { ok: false, why: '恢复分支没有 catch' }
  if (!/console\.warn/.test(tail)) return { ok: false, why: '恢复失败没有日志 ⇒ 无法区分「探针坏了」与「没选过」' }
  return { ok: true }
}

describe('§866 · 变异负控（负控必须与正例跑同一把尺子，否则绿得没有意义）', () => {
  it('★ 先证明这把尺子在真源码上是绿的（否则下面的红只是尺子坏了）', () => {
    assert.equal(storeTellsUserTheTruth(STORE).ok, true, storeTellsUserTheTruth(STORE).why)
    assert.equal(dialogTellUserTheTruth(DIALOG).ok, true, dialogTellUserTheTruth(DIALOG).why)
    assert.equal(cacheWriteNeverBlocks(HUB).ok, true, cacheWriteNeverBlocks(HUB).why)
    assert.equal(restoreNeverBreaksMount(SESSIONS).ok, true, restoreNeverBreaksMount(SESSIONS).why)
  })

  it('把读侧恢复改回裸 JSON.parse ⇒ D32 读侧立刻红', () => {
    const mutated = SESSIONS.replace(/try \{\n      const savedInstance/, 'const savedInstance')
    assert.notEqual(mutated, SESSIONS, '变异没生效（锚点与真源码不匹配）')
    assert.equal(restoreNeverBreaksMount(mutated).ok, false, '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })

  it('把缓存写入改回裸奔 ⇒ D32 那组立刻红', () => {
    // ⚠ 变异必须保持括号配平，否则 fnBody 会在第一个 `}` 就截断，
    //   测到的就不是「缺陷形态」而是「语法坏掉的文件」——量具又坏了。
    const mutated = HUB.replace(
      /try \{\n    localStorage\.setItem\('selected_opencode_instance', JSON\.stringify\(instance\)\)\n  \} catch \(err\) \{\n    console\.warn[\s\S]*?\n  \}/,
      "localStorage.setItem('selected_opencode_instance', JSON.stringify(instance))")
    assert.notEqual(mutated, HUB, '变异没生效（锚点与真源码不匹配）')
    assert.equal(cacheWriteNeverBlocks(mutated).ok, false, '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })

  it('把 persist 的 catch 改回空转 ⇒ 第一组立刻红', () => {
    const mutated = STORE.replace('persistFailed.value = true', '/* 容量超限等忽略 */')
    assert.notEqual(mutated, STORE, '变异没生效')
    assert.equal(storeTellsUserTheTruth(mutated).ok, false,
      '尺子坏了：把缺陷形态喂回去竟然还判绿 ⇒ 前面那条正例是假绿')
  })

  it('把弹窗文案改回「初始化失败」⇒ 第三组立刻红', () => {
    // ⚠ 只替换字符串**字面量**：早先用 /主密码已通过验证并解锁成功[^\n]*/ 把整行
    //   连同 `error.value = '` 前缀一起吃掉，变异后的源码里根本没有 error.value =，
    //   断言恒红却看起来像「抓到了」。
    const mutated = DIALOG.replace(
      /'主密码已通过验证并解锁成功[^']*'/,
      "'本地数据初始化失败'")
    assert.notEqual(mutated, DIALOG, '变异没生效（锚点与真源码不匹配）')
    assert.equal(dialogTellUserTheTruth(mutated).ok, false,
      '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })
})
