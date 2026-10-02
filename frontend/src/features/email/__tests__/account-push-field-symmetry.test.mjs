/**
 * 上行 payload 必须与下行覆盖的字段集对称。
 *
 * 缺陷现场（2026-10-02）：
 *   下行 buildMirrorAccountWrite 会用服务端值覆盖本地
 *   imapHost / imapPort / authType / emailAddress；
 *   上行 pushAccountToServer 的 patch 原本只有
 *   displayName / syncIntervalMin / enabled。
 *
 * 后果（静默丢改动，无任何报错）：
 *   用户在设置页改了 IMAP 主机 → 本地 updatedAt 变大
 *   → planAccountSync 判「本地更新」→ 触发上行
 *   → payload 里没有 imapHost，服务端原样不动
 *   → 下一轮下行又用服务端的旧 imapHost 覆盖回来
 *   → 用户改动凭空消失。
 *
 * 服务端 updateEmailAccount 的 body 明确接受这三个字段
 * （IMAPHost/IMAPPort/AuthType 三个指针），所以是客户端漏发，不是服务端不支持。
 *
 * Run: node --experimental-strip-types --test src/features/email/__tests__/account-push-field-symmetry.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

/** 下行 buildMirrorAccountWrite 会覆盖的列（UPDATE 分支的 SET 列表）。 */
const DOWNSTREAM_OVERWRITTEN = [
  'display_name',
  'email_address',
  'imap_host',
  'imap_port',
  'auth_type',
  'sync_interval_min',
  'enabled',
]

function readSource(rel) {
  return readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8')
}

/** 去掉注释，避免注释里的字段名把断言喂饱（这条护栏第一版就栽在这）。 */
function stripComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/.*$/gm, '$1')
}

describe('上行 payload 与下行覆盖字段集对称', () => {
  it('buildMirrorAccountWrite 覆盖的每一列，上行 patch 都要能带上', () => {
    const mirror = stripComments(readSource('../account-mirror-write.ts'))
    const updateBranch = mirror.slice(mirror.indexOf('UPDATE local_email_accounts'))
    for (const col of DOWNSTREAM_OVERWRITTEN) {
      assert.ok(
        updateBranch.includes(col),
        `下游 UPDATE 里没有 ${col}；若它被覆盖而上行不带，用户改它就会被静默丢弃。` +
          `若这是有意为之，请更新 DOWNSTREAM_OVERWRITTEN 列表并写明理由。`,
      )
    }
  })

  it('pushAccountToServer 的 patch 含 imapHost / imapPort / authType', () => {
    const src = stripComments(readSource('../account-sync.ts'))
    const body = src.slice(src.indexOf('const patch = {'), src.indexOf('try {', src.indexOf('const patch = {')))
    // 断言「键: 值」形式，**不能**只查字段名 —— `imapHost: undefined` 同样
    // 包含 "imapHost" 字样，纯文本匹配会把这个护栏喂饱。负控 1 正是这么
    // 骗过第一版判据的：注入 undefined 后 4 例全绿。
    for (const f of [
      'displayName: _a.displayName',
      'syncIntervalMin: _a.syncIntervalMin',
      'enabled: _a.enabled',
      'imapHost: _a.imapHost',
      'imapPort: _a.imapPort',
      'authType: narrowAuthType(_a.authType)',
    ]) {
      assert.ok(body.includes(f), `上行 patch 缺少 "${f}"（必须是键:值 形式，不能只是出现字段名）`)
    }
    // 显式禁止 undefined：字段在但没值 = 改动同样会被静默丢弃
    assert.doesNotMatch(
      body,
      /\b(imapHost|imapPort|authType)\s*:\s*undefined/,
      '上行 patch 里出现了 `xxx: undefined`，JSON.stringify 会把它整个抹掉，' +
        '字段名虽在、值却没带上 —— 正是负控 1 骗过本护栏的形态',
    )
  })

  it('上行取的是本地值，不是 spread 进来的服务端值', () => {
    const src = stripComments(readSource('../account-sync.ts'))
    // pushAccountToServer({...target, ...}) 里必须逐个显式覆盖这三个，
    // 否则 spread 的 target（服务端对象）会把服务端的旧值原样发回去。
    const call = src.slice(src.indexOf('await pushAccountToServer({'))
    const end = call.indexOf('})')
    const body = call.slice(0, end)
    for (const f of [
      'imapHost: l.imapHost',
      'imapPort: l.imapPort',
      'authType: narrowAuthType(l.authType)',
    ]) {
      assert.ok(body.includes(f), `上行调用缺 ${f} —— 字段虽然带上了，但值仍来自服务端，等于没修`)
    }
  })

  it('服务端确实接受这三个字段（否则不该上行它们）', () => {
    // 测试文件位于 frontend/src/features/email/__tests__/：
    // __tests__ -> email -> features -> src -> frontend -> worktree 根，共 5 层。
    // backend 是 worktree 根下的兄弟目录。
    const go = readSource('../../../../../backend/internal/server/server_assistant.go')
    const body = go.slice(go.indexOf('func (s *Server) updateEmailAccount'))
    const end = body.indexOf('json.NewDecoder')
    const decl = body.slice(0, end)
    for (const f of ['IMAPHost', 'IMAPPort', 'AuthType']) {
      assert.ok(decl.includes(f), `服务端 updateEmailAccount 的 body 里没有 ${f}`)
    }
  })
})

