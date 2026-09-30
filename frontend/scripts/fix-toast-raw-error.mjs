/**
 * 把「原始错误文本直接进 toast / showToast」统一改成走 error-message 归一。
 *
 * 背景：同一个缺陷类（把后端/网络原文摆给用户）在真机上被反复发现 ——
 *   RSS 页 `rss_unavailable: store not configured`、登录页 `Failed to fetch`、
 *   AI 网关页 `✗ Failed to fetch`。每次代码写法都不同，所以逐个 review 必然漏。
 *
 * 覆盖的写法：
 *   toast.error(e?.message || 'X')
 *   toast.error('X：' + (e?.message || String(e)))
 *   toast.error(`X：${e.message || e}`)
 *   toast.error(e instanceof Error ? e.message : 'X')
 *   toast.error(e instanceof MyError ? e.message : 'X')      // 保留自定义错误分支
 *   toast.error(e instanceof MyError ? e.message : (e?.message || 'X'))
 *   toast.error(e.message)
 *   showToast(`X：${e.message || e}`, 'danger')
 *
 * 不改的：
 *   - 纯中文常量（没有 err 参与）
 *   - 自定义错误类分支本身（那是我们写给用户看的提示）
 *   - console.* 留档
 *
 * 找不到对应 errors.* key 时跳过并报告，**绝不猜**。
 *
 * 用法：
 *   node scripts/fix-toast-raw-error.mjs --file <相对 src 的路径> [--apply] [--store]
 *   --store : 目标是 Pinia store（用 i18n.global.t，而不是依赖组件实例的 useApiError）
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const args = process.argv.slice(2)
const fileArg = args.indexOf('--file')
const target = fileArg === -1 ? null : args[fileArg + 1]
const apply = args.includes('--apply')
const isStore = args.includes('--store')

if (!target) {
  console.error('用法: node scripts/fix-toast-raw-error.mjs --file <相对 src 的路径> [--apply] [--store]')
  process.exit(2)
}

/** 中文兜底文案 → 已有 errors.* key。找不到就跳过并报告，绝不猜。 */
const FALLBACK_KEY = new Map(Object.entries({
  '无法打开发票文件': 'errors.notFound',
  '加载更多失败': 'errors.loadEmailFailed',
  '整理失败': 'errors.operateFailed',
  '同步失败': 'errors.operateFailed',
  '导出失败': 'errors.operateFailed',
  '推送失败': 'errors.operateFailed',
  '下载失败': 'errors.operateFailed',
  '操作失败': 'errors.operateFailed',
  '入账失败': 'errors.operateFailed',
  '删除失败': 'errors.operateFailed',
  '保存失败': 'errors.saveFailed',
  '新增失败': 'errors.operateFailed',
  '翻译失败': 'errors.operateFailed',
  '查找联系人失败': 'errors.notFound',
  '保存过滤策略失败': 'errors.saveFailed',
  '更新失败': 'errors.operateFailed',
  'SMTP 测试失败': 'errors.operateFailed',
  '保存自动回复失败': 'errors.saveFailed',
  '转交失败': 'errors.operateFailed',
  '转交 ACC 失败': 'errors.operateFailed',
  '下达失败': 'errors.operateFailed',
  '总结失败': 'errors.operateFailed',
  '发送失败': 'errors.sendEmailFailed',
  '生成失败': 'errors.operateFailed',
  '优化失败': 'errors.operateFailed',
  '提交失败': 'errors.saveFailed',
  '审批失败': 'errors.operateFailed',
  '委托任务失败': 'errors.operateFailed',
  '加载失败': 'errors.loadSettingsFailed',
  '加载会话失败': 'errors.loadSessionsFailed',
  '加载自动化失败': 'errors.loadTasksFailed',
  '加载审批失败': 'errors.loadTasksFailed',
  '同步卡片失败': 'errors.operateFailed',
  '保存卡片失败': 'errors.saveFailed',
  '未知错误': 'errors.server',
  '操作异常': 'errors.server',
}))

const full = join(SRC, target)
const source = readFileSync(full, 'utf8')
const lines = source.split(/\r?\n/)
const changed = []
const skipped = []

/** store 里没有组件实例，用 i18n.global.t；组件/组合式里用 useApiError()。 */
const wrap = (varName, key) =>
  isStore ? `toUserMessage(${varName}, (k: string) => i18n.global.t(k) as string, i18n.global.t('${key}') as string)`
    : `apiError(${varName}, '${key}')`

