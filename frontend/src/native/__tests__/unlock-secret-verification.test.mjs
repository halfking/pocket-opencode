// 门禁：解锁本地加密库时，**主密码必须先被校验**，再交给 initLobster/localDB.init。
//
// 为什么要有这道门（2026-10-06 真机实锤，非读码推断）：
//   真机 `emulator-5554` 三轮对照（scripts 见 /tmp/opdev/wrongpass.mjs，判据有牙）：
//     A 正确口令 e2e-master-pass-123 → clicked=1，离开解锁页，hash=#/notes
//     B 错误口令 definitely-not-the-master-pw-zzz-9182
//       → clicked=1，**同样离开解锁页**，hash=#/notes
//       console 同时打出被吞掉的那条：
//         error: SetEncryptionSecret: a passphrase has already been set
//         warn:  [localDB] setEncryptionSecret 已存或失败，沿用现有 secret
//     C 空口令 → clicked=0，留在 #/login?returnTo=/notes&unlock=1
//       （正向对照：证明 unlock 路径**本身有门**，不是「怎么点都能过」；
//         也排除了 B 的判据恒真）
//
// 三处代码合成这个洞：
//   1) `crypto.ts:53 initAppCrypto()` —— 只做 PBKDF2 派生，**对任何字符串都成功**，
//      从不比对。它不是校验点。
//   2) `local-db.ts:152 setEncryptionSecret(secret)` —— 插件原生侧
//      （@capacitor-community/sqlite `UtilsSecret.java:42`）在「已存过 secret」时
//      抛 `a passphrase has already been set`；`local-db.ts:156` 的 catch 把它吞成
//      一条 warn 并**继续往下走**。
//   3) 插件 `Database.java:245` 的 `password = _uSecret.getPassphrase()` ——
//      `open()` 取的是 SharedPreferences 里**已存的那个**，不是本次传入的。
//   ⇒ 输错口令 ⇒ 抛错被吞 ⇒ 用**旧 secret 正常开库** ⇒ `_ready=true` ⇒
//     `LoginView.vue:436 needUnlock=false` ⇒ 页面放行。
//   真正被"验证"的其实只有服务端 token（`LoginView.vue` 的登录流程），
//   本地加密库这一层等于**零校验**。
//
// 判据钉的是**契约**（进入 init 前必须存在一次 secret 校验），
// 不是钉某个函数名——换实现方式不该红，真缺校验才红。
//
// 为什么不直接改代码：改法有产品语义（改密路径要不要一起做、
// Keystore 插件当前在本机根本 reject，见 keystore.ts:128-136），
// 属属主决定。本门禁只保证「这个洞不会被静默改回来」。
//
// Run: node --test src/native/__tests__/unlock-secret-verification.test.mjs
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const NATIVE = join(here, '..')
const AUTH = join(here, '..', '..', 'features', 'auth')

const LOBSTER = readFileSync(join(NATIVE, 'lobster-init.ts'), 'utf8')
const LOCALDB = readFileSync(join(NATIVE, 'local-db.ts'), 'utf8')
const LOGIN = readFileSync(join(AUTH, 'LoginView.vue'), 'utf8')

/**
 * 从函数/方法名起，用**括号配平**取到函数体末尾。
 * ⚠️ 不用固定缩进收尾：这两个函数里都嵌着 try/catch 与 async IIFE，
 * 按缩进会在内层 } 截断（与 migration-guards 门禁同一条教训）。
 *
 * ⚠️ 必须同时覆盖**三种声明形态**，否则量具自己会红：
 *   `function init(` / `async init(`（实例方法）/ `private async init(`。
 *   第一版只认 `function init(`，导致 `local-db.ts` 的 `async init(dbSecret)`
 *   取不到 → 报「init 必须存在」——那是量具的错，不是产品的错。
 */
function fnBody(src, name) {
  const decl = new RegExp(
    `(?:^|[\\s;{])(?:export\\s+)?(?:private\\s+|public\\s+|static\\s+|async\\s+)*${name}\\s*\\([^)]*\\)\\s*(?::[^\\{]*)?\\{`,
    'm',
  )
  const m = decl.exec(src)
  if (!m) return null
  const open = m.index + m[0].length - 1
  let depth = 0
  for (let i = open; i < src.length; i++) {
    const c = src[i]
    if (c === '{') depth++
    else if (c === '}') {
      depth--
      if (depth === 0) return src.slice(m.index, i + 1)
    }
  }
  return null
}

