<!--
  SettingsLLMGateway — AI 网关配置编辑（Phase 5 + P3 反馈轮升级）。

  路由：/settings/llm-gateway
  - baseURL / apiKey / models（原有）
  - 消息格式下拉框：openai-chat（默认，当前唯一实现）/ anthropic-messages /
    openai-responses —— 选项对齐 llm-gateway-go 暴露的端点族（GET /config
    返回 formats，与后端 gatewayFormats 同源）
  - 常用模型勾选：测试连通后从模型目录多选；保存后 /ai-chat 等模型选择器
    只展示勾选集（目录过大时降噪），全不选 = 展示全部
  - "测试连接" → POST /api/llm-gateway/test（拉 /v1/models）
  - "保存" → POST /api/llm-gateway/config（触发 OpenCode 热更新）
-->
<template>
  <div class="llm-gateway-view">
    <div
      ref="chromeRef"
      class="chrome-shell"
      :class="{ 'is-snapping': chrome.snapping }"
      :style="chromeShellStyle"
    >
      <header class="top-bar">
      <button class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">AI 网关</h1>
      <div class="top-spacer"></div>
      </header>

<!-- 状态条 -->
    <div v-if="status" :class="['status-bar', `status-${status.kind}`]" role="status" aria-live="polite">
      {{ status.text }}
    </div>
    </div><!-- /chrome-shell：fixed 顶栏到此为止，main 为兄弟节点（paddingTop 避让） -->

    <main
      ref="scrollRef"
      class="form-container"
      :style="{ paddingTop: chromeHeight + 'px' }"
      @scroll="onScroll"
    >
      <div class="form-section">
        <label class="form-label" for="gateway-base-url">网关地址 *</label>
        <input
          id="gateway-base-url"
          v-model="form.baseURL"
          class="form-input"
          type="text"
          placeholder="https://llm.kxpms.cn/v1"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
        />
        <div class="form-hint">OpenAI 兼容端点（含 /v1 后缀）</div>
      </div>

      <div class="form-section">
        <label class="form-label" for="gateway-api-key">API Key</label>
        <div class="key-row">
          <input
            id="gateway-api-key"
            v-model="form.apiKey"
            class="form-input"
            :type="showKey ? 'text' : 'password'"
            :placeholder="original.apiKeySet ? '已设置（留空保留）' : 'sk-...'"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
          />
          <button class="key-toggle" type="button" :aria-label="showKey ? '隐藏 API Key' : '显示 API Key'" @click="showKey = !showKey">
            <span aria-hidden="true">{{ showKey ? '🙈' : '👁' }}</span>
          </button>
        </div>
        <div v-if="original.apiKeySet" class="form-hint">
          当前：<code>{{ original.apiKey || 'sk-****' }}</code>（留空 = 保留）
        </div>
      </div>

      <div class="form-section">
        <label class="form-label" for="gateway-format">消息格式</label>
        <select id="gateway-format" v-model="form.format" class="form-input format-select">
          <option v-for="f in formatOptions" :key="f.value" :value="f.value">{{ f.label }}</option>
        </select>
        <div v-if="form.format !== 'openai-chat'" class="form-hint format-hint">
          ⚠ 已保存该格式；当前对话链路暂仅实现 OpenAI Chat 端点，其余格式适配中
        </div>
        <div v-else class="form-hint">对齐 llm-gateway-go 端点族；默认 OpenAI Chat（/v1/chat/completions）</div>
      </div>

      <div class="form-section">
        <label class="form-label" for="gateway-models">模型列表（逗号分隔）</label>
        <input
          id="gateway-models"
          v-model="modelsInput"
          class="form-input"
          type="text"
          placeholder="deepseek-v3, claude-sonnet-4-6, gpt-4o"
        />
        <div class="form-hint">
          测试连接后自动填充。当前：
          <span v-if="original.models.length === 0" class="hint-empty">未配置</span>
          <span v-else>
            <code v-for="m in original.models.slice(0, 5)" :key="m" class="model-chip">{{ m }}</code>
            <span v-if="original.models.length > 5" class="hint-extra">+{{ original.models.length - 5 }}</span>
          </span>
        </div>
      </div>

      <!-- 常用模型勾选：非空时模型选择器只显示这些 -->
      <div class="form-section">
        <div class="pref-head">
          <label class="form-label">常用模型</label>
          <button v-if="preferredSel.size > 0" type="button" class="pref-clear" @click="preferredSel.clear()">清空</button>
        </div>
        <div v-if="catalogModels.length === 0" class="form-hint">
          「测试连接」拉取目录后可勾选常用模型；不勾选 = 显示全部模型
        </div>
        <template v-else>
          <input
            v-model="modelSearch"
            class="form-input model-search"
            type="search"
            placeholder="搜索模型（共 {{ catalogModels.length }} 个）…"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
          />
          <details
            v-for="g in groupedModels"
            :key="g.name"
            class="model-group"
            open
          >
            <summary class="group-head">
              <span class="group-name">{{ g.name }}</span>
              <span class="group-count">{{ g.models.length }}</span>
            </summary>
            <div class="pref-grid" role="group" :aria-label="g.name + ' 模型多选'">
              <button
                v-for="m in g.models"
                :key="m"
                type="button"
                class="pref-chip"
                :class="{ selected: preferredSel.has(m) }"
                :aria-pressed="preferredSel.has(m)"
                @click="togglePreferred(m)"
              >
                <span v-if="preferredSel.has(m)" class="material-symbols-outlined chip-check" aria-hidden="true">check_circle</span>
                <span class="chip-name">{{ m }}</span>
              </button>
            </div>
          </details>
          <div v-if="groupedModels.length === 0" class="form-hint">无匹配「{{ modelSearch }}」的模型</div>
        </template>
        <div class="form-hint">已选 {{ preferredSel.size }} 个——聊天等模型选择器将只显示勾选的模型</div>
      </div>

      <!-- 操作按钮 -->
      <div class="action-row">
        <button class="btn-secondary" :disabled="!canTest || testing" @click="onTest">
          <span v-if="!testing">测试连接</span>
          <span v-else>测试中…</span>
        </button>
        <button class="btn-primary" :disabled="!canSave || saving" @click="onSave">
          <span v-if="!saving">保存</span>
          <span v-else>保存中…</span>
        </button>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { api, type GatewayConfig, type GatewayTestResult } from '../../api/client'
