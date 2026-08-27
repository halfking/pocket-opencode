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
import { listNodes, getAvailableModels } from '../../api/gateway'
import { useToast } from '../../composables/useToast'

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
  createdAt: number
  updatedAt: number
}

export interface ChatSettings {
  temperature: number
  maxTokens: number
  systemPrompt: string
  defaultModel: string
  /** 按模态的默认模型（'auto' = 网关智能路由）。仅在会话模型为 auto 时生效。 */
  modelByModality: Record<ModalityKey, string>
}

const STORAGE_KEY = 'pocket:ai-chat:v1'
const SETTINGS_KEY = 'pocket:ai-chat:settings:v1'
const MAX_HISTORY = 40 // 单轮发送给网关的历史消息上限
const AUTO = 'auto'

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
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Conversation[]
    // 恢复时清掉残留的流式标记，避免刷新后卡在「正在输入」。
    for (const c of parsed) {
      for (const m of c.messages) {
        m.streaming = false
        if (m.error) m.error = undefined
      }
    }
    return parsed
  } catch {
    return null
  }
}

function loadSettings(): ChatSettings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
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
  const toast = useToast()

  const conversations = ref<Conversation[]>([])
  const activeId = ref<string | null>(null)
  const models = ref<string[]>([])
  const modelsLoading = ref(false)
  const modelsError = ref('')
  /** 模型 id → 模态。优先来自网关 available-models，缺失时用启发式推断。 */
  const modalityMap = ref<Record<string, ModalityKey>>({})
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
      localStorage.setItem(STORAGE_KEY, JSON.stringify(conversations.value))
      localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings.value))
    } catch {
      /* 容量超限等忽略 */
    }
  }

  /** 模型 id → 模态；目录缺失时退化为命名启发式。 */
  function modalityOf(modelId: string): ModalityKey {
    return modalityMap.value[modelId] ?? inferModality(modelId)
  }

  /**
   * 拉取网关的可用模型目录（含官方 modality 标注）。
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
      const res = await getAvailableModels(node.id)
      const map: Record<string, ModalityKey> = {}
      for (const fam of res.families ?? []) {
        for (const v of fam.versions ?? []) {
          const mod = String(v.modality ?? '').toLowerCase()
          if (v.canonical_name && MODALITY_KEYS.includes(mod as ModalityKey)) {
            map[v.canonical_name] = mod as ModalityKey
          }
        }
      }
      modalityMap.value = map
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
        toast.error('模型列表获取失败：请先在「设置 → AI 模型」配置网关密钥')
      }
    } finally {
      modelsLoading.value = false
    }
  }

  function selectConversation(id: string) {
    activeId.value = id
    drawerOpen.value = false
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

  /** 把可见对话历史（去掉流式/错误中间态）整理成发给网关的 messages。 */
  function buildRequestMessages(conv: Conversation, extraUserText?: string, extraImages?: string[]): ChatMessage[] {
    const out: ChatMessage[] = []
    if (settings.value.systemPrompt.trim()) {
      out.push({ role: 'system', content: settings.value.systemPrompt.trim() })
    }
    const visible = conv.messages.filter((m) => !m.streaming && !m.error)
    for (const m of visible.slice(-MAX_HISTORY)) {
      if (m.role === 'system') continue
      const msg: ChatMessage = { role: m.role, content: m.content }
      if (m.role === 'user' && m.images?.length) msg.images = m.images
      out.push(msg)
    }
    if (extraUserText) {
      const msg: ChatMessage = { role: 'user', content: extraUserText }
      if (extraImages?.length) msg.images = extraImages
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
    return settings.value.modelByModality[key] || AUTO
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
    for (const m of active.value.messages) {
      if (m.streaming) {
        m.streaming = false
        if (!m.content) m.content = '（已停止）'
      }
    }
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

    if (compareMode.value && compareModels.value.length > 0) {
      conv.mode = 'compare'
      for (const model of compareModels.value) {
        spawnStream(conv!, model, prompt, imgs)
      }
    } else {
      conv.mode = 'single'
      spawnStream(conv!, resolveModel(conv, imgs.length > 0), prompt, imgs)
    }
    persist()
  }

  function spawnStream(conv: Conversation, model: string, prompt: string, images?: string[]) {
    const assistant: ChatMsg = {
      id: uid(),
      role: 'assistant',
      content: '',
      model: compareMode.value ? model : undefined,
      streaming: true,
      createdAt: Date.now(),
    }
    conv.messages.push(assistant)
    const streamKey = compareMode.value ? `${conv.id}:${assistant.id}` : conv.id
    const reqMessages = buildRequestMessages(conv, prompt, images)

    const ctrl = llmBffApi.streamChat(
      {
        messages: reqMessages,
        model: model || undefined,
        temperature: settings.value.temperature,
        max_tokens: settings.value.maxTokens,
        kind: 'chat',
      },
      {
        onDelta: (d) => {
          if (d.content) assistant.content += d.content
          if (d.usage) assistant.usage = d.usage
        },
        onDone: (usage) => {
          assistant.streaming = false
          if (usage) assistant.usage = usage
          controllers.delete(streamKey)
          conv.updatedAt = Date.now()
          persist()
        },
        onError: (err) => {
          assistant.streaming = false
          assistant.error = err.message || String(err)
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
          if (d.content) assistant.content += d.content
          if (d.usage) assistant.usage = d.usage
        },
        onDone: (usage) => {
          assistant.streaming = false
          if (usage) assistant.usage = usage
          controllers.delete(streamKey)
          conv!.updatedAt = Date.now()
          persist()
        },
        onError: (err) => {
          assistant.streaming = false
          assistant.error = err.message || String(err)
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
    // 找到对应的问题文本（该助手消息之前最近的一条 user）
    let question = ''
    for (let i = idx - 1; i >= 0; i--) {
      if (conv.messages[i].role === 'user') {
        question = conv.messages[i].content
        break
      }
    }
    // 移除该助手消息后重新生成（沿用原问题的图片与当前模型解析规则）
    conv.messages.splice(idx, 1)
    if (question) {
      const origin = conv.messages.find((m) => m.role === 'user' && m.content === question)
      const hasImages = !!origin?.images?.length
      spawnStream(conv, msg.model && msg.model !== AUTO ? msg.model : resolveModel(conv, hasImages), question, origin?.images)
    }
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
    resolveModel,
    selectConversation,
    newConversation,
    renameConversation,
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
