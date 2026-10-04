// 门禁：backend 读的每个 POCKET_* 配置项，都必须在 .env.example 里有条目。
//
// 为什么需要它：`.env.example` 是部署时唯一的配置清单，但它长期落后于代码 ——
// 2026-10-04 实测，`backend/internal/config` 读了 128 个 POCKET_* 变量，
// 而 .env.example 只声明了 45 个，**84 个（66%）没有出处**，
// 其中包括 POCKET_ALLOWED_ORIGINS / POCKET_REDCLAW_*_SECRET /
// POCKET_COMPANION_SECRET / POCKET_SMTP_PASSWORD / POCKET_WEBAUTHN_RP_* /
// POCKET_PG_SCHEMA 这类**安全与部署相关**的项。
//
// 缺口的代价是静默的：照 .env.example 配出来的服务**能启动、/healthz 回 200**，
// 但回调凭据、CORS、WebAuthn 域、SMTP 全是空值 —— 只有真正用到那条功能时
// 才炸。本轮实测就是这样：pocketd 正常跑着，回调端点却因为一个 FEISHU/WECOM
// 变量都没配而 503。
//
// 方向性（重要）：
//   - 「代码里有、example 里没有」→ **判红**。这是部署踩坑的来源。
//   - 「example 里有、config 不读」→ 只提示不判红。那可能是给脚本/前端用的，
//     判红会逼出假阳性，而假阳性门禁一周内就会被 `--list | head` 忽略掉。
//
// 口径：名字取自 backend/internal/config/*.go 里出现的全部 "POCKET_*" 字面量
// （非 _test.go）。这是**超集**，宁可多要求也不放过 —— 漏掉的代价是部署踩坑，
// 多要求的代价只是 .env.example 多几行注释。
//
// 运行：node scripts/check-env-example.mjs [--selftest] [--list]
import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = fileURLToPath(import.meta.url)
const ROOT = resolve(dirname(SELF), '..')
const CONFIG_DIR = join(ROOT, 'backend', 'internal', 'config')
const EXAMPLE = join(ROOT, '.env.example')

const NAME_RE = /"(POCKET_[A-Z0-9_]+)"/g

/** backend config 读到的所有 POCKET_* 名字（含 getFirstEnv 的别名与兜底名）。 */
export function configNames(configDir = CONFIG_DIR) {
  const names = new Set()
  if (!existsSync(configDir)) return names
  for (const f of readdirSync(configDir)) {
    if (!f.endsWith('.go') || f.endsWith('_test.go')) continue
    for (const m of readFileSync(join(configDir, f), 'utf8').matchAll(NAME_RE)) names.add(m[1])
  }
  return names
}

/** .env.example 里声明的 POCKET_* 名字。 */
export function exampleNames(examplePath = EXAMPLE) {
  const names = new Set()
  if (!existsSync(examplePath)) return names
  for (const m of readFileSync(examplePath, 'utf8').matchAll(/^(?:export\s+)?(POCKET_[A-Z0-9_]+)\s*=/gm)) names.add(m[1])
  return names
}

/**
 * 核心判据。纯函数，方便自检直接喂合成输入。
 * 返回 missing（该红的）与 extra（只提示的）。
 */
export function diff(cfg, ex) {
  return {
    missing: [...cfg].filter((n) => !ex.has(n)).sort(),
    extra: [...ex].filter((n) => !cfg.has(n)).sort(),
  }
}