let needsDecl = !/const apiError = useApiError\(\)/.test(source)

const out = lines.map((line, i) => {
  const note = (msg) => skipped.push(`${i + 1}: ${msg}`)

  // toast.error(e?.message || 'X')  /  toast.error(e?.body || 'X')
  let m = line.match(/^(\s*)(\w+)\.error\((e|err|error)\??\.(?:message|body)(?:\s*\|\|\s*'([^']*)')?\)/)
  if (m) {
    const [, indent, fn, v, fb] = m
    const key = fb ? FALLBACK_KEY.get(fb) : 'errors.server'
    if (fb && !key) { note(`兜底「${fb}」无对应 errors.* key，跳过`); return line }
    needsDecl = true
    const next = `${indent}${fn}.error(${wrap(v, key)})`
    changed.push(`${i + 1}: ${line.trim()}\n   → ${next.trim()}`)
    return next
  }

  // toast.error('X：' + (e?.message || String(e)))
  m = line.match(/^(\s*)(\w+)\.error\('([^']*)'\s*\+\s*\(?(?:e|err|error)\??\.(?:message|body)\s*\|\|\s*String\((?:e|err|error)\)\)?\)/)
  if (m) {
    const [, indent, fn, label, ] = m
    const v = line.includes('err.') || line.includes('err?.') ? 'err' : 'e'
    const key = FALLBACK_KEY.get(label.replace(/[：:]\s*$/, ''))
    if (!key) { note(`前缀「${label}」无对应 errors.* key，跳过`); return line }
    needsDecl = true
    const next = `${indent}${fn}.error(${wrap(v, key)})`
    changed.push(`${i + 1}: ${line.trim()}\n   → ${next.trim()}`)
    return next
  }

  // showToast(`X：${e.message || e}`, 'danger')
  m = line.match(/^(\s*)(\w+)\(`([^`]*?)\$\{(?:e|err|error)\.message\s*\|\|\s*(?:e|err|error)\}`(?:,\s*'[a-z]+')?\)/)
  if (m) {
    const [, indent, fn, label] = m
    const v = /err\./.test(line) ? 'err' : 'e'
    const key = FALLBACK_KEY.get(label.replace(/[：:]\s*$/, ''))
    if (!key) { note(`前缀「${label}」无对应 errors.* key，跳过`); return line }
    needsDecl = true
    const next = `${indent}${fn}(${wrap(v, key)})`
    changed.push(`${i + 1}: ${line.trim()}\n   → ${next.trim()}`)
    return next
  }

  // toast.error(e instanceof MyErr ? e.message : 'X')   （无括号兜底）
  m = line.match(/^(\s*)(\w+)\.error\((e|err|error) instanceof (\w+) \? \3\.message : '([^']*)'\)/)
  if (m) {
    const [, indent, fn, v, errClass, fb] = m
    const key = FALLBACK_KEY.get(fb)
    if (!key) { note(`兜底「${fb}」无对应 errors.* key，跳过`); return line }
    needsDecl = true
    // 保留自定义错误分支：它是我们写给用户看的提示
    const next = `${indent}${fn}.error(${v} instanceof ${errClass} ? ${v}.message : ${wrap(v, key)})`
    changed.push(`${i + 1}: ${line.trim()}\n   → ${next.trim()}`)
    return next
  }

  return line
})

console.log(`文件: ${target}${isStore ? '  [store 模式]' : ''}`)
console.log(`将修改 ${changed.length} 行：`)
for (const c of changed) console.log('  ' + c)
if (skipped.length) {
  console.log('\n跳过：')
  for (const s of skipped) console.log('  ' + s)
}
if (needsDecl && changed.length) {
  console.log(isStore
    ? '\n⚠ 该文件还没有 `const apiError = ...`，store 模式需要手工补 i18n / toUserMessage 引入。'
    : '\n⚠ 该文件还没有 `const apiError = useApiError()` 声明，需要手工补。')
}

if (apply && changed.length) {
  writeFileSync(full, out.join('\n'), 'utf8')
  console.log(`\n已写入 ${relative(process.cwd(), full)}（${changed.length} 行）`)
} else if (changed.length) {
  console.log('\n（干跑，加 --apply 生效）')
}
