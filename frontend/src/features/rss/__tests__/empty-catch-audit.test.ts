// empty-catch-audit.test.ts — §849：**失败被静默丢弃**，且失败发生在「成功态之后」。
//
// 缺陷背景（RssItemDetail.doShare）：
//   const r = await rssApi.share(...)   // 服务端成功
//   shareResult.value = r               // ← 成功态已经设上
//   if (dest === 'clipboard' && r.copyText) {
//     try { await navigator.clipboard.writeText(r.copyText) }
//     catch { /* 浏览器/Capacitor 不支持时静默 */ }   // ← 失败被吞
//   }
// ⇒ 用户点「复制到剪贴板」，界面显示复制内容（看起来成功），
//   粘贴出来却是空的上一次内容；而 WebView 无剪贴板权限**在本仓是被记录过的**
//   （composables/useVoiceInput.ts 的注释「WebView 可能无剪贴板权限」）。
//
// ⚠ 这一族的难点不是「catch 为空」，而是**失败发生在设置成功态之后**：
//   普通的静默 catch 至少发生在「还没有对外宣称成功」的时候；
//   这里成功态已经渲染出去了，再吞掉失败 ⇒ 界面在**主动说谎**。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
// ⚠ 本文件在 src/features/rss/__tests__/ 下 ⇒ 要回退**三层**才是 src。
//   写成两层时 `read()` 会去 src/features/features/… 找，报 ENOENT ——
//   而第一个报错的 suite 看起来像「判据本身有问题」，容易查错方向。
const SRC = join(HERE, '..', '..', '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

function collectVue(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === '__tests__' || name === 'node_modules') continue
    const full = join(dir, name)
    let st
    try { st = statSync(full) } catch (err: any) {
      if (err?.code !== 'ENOENT') throw err
      continue
    }
    if (st.isDirectory()) collectVue(full, out)
    else if (name.endsWith('.vue')) out.push(full)
  }
  return out
}

const VUE_FILES = collectVue(SRC)

const templateOf = (src: string): string => {
  const scriptAt = src.search(/<script\b/)
  const head = scriptAt === -1 ? src : src.slice(0, scriptAt)
  if (/<template[\s>]/.test(head)) return head
  const t0 = src.indexOf('<template')
  const t1 = src.lastIndexOf('</template>')
  return t0 >= 0 && t1 > t0 ? src.slice(t0, t1 + '</template>'.length) : head
}
const tplCode = (src: string): string => templateOf(src).replace(/<!--[\s\S]*?-->/g, '')

/** 从 `catch {` 起取到配平的 `}`。 */
const catchBody = (code: string, from = 0): string => {
  const re = /\bcatch\s*(?:\([^)]*\))?\s*\{/g
  re.lastIndex = from
  const m = re.exec(code)
  if (!m) return ''
  const open = m.index + m[0].length - 1
  let depth = 0
  for (let k = open; k < code.length && k < open + 2000; k += 1) {
    if (code[k] === '{') depth += 1
    else if (code[k] === '}') { depth -= 1; if (depth === 0) return code.slice(open + 1, k) }
  }
  return ''
}

/** 剥掉注释与空白后的「有效内容」。 */
const effective = (body: string): string =>
  body.replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
      .trim()

