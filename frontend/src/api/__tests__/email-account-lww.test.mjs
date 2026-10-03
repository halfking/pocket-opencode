/**
 * 邮箱账户配置 LWW 守卫的回归（2026-10-04）。
 *
 * 需求原文：「这个信息有最后修改时间，在服务端与客户端中，以最后时间为准来
 * 更新旧的一方。」服务端据此实现了 UpdateAccountLWTScoped（带 baseUpdatedAt
 * 守卫，冲突回 409 + 服务端当前 updatedAt）。
 *
 * 但守卫**从未在交互式 UI 路径上生效过**：`updateAccount` 早先是两参签名，
 * body 里没有 updatedAt ⇒ 服务端走 baseUpdatedAt<=0 的显式分支退化成
 * 无条件覆盖 ⇒ 行为是 store.go 注释里记的 last-write-by-arrival 原始 bug。
 * 也就是说「设置页改配置时守卫完全失效」，且不报任何错。
 *
 * 全文件是**静态判据**，不做真跑。原因：email.ts 用无扩展名 import
 * （'./http'），Node 的 ESM 解析器要求显式扩展名，直接 import 会
 * ERR_MODULE_NOT_FOUND（与 flashcards/utils/__tests__/flashcardIo.test.ts
 * 同因，见 test-coverage-waivers.json）。修它要给 tsconfig 开
 * allowImportingTsExtensions 并改源码 import，属于另一个 PR。
 *
 * 判据的边界（写在文件里，免得读数被误用）：
 *   - 它只认**第三个实参的字面文本**，所以挡得住"漏传"（实参数 ≠ 3）与
 *     "写 0"（基准 === '0'）；挡不住"传了个拼错的变量名"—— 那种由
 *     vue-tsc 报 TS2554 兜住（见下方"实测"）。
 *   - 已知且**故意不覆盖**的洞：把基准写成 `0` 之外的字面量（如 `1`）
 *     不会红。它同样会让服务端退化成"比 updated_at<=1"这种近乎恒真的守卫。
 *     这是本判据的残留缺口，不要把它当成"LWW 已完备"的证据。
 *   - 它扫 emailApi.updateAccount( 的调用点；emails-store.ts 里那个同名
 *     的本地库 updateAccount 不在范围内（它写的是 SQLite，不是服务端）。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

const CALL_SITES = [
  { file: '../../features/email/EmailSettingsView.vue', want: 3, why: '保存过滤策略 / 启停 / 同步间隔' },
  { file: '../../features/email/EmailAccountSetup.vue', want: 2, why: 'SMTP 测试 / 保存账户' },
  { file: '../../features/email/account-sync.ts', want: 1, why: 'LWW 上行' },
  { file: '../../native/config-sync/runtime.ts', want: 1, why: 'outbox 回放' },
]

/** 取出 `emailApi.updateAccount(` 每次调用的完整实参文本（括号配平）。 */
function extractCallArgs(src) {
  const out = []
  const needle = 'emailApi.updateAccount('
  let i = 0
  for (;;) {
    const at = src.indexOf(needle, i)
    if (at < 0) break
    let depth = 0
    let j = at + needle.length - 1
    for (; j < src.length; j++) {
      const ch = src[j]
      if (ch === '(') depth++
      else if (ch === ')') {
        depth--
        if (depth === 0) break
      }
    }
    out.push(src.slice(at + needle.length, j))
    i = j + 1
  }
  return out
}

/** 按顶层逗号切分实参（忽略括号 / 字符串 / 模板串里的逗号）。 */
function splitArgs(text) {
  const parts = []
  let depth = 0
  let cur = ''
  let quote = null
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      cur += ch
      if (ch === '\\') { cur += text[i + 1] ?? ''; i++; continue }
      if (ch === quote) quote = null
      continue
    }
    if (ch === "'" || ch === '"' || ch === '`') { quote = ch; cur += ch; continue }
    if (ch === '(' || ch === '{' || ch === '[') depth++
    if (ch === ')' || ch === '}' || ch === ']') depth--
    if (ch === ',' && depth === 0) { parts.push(cur); cur = ''; continue }
    cur += ch
  }
  parts.push(cur)
  return parts.map((s) => s.trim())
}

