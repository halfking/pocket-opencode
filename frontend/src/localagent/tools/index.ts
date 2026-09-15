/**
 * localagent/tools/index.ts — 内置工具注册表。
 *
 * MVP 工具面(全部 WebView 可达,零后端改动):
 *   current_time / calculate / device_info / http_fetch(GET) /
 *   read_file / write_file / list_files(沙箱 FS) / task_plan / load_skill。
 *
 * 风险分级:low 自动放行;write_file / http_fetch 为 medium 需审批
 * (openhands confirmation 分级风格)。
 */

import type { AgentTool, PlanItem } from '../types.ts'
import type { SkillRegistry } from '../skills.ts'
import { evaluateExpression } from './calc.ts'
import { getFsBackend, normalizePath } from './fs.ts'

// ---------------------------------------------------------------------------
// current_time
// ---------------------------------------------------------------------------

const current_time: AgentTool = {
  name: 'current_time',
  label: '当前时间',
  description: '获取设备当前日期时间与星期。任何涉及「今天/现在/最近」的问题先调它。',
  parameters: { type: 'object', properties: {} },
  risk: 'low',
  async execute() {
    const d = new Date()
    const pad = (n: number) => String(n).padStart(2, '0')
    const week = ['日', '一', '二', '三', '四', '五', '六'][d.getDay()]
    return {
      ok: true,
      result: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())} 星期${week}(本地时区)`,
    }
  },
}

// ---------------------------------------------------------------------------
// calculate
// ---------------------------------------------------------------------------

const calculate: AgentTool = {
  name: 'calculate',
  label: '计算器',
  description: '精确计算算术表达式。所有数值计算必须用它,禁止心算。支持 + - * / % ^ 括号与 sqrt/min/max/round/floor/ceil/abs 函数。',
  promptSnippet: '乘号写 *,除号写 /;百分数先化成小数。',
  parameters: {
    type: 'object',
    properties: {
      expression: { type: 'string', description: '算术表达式,如 (1200*1.13)/3' },
    },
    required: ['expression'],
  },
  risk: 'low',
  async execute(args) {
    const expr = String(args['expression'] ?? args['value'] ?? '')
    if (!expr.trim()) return { ok: false, error: '缺少 expression 参数' }
    try {
      const val = evaluateExpression(expr)
      const pretty = Number.isInteger(val) ? String(val) : String(parseFloat(val.toFixed(10)))
      return { ok: true, result: `${expr} = ${pretty}` }
    } catch (err) {
      return { ok: false, error: `表达式无效:${err instanceof Error ? err.message : String(err)}` }
    }
  },
}

// ---------------------------------------------------------------------------
// device_info
// ---------------------------------------------------------------------------

const device_info: AgentTool = {
  name: 'device_info',
  label: '设备信息',
  description: '获取设备基础信息:平台、系统版本、屏幕、语言、网络状态。',
  parameters: { type: 'object', properties: {} },
  risk: 'low',
  async execute() {
    const nav = globalThis.navigator as
      | (Navigator & { connection?: { effectiveType?: string } })
      | undefined
    const conn = nav?.connection
    const lines = [
      `平台:${nav?.userAgent ?? '未知'}`,
      `语言:${nav?.language ?? '未知'}`,
      `屏幕:${globalThis.screen ? `${globalThis.screen.width}x${globalThis.screen.height}` : '未知'}`,
      `网络:${conn?.effectiveType ?? '未知'}`,
      `时区偏移(分钟):-${new Date().getTimezoneOffset()}`,
    ]
    return { ok: true, result: lines.join('\n') }
  },
}

// ---------------------------------------------------------------------------
// http_fetch(GET only)
// ---------------------------------------------------------------------------

const http_fetch: AgentTool = {
  name: 'http_fetch',
  label: '网页获取',
  description: '发起 GET 请求获取网页/JSON 内容(限 100KB)。适合查公开 API、读取文章。需要登录或 POST 的接口不支持。',
  promptSnippet: 'App 内部接口用相对路径(如 /api/llm/models);外域接口可能因 CORS 失败,失败时换数据源。',
  parameters: {
    type: 'object',
    properties: {
      url: { type: 'string', description: '目标 URL,http(s) 或 App 内相对路径' },
    },
    required: ['url'],
  },
  risk: 'medium',
  async execute(args, ctx) {
    const raw = String(args['url'] ?? args['value'] ?? '').trim()
    if (!raw) return { ok: false, error: '缺少 url 参数' }
    let url = raw
    if (!/^https?:\/\//.test(raw)) {
      if (raw.startsWith('//') || /\s/.test(raw)) return { ok: false, error: 'URL 不合法' }
      url = new URL(raw, globalThis.location?.href ?? 'http://localhost/').toString()
    }
    const ctrl = new AbortController()
    const onStop = () => ctrl.abort()
    ctx.signal.addEventListener('abort', onStop, { once: true })
    const timer = setTimeout(() => ctrl.abort(), 15_000)
    try {
      const res = await fetch(url, { signal: ctrl.signal, headers: { Accept: 'application/json, text/*;q=0.8' } })
      if (!res.ok) return { ok: false, error: `HTTP ${res.status} ${res.statusText}` }
      const text = (await res.text()).slice(0, 100 * 1024)
      const ct = res.headers.get('content-type') ?? ''
      const body = ct.includes('html') ? text.replace(/<script[\s\S]*?<\/script>/gi, '').replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ') : text
      const clipped = body.length > 4000 ? `${body.slice(0, 4000)}\n…(截断,共 ${body.length} 字符)` : body
      return { ok: true, result: `[${res.status}] ${url}\n${clipped}` }
    } catch (err) {
      if (ctx.signal.aborted) return { ok: false, error: '已取消' }
      return { ok: false, error: `请求失败:${err instanceof Error ? err.message : String(err)}(外域接口常因 CORS 失败,可换源)` }
    } finally {
      clearTimeout(timer)
      ctx.signal.removeEventListener('abort', onStop)
    }
  },
}

// ---------------------------------------------------------------------------
// 文件工具(沙箱 FS)
// ---------------------------------------------------------------------------

const read_file: AgentTool = {
  name: 'read_file',
  label: '读文件',
  description: '读取智能体沙箱内的文本文件(如之前保存的笔记、清单)。无法读取手机系统文件。',
  parameters: {
    type: 'object',
    properties: { path: { type: 'string', description: '沙箱内相对路径,如 notes/todo.md' } },
    required: ['path'],
  },
  risk: 'low',
  async execute(args) {
    try {
      const p = normalizePath(String(args['path'] ?? ''))
      const fs = await getFsBackend()
      const content = await fs.read(p)
      const clipped = content.length > 8000 ? `${content.slice(0, 8000)}\n…(截断)` : content
      return { ok: true, result: clipped || '(空文件)' }
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) }
    }
  },
}

const write_file: AgentTool = {
  name: 'write_file',
  label: '写文件',
  description: '把文本保存到智能体沙箱(如整理结果、清单),可被 read_file 再次读取。',
  parameters: {
    type: 'object',
    properties: {
      path: { type: 'string', description: '沙箱内相对路径,如 notes/todo.md' },
      content: { type: 'string', description: '要写入的完整文本(覆盖写)' },
    },
    required: ['path', 'content'],
  },
  risk: 'medium',
  async execute(args) {
    try {
      const p = normalizePath(String(args['path'] ?? ''))
      const content = String(args['content'] ?? '')
      if (!p) return { ok: false, error: '缺少 path' }
      const fs = await getFsBackend()
      await fs.write(p, content)
      return { ok: true, result: `已写入 ${p}(${content.length} 字符)` }
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) }
    }
  },
}

const list_files: AgentTool = {
  name: 'list_files',
  label: '列文件',
  description: '列出智能体沙箱内某目录下的文件与子目录。',
  parameters: {
    type: 'object',
    properties: { path: { type: 'string', description: '目录相对路径,根目录传空字符串' } },
    required: [],
  },
  risk: 'low',
  async execute(args) {
    try {
      const p = normalizePath(String(args['path'] ?? ''))
      const fs = await getFsBackend()
      const names = await fs.list(p)
      return { ok: true, result: names.length ? names.join('\n') : '(空目录)' }
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) }
    }
  },
}

// ---------------------------------------------------------------------------
// task_plan(openhands TaskTracker 语义:计划卡 UI 数据源)
// ---------------------------------------------------------------------------

let planState: PlanItem[] = []

export function resetPlanState(): void {
  planState = []
}

export function currentPlan(): PlanItem[] {
  return planState
}

const task_plan: AgentTool = {
  name: 'task_plan',
  label: '任务计划',
  description: '建立或更新本次任务的计划清单(展示为计划卡)。多步任务开始时用 set 建条目;每完成一步用 update 改状态。',
  promptSnippet: 'status 取 todo/in_progress/done;复杂任务务必先用它拆解,再逐步推进。',
  parameters: {
    type: 'object',
    properties: {
      action: { type: 'string', enum: ['set', 'update'], description: 'set=整表重建;update=按 index 更新单条状态' },
      items: { type: 'string', description: 'action=set 时:JSON 数组字符串,如 [{"title":"查天气","status":"todo"}](可选 notes)' },
      index: { type: 'string', description: 'action=update 时:条目序号(从 1 开始)' },
      status: { type: 'string', enum: ['todo', 'in_progress', 'done'], description: 'action=update 时:新状态' },
    },
    required: ['action'],
  },
  risk: 'low',
  async execute(args, ctx) {
    const action = String(args['action'] ?? '')
    if (action === 'set') {
      let items: PlanItem[] = []
      const raw = args['items']
      try {
        const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw
        if (Array.isArray(parsed)) {
          items = parsed
            .map((it) => {
              const o = it as Record<string, unknown>
              const status: PlanItem['status'] =
                o['status'] === 'done' || o['status'] === 'in_progress' ? o['status'] : 'todo'
              return { title: String(o['title'] ?? '(未命名)'), notes: o['notes'] ? String(o['notes']) : undefined, status }
            })
            .slice(0, 20)
        }
      } catch {
        return { ok: false, error: 'items 不是合法的 JSON 数组' }
      }
      if (items.length === 0) return { ok: false, error: 'items 为空' }
      planState = items
      ctx.emit?.({ type: 'plan', items: planState })
      return { ok: true, result: `已建立 ${items.length} 项计划`, data: planState }
    }
    if (action === 'update') {
      const idx = parseInt(String(args['index'] ?? ''), 10) - 1
      const status = String(args['status'] ?? '')
      if (!Number.isInteger(idx) || idx < 0 || idx >= planState.length) return { ok: false, error: `index 越界(当前 ${planState.length} 项)` }
      if (status !== 'todo' && status !== 'in_progress' && status !== 'done') return { ok: false, error: 'status 必须是 todo/in_progress/done' }
      planState[idx] = { ...planState[idx], status }
      ctx.emit?.({ type: 'plan', items: planState })
      return { ok: true, result: `第 ${idx + 1} 项 → ${status}`, data: planState }
    }
    return { ok: false, error: 'action 必须是 set 或 update' }
  },
}

// ---------------------------------------------------------------------------
// load_skill
// ---------------------------------------------------------------------------

export function createLoadSkillTool(registry: SkillRegistry): AgentTool {
  return {
    name: 'load_skill',
    label: '加载技能',
    description: '获取某个技能的完整操作指引(参数填技能名)。任务匹配技能清单里的技能时先调它。',
    parameters: {
      type: 'object',
      properties: { name: { type: 'string', description: '技能名(见系统提示中的技能清单)' } },
      required: ['name'],
    },
    risk: 'low',
    async execute(args) {
      const name = String(args['name'] ?? args['value'] ?? '').trim()
      const skill = registry.get(name)
      if (!skill) {
        return { ok: false, error: `技能不存在:${name}。可用:${registry.list().map((s) => s.name).join(', ')}` }
      }
      return { ok: true, result: skill.body }
    },
  }
}

// ---------------------------------------------------------------------------
// 注册表
// ---------------------------------------------------------------------------

export interface BuiltinToolsOptions {
  skills: SkillRegistry
}

export function createBuiltinTools(opts: BuiltinToolsOptions): AgentTool[] {
  return [current_time, calculate, device_info, http_fetch, read_file, write_file, list_files, task_plan, createLoadSkillTool(opts.skills)]
}
