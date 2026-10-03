// verify-audit-detects.mjs — 注入实验：证明 audit-vue-mustache.mjs 真的能抓到
// BUG-T 那种漏了 {{ }} 的裸表达式，而不是因为"扫不到"才报 OK。
//
// 这是审计脚本的**元验证**。一个永远返回 OK 的检查和一个真正有效的检查，
// 在报告上长得一模一样。必须证明它有区分能力。
//
// 做法：复制一个 .vue 副本到临时位置，在副本里注入与 BUG-T 同形态的缺陷，
// 对副本跑审计，确认被抓到；再对**未改动的原文件**跑一次，确认不报。
// 全程不碰真实源码。
import { readFileSync, writeFileSync, mkdtempSync, mkdirSync, rmSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { tmpdir } from 'node:os'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const TMP = mkdtempSync(join(tmpdir(), 'mustache-verify-'))
const SRC = join(TMP, 'src')

// 取一个真实存在的 .vue 作为基底
const base = join(ROOT, 'frontend/src/features/flashcards/FlashcardListView.vue')
const orig = readFileSync(base, 'utf8')

function runAudit(dir) {
  try {
    const out = execFileSync('node', [join(ROOT, 'scripts/audit-vue-mustache.mjs')], {
      encoding: 'utf8',
      // 让审计脚本扫临时目录：用 env 覆盖扫描根
      env: { ...process.env, MUSTACHE_AUDIT_SRC: dir },
    })
    return { out, code: 0 }
  } catch (e) {
    return { out: (e.stdout || '') + (e.stderr || ''), code: e.status ?? 1 }
  }
}

mkdirSync(join(SRC, 'features'), { recursive: true })
const target = join(SRC, 'features/FlashcardListView.vue')

// 1) 基线：未改动的原文件 -> 应当不报
writeFileSync(target, orig)
const baseline = runAudit(SRC)
const baselineClean = baseline.out.includes('没有发现疑似漏')
console.log(`[基线] 未改动的原文件: ${baselineClean ? 'OK（不报）✓' : '竟然报了 ✗'}`)

// 2) 注入 BUG-T 同形态缺陷 -> 应当被抓到
const injected = orig.replace(
  "<h1>{{ t('flashcards.list.title') }}</h1>",
  "<h1>t('flashcards.list.title')</h1>",
)
if (injected === orig) {
  console.error('注入失败：没找到预期的 <h1> 片段，无法验证')
  rmSync(TMP, { recursive: true, force: true })
  process.exit(3)
}
writeFileSync(target, injected)
const broken = runAudit(SRC)
const caught = broken.out.includes('FlashcardListView.vue') && broken.code !== 0
console.log(`[注入] 漏 {{ }} 的裸表达式: ${caught ? '被抓到 ✓（脚本有区分能力）' : '漏掉了 ✗ 脚本无效'}`)

rmSync(TMP, { recursive: true, force: true })

if (baselineClean && caught) {
  console.log('\n结论：审计脚本有效 —— 能抓到 BUG-T 同形态缺陷，且不对正常代码误报。')
  process.exit(0)
}
console.log('\n结论：**审计脚本不可信**，别用它下结论。')
process.exit(1)