/** 剥掉注释与字符串字面量，避免「注释里提到校验」被判成「代码里在校验」。 */
function stripNoise(text) {
  return text
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')
    .replace(/`(?:\\.|[^`\\])*`/g, '``')
    .replace(/'(?:\\.|[^'\\])*'/g, "''")
    .replace(/"(?:\\.|[^"\\])*"/g, '""')
}

test('initLobster 在建库/开库之前必须校验主密码（不能只派生不比对）', () => {
  const body = fnBody(LOBSTER, 'initLobster')
  assert.ok(body, 'initLobster 必须存在于 lobster-init.ts')
  const code = stripNoise(body)

  // 1) 必须有一处「拿本次传入的 secret 去比对」的调用。
  //
  // ⚠️ 口径（第三次踩同一类坑后定稿）：这里必须认**语义**而不是某个具体函数名。
  // 前两版分别只认 `checkEncryptionSecret/verifySecret/...` 和「helper 转发」，
  // 结果真实实现 `localDB.verifyMasterPassword(...)` 两版都不认 ⇒ 判据恒红。
  // 正确口径：任何**含 verify|check|validate + secret|password|master 语义**的调用都算数。
  const semantic = /[\w.]*\b(verify|check|validate|assert)\w*(Secret|Password|Master|Credential)\w*\s*\(/i
  const hasCompare = semantic.test(code)
  assert.ok(
    hasCompare,
    'initLobster 里找不到任何主密码/secret 校验调用。' +
      '当前形状是「PBKDF2 派生成功即视为口令正确」，而插件 open() 用的是已存的旧 secret，' +
      '于是任意非空错误口令都能开库（2026-10-06 真机 A/B/C 三轮对照实锤）。',
  )

  // 2) 校验必须发生在 initAppCrypto / localDB.init 之前。
  //    顺序错了 = 错口令先用错口令派生了 AES key 并写进模块级 cryptoKey。
  const cmpAt = code.search(semantic)
  const cryptoAt = code.search(/initAppCrypto\s*\(/)
  const initAt = code.search(/localDB\.init\s*\(/)
  assert.ok(initAt >= 0, 'initLobster 里应调用 localDB.init(...)')
  assert.ok(
    cryptoAt < 0 || cmpAt < cryptoAt,
    `主密码校验必须早于 initAppCrypto（校验在第 ${cmpAt}，initAppCrypto 在第 ${cryptoAt}）。` +
      '晚于它意味着错口令已经污染了 cryptoKey。',
  )
  assert.ok(
    cmpAt < initAt,
    `主密码校验必须早于 localDB.init（校验在第 ${cmpAt}，init 在第 ${initAt}）。`,
  )

  // 3) 校验失败必须让 initLobster 走 reject（否则调用方 LoginView 的 catch 收不到）。
  const afterCmp = code.slice(cmpAt, cmpAt + 400)
  assert.ok(
    /throw\s|!/.test(afterCmp),
    '校验分支必须有显式失败出口（throw / 条件不满足即失败），' +
      '否则「校验」只是打个日志，实际仍放行。',
  )
})

test('local-db 不得再用「setEncryptionSecret 抛不抛」来推断口令一致', () => {
  const body = fnBody(LOCALDB, 'init')
  assert.ok(body, 'init 必须存在于 local-db.ts')
  const code = stripNoise(body)

  const callAt = code.search(/setEncryptionSecret\s*\(/)
  assert.ok(callAt >= 0, 'init 里应调用 setEncryptionSecret(...)')

  // 新契约（2026-10-06 修复后）：secret 已存与否由 isSecretStored 判定，
  // 一致性由 checkEncryptionSecret **真比对**，不一致就 throw 拒绝开库。
  // 旧契约（不安全）：try/catch 吞掉 "a passphrase has already been set" 后放行。
  //
  // ⚠️ 口径：init 里调的是**包装 helper**（isSecretStoredSafely /
  // checkEncryptionSecretSafely），不是插件原名。两种写法都要认，
  // 否则判据会因为「实现换了层转发」而红——那是判据的错，不是产品的错。
  const usesStoredProbe = /isSecretStored(?:Safely)?\s*\(/.test(code)
  assert.ok(
    usesStoredProbe,
    'init 里应先用 isSecretStored()/isSecretStoredSafely() 判定 secret 是否已存，' +
      '而不是靠 setEncryptionSecret 抛不抛来推断——后者无法区分「已存（正常）」' +
      '与「用户输错口令」，这正是任意错误口令都能开库的成因。',
  )
  assert.ok(
    /checkEncryptionSecret(?:Safely)?\s*\(/.test(code),
    'init 里应对已存的 secret 做 checkEncryptionSecret() 真比对。' +
      '没有比对就没有「口令是否正确」这个判断。',
  )

  // 口令不一致必须导致 init 走 reject（否则「校验」只是日志，照样放行）
  const cmpAt = code.search(/checkEncryptionSecret(?:Safely)?\s*\(/)
  const afterCmp = code.slice(cmpAt, cmpAt + 500)
  assert.ok(
    /throw\s/.test(afterCmp),
    'checkEncryptionSecret 返回 false 时必须 throw 拒绝开库（fail-closed）。' +
      '若只是 warn 后继续，插件 Database.java:245 仍会用已存的旧 secret 打开数据库。',
  )

  // 探针失败必须 fail-closed：isSecretStored / checkEncryptionSecret 抛错时
  // 不得被当成「已校验通过」。
  const stored = LOCALDB.indexOf('isSecretStoredSafely')
  const check = LOCALDB.indexOf('checkEncryptionSecretSafely')
  assert.ok(stored > 0 && check > 0, '两个探针 helper 应存在')
  for (const [name, at] of [['isSecretStoredSafely', stored], ['checkEncryptionSecretSafely', check]]) {
    const seg = LOCALDB.slice(at, at + 700)
    const retFalse = /catch[\s\S]{0,400}?return false/.test(seg)
    assert.ok(retFalse, `${name} 的 catch 必须 return false（fail-closed），不得放行`)
  }
})

test('LoginView.unlock 必须把 initLobster 的失败暴露出来（不得吞掉校验拒绝）', () => {
  const body = fnBody(LOGIN, 'unlock')
  assert.ok(body, 'unlock 必须存在于 LoginView.vue')
  const code = stripNoise(body)

  const initAt = code.search(/initLobster\s*\(/)
  assert.ok(initAt >= 0, 'unlock 里应调用 initLobster(...)')

  // 口径澄清（重要）：**解锁校验的唯一权威点是 initLobster**（它调
  // localDB.verifyMasterPassword）。LoginView 不应该、也不需要自己再校验一遍——
  // 那是重复真理源。但它**必须**把 initLobster 的 reject 冒泡/呈现出来：
  // 若这里把失败 catch 掉却仍然 needUnlock=false + 跳转，那校验就形同虚设。
  //
  // 所以本条钉的是「失败不得被吞」：initLobster 之后的放行动作
  // （needUnlock=false / router.replace）必须**只**在 initLobster 成功时可达。
  const afterInit = code.slice(initAt)
  const hasCatch = /catch\s*\(/.test(afterInit)
  assert.ok(
    hasCatch,
    'unlock 必须有 catch 处理 initLobster 的 reject（口令不符会抛），否则是未处理拒绝。',
  )

  // 放行动作必须位于 initLobster 之后、且不在 catch 的失败分支里
  // （用括号配平取 catch 块，确认 needUnlock=false / router.replace 不在 catch 体内）。
  const catchAt = code.indexOf('catch', initAt)
  const openBrace = code.indexOf('{', catchAt)
  let depth = 0
  let catchEnd = -1
  for (let i = openBrace; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) { catchEnd = i; break }
    }
  }
  assert.ok(catchEnd > 0, 'catch 块括号未能配平')

  const catchBody = code.slice(openBrace, catchEnd)
  assert.ok(
    !/needUnlock\.value\s*=\s*false/.test(catchBody),
    'catch（失败分支）里不得把 needUnlock 置 false —— 那会在口令错误时仍放行。',
  )
  assert.ok(
    !/router\.(replace|push)/.test(catchBody),
    'catch（失败分支）里不得跳转 —— 口令错误时必须停在解锁页。',
  )
})