describe('§849 · 分享：写剪贴板失败不能被吞', () => {
  const RSS = read('features/rss/RssItemDetail.vue')

  it('★ 尺子非空转：全仓 .vue 至少扫到 100 个', () => {
    assert.ok(VUE_FILES.length > 100, `只扫到 ${VUE_FILES.length} 个 .vue，路径写错了`)
  })

  it('★★★ doShare 的剪贴板 catch 不得为空', () => {
    const at = RSS.indexOf("dest === 'clipboard'")
    assert.ok(at > 0, '没找到剪贴板分支（请核对源码是否已改形状）')
    const body = catchBody(RSS, at)
    assert.notEqual(body, '', 'doShare 里没有 catch 块了')
    assert.notEqual(effective(body), '',
      '剪贴板写入失败的 catch 又是空的了 ⇒ 用户会看到「复制成功」而剪贴板是空的')
  })

  it('★★★ 失败必须写进一个会被渲染的标志', () => {
    // 找 `catch` 里那一格被赋成了什么
    const at = RSS.indexOf("dest === 'clipboard'")
    const body = catchBody(RSS, at)
    const assigned = /(\w+)\.value\s*=/.exec(effective(body))
    assert.ok(assigned, 'catch 里没有任何赋值 ⇒ 失败无处可去')
    assert.ok(tplCode(RSS).includes(assigned[1]),
      `${assigned[1]} 被设上了却没有任何模板渲染它 ⇒ 又是「设了没人看」`)
  })

  it('★★ 提示位必须在分享弹窗内部，不能用页面级的 errorMsg', () => {
    // errorMsg 挂在 `v-else-if="errorMsg"` 上，与 <article> 互斥
    // ⇒ 用它报「复制失败」会让正文整块消失。
    const at = RSS.indexOf("dest === 'clipboard'")
    const flag = /(\w+)\.value\s*=/.exec(effective(catchBody(RSS, at)))?.[1]
    assert.notEqual(flag, 'errorMsg',
      '复制失败写进了 errorMsg ⇒ 正文会整块消失（它在 v-else-if 链上）')
    const tpl = tplCode(RSS)
    const atNotice = tpl.indexOf(`v-if="${flag}"`)
    const atModal = tpl.indexOf('class="modal-mask"')
    assert.ok(atNotice > 0 && atModal >= 0 && atNotice > atModal,
      `${flag} 的提示位不在分享弹窗内部 ⇒ 用户在弹窗里看不到`)
  })

  it('★★ 每次分享都要复位该提示', () => {
    const flag = /(\w+)\.value\s*=/.exec(effective(catchBody(RSS, RSS.indexOf("dest === 'clipboard'"))))?.[1]
    assert.ok(flag, '取不到提示位名字')
    const cleared = (RSS.match(new RegExp(`${flag}\\.value\\s*=\\s*''`, 'g')) || []).length
    assert.ok(cleared >= 2,
      `${flag} 只被清空了 ${cleared} 次 ⇒ 应在 openShare() 与 doShare() 开头各清一次，` +
      '否则上一次失败的提示会挂在这次成功的分享上')
  })
})

describe('§849 · 全仓空 catch 扫面（登记口径，不是禁令）', () => {
  /**
   * ⚠ **本组不判红**。全仓 catch 711 个、其中空 catch 128 个，
   *   绝大多数是**正当**的（幂等清理、尽力预载、用户取消、不支持时的降级）。
   *   收窄到「.vue 里 + 所在函数被用户动作触发」后只剩 3 个，其中 2 个是
   *   带明确理由的刻意静默（StudyHubView 的 advance / setDailyDigest，
   *   理由都写在注释里且站得住）。
   * ⇒ 它的作用是**把这份清单固定下来**，让以后新增的空 catch 有人看一眼，
   *   而不是「凡是空 catch 就算缺陷」—— 那种判据第一天就会是错的。
   */
  it('★ 清单与读数固定：空 catch 的数量变化必须有人解释', () => {
    const empties: string[] = []
    let total = 0
    for (const f of VUE_FILES) {
      const text = readFileSync(f, 'utf8')
      const re = /\bcatch\s*(?:\([^)]*\))?\s*\{/g
      let m
      while ((m = re.exec(text))) {
        total += 1
        const body = catchBody(text, m.index)
        if (effective(body) === '') {
          empties.push(`${relOf(f)}:${text.slice(0, m.index).split('\n').length}`)
        }
      }
    }
    assert.ok(total > 200, `全仓 .vue 只扫到 ${total} 个 catch，路径八成写错了`)
    // ⚠ 棘轮上界必须**按真实基线定**，不能拍脑袋。
    //   第一版写 20，而实测基线就是 21 ⇒ 提交当天就红，报的还是「新增空 catch」，
    //   会把人引去查一个并不存在的回归。基线 = 2026-10-18 实测（.vue 口径）。
    //   只钉上界：新增会被抓住，减少不会误报（与 check:raw-error 同一做法）。
    // ⚠ 2026-10-29：基线 21 → 22 —— 67c4d645 给 StorageSyncSection 的
    //   syncPolicy 刷新加了一处带理由的刻意静默（「横幅持续显示直到显式重试
    //   成功」，见该文件 :181 的行内注释）。属「有人看一眼后放行」的口径内。
    assert.ok(empties.length <= 22,
      `.vue 里的空 catch 有 ${empties.length} 个（>22）：\n  ${empties.join('\n  ')}\n` +
      '⇒ 新增的每一个都必须说明「为什么这里可以静默」，' +
      '否则把它改成有出口，或把它登记进例外清单')
  })
})
