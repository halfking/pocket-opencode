<template>
  <div class="server-select-view">
    <p class="lead">{{ t('settings.backendHint') }}</p>

    <div class="preset-list">
      <button
        v-if="buildDefault"
        type="button"
        :class="['preset', { active: kind === 'build' }]"
        @click="kind = 'build'"
      >
        <span class="preset-title">{{ t('settings.buildDefault') }}</span>
        <span class="preset-url">{{ buildDefault }}</span>
      </button>
      <button type="button" :class="['preset', { active: kind === 'origin' }]" @click="kind = 'origin'">
        <span class="preset-title">{{ t('settings.sameOrigin') }}</span>
        <span class="preset-url">{{ pageOrigin || '—' }}</span>
      </button>
      <button
        type="button"
        :class="['preset', { active: kind === 'production' }]"
        @click="kind = 'production'"
      >
        <span class="preset-title">{{ t('settings.productionServer') }}</span>
        <span class="preset-url">{{ PRODUCTION_API_BASE }}</span>
      </button>
      <button type="button" :class="['preset', { active: kind === 'custom' }]" @click="kind = 'custom'">
        <span class="preset-title">{{ t('settings.customServer') }}</span>
        <span class="preset-url">{{ customUrl || t('settings.customServerHint') }}</span>
      </button>
    </div>

    <div v-if="kind === 'custom'" class="custom-box">
      <label class="form-label" for="custom-api-base">{{ t('settings.apiAddress') }}</label>
      <input
        id="custom-api-base"
        v-model="customUrl"
        class="form-input"
        type="url"
        inputmode="url"
        autocapitalize="off"
        autocomplete="off"
        spellcheck="false"
        placeholder="https://pocket.example.com"
      />
    </div>

    <div v-if="formError" class="test-result fail">{{ formError }}</div>
    <div v-if="testResult" :class="['test-result', testResult.ok ? 'ok' : 'fail']">
      {{ testResult.text }}
    </div>

    <div class="actions">
      <button class="action-btn secondary" type="button" :disabled="testing" @click="testConnection">
        {{ testing ? t('settings.testing') : t('settings.testConnection') }}
      </button>
      <button class="action-btn primary" type="button" :disabled="saving" @click="saveAndUse">
        {{ saving ? t('settings.saving') : t('settings.saveAndUse') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  PRODUCTION_API_BASE,
  normalizeApiBase,
  persistApiBase,
  probeHealthz,
  readApiBaseOverride,
  resolveApiBase,
} from '../../config/api-base'
import { clearSelectedInstance } from '../../config/selected-instance'
import { useAuthStore } from '../../stores/auth'

type Kind = 'build' | 'origin' | 'production' | 'custom'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const pageOrigin = typeof window !== 'undefined' ? window.location.origin : ''
const buildDefault = String(import.meta.env.VITE_API_BASE || '')

function detectKind(): { kind: Kind; custom: string } {
  const override = readApiBaseOverride()
  if (override === null) return { kind: buildDefault ? 'build' : 'origin', custom: '' }
  if (override === '') return { kind: 'origin', custom: '' }
  if (override === PRODUCTION_API_BASE) return { kind: 'production', custom: '' }
  return { kind: 'custom', custom: override }
}

const initial = detectKind()
const kind = ref<Kind>(initial.kind)
const customUrl = ref(initial.custom)
const testing = ref(false)
const saving = ref(false)
const formError = ref('')
const testResult = ref<{ ok: boolean; text: string } | null>(null)

function previewBase(): string {
  if (kind.value === 'build') return buildDefault ? normalizeApiBase(buildDefault) : ''
  if (kind.value === 'origin') return ''
  if (kind.value === 'production') return PRODUCTION_API_BASE
  return normalizeApiBase(customUrl.value, pageOrigin)
}

function persistChoice(): string {
  if (kind.value === 'build') return persistApiBase(null)
  if (kind.value === 'origin') return persistApiBase('')
  if (kind.value === 'production') return persistApiBase(PRODUCTION_API_BASE)
  return persistApiBase(normalizeApiBase(customUrl.value, pageOrigin))
}

async function testConnection() {
  formError.value = ''
  testResult.value = null
  testing.value = true
  try {
    const base = previewBase()
    const probeAt = base || pageOrigin
    const result = await probeHealthz(probeAt)
    testResult.value = result.ok
      ? { ok: true, text: t('settings.healthOk') }
      : { ok: false, text: t('settings.testFailed', { error: result.error }) }
  } catch (err) {
    formError.value = err instanceof Error ? err.message : String(err)
  } finally {
    testing.value = false
  }
}

async function saveAndUse() {
  formError.value = ''
  saving.value = true
  try {
    const previous = resolveApiBase()
    persistChoice()
    const next = resolveApiBase()
    if (previous !== next) {
      clearSelectedInstance()
      localStorage.removeItem('selected_server')
      if (auth.isAuthenticated) await auth.logout()
      if (typeof window !== 'undefined') {
        window.location.assign(`${window.location.pathname}${window.location.search}#/login`)
        window.location.reload()
        return
      }
      router.replace('/login')
      return
    }
    if (auth.isAuthenticated) router.replace('/settings')
    else router.replace('/login')
  } catch (err) {
    formError.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.server-select-view {
  min-height: 100%;
  padding: var(--space-3);
}
.lead {
  margin: 0 0 var(--space-3);
  color: var(--text-secondary);
  font-size: var(--text-sm);
}
.preset-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.preset {
  text-align: left;
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
}
.preset.active {
  border-color: var(--brand-primary);
  background: var(--brand-bg);
}
.preset-title {
  display: block;
  font-weight: var(--font-weight-semibold);
}
.preset-url {
  display: block;
  margin-top: 2px;
  font-size: var(--text-xs);
  font-family: monospace;
  color: var(--text-muted);
  word-break: break-all;
}
.custom-box {
  margin-top: var(--space-3);
}
.form-label {
  display: block;
  margin-bottom: var(--space-1);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
}
.form-input {
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
}
.actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-4);
}
.action-btn {
  flex: 1;
  padding: var(--space-3);
  border-radius: var(--radius-md);
  font-weight: var(--font-weight-semibold);
  border: 1px solid var(--border);
}
.action-btn.primary {
  background: var(--brand-gradient);
  color: var(--text-inverse);
  border: none;
}
.action-btn.secondary {
  background: var(--brand-bg);
  color: var(--brand-primary);
}
.action-btn:disabled {
  opacity: 0.6;
}
.test-result {
  margin-top: var(--space-3);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}
.test-result.ok {
  background: var(--success-bg);
  color: var(--success);
}
.test-result.fail {
  background: var(--danger-bg);
  color: var(--danger);
}
</style>
