<!--
  SettingsLLMGateway — Phase 5: 编辑 LLM Gateway 配置。

  路由：/settings/llm-gateway?instance_id=xxx
  - 加载现有 baseURL / apiKey(掩码) / models
  - 用户输入 baseURL / apiKey / models(逗号分隔)
  - "测试连接" → POST /api/llm-gateway/test（拉 /v1/models）
  - "保存" → POST /api/llm-gateway/config（触发 OpenCode 热更新）
  - 顶部 ← 返回，底部保存/取消
-->
<template>
  <div class="llm-gateway-view">
    <!-- 顶部栏 -->
    <header class="top-bar">
      <button class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">AI 模型</h1>
      <div class="top-spacer"></div>
    </header>

    <!-- 状态条 -->
    <div v-if="status" :class="['status-bar', `status-${status.kind}`]">
      {{ status.text }}
    </div>

    <!-- 表单 -->
    <main class="form-container">
      <div class="form-section">
        <label class="form-label">Gateway Base URL *</label>
        <input
          v-model="form.baseURL"
          class="form-input"
          type="text"
          placeholder="https://llmgo.kxpms.cn/v1"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
        />
        <div class="form-hint">OpenAI 兼容端点（含 /v1 后缀）</div>
      </div>

      <div class="form-section">
        <label class="form-label">API Key</label>
        <div class="key-row">
          <input
            v-model="form.apiKey"
            class="form-input"
            :type="showKey ? 'text' : 'password'"
            :placeholder="original.apiKeySet ? '已设置（留空保留）' : 'sk-...'"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
          />
          <button class="key-toggle" type="button" @click="showKey = !showKey">
            {{ showKey ? '🙈' : '👁' }}
          </button>
        </div>
        <div v-if="original.apiKeySet" class="form-hint">
          当前：<code>{{ original.apiKey || 'sk-****' }}</code>（留空 = 保留）
        </div>
      </div>

      <div class="form-section">
        <label class="form-label">模型列表（逗号分隔）</label>
        <input
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

      <!-- 操作按钮 -->
      <div class="action-row">
        <button class="btn-secondary" :disabled="!canTest || testing" @click="onTest">
          <span v-if="!testing">🧪 测试连接</span>
          <span v-else>测试中…</span>
        </button>
        <button class="btn-primary" :disabled="!canSave || saving" @click="onSave">
          <span v-if="!saving">💾 保存</span>
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

const router = useRouter()

const original = reactive<GatewayConfig>({
  baseURL: '',
  apiKeySet: false,
  apiKey: '',
  models: [],
  source: 'pocketd',
})

const form = reactive({
  baseURL: '',
  apiKey: '',
})

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
  try {
    const cfg = await api.getGatewayConfig()
    Object.assign(original, cfg)
    form.baseURL = cfg.baseURL
    modelsInput.value = cfg.models.join(', ')
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
      })
    }
    const r: GatewayTestResult = await api.testGateway()
    if (r.ok) {
      setStatus('success', `✓ 连通 (HTTP ${r.status}) · ${r.models?.length || 0} 个模型`)
      // 自动刷新 models
      try {
        const cfg = await api.getGatewayConfig()
        Object.assign(original, cfg)
        modelsInput.value = cfg.models.join(', ')
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
  min-height: 100vh;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
  padding-top: env(safe-area-inset-top);
  padding-bottom: env(safe-area-inset-bottom);
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

.back-btn:hover {
  background: var(--bg-subtle);
}

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
  background: rgba(16, 185, 129, 0.12);
  color: var(--success);
}

.status-bar.status-error {
  background: rgba(239, 68, 68, 0.12);
  color: var(--error, #ef4444);
}

.form-container {
  flex: 1;
  padding: 16px;
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
  border-radius: 8px;
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
  border-radius: 8px;
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
  color: #fff;
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