import { createScrollHideChrome } from '../../composables/useScrollHideChrome'

const router = useRouter()

const chromeRef = ref<HTMLElement | null>(null)
const scrollRef = ref<HTMLElement | null>(null)
const chromeHeight = ref(96)
const chrome = createScrollHideChrome(() => chromeHeight.value)
const chromeShellStyle = computed(() => ({
  transform: `translate3d(0, -${chrome.hiddenOffset.value}px, 0)`,
}))

let lastScrollTop = 0
function onScroll() {
  const el = scrollRef.value
  if (!el) return
  const delta = el.scrollTop - lastScrollTop
  lastScrollTop = el.scrollTop
  chrome.reportScroll({ scrollTop: el.scrollTop, delta })
}

function measureChrome() {
  chromeHeight.value = chromeRef.value?.offsetHeight ?? 96
}

const original = reactive<GatewayConfig>({
  baseURL: '',
  apiKeySet: false,
  apiKey: '',
  models: [],
  source: 'pocketd',
  format: 'openai-chat',
  preferredModels: [],
  formats: [],
})

const form = reactive({
  baseURL: '',
  apiKey: '',
  format: 'openai-chat',
})

/** 消息格式下拉框（优先服务端 formats，本地兜底同源常量）。 */
const FORMAT_LABELS: Record<string, string> = {
  'openai-chat': 'OpenAI Chat（/v1/chat/completions）',
  'anthropic-messages': 'Anthropic Messages（/v1/messages）',
  'openai-responses': 'OpenAI Responses（/v1/responses）',
}
const formatOptions = computed(() =>
  (original.formats?.length ?? 0) > 0
    ? original.formats!.map((f) => ({ value: f, label: FORMAT_LABELS[f] || f }))
    : Object.entries(FORMAT_LABELS).map(([value, label]) => ({ value, label })),
)

