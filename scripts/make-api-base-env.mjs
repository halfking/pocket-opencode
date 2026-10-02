// make-api-base-env.mjs —— 把 API 探针里硬编码的 127.0.0.1:8088 改成走环境变量。
//
// ## 为什么
//
// §4.88 把 32 个脚本的口令来源修好了，但它们多数还**硬编码 8088**——
// 那个端口现在根本没有服务在跑。所以「语法通过」并不等于「能跑」：
// 逐个实跑会全部撞在 ECONNREFUSED 上，看起来像「脚本坏了」，
// 其实是**地址写死**。
//
// 约定与仓库里已有的 env 版一致（probe-instances-api.mjs / probe-finance-api.mjs）：
//   const HOST = process.env.POCKET_API_HOST || '127.0.0.1';
//   const PORT = Number(process.env.POCKET_API_PORT || 8088);
//
// ⚠️ 刻意**只动 API 族**（`POCKET_CDP_PORT` 那批是 CDP 端口，属于另一族，
//    要迁的是 lib/adb-cdp.mjs，混在一起会把「CDP 端口」也改成 API 端口——
//    那是把好实现换成瞎实现）。判据用**字面量 8088**来区分两族，因为
//    CDP 那批写的是 92xx。
//
// 安全措施同 migrate-dev-pass.mjs：--dry 优先、逐文件 node --check + 失败即回退、
// UTF-8 往返自证、只认两种形态、匹配不上就跳过并报告。
import { readFileSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..')
const DRY = process.argv.includes('--dry')

// 形态 1：const HOST = '127.0.0.1'  +  const PORT = 8088（有无分号都行）
const P1_HOST = /const\s+HOST\s*=\s*'127\.0\.0\.1'\s*;?/
const P1_PORT = /const\s+PORT\s*=\s*8088\s*;?/
// 形态 2：内联 port:8088（http.request({ port: 8088, ... })）
const P2 = /(\bport\s*:\s*)8088\b/g

const HOST_NEW = "const HOST = process.env.POCKET_API_HOST || '127.0.0.1';"
const PORT_NEW = 'const PORT = Number(process.env.POCKET_API_PORT || 8088);'
const PORT_INLINE = '$1Number(process.env.POCKET_API_PORT || 8088)'

const files = execFileSync('git', ['ls-files', 'scripts'], { encoding: 'utf8' })
  .split(/\r?\n/).filter((f) => f.endsWith('.mjs') && f.startsWith('scripts/') && !f.includes('/lib/'))

const done = [], skipped = [], failed = []
for (const rel of files) {
  const p = join(ROOT, rel)
  const buf = readFileSync(p)
  const src = buf.toString('utf8')
  if (!Buffer.from(src, 'utf8').equals(buf)) { failed.push([rel, 'UTF-8 往返不一致']); continue }

  let next = src
  let hits = 0
  if (P1_PORT.test(next) && P1_HOST.test(next)) {
    next = next.replace(P1_PORT, PORT_NEW).replace(P1_HOST, HOST_NEW)
    hits += 2
  }
  P2.lastIndex = 0
  const m = [...next.matchAll(P2)]
  if (m.length) { next = next.replace(P2, PORT_INLINE); hits += m.length }

  if (hits === 0) { skipped.push(rel); continue }
  if (next === src) { skipped.push(rel); continue }

  if (DRY) { done.push(rel); continue }
  writeFileSync(p, next, 'utf8')
  try {
    execFileSync(process.execPath, ['--check', p], { stdio: 'pipe' })
    done.push(rel)
  } catch (e) {
    writeFileSync(p, buf)
    failed.push([rel, `node --check 失败已回退：${String(e.stderr || e).split('\n')[0].slice(0, 80)}`])
  }
}

console.log(`API base 改 env：成功 ${done.length}、未匹配 ${skipped.length}、失败回退 ${failed.length}${DRY ? '（--dry）' : ''}`)
if (done.length) { console.log('\n已改：'); done.forEach((f) => console.log(`  ${f}`)) }
if (failed.length) { console.log('\n失败：'); failed.forEach(([f, w]) => console.log(`  ${f}  ${w}`)) }
process.exit(failed.length ? 1 : 0)
