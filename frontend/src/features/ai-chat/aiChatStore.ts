/**
 * aiChatStore — AI 对话（豆包式）本地状态。
 *
 * 设计要点：
 *   - 对话与消息全部存于本地（localStorage），网关本身是无状态的：每次发送都把
 *     完整历史回传给 /api/llm/stream，因此无需后端维护会话。
 *   - 支持单模型对话 + 多模型「对比模式」：同一问题并行发给多个模型，分别渲染。
 *   - 每条助手消息可「用另一模型检查/优化」：把原问题与回答交给选定模型评审并改进。
 *   - 流式输出通过 llmBffApi.streamChat 消费 SSE，按消息增量追加。
 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { llmBffApi, type ChatMessage } from '../../api/llm-bff'
import { listNodes, getAvailableModels, getFeaturedModels } from '../../api/gateway'
import { useToast } from '../../composables/useToast'
import { useChatAgentStore } from '../../stores/chatAgentStore'

export type ChatRole = 'system' | 'user' | 'assistant'

/** 网关支持的模态集合（与 llm-gateway-go 的 modality 对齐）。 */
export type ModalityKey = 'text' | 'vision' | 'audio' | 'video' | 'embedding'

export const MODALITY_KEYS: ModalityKey[] = ['text', 'vision', 'audio', 'video', 'embedding']

export const MODALITY_LABELS: Record<ModalityKey, string> = {
  text: '文本对话',
  vision: '图像理解',
  audio: '语音',
  video: '视频',
  embedding: '向量嵌入',
}

export interface ChatMsg {
  id: string
  role: ChatRole
  content: string
  /** 多模态：本条消息携带的图片（data: 内联或 https: 外链）。 */
  images?: string[]
  /** 对比模式下标记该助手消息来自哪个模型。 */
  model?: string
  /** 该条消息是否正在流式输出。 */
  streaming?: boolean
  /** 错误信息（流式失败 / 网关未配置）。 */
  error?: string
  /** 回退重试提示：后端 auto 链切换候选 model 时下发 retry 帧的展示文案。 */
  retryHint?: string
  /** 页面刷新或应用重启时流被中断。 */
  interrupted?: boolean
  createdAt: number
  usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number }
}

export type ChatMode = 'single' | 'compare'

export interface Conversation {
  id: string
  title: string
  /** 'auto' 表示交给网关智能路由（结合按模态默认模型）。 */
  model: string
  mode: ChatMode
  messages: ChatMsg[]
  agentId?: string           // 绑定的智能体角色 id（优先级高于全局 settings.systemPrompt）
  customSystemPrompt?: string // 会话级 system prompt 覆盖（最高优先级）
  archivedAt?: number
  createdAt: number
  updatedAt: number
}

export interface ChatSettings {
  temperature: number
  maxTokens: number
  systemPrompt: string       // 全局 system prompt（无角色时兜底）
  defaultModel: string
  defaultAgentId?: string    // 新建会话时默认角色
  /** 按模态的默认模型（'auto' = 网关智能路由）。仅在会话模型为 auto 时生效。 */
  modelByModality: Record<ModalityKey, string>
}

const STORAGE_KEY_PREFIX = 'pocket:ai-chat:v2'
const SETTINGS_KEY_PREFIX = 'pocket:ai-chat:settings:v2'
const LEGACY_STORAGE_KEY = 'pocket:ai-chat:v1'
const LEGACY_SETTINGS_KEY = 'pocket:ai-chat:settings:v1'
const MAX_HISTORY = 40 // 单轮发送给网关的历史消息上限
const AUTO = 'auto'

function storageScope(): string {
  const workspace = localStorage.getItem('pocket_workspace_id') || ''
  const user = localStorage.getItem('pocket_user') || ''
  return encodeURIComponent(workspace || user || 'local')
}

function storageKey(prefix: string): string {
  return `${prefix}:${storageScope()}`
}

const DEFAULT_SETTINGS: ChatSettings = {
  temperature: 0.7,
  maxTokens: 2048,
  systemPrompt: '',
  defaultModel: '',
  modelByModality: { text: AUTO, vision: AUTO, audio: AUTO, video: AUTO, embedding: AUTO },
}

/**
 * 模态启发式推断：没有网关目录（未配置 admin 节点）时，按模型 id 的
 * 命名惯例给出模态建议，只用于排序/徽标，不影响正确性。
 */