function selftest() {
  const results = []
  const add = (name, pass) => results.push({ name, pass })

  // 敏感度：代码新增一个没进 example 的变量 → 必须报出来
  {
    const d = diff(new Set(['POCKET_A', 'POCKET_NEW_ONE']), new Set(['POCKET_A']))
    add('敏感度·新增未记录变量被报出', d.missing.length === 1 && d.missing[0] === 'POCKET_NEW_ONE')
  }
  // 敏感度：缺的不止一个时要全报（只报第一个会让人以为只有一个）
  {
    const d = diff(new Set(['POCKET_A', 'POCKET_B', 'POCKET_C']), new Set(['POCKET_A']))
    add('敏感度·多个缺失全部报出', d.missing.length === 2 && d.missing.join(',') === 'POCKET_B,POCKET_C')
  }
  // 特异度：全部记录 ⇒ 不报
  {
    const d = diff(new Set(['POCKET_A', 'POCKET_B']), new Set(['POCKET_A', 'POCKET_B']))
    add('特异度·全部已记录不误报', d.missing.length === 0)
  }
  // 特异度：反向差集只进 extra，不进 missing（不判红）
  {
    const d = diff(new Set(['POCKET_A']), new Set(['POCKET_A', 'POCKET_SCRIPT_ONLY']))
    add('特异度·example 多出来的只进 extra', d.missing.length === 0 && d.extra.join(',') === 'POCKET_SCRIPT_ONLY')
  }
  // 变盲：空输入不能被判成有问题（否则空文件会误报一整片）
  {
    const d = diff(new Set(), new Set())
    add('变盲·双空不误报', d.missing.length === 0 && d.extra.length === 0)
  }
  // 变盲：code 空、example 非空 ⇒ 只 extra，不判红
  {
    const d = diff(new Set(), new Set(['POCKET_A', 'POCKET_B']))
    add('变盲·仅 example 有内容不判红', d.missing.length === 0 && d.extra.length === 2)
  }
  // 口径：getFirstEnv 的别名也算「代码要」（部署只配别名之一就够，但都得有出处）
  {
    const d = diff(new Set(['POCKET_PRIMARY', 'POCKET_LEGACY_ALT']), new Set(['POCKET_PRIMARY']))
    add('口径·别名未记录也被报出', d.missing.includes('POCKET_LEGACY_ALT'))
  }

  const bad = results.filter((r) => !r.pass)
  for (const r of results) console.log(`  ${r.pass ? '通过' : '失败'}  ${r.name}`)
  console.log(`\n自检: ${results.length - bad.length}/${results.length} 通过`)
  process.exit(bad.length === 0 ? 0 : 1)
}

if (process.argv.includes('--selftest')) {
  console.log('[check-env-example] 自检：验证本检查仍能报错')
  selftest()
  process.exit(0)
}

const cfg = configNames()
const ex = exampleNames()
if (cfg.size === 0) {
  console.error('FAIL 没从 backend/internal/config 读到任何 POCKET_* —— 判据失明，拒绝给结论')
  process.exit(1)
}
if (ex.size === 0) {
  console.error('FAIL 没从 .env.example 读到任何 POCKET_* —— 判据失明，拒绝给结论')
  process.exit(1)
}

const { missing, extra } = diff(cfg, ex)
console.log(`backend/internal/config 读到 ${cfg.size} 个 POCKET_*，.env.example 声明 ${ex.size} 个`)

if (extra.length) {
  console.log(`\nnote ${extra.length} 个在 .env.example 但 config 不读（只提示，不判红 —— 可能是给脚本/前端用的）:`)
  extra.forEach((n) => console.log(`  - ${n}`))
}

if (process.argv.includes('--list')) {
  if (missing.length) {
    console.log(`\n未记录的 ${missing.length} 个:`)
    missing.forEach((n) => console.log(`  ${n}=`))
  } else {
    console.log('\n全部已记录')
  }
  process.exit(0)
}

if (missing.length) {
  console.error(`\n✗ ${missing.length} 个 POCKET_* 被 backend 读取却没写进 .env.example:`)
  missing.forEach((n) => console.error(`  - ${n}`))
  console.error('\n照 .env.example 配出来的服务能启动、/healthz 回 200，但这些项全是空值，')
  console.error('只有真正用到对应功能时才炸。加进 .env.example（值可用代码里的默认值）。')
  process.exit(1)
}

console.log('\n✓ backend 读取的每个 POCKET_* 都在 .env.example 里有出处')