/** 模型目录（测试连接后填充，勾选常用模型的候选）。 */
const catalogModels = ref<string[]>([])
const preferredSel = ref<Set<string>>(new Set())
const modelSearch = ref('')

/** 模型 id → 原厂分组。规则按前缀匹配，未识别归入「其他」（排在最后）。 */
const VENDOR_RULES: [RegExp, string][] = [
  [/^(gpt|o[134]\b|o[134][-.]|chatgpt|davinci|whisper|tts-|ada|babbage|curie|dall-e|sora)/i, 'OpenAI'],
  [/^claude/i, 'Anthropic'],
  [/^(gemini|gemma|imagen|palm|bard)/i, 'Google'],
  [/^deepseek/i, 'DeepSeek'],
  [/^(qwen|qwq|qvq)/i, '阿里通义'],
  [/^glm/i, '智谱 GLM'],
  [/^doubao|^skylark/i, '字节豆包'],
  [/^(moonshot|kimi)/i, 'Moonshot'],
  [/^minimax|^abab/i, 'MiniMax'],
  [/^(llama|meta-|codellama)/i, 'Meta'],
  [/^(mistral|mixtral|codestral|pixtral|magistral)/i, 'Mistral'],
  [/^grok/i, 'xAI'],
  [/^ernie/i, '百度文心'],
  [/^hunyuan/i, '腾讯混元'],
  [/^yi-/i, '零一万物'],
  [/^spark/i, '讯飞星火'],
  [/^(phi-|microsoft-)/i, 'Microsoft'],
  [/^nova/i, 'Amazon'],
  [/^(jamba|command)/i, 'Cohere'],
]

function vendorOf(m: string): string {
  for (const [re, name] of VENDOR_RULES) if (re.test(m)) return name
  return '其他'
}

/** 按原厂分组（可被搜索框过滤）；组内字典序，「其他」固定垫底。 */
const groupedModels = computed(() => {
  const q = modelSearch.value.trim().toLowerCase()
  const groups = new Map<string, string[]>()
  for (const m of catalogModels.value) {
    if (q && !m.toLowerCase().includes(q)) continue
    const v = vendorOf(m)
    if (!groups.has(v)) groups.set(v, [])
    groups.get(v)!.push(m)
  }
  return [...groups.entries()]
    .map(([name, models]) => ({ name, models: models.sort((a, b) => a.localeCompare(b)) }))
    .sort((a, b) => {
      if (a.name === '其他') return 1
      if (b.name === '其他') return -1
      return b.models.length - a.models.length || a.name.localeCompare(b.name)
    })
})

function togglePreferred(m: string): void {
  const next = new Set(preferredSel.value)
  if (next.has(m)) next.delete(m)
  else next.add(m)
  preferredSel.value = next
}

const modelsInput = ref('')
const showKey = ref(false)
const testing = ref(false)
const saving = ref(false)

type StatusKind = 'info' | 'success' | 'error'
const status = ref<{ kind: StatusKind; text: string } | null>(null)

const canTest = computed(() => form.baseURL.trim().length > 0)
const canSave = computed(
  () => form.baseURL.trim().length > 0 && (form.apiKey.length > 0 || original.apiKeySet),
)

onMounted(async () => {
  measureChrome()
  try {
    const raw = await api.getGatewayConfig()
    const cfg = { ...raw, models: raw.models ?? [], preferredModels: raw.preferredModels ?? [] }
    Object.assign(original, cfg)
    form.baseURL = cfg.baseURL
    form.format = cfg.format || 'openai-chat'
    modelsInput.value = cfg.models.join(', ')
    preferredSel.value = new Set(cfg.preferredModels)
    // 目录已有缓存模型时直接可作为勾选候选
    if (cfg.models.length > 0) catalogModels.value = cfg.models
  } catch (err: any) {
    setStatus('error', '加载失败：' + (err?.message || err))
  }
})