export function inferModality(id: string): ModalityKey {
  const s = id.toLowerCase()
  if (/embed|bge-|arctic-embed|nv-embed|nvclip|text-embedding/.test(s)) return 'embedding'
  if (/tts|asr|whisper|audio|speech|voice/.test(s)) return 'audio'
  if (/video|t2v|i2v|seedance|wan2|sora|lyria/.test(s)) return 'video'
  if (/vl|vision|image|seedream|seededit|seaweed|ui-tars/.test(s)) return 'vision'
  return 'text'
}

function loadConversations(): Conversation[] | null {
  try {
    const key = storageKey(STORAGE_KEY_PREFIX)
    let raw = localStorage.getItem(key)
    if (!raw) {
      raw = localStorage.getItem(LEGACY_STORAGE_KEY)
      if (raw) localStorage.setItem(key, raw)
    }
    return raw ? (migrateConversations(JSON.parse(raw)) as Conversation[]) : null
  } catch {
    return null
  }
}

/** 测试可复用的纯函数：恢复流式消息为「中断」、剔除异常 messages。 */
export { migrateConversations } from './conversationMigration'
import { migrateConversations } from './conversationMigration'
void migrateConversations

function loadSettings(): ChatSettings {
  try {
    const key = storageKey(SETTINGS_KEY_PREFIX)
    let raw = localStorage.getItem(key)
    if (!raw) {
      raw = localStorage.getItem(LEGACY_SETTINGS_KEY)
      if (raw) localStorage.setItem(key, raw)
    }
    if (!raw) return { ...DEFAULT_SETTINGS }
    const parsed = JSON.parse(raw) as Partial<ChatSettings>
    return {
      ...DEFAULT_SETTINGS,
      ...parsed,
      modelByModality: { ...DEFAULT_SETTINGS.modelByModality, ...(parsed.modelByModality ?? {}) },
    }
  } catch {
    return { ...DEFAULT_SETTINGS }
  }
}

