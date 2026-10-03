// verify-apk-rebuild.mjs -- check that a rebuilt APK really meets the criteria
//
// Why this file exists: a previous round reported an APK as "confirmed aligned
// with the current commit under the dirty=0 criterion" when it was actually
// 86 commits stale, with 180 files changed inside the APK input closure.
// That was swapping the ruler instead of meeting the bar. The criteria are
// hardcoded here and every one of them must be backed by measured output.
//
// Deliberately ASCII-only: PowerShell 5.1 reads BOM-less files as GBK, and a
// CJK char can decode to a trailing backslash that silently swallows the next
// line -- which surfaced as a node SyntaxError on the first version.
//
// Usage: node scripts/verify-apk-rebuild.mjs <apkPath> <commit> <worktreeRoot>
import { createHash } from 'node:crypto'
import { readFileSync, statSync } from 'node:fs'
import { execFileSync } from 'node:child_process'

const [apk, expectedCommit, worktreeRoot] = process.argv.slice(2)
if (!apk || !expectedCommit || !worktreeRoot) {
  console.error('usage: node verify-apk-rebuild.mjs <apkPath> <commit> <worktreeRoot>')
  process.exit(2)
}

const results = []
const check = (name, pass, detail) => {
  results.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}\n      ${detail}`)
}
const git = (root, ...args) =>
  execFileSync('git', ['-C', root, ...args], { encoding: 'utf8' }).trim()

// 1. artifact exists and is readable
let size = 0
let sha = ''
let mtime = ''
try {
  const st = statSync(apk)
  size = st.size
  mtime = st.mtime.toISOString()
  sha = createHash('sha256').update(readFileSync(apk)).digest('hex').toUpperCase()
  check('1 artifact exists and is readable', size > 0,
    `${size} bytes, mtime=${mtime}, sha256=${sha.slice(0, 16)}...`)
} catch (e) {
  check('1 artifact exists and is readable', false, String(e.message))
  process.exit(1)
}

// 2. the worktree is actually checked out at the stated commit
let wtHead = ''
try {
  wtHead = git(worktreeRoot, 'rev-parse', 'HEAD')
  check('2 worktree is at the stated commit', wtHead.startsWith(expectedCommit),
    `worktree HEAD=${wtHead.slice(0, 12)}, expected=${expectedCommit.slice(0, 12)}`)
} catch (e) {
  check('2 worktree is at the stated commit', false, String(e.message))
}

// 3. dirty=0 -- the literal criterion from the request, not a self-invented one
let dirty = ''
try {
  dirty = git(worktreeRoot, 'status', '--porcelain')
  check('3 worktree dirty=0', dirty === '',
    dirty === '' ? 'status --porcelain is empty' : `dirty: ${dirty.slice(0, 160)}`)
} catch (e) {
  check('3 worktree dirty=0', false, String(e.message))
}

// 4. APK input closure vs current HEAD -- zero diff means the inputs match
const CLOSURE = ['frontend/src', 'frontend/public', 'frontend/index.html',
  'frontend/vite.config.ts', 'frontend/package.json', 'frontend/capacitor.config.ts',
  'frontend/android']
let closureDiff = -1
try {
  const out = execFileSync('git',
    ['-C', worktreeRoot, 'diff', '--name-only', wtHead, 'HEAD', '--', ...CLOSURE],
    { encoding: 'utf8' })
  closureDiff = out.trim() === '' ? 0 : out.trim().split('\n').length
  check('4 closure diff vs HEAD = 0', closureDiff === 0,
    closureDiff < 0 ? 'diff failed' : `${closureDiff} file(s) differ inside the APK input closure`)
} catch (e) {
  check('4 closure diff vs HEAD = 0', false, String(e.message))
}

console.log('')
const failed = results.filter((r) => !r.pass)
console.log(`TOTAL=${results.length} PASS=${results.length - failed.length} FAIL=${failed.length}`)
if (failed.length) {
  for (const f of failed) console.log(`  FAILED: ${f.name} -> ${f.detail}`)
  process.exit(1)
}
console.log(`APK_SHA256=${sha}`)
console.log(`APK_SIZE=${size}`)
console.log(`APK_MTIME=${mtime}`)
console.log(`COMMIT=${wtHead}`)