describe('emailApi.updateAccount 调用点必须携带 LWW 基准', () => {
  for (const site of CALL_SITES) {
    it(`${site.file}：${site.want} 处调用全部带基准（${site.why}）`, () => {
      const args = extractCallArgs(read(site.file))
      assert.equal(
        args.length,
        site.want,
        `${site.file} 的 emailApi.updateAccount 调用数变成 ${args.length}（期望 ${site.want}）。` +
          '新增/删除调用点时请同步更新 CALL_SITES，否则这条判据会开始漏判。',
      )
      for (const a of args) {
        const parts = splitArgs(a)
        assert.equal(
          parts.length,
          3,
          `实参个数应为 3（id, patch, baseUpdatedAt），实际 ${parts.length}：${a}`,
        )
        assert.notEqual(
          parts[2],
          '0',
          `基准写成了字面量 0：${a}\n` +
            '服务端会走 baseUpdatedAt<=0 的分支退化成无条件覆盖，守卫等于不存在（store.go）。',
        )
      }
    })
  }
})

describe('updateAccount 签名与请求体', () => {
  it('第三参是必填 number（退回可选就会重新静默漏传）', () => {
    assert.match(
      read('../email.ts'),
      /updateAccount\(\s*id: string,\s*patch: Partial<EmailAccount> & EmailCredentialInput,\s*baseUpdatedAt: number,\s*\)/,
      'baseUpdatedAt 不再是必填参数：以后新增调用点会重新静默漏传，守卫失效且无编译错误。',
    )
  })

  it('body 必须注入 updatedAt = baseUpdatedAt', () => {
    assert.match(
      read('../email.ts'),
      /body: JSON\.stringify\(\{ \.\.\.patch, updatedAt: baseUpdatedAt \}\)/,
      '请求体里没有 updatedAt，服务端读不到 baseUpdatedAt（server_assistant.go:1263）。',
    )
  })
})

describe('409 的消费方', () => {
  it('isStaleWriteError 按 instanceof ApiError && status===409 判定', () => {
    assert.match(
      read('../email.ts'),
      /export function isStaleWriteError\(e: unknown\): boolean \{\s*return e instanceof ApiError && e\.status === 409\s*\}/,
      'isStaleWriteError 的实现变了：所有 409 处理点的行为都随之改变，本判据需重新评估。',
    )
  })

  // 服务端确实在 409 body 里回 updatedAt（server_assistant.go 的
  // ErrStaleWrite 分支），但现有处理点都是重拉全量覆盖本地，用不到单值，
  // 所以没有导出取值函数（check:dead-api 会判无调用方的导出为死能力）。
  it('没有为 409 的 updatedAt 导出无人调用的取值函数', () => {
    assert.doesNotMatch(
      read('../email.ts'),
      /export function staleWriteServerUpdatedAt/,
      '若新增了取值函数，必须接上调用方，否则 check:dead-api 会报新增死能力。',
    )
  })

  // 409 若掉进通用 catch，会显示成「SMTP 测试失败：HTTP 409」/「连接失败（HTTP 409）」，
  // 把版本冲突误报成网络或凭证问题。这两个 catch 必须各自识别 409。
  for (const f of ['../../features/email/EmailAccountSetup.vue']) {
    it(`${f}：两处 catch 都识别 409`, () => {
      const src = read(f)
      const uses = (src.match(/isStaleWriteError\(e\)/g) || []).length
      assert.equal(uses, 2, `期望 2 处 409 分支，实际 ${uses} 处`)
    })
  }

  // outbox 回放必须**就地**消化 409：外层 catch 只会 markConfigPushFailed，
  // 而它只加 attempts、不改 state ⇒ 该行永远是 'queued'，每轮重放、永远推不进去。
  it('runtime.ts：outbox 回放就地消化 409，不许掉进外层 catch', () => {
    const src = read('../../native/config-sync/runtime.ts')
    const branch = src.slice(src.indexOf("row.namespace === 'email_account'"), src.indexOf("row.namespace === 'scheduled_task'"))
    assert.match(branch, /isStaleWriteError\(err\)/, 'outbox 的 email_account 分支没有识别 409')
    assert.match(branch, /if \(!isStaleWriteError\(err\)\) throw err/, '非 409 的错误必须继续上抛')
    assert.match(branch, /markConfigPushDone\(row\.id\)/, '409 也必须 markConfigPushDone，否则该行永远留在 queued')
  })
})