function uid(): string {
  return `m-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

export const useAIChatStore = defineStore('ai-chat', () => {
  // 角色库：send/regenerate 构造请求消息时解析会话绑定角色的 system prompt。
  // （此前未传 agents 列表，导致选中的角色提示词实际不会注入。）
  const agentStore = useChatAgentStore()
  const toast = useToast()

  const conversations = ref<Conversation[]>([])
  const activeId = ref<string | null>(null)
  const models = ref<string[]>([])
  const modelsLoading = ref(false)
  const modelsError = ref('')
  /** 模型 id → 模态。优先来自网关 available-models，缺失时用启发式推断。 */
  const modalityMap = ref<Record<string, ModalityKey>>({})
  /** 网关标记的精选模型名集合（canonical 名 + 别名 + 原始名），用于对话模型选择器打 ★。 */
  const featuredNames = ref<Set<string>>(new Set())
  const catalogLoaded = ref(false)
  const drawerOpen = ref(false)
  const settingsOpen = ref(false)
  const compareMode = ref(false)
  const compareModels = ref<string[]>([])

  const settings = ref<ChatSettings>(loadSettings())

  // 运行时的 AbortController 集合（不持久化）。
  const controllers = new Map<string, AbortController>()

  const active = computed<Conversation | null>(
    () => conversations.value.find((c) => c.id === activeId.value) ?? null,
  )

  const isStreaming = computed(() =>
    active.value ? active.value.messages.some((m) => m.streaming) : false,
  )

  function persist() {
    try {
      localStorage.setItem(storageKey(STORAGE_KEY_PREFIX), JSON.stringify(conversations.value))
      localStorage.setItem(storageKey(SETTINGS_KEY_PREFIX), JSON.stringify(settings.value))
    } catch {
      /* 容量超限等忽略 */
    }
  }

  /** 模型 id → 模态；目录缺失时退化为命名启发式。 */
  function modalityOf(modelId: string): ModalityKey {
    return modalityMap.value[modelId] ?? inferModality(modelId)
  }

  /** 对话里展示的模型 id 是否为网关精选（canonical 名、别名或原始名命中）。 */
  function isFeatured(modelId: string): boolean {
    if (featuredNames.value.size === 0) return false
    if (featuredNames.value.has(modelId)) return true
    // 容错：网关目录按 canonical 名标记精选，而 /v1/models 常带日期后缀
    // （如 gpt-4o → gpt-4o-2024-08-06），按「canonical 名 + 分隔符」前缀匹配。
    for (const name of featuredNames.value) {
      if (!name) continue
      if (modelId.startsWith(`${name}-`) || modelId.startsWith(`${name}@`)) return true
    }
    return false
  }

  /** 从网关精选配置接口解析模型名列表（不同版本字段名有出入，逐个兜底）。 */
  function parseFeaturedPayload(payload: any): string[] {
    const raw =
      payload?.featured_models ??
      payload?.models ??
      payload?.featured ??
      (Array.isArray(payload) ? payload : [])
    if (!Array.isArray(raw)) return []
    return raw
      .map((it: any) => (typeof it === 'string' ? it : it?.model || it?.canonical_name || ''))
      .filter((s: string) => !!s)
  }

  /**
   * 拉取网关的可用模型目录（含官方 modality 标注与精选标记）。
   * 需要已配置带 admin 凭据的网关节点；失败时静默回退到启发式（modalityMap
   * 保持为空，modalityOf 会兜底），不打断对话主流程。
   */
  async function loadModalityCatalog() {
    if (catalogLoaded.value) return
    catalogLoaded.value = true
    try {
      const { nodes } = await listNodes()
      const node = (nodes ?? []).find((n) => n.enabled)
      if (!node) return
      const [res, featuredFromConfig] = await Promise.all([
        getAvailableModels(node.id),
        // 精选列表与目录是两个上游端点：目录里有 featured 字段但可能为空，
        // routing/featured 是权威配置源，两者合并；失败互不影响。
        getFeaturedModels(node.id)
          .then((f) => parseFeaturedPayload(f))
          .catch(() => [] as string[]),
      ])
      const map: Record<string, ModalityKey> = {}
      const featured = new Set<string>(featuredFromConfig)
      for (const fam of res.families ?? []) {
        for (const v of fam.versions ?? []) {
          const mod = String(v.modality ?? '').toLowerCase()
          if (v.canonical_name && MODALITY_KEYS.includes(mod as ModalityKey)) {
            map[v.canonical_name] = mod as ModalityKey
          }
          if (v.canonical_name && v.featured) {
            featured.add(v.canonical_name)
            for (const a of v.aliases ?? []) if (a) featured.add(a)
            for (const r of v.raw_names ?? []) if (r) featured.add(r)
          }
        }
      }
      modalityMap.value = map
      featuredNames.value = featured
    } catch {
      // 目录是增强信息：拿不到就用启发式，不提示打扰。
    }
  }

  function ensureDefaultModel() {
    if (!settings.value.defaultModel && models.value.length > 0) {
      settings.value.defaultModel = AUTO
    }
  }

  function createConversation(model?: string): Conversation {
    const conv: Conversation = {
      id: uid(),
      title: '新对话',
      model: model || AUTO,
      mode: compareMode.value ? 'compare' : 'single',
      messages: [],
      ...(settings.value.defaultAgentId ? { agentId: settings.value.defaultAgentId } : {}),
      createdAt: Date.now(),
      updatedAt: Date.now(),
    }
    conversations.value.unshift(conv)
    activeId.value = conv.id
    persist()
    return conv
  }

  function init() {
    const saved = loadConversations()
    if (saved && saved.length > 0) {
      conversations.value = saved
      activeId.value = saved[0].id
    } else {
      createConversation()
    }
    void loadModels()
    void loadModalityCatalog()
  }

  async function loadModels() {
    modelsLoading.value = true
    modelsError.value = ''
    try {
      const list = await llmBffApi.listModels()
      models.value = list
      ensureDefaultModel()
    } catch (e: any) {
      modelsError.value = e?.message || String(e)
      // 网关未配置时给出温和提示，不阻断 UI。
      if (!models.value.length) {
        toast.error('模型列表获取失败：请先在「设置 → AI 网关」配置网关密钥')
      }
    } finally {
      modelsLoading.value = false
    }
  }

  function selectConversation(id: string) {
    if (!conversations.value.some((c) => c.id === id)) return
    activeId.value = id
    drawerOpen.value = false
    persist()
  }

  function archiveConversation(id: string) {
    const c = conversations.value.find((x) => x.id === id)
    if (!c) return
    c.archivedAt = Date.now()
    c.updatedAt = Date.now()
    if (activeId.value === id) {
      activeId.value = conversations.value.find((x) => !x.archivedAt && x.id !== id)?.id ?? null
      if (!activeId.value) createConversation()
    }
    persist()
  }

  function restoreConversation(id: string) {
    const c = conversations.value.find((x) => x.id === id)
    if (!c) return
    c.archivedAt = undefined
    c.updatedAt = Date.now()
    persist()
  }

  function newConversation() {
    createConversation()
    drawerOpen.value = false
  }

  function renameConversation(id: string, title: string) {
    const c = conversations.value.find((x) => x.id === id)
    if (c) {
      c.title = title.trim() || '未命名对话'
      c.updatedAt = Date.now()
      persist()
    }
  }

  function deleteConversation(id: string) {
    const idx = conversations.value.findIndex((x) => x.id === id)
    if (idx < 0) return
    // 中止该对话可能存在的流
    for (const [k, ctrl] of controllers) {
      if (k.startsWith(id)) {
        ctrl.abort()
        controllers.delete(k)
      }
    }
    conversations.value.splice(idx, 1)
    if (activeId.value === id) {
      activeId.value = conversations.value[0]?.id ?? null
      if (!activeId.value) createConversation()
    }
    persist()
  }

  function setSettings(patch: Partial<ChatSettings>) {
    Object.assign(settings.value, patch)
    persist()
  }

  function setCompareModels(list: string[]) {
    compareModels.value = list
    persist()
  }

  function toggleCompareMode() {
    compareMode.value = !compareMode.value
    if (active.value) {
      active.value.mode = compareMode.value ? 'compare' : 'single'
      persist()
    }
  }

  /** 测试可复用：根据「会话 + 角色列表 + 全局设置」解析最终 systemPrompt。 */
  function resolveSystemPrompt(
    conv: Conversation,
    agents: { id: string; system_prompt: string }[] = [],
  ): string {
    if (conv.customSystemPrompt?.trim()) return conv.customSystemPrompt.trim()
    if (conv.agentId) {
      const agent = agents.find((a) => a.id === conv.agentId)
      if (agent) return agent.system_prompt.trim()
    }
    return settings.value.systemPrompt.trim()
  }

  /** 把可见对话历史（去掉流式/错误/中断）整理成发给网关的 messages。 */
  function buildRequestMessages(
    conv: Conversation,
    agents: { id: string; system_prompt: string }[] = [],
  ): ChatMessage[] {
    const out: ChatMessage[] = []
    const systemPrompt = resolveSystemPrompt(conv, agents)
    if (systemPrompt) out.push({ role: 'system', content: systemPrompt })
    const visible = conv.messages.filter((m) => !m.streaming && !m.error && !m.interrupted)
    for (const m of visible.slice(-MAX_HISTORY)) {
      if (m.role === 'system') continue
      const msg: ChatMessage = { role: m.role, content: m.content }
      if (m.role === 'user' && m.images?.length) msg.images = m.images
      out.push(msg)
    }
    return out
  }

  /**
   * 解析本条消息实际使用的模型：
   *   1. 会话手动指定了具体模型（≠auto）→ 用它；
   *   2. 否则按模态取默认：带图 → vision 默认，纯文本 → text 默认；
   *      模态默认本身也可以是 'auto'（网关智能路由，含任务识别）。
   */
  function resolveModel(conv: Conversation, hasImages: boolean): string {
    if (conv.model && conv.model !== AUTO) return conv.model
    const key: ModalityKey = hasImages ? 'vision' : 'text'
    return settings.value.modelByModality[key] || (key === 'text' ? settings.value.defaultModel : AUTO) || AUTO
  }

  function stop() {
    if (!active.value) return
    for (const [k, ctrl] of controllers) {
      if (k.startsWith(active.value.id)) {
        ctrl.abort()
        controllers.delete(k)
      }
    }
    // 把仍处于 streaming 的消息标记为完成
    let changed = false
    for (const m of active.value.messages) {
      if (m.streaming) {
        m.streaming = false
        if (!m.content) m.content = '（已停止）'
        changed = true
      }
    }
    if (changed) persist()
  }

  /**
   * 发送一条用户消息（可带图片附件，走多模态）。单模型模式下按
   * resolveModel 选模型（auto→按模态默认→网关智能路由）；对比模式下为
   * 每个选中的模型各追加一个助手消息（标 model），并行流式。
   */
  function send(text: string, images?: string[]) {
    const prompt = text.trim()
    const imgs = (images ?? []).filter((u) => u.startsWith('https://') || u.startsWith('data:image/'))
    if ((!prompt && imgs.length === 0) || isStreaming.value) return
    let conv = active.value
    if (!conv) conv = createConversation()

    // 用户消息
    const userMsg: ChatMsg = {
      id: uid(),
      role: 'user',
      content: prompt,
      ...(imgs.length ? { images: imgs } : {}),
      createdAt: Date.now(),
    }
    conv.messages.push(userMsg)

    // 首条消息时自动用问题前 20 字作为标题（纯图消息用固定标题）
    if (conv.messages.filter((m) => m.role === 'user').length === 1) {
      conv.title = prompt.slice(0, 20) || '图片对话'
    }
    conv.updatedAt = Date.now()

    // Snapshot the complete history after appending the user message. Every parallel
    // model receives the same request and no assistant placeholder is included.
    const requestMessages = buildRequestMessages(conv, agentStore.agents)
    // push 之后的任何同步异常都会留下「用户气泡在列表、stream 却未发出」的悬空态
    // （runbook §16.6-1 观察过一次）：这里兜底回滚 userMsg 并给用户明确报错，
    // 保证「气泡入列表」与「流已发起」要么同时发生、要么都不发生。
    try {
      if (compareMode.value && compareModels.value.length > 0) {
        conv.mode = 'compare'
        for (const model of compareModels.value) {
          spawnStream(conv, model, requestMessages)
        }
      } else {
        conv.mode = 'single'
        spawnStream(conv, resolveModel(conv, imgs.length > 0), requestMessages)
      }
    } catch (err) {
      const idx = conv.messages.indexOf(userMsg)
      if (idx >= 0) conv.messages.splice(idx, 1)
      toast.error('发送失败：' + (err instanceof Error ? err.message : String(err)))
      console.error('[ai-chat] send failed after user bubble pushed', err)
      return
    }
    persist()
  }

  function spawnStream(conv: Conversation, model: string, requestMessages: ChatMessage[]) {
    const assistant: ChatMsg = {
      id: uid(),
      role: 'assistant',
      content: '',
      model: compareMode.value ? model : undefined,
      streaming: true,
      createdAt: Date.now(),
    }
    conv.messages.push(assistant)
    // 异步增量必须写入「响应式代理」：push 进数组后模板读到的是代理，
    // 而上面的 assistant 是原始对象——直接改原始对象不会触发模板更新，
    // 表现为流式期间气泡永远空白、重启后才从持久化里显示内容。
    const liveAssistant = conv.messages[conv.messages.length - 1]
    const streamKey = compareMode.value ? `${conv.id}:${assistant.id}` : conv.id

    const ctrl = llmBffApi.streamChat(
      {
        messages: requestMessages.map((message) => ({
          ...message,
          ...(message.images ? { images: [...message.images] } : {}),
        })), 
        model: model || undefined,
        temperature: settings.value.temperature,
        max_tokens: settings.value.maxTokens,
        kind: 'chat',
      },
      {
        onDelta: (d) => {
          if (d.content) liveAssistant.content += d.content
          if (d.usage) liveAssistant.usage = d.usage
        },
        // 后端 auto 回退链切换候选 model：气泡内先给一行进度提示，
        // 正文仍继续追加到同一条消息。
        onRetry: (model) => {
          liveAssistant.retryHint = `上游模型不可用，已切换到 ${model} 重试…`
        },
        onDone: (usage) => {
          liveAssistant.streaming = false
          if (usage) liveAssistant.usage = usage
          controllers.delete(streamKey)
          conv.updatedAt = Date.now()
          persist()
        },
        onError: (err) => {
          liveAssistant.streaming = false
          liveAssistant.error = err.message || String(err)
          controllers.delete(streamKey)
          conv.updatedAt = Date.now()
          persist()
          toast.error('生成失败：' + (err.message || String(err)))
        },
      },
    )
    controllers.set(streamKey, ctrl)
  }

  /**
   * 用另一个模型「检查并优化」某条助手回答：把原问题与回答交给选定模型，
   * 让它评审质量并给出优化后的版本。结果作为新助手消息追加到对话。
   */
  function optimize(targetMsgId: string, model: string) {
    const conv = active.value
    if (!conv || isStreaming.value) return
    const idx = conv.messages.findIndex((m) => m.id === targetMsgId)
    if (idx < 0) return
    const answer = conv.messages[idx]
    // 向前找到最近的用户问题
    let question = ''
    for (let i = idx - 1; i >= 0; i--) {
      if (conv.messages[i].role === 'user') {
        question = conv.messages[i].content
        break
      }
    }
    const metaPrompt =
      `请用你自己的话，检查下面这个回答的准确性、完整性与表达质量，指出问题，` +
      `并给出一份优化后的版本。\n\n【原问题】\n${question}\n\n【待检查的回答】\n${answer.content}`

    const userMsg: ChatMsg = {
      id: uid(),
      role: 'user',
      content: `（用 ${model} 检查并优化上一条回答）`,
      createdAt: Date.now(),
    }
    conv.messages.push(userMsg)

    const assistant: ChatMsg = {
      id: uid(),
      role: 'assistant',
      content: '',
      model,
      streaming: true,
      createdAt: Date.now(),
    }
    conv.messages.push(assistant)
    // 同 spawnStream：增量写入走代理引用（原因见 spawnStream 注释）。
    const liveAssistant = conv.messages[conv.messages.length - 1]
    conv.updatedAt = Date.now()

    const streamKey = `${conv.id}:opt:${assistant.id}`
    const ctrl = llmBffApi.streamChat(
      {
        // 优化请求不带入原对话历史，只发该次评审上下文
        messages: [{ role: 'user', content: metaPrompt }],
        model,
        temperature: settings.value.temperature,
        max_tokens: settings.value.maxTokens,
        kind: 'optimize',
      },
      {
        onDelta: (d) => {
          if (d.content) liveAssistant.content += d.content
          if (d.usage) liveAssistant.usage = d.usage
        },
        // 同 spawnStream：回退重试进度写进同一条消息的 retryHint。
        onRetry: (model) => {
          liveAssistant.retryHint = `上游模型不可用，已切换到 ${model} 重试…`
        },
        onDone: (usage) => {
          liveAssistant.streaming = false
          if (usage) liveAssistant.usage = usage
          controllers.delete(streamKey)
          conv!.updatedAt = Date.now()
          persist()
        },
        onError: (err) => {
          liveAssistant.streaming = false
          liveAssistant.error = err.message || String(err)
          controllers.delete(streamKey)
          persist()
          toast.error('优化失败：' + (err.message || String(err)))
        },
      },
    )
    controllers.set(streamKey, ctrl)
  }

  function regenerate(messageId: string) {
    const conv = active.value
    if (!conv || isStreaming.value) return
    const idx = conv.messages.findIndex((m) => m.id === messageId)
    if (idx < 0) return
    const msg = conv.messages[idx]
    if (msg.role !== 'assistant') return
    // Use the nearest preceding user message by position, preserving duplicate prompts and images.
    const origin = [...conv.messages.slice(0, idx)].reverse().find((m) => m.role === 'user')
    if (!origin) return
    conv.messages.splice(idx, 1)
    const requestMessages = buildRequestMessages(conv, agentStore.agents)
    const hasImages = !!origin.images?.length
    spawnStream(conv, msg.model && msg.model !== AUTO ? msg.model : resolveModel(conv, hasImages), requestMessages)
    persist()
  }

  return {
    // state
    conversations,
    activeId,
    models,
    modelsLoading,
    modelsError,
    modalityMap,
    drawerOpen,
    settingsOpen,
    compareMode,
    compareModels,
    settings,
    // getters
    active,
    isStreaming,
    // actions
    init,
    loadModels,
    loadModalityCatalog,
    modalityOf,
    isFeatured,
    resolveModel,
    persist,
    selectConversation,
    newConversation,
    renameConversation,
    archiveConversation,
    restoreConversation,
    deleteConversation,
    setSettings,
    setCompareModels,
    toggleCompareMode,
    send,
    stop,
    optimize,
    regenerate,
  }
})