function setStatus(kind: StatusKind, text: string, ttl = 5000) {
  status.value = { kind, text }
  if (ttl > 0) {
    setTimeout(() => {
      if (status.value?.text === text) status.value = null
    }, ttl)
  }
}

async function onTest() {
  testing.value = true
  setStatus('info', '正在拉取 ' + form.baseURL + '/models ...', 0)
  try {
    // 先临时保存（不持久化），或者直接 GET — 这里用 test endpoint
    // 后端的 /api/llm-gateway/test 用的是 currentLLMGateway 状态，
    // 所以测试前需要先 POST 当前表单到 /config（apiKey 留空保留旧值）
    const models = parseModels(modelsInput.value)
    if (form.apiKey || original.apiKeySet) {
      await api.saveGatewayConfig({
        baseURL: form.baseURL,
        apiKey: form.apiKey || undefined,
        models,
        format: form.format,
        preferredModels: [...preferredSel.value],
      })
    }
    const r: GatewayTestResult = await api.testGateway()
    if (r.ok) {
      setStatus('success', `✓ 连通 (HTTP ${r.status}) · ${r.models?.length || 0} 个模型`)
      // 自动刷新 models + 勾选目录候选
      try {
        const cfg = await api.getGatewayConfig()
        Object.assign(original, cfg, { models: cfg.models ?? [], preferredModels: cfg.preferredModels ?? [] })
        modelsInput.value = (cfg.models ?? []).join(', ')
        if ((cfg.models ?? []).length > 0) catalogModels.value = cfg.models ?? []
      } catch {}
    } else {
      setStatus('error', `✗ 失败：${r.error || r.response || 'HTTP ' + r.status}`)
    }
  } catch (err: any) {
    setStatus('error', '✗ ' + (err?.message || String(err)))
  } finally {
    testing.value = false
  }
}

async function onSave() {
  saving.value = true
  try {
    const models = parseModels(modelsInput.value)
    const r = await api.saveGatewayConfig({
      baseURL: form.baseURL,
      apiKey: form.apiKey || undefined,
      models,
      format: form.format,
      preferredModels: [...preferredSel.value],
    })
    if (r.ok) {
      setStatus('success', '✓ 已保存，OpenCode 配置热更新已触发')
      setTimeout(() => router.back(), 800)
    } else {
      setStatus('error', '保存失败')
    }
  } catch (err: any) {
    setStatus('error', '保存失败：' + (err?.message || err))
  } finally {
    saving.value = false
  }
}

function parseModels(s: string): string[] {
  return s
    .split(',')
    .map((m) => m.trim())
    .filter((m) => m.length > 0)
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/settings')
}
</script>

<style scoped>
.llm-gateway-view {
  /* Chrome<108（如 Android 11 系统 WebView 83）不支持 dvh，且对无效声明呈现
     "CSSOM 假有效"——写在同一规则里的渐进回退链会整体失效，height 塌成 auto，
     被内容撑开后外层 #app overflow:hidden 裁切 → 整页无法滚动。
     因此增强声明必须放 @supports 里，老内核只拿到它能解析的前两条。 */
  height: 100%;
  min-height: 0;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}

@supports (height: 100dvh) {
  .llm-gateway-view {
    height: 100%;
  }
}

.chrome-shell {
  position: fixed;
  top: var(--app-safe-top);
  /* safe-area-top 由 body 唯一注入（styles.css:32）；这里改为 top 偏移而非 padding，
     避免与全局 body padding-top 叠加成两倍状态栏高度。 */
  left: 0;
  right: 0;
  z-index: var(--z-sticky);
  will-change: transform;
  background: var(--bg-card);
}

.chrome-shell.is-snapping {
  transition: transform var(--duration-chrome) var(--ease-chrome);
}

.top-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.back-btn,
.top-spacer {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: transparent;
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--text-primary);
}

.back-btn:hover,
.back-btn:focus-visible {
  background: var(--bg-subtle);
}
.back-btn:focus-visible { outline: none; box-shadow: 0 0 0 2px var(--brand-primary); }

.title {
  flex: 1;
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
}