function makeNarrow() {
  const src = readSource('../account-sync.ts')
  const start = src.indexOf('function narrowAuthType')
  assert.ok(start >= 0, 'account-sync.ts 里找不到 narrowAuthType')
  const open = src.indexOf('{', start)
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) {
        // 剥掉 TS 类型标注与 `as` 断言，让 new Function 能求值。
        // 剥离规则必须覆盖实现里可能出现的每一种 TS 语法：负控 3 用了
        // `raw as AuthType`，规则漏了 `as` 之后 describe 块在**收集阶段**
        // 抛 SyntaxError、整块静默跳过，表现为「# pass 4」的假绿。
        const js = src
          .slice(start, i + 1)
          .replace(/:\s*string\s*\|\s*undefined\s*\|\s*null/g, '')
          .replace(/\)\s*:\s*AuthType\b/g, ')')
          .replace(/\bas\s+[A-Za-z_$][\w$]*/g, '')
        // eslint-disable-next-line no-new-func
        return new Function(`${js}; return narrowAuthType`)()
      }
    }
  }
  throw new Error('narrowAuthType 函数体未闭合')
}

// narrowAuthType 由 vue-tsc 逼出来（本地 SQLite 读回的 auth_type 是 string，
// 服务端要 AuthType 联合类型）。它必须有**行为**测试：非法值不能整轮同步失败。
//
// 这里不 import account-sync.ts（它 import 了 api/email → http → 整个运行时，
// 纯 node 环境跑不起来），改为从源文件里抽出函数体求值，测真实行为。
// 不这么做就退化成「重复断言同一句源码」——那种测试无论实现怎么改都全绿。
describe('narrowAuthType 行为', () => {
  // 顶层 new Function 若抛 SyntaxError，这个 describe 的**所有**用例都会被
  // node:test 静默跳过，汇总只显示「# pass 4」之类的假绿。
  // 这里先把函数求值成功，并显式断言用例确实被注册了。
  let narrow
  let loadError = null
  try {
    narrow = makeNarrow()
  } catch (e) {
    loadError = e
  }

  it('narrowAuthType 能被加载并求值（否则下面两条是假测试）', () => {
    assert.equal(loadError, null, `加载/求值失败：${loadError && loadError.message}`)
    assert.equal(typeof narrow, 'function')
  })

  it('合法值原样通过', () => {
    assert.equal(narrow('password'), 'password')
    assert.equal(narrow('oauth2'), 'oauth2')
  })

  it('非法/空值回落到 password，不得让整轮 LWW 同步失败', () => {
    for (const bad of ['', '   ', 'garbage', 'OAUTH2', 'basic', null, undefined, 0, {}]) {
      assert.equal(
        narrow(bad),
        'password',
        `narrowAuthType(${JSON.stringify(bad)}) 应回落到 password`,
      )
    }
  })
})
