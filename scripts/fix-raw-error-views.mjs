#!/usr/bin/env node
/**
 * 把「直接把后端错误写进 UI」的视图改造成走统一错误文案层。
 *
 * 缺陷背景（真机 RSS 页实测）：
 *   屏幕上出现 `rss_unavailable: store not configured` —— 后端英文技术
 *   标识直接渲染给用户，既没过 i18n，也没告诉用户该做什么。
 *   全库这类写法有 82 处；其中 18 处还是
 *     `xxx.value = e?.message || '中文兜底'`
 *   的形态 —— 兜底文案硬编码中文，非中文用户直接看到中文。
 *
 * 本脚本针对后者做机械化改造：
 *   1. 补 `import { useApiError } from '<相对路径>/useApiError'`
 *   2. 在组件 setup 里注入 `const apiError = useApiError()`
 *   3. 把 `= e?.message || '中文兜底'` 换成 `= apiError(e, 'errors.<key>')`
 *
 * 兜底文案从中文原文反查 i18n key，查不到则跳过该处并报告，
 * 交人工确认，绝不猜。
 *
 * 安全性：改造后必须跑 `npm run typecheck` —— 未导入 / 未定义 / 路径写错
 * 都会被 vue-tsc 拦下。
 *
 * Run: node scripts/fix-raw-error-views.mjs [--apply]
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const SRC = path.join(ROOT, 'frontend', 'src')
const APPLY = process.argv.includes('--apply')

/** 兜底中文原文 → errors.* 的 i18n key。只收录已确认有翻译的。 */
const FALLBACK_KEYS = {
  '加载失败': 'errors.loadGatewayFailed',
  '探测失败': 'errors.probeFailed',
  '加载笔记失败，请稍后重试。': 'errors.loadNotesFailed',
  '搜索笔记失败，请稍后重试。': 'errors.operateFailed',
  '加载邮件失败，请稍后重试。': 'errors.loadEmailFailed',
  '加载邮箱设置失败': 'errors.loadEmailSettingsFailed',
  '加载设置失败': 'errors.loadSettingsFailed',
  '保存失败': 'errors.saveFailed',
  '保存失败，请稍后重试': 'errors.saveFailed',
  '创建任务失败': 'errors.createTaskFailed',
  '发送失败': 'errors.sendEmailFailed',
  '执行失败': 'errors.operateFailed',
  '加载自动化失败': 'errors.loadSettingsFailed',
  '加载自动化详情失败': 'errors.loadSettingsFailed',
  '注册失败': 'errors.operateFailed',
  '验证码错误或已过期': 'errors.operateFailed',
  'save failed': 'errors.saveFailed',
}

/**
 * 明确要处理的视图。`anchor` 指定 setup 初始化锚点的正则：
 * 默认找 `const route/router = useRoute/useRouter()`，
 * FinanceView 这类没有路由依赖的页面改用它自己的 `const toast = useToast()`。
 *
 * 注意：Pinia store（features/scheduled-tasks/store.ts、stores/flashcards.ts）
 * 不在处理范围内 —— 它们没有组件上下文，拿不到 useI18n，
 * 需要先引入全局 i18n 访问器，属于架构决策，应人工确认后再做。
 */
const TARGETS = [
  { rel: 'features/gateway/GatewayCredentialDetailView.vue' },
  { rel: 'features/gateway/GatewayCredentialsView.vue' },
  { rel: 'features/gateway/GatewayModelsView.vue' },
  { rel: 'features/gateway/GatewayOverviewView.vue' },
  { rel: 'features/gateway/GatewayProvidersView.vue' },
  { rel: 'features/pkm/PkmTodayView.vue' },
  { rel: 'features/flashcards/FlashcardEditView.vue' },
  { rel: 'features/scheduled-tasks/ScheduledTaskEditView.vue' },
  { rel: 'features/scheduled-tasks/ScheduledTaskDetailView.vue' },
  { rel: 'features/scheduled-tasks/ScheduledTaskEditView.vue', keys: { 加载失败: 'errors.loadTasksFailed' } },
  { rel: 'features/finance/FinanceView.vue', keys: { 加载失败: 'errors.loadFinanceFailed' }, anchor: /^const toast = useToast\(\)\r?\n/m },
]

const RAW_RE = /=\s*e\?\.(?:body\?\.)?(?:error|message)\s*\|\|\s*'([^']+)'/g

const skipped = []
let touched = 0

for (const target of TARGETS) {
  const rel = typeof target === 'string' ? target : target.rel
  const anchorRe =
    (typeof target === 'object' && target.anchor) ||
    /^const (?:route|router) = use(?:Route|Router)\(\)\r?\n/m
  const file = path.join(SRC, rel)
  if (!fs.existsSync(file)) {
    skipped.push(`${rel}（文件不存在）`)
    continue
  }
  let text = fs.readFileSync(file, 'utf8')

  // 1) 收集要替换的位置，先确认每个兜底文案都能映射到 key
  const hits = []
  // 同一句中文兜底在不同页面语义不同（'加载失败' 在网关页是网关、
  // 在定时任务页是任务、在财务页是账单），允许按文件覆盖映射。
  const perFile = (typeof target === 'object' && target.keys) || {}
  for (const m of text.matchAll(RAW_RE)) {
    const literal = m[1]
    const key = perFile[literal] ?? FALLBACK_KEYS[literal]
    if (!key) {
      skipped.push(`${rel}：兜底文案「${literal}」未登记 i18n key，需人工确认`)
      continue
    }
    hits.push({
      start: m.index,
      end: m.index + m[0].length,
      replacement: `= apiError(e, '${key}')`,
    })
  }
  if (!hits.length) continue

  // 从后往前替换，避免偏移失效
  for (let i = hits.length - 1; i >= 0; i--) {
    const h = hits[i]
    text = text.slice(0, h.start) + h.replacement + text.slice(h.end)
  }

  // 2) 注入 import（放在最后一条 import 之后）
  if (!text.includes('useApiError')) {
    const imports = [...text.matchAll(/^import [^\n]*\n/gm)]
    if (!imports.length) {
      skipped.push(`${rel}：找不到 import 锚点`)
      continue
    }
    const last = imports[imports.length - 1]
    const depth = path
      .relative(path.join(SRC, path.dirname(rel)), path.join(SRC, 'composables'))
      .split(path.sep)
      .join('/')
    const spec = depth.startsWith('.') ? depth : `./${depth}`
    text =
      text.slice(0, last.index) +
      `import { useApiError } from '${spec}/useApiError'\n` +
      text.slice(last.index)
  }

  // 3) 注入 setup 初始化（放在配置的锚点之前）
  //    行尾要兼容 CRLF：仓库文件在 Windows 上是 \r\n，\(\)\n 匹配不到。
  if (!/const apiError = useApiError\(\)/.test(text)) {
    const anchor = text.match(anchorRe)
    if (!anchor) {
      skipped.push(`${rel}：找不到 setup 锚点（${anchorRe}）`)
      continue
    }
    text =
      text.slice(0, anchor.index) + 'const apiError = useApiError()\n' + text.slice(anchor.index)
  }

  if (APPLY) fs.writeFileSync(file, text)
  touched++
  console.log(`${APPLY ? '已改造' : '将改造'} ${rel}  （${hits.length} 处）`)
}

console.log(`\n共 ${touched} 个文件${APPLY ? '已写入' : '（未写入，加 --apply 生效）'}。`)
if (skipped.length) {
  console.log('\n需人工确认，已跳过：')
  for (const s of skipped) console.log(`  - ${s}`)
}