.status-bar {
  flex: 0 0 auto;
  padding: 10px 16px;
  font-size: 13px;
  text-align: center;
  font-weight: 500;
  border-bottom: 1px solid var(--border);
}

.status-bar.status-info {
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.status-bar.status-success {
  background: var(--success-bg);
  color: var(--success);
}

.status-bar.status-error {
  background: var(--danger-bg);
  color: var(--danger);
}

.form-container {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  padding: 16px;
  padding-bottom: calc(16px + var(--app-safe-bottom));
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.form-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
}

.form-input {
  width: 100%;
  padding: 12px 14px;
  font-size: 14px;
  font-family: 'SF Mono', Menlo, monospace;
  background: var(--bg-card);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-sizing: border-box;
  transition: border-color 180ms ease;
}

.form-input:focus {
  outline: none;
  border-color: var(--brand-primary);
}

.form-hint {
  font-size: 12px;
  color: var(--text-muted);
}

.form-hint code {
  font-family: 'SF Mono', Menlo, monospace;
  background: var(--bg-subtle);
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 11px;
}

.form-hint .model-chip {
  display: inline-block;
  margin: 0 4px 2px 0;
}

.form-hint .hint-extra {
  color: var(--text-secondary);
  font-weight: 600;
}

.form-hint .hint-empty {
  color: var(--warning);
  font-style: italic;
}

.key-row {
  display: flex;
  gap: 8px;
}

.key-row .form-input {
  flex: 1;
  font-family: 'SF Mono', Menlo, monospace;
}

.key-toggle {
  width: 44px;
  height: 44px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  cursor: pointer;
  font-size: 18px;
}

.action-row {
  display: flex;
  gap: 12px;
  margin-top: 8px;
}

/* ── 消息格式下拉 + 常用模型勾选（P3 反馈轮） ── */
.format-select {
  font-family: inherit;
  min-height: 44px;
  appearance: none;
  background-image: none;
}

.format-hint {
  color: var(--warning);
}

.pref-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.pref-clear {
  min-height: 32px;
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: var(--text-xs);
}

.pref-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

/* ── 模型目录分组 + 搜索 ── */
.model-search {
  font-family: inherit;
  min-height: 44px;
  margin-bottom: 4px;
}

.model-group {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  margin-bottom: 8px;
  overflow: hidden;
}

.model-group[open] .group-head {
  border-bottom: 1px solid var(--border);
}

.group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  cursor: pointer;
  list-style: none; /* 隐藏默认三角，用 ::after 画 */
  user-select: none;
}

.group-head::-webkit-details-marker {
  display: none;
}

.group-head::after {
  content: '▾';
  color: var(--text-muted);
  font-size: 12px;
  transition: transform 160ms ease;
}

.model-group:not([open]) .group-head::after {
  transform: rotate(-90deg);
}

.group-name {
  flex: 1;
}

.group-count {
  margin-right: 10px;
  font-size: 11px;
  font-weight: 500;
  color: var(--text-secondary);
  background: var(--bg-subtle);
  border-radius: 999px;
  padding: 1px 8px;
}

.model-group .pref-grid {
  padding: 10px 12px;
}

.pref-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 36px;
  padding: var(--space-1) var(--space-2-5);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
}

.pref-chip.selected {
  background: var(--brand-bg);
  border-color: var(--brand-primary);
  color: var(--brand-primary);
  font-weight: var(--font-weight-medium);
}

.chip-check {
  font-size: 14px;
}

.chip-name {
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.btn-secondary,
.btn-primary {
  flex: 1;
  padding: 14px;
  font-size: 14px;
  font-weight: 600;
  border: none;
  border-radius: 999px;
  cursor: pointer;
  transition:
    background 180ms ease,
    transform 120ms ease;
}

.btn-secondary {
  background: var(--bg-subtle);
  color: var(--text-primary);
}

.btn-primary {
  background: var(--brand-primary);
  color: var(--text-inverse);
}

.btn-primary:disabled,
.btn-secondary:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.btn-primary:active:not(:disabled),
.btn-secondary:active:not(:disabled) {
  transform: scale(0.97);
}
</style>