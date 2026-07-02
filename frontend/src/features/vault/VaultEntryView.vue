<!--
  VaultEntryView — 密码箱条目详情 + 编辑合一
  - 路由: /vault/:id      详情（只读）
  -       /vault/:id/edit 编辑

  安全机制：
  - 复制密码触发 30 秒倒计时，到期自动清空剪贴板
  - 页面 unmount 或路由离开时立即清空剪贴板 + 销毁定时器
  - TOTP 每秒刷新当前 6 位动态码（otpauth 库）
-->
<template>
  <AppLayout>
    <template #actions>
      <span class="header-extra">
        <span class="entry-icon">{{ categoryIcon(entry?.category) }}</span>
      </span>
    </template>

    <!-- 加载中 -->
    <div v-if="loading" class="state">加载中…</div>

    <!-- 不存在 -->
    <div v-else-if="!entry" class="state">
      <p>条目不存在或已被删除</p>
      <button class="action-btn ghost" @click="goBack">返回</button>
    </div>

    <!-- ======== 编辑模式 ======== -->
    <div v-else-if="isEdit" class="edit-form">
      <div class="form-group">
        <label>标题</label>
        <input v-model="form.title" placeholder="标题" />
      </div>

      <div class="form-group">
        <label>用户名</label>
        <input v-model="form.username" placeholder="用户名" />
      </div>

      <div class="form-group">
        <label>网址</label>
        <input v-model="form.url" placeholder="https://" />
      </div>

      <div class="form-group">
        <label>分类</label>
        <select v-model="form.category">
          <option value="login">登录</option>
          <option value="card">银行卡</option>
          <option value="note">安全笔记</option>
          <option value="identity">身份信息</option>
        </select>
      </div>

      <div class="form-group">
        <label>密码</label>
        <input v-model="form.password" type="password" placeholder="密码" />
      </div>

      <div class="form-group">
        <label>TOTP 种子（可选）</label>
        <input v-model="form.totpSecret" placeholder="JBSWY3DPEHPK3PXP" />
      </div>

      <div class="form-group">
        <label>备注</label>
        <textarea v-model="form.notes" placeholder="备注"></textarea>
      </div>

      <div class="actions">
        <button class="action-btn ghost" @click="goBack" :disabled="saving">取消</button>
        <button class="action-btn primary" @click="onSave" :disabled="saving || !form.title">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>

    <!-- ======== 详情模式 ======== -->
    <div v-else class="entry-detail">
      <!-- 标题块 -->
      <header class="entry-header" :class="`cat-${entry.category || 'login'}`">
        <div class="header-icon">{{ categoryIcon(entry.category) }}</div>
        <h1 class="header-title">{{ entry.title }}</h1>
      </header>

      <!-- 字段列表 -->
      <div class="field-list">
        <!-- 用户名 -->
        <div v-if="entry.username" class="field">
          <div class="field-label">用户名</div>
          <div class="field-row">
            <span class="field-value">{{ entry.username }}</span>
            <button class="field-icon-btn" @click="copyField('username', entry.username || '', '用户名')">📋</button>
          </div>
        </div>

        <!-- 网址 -->
        <div v-if="entry.url" class="field">
          <div class="field-label">网址</div>
          <div class="field-row">
            <a class="field-value link" :href="entry.url" target="_blank" rel="noopener">{{ entry.url }}</a>
          </div>
        </div>

        <!-- 密码（核心安全字段） -->
        <div v-if="entry.data.password" class="field password-field">
          <div class="field-label">密码</div>
          <div class="field-row">
            <span class="field-value mono">
              <template v-if="passwordVisible">{{ entry.data.password }}</template>
              <template v-else>{{ maskedPassword }}</template>
            </span>
            <button class="field-icon-btn" :title="passwordVisible ? '隐藏' : '显示'"
              @click="passwordVisible = !passwordVisible">
              {{ passwordVisible ? '🙈' : '👁' }}
            </button>
            <button
              v-if="!copiedSlot"
              class="field-icon-btn primary-copy"
              @click="copyPassword"
            >📋 复制</button>
            <button
              v-else
              class="field-icon-btn countdown"
              disabled
            >📋 {{ copyCountdown }}s</button>
          </div>
        </div>

        <!-- TOTP 动态码 -->
        <div v-if="entry.data.totpSecret" class="field totp-field">
          <div class="field-label">
            动态码 <span class="totp-hint">每 30 秒刷新</span>
          </div>
          <div class="field-row">
            <span class="field-value mono totp-code">{{ totpCode }}</span>
            <span class="totp-remaining" :class="totpRemaining <= 5 ? 'urgent' : ''">
              {{ totpRemaining }}s
            </span>
            <button class="field-icon-btn primary-copy" @click="copyField('otp', totpCode, '动态码')">📋 复制</button>
          </div>
        </div>

        <!-- 备注 -->
        <div v-if="entry.data.notes" class="field">
          <div class="field-label">备注</div>
          <div class="field-value notes">{{ entry.data.notes }}</div>
        </div>

        <!-- 自定义字段 -->
        <div v-for="(cf, idx) in entry.data.customFields || []" :key="idx" class="field">
          <div class="field-label">{{ cf.key }}</div>
          <div class="field-value">{{ cf.value }}</div>
        </div>
      </div>

      <!-- 元信息 -->
      <div class="meta">
        <span>更新于 {{ formatTime(entry.updatedAt) }}</span>
      </div>

      <!-- 操作 -->
      <div class="actions">
        <button class="action-btn primary" @click="goEdit">✎ 编辑</button>
        <button class="action-btn danger" @click="onDelete" :disabled="deleting">
          {{ deleting ? '删除中…' : '🗑 删除' }}
        </button>
      </div>
    </div>

    <!-- Toast：醒目的复制提示 -->
    <transition name="toast">
      <div v-if="toast" class="toast" :class="toast.type">
        {{ toast.msg }}
      </div>
    </transition>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { TOTP, Secret } from 'otpauth'
import AppLayout from '../../app/AppLayout.vue'
import * as vaultStore from './vault-store'
import type { VaultEntry } from './vault-store'

const route = useRoute()
const router = useRouter()

const entry = ref<VaultEntry | null>(null)
const loading = ref(true)
const saving = ref(false)
const deleting = ref(false)
const passwordVisible = ref(false)

// ---- toast ----
interface Toast { msg: string; type: 'danger' | 'success' }
const toast = ref<Toast | null>(null)
let toastTimer: ReturnType<typeof setTimeout> | null = null

function showToast(msg: string, type: 'danger' | 'success' = 'danger') {
  toast.value = { msg, type }
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = null }, 4000)
}

function formatTime(ms: number) {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// ---- mode ----
const isEdit = computed(() => route.path.endsWith('/edit'))

// ---- category icon ----
const categoryIcon = (c?: string | null) =>
  ({ login: '🔑', card: '💳', note: '🗒', identity: '🪪' }[c || 'login'] || '🔑')

// ---- password display ----
const maskedPassword = computed(() => {
  const len = entry.value?.data.password?.length ?? 8
  return '•'.repeat(Math.max(8, Math.min(len, 16)))
})

// ---- copy with 30s auto-clear ----
const COPY_CLEAR_TIMEOUT = 30 // 秒
const copyCountdown = ref(0)
const copiedSlot = ref(false)
let copyInterval: ReturnType<typeof setInterval> | null = null

function clearCopyTimer() {
  if (copyInterval) {
    clearInterval(copyInterval)
    copyInterval = null
  }
  copyCountdown.value = 0
  copiedSlot.value = false
}

/** 立即清空剪贴板 + 显示提示 */
async function clearClipboardNow(reason: string) {
  try {
    // 先读取当前剪贴板，比对确认我们要清的还是它
    await navigator.clipboard.writeText('')
  } catch {
    // 浏览器权限不足时静默失败；Tauri/Capacitor 环境通常 OK
  }
  showToast(reason, 'danger')
  clearCopyTimer()
}

async function copyPassword() {
  if (!entry.value?.data.password) return
  try {
    await navigator.clipboard.writeText(entry.value.data.password)
    showToast('已复制密码（30 秒后自动清空剪贴板）', 'danger')
    // 清除旧定时器后启动新倒计时
    clearCopyTimer()
    copiedSlot.value = true
    copyCountdown.value = COPY_CLEAR_TIMEOUT
    copyInterval = setInterval(async () => {
      copyCountdown.value -= 1
      if (copyCountdown.value <= 0) {
        await clearClipboardNow('🔒 已清空剪贴板（30 秒到期）')
      }
    }, 1000)
  } catch (e: any) {
    showToast('复制失败：浏览器拒绝剪贴板权限', 'danger')
  }
}

/** 通用字段复制（无倒计时，适用于用户名/TOTP 等低敏感字段） */
async function copyField(_key: string, value: string, _label: string) {
  try {
    await navigator.clipboard.writeText(value)
    showToast('已复制', 'success')
  } catch {
    showToast('复制失败：浏览器拒绝剪贴板权限', 'danger')
  }
}

// ---- TOTP ----
const totpCode = ref('------')
const totpRemaining = ref(30)

function refreshTotp() {
  const secret = entry.value?.data.totpSecret
  if (!secret) {
    totpCode.value = '------'
    return
  }
  try {
    // 兼容 Base32 字符串、空格自动 trim
    const clean = secret.replace(/\s+/g, '').toUpperCase()
    const totp = new TOTP({
      issuer: 'Pocket',
      label: entry.value?.username || entry.value?.title || 'vault',
      algorithm: 'SHA1',
      digits: 6,
      period: 30,
      secret: Secret.fromBase32(clean),
    })
    totpCode.value = totp.generate()
    totpRemaining.value = totp.period - (Math.floor(Date.now() / 1000) % totp.period)
  } catch {
    totpCode.value = '无效'
  }
}

let totpInterval: ReturnType<typeof setInterval> | null = null
function startTotpTicker() {
  stopTotpTicker()
  if (!entry.value?.data.totpSecret) return
  refreshTotp()
  // 每秒刷新一次（动态码本身每 30 秒变一次，但剩余时间每秒变）
  totpInterval = setInterval(refreshTotp, 1000)
}
function stopTotpTicker() {
  if (totpInterval) {
    clearInterval(totpInterval)
    totpInterval = null
  }
}

// ---- edit form ----
const form = reactive({
  title: '',
  username: '',
  url: '',
  category: 'login',
  password: '',
  totpSecret: '',
  notes: '',
})

function loadForm() {
  if (!entry.value) return
  form.title = entry.value.title
  form.username = entry.value.username || ''
  form.url = entry.value.url || ''
  form.category = entry.value.category || 'login'
  form.password = entry.value.data.password || ''
  form.totpSecret = entry.value.data.totpSecret || ''
  form.notes = entry.value.data.notes || ''
}

watch(() => [entry.value, isEdit.value], () => {
  if (isEdit.value) loadForm()
})

async function onSave() {
  if (!entry.value || !form.title.trim()) {
    showToast('标题不能为空', 'danger')
    return
  }
  saving.value = true
  try {
    await vaultStore.saveEntry({
      id: entry.value.id,
      title: form.title.trim(),
      username: form.username || undefined,
      url: form.url || undefined,
      category: form.category,
      data: {
        password: form.password || undefined,
        notes: form.notes || undefined,
        totpSecret: form.totpSecret ? form.totpSecret.replace(/\s+/g, '') : undefined,
      },
    })
    showToast('已保存', 'success')
    router.push(`/vault/${entry.value.id}`)
  } catch (e: any) {
    showToast(`保存失败：${e.message || e}`, 'danger')
  } finally {
    saving.value = false
  }
}

// ---- delete ----
async function onDelete() {
  if (!entry.value) return
  const ok = confirm(`确认删除「${entry.value.title}」？此操作不可撤销。`)
  if (!ok) return
  deleting.value = true
  try {
    // 删除前先把可能还在剪贴板里的密码清空
    if (copiedSlot.value) {
      try { await navigator.clipboard.writeText('') } catch { /* ignore */ }
    }
    await vaultStore.deleteEntry(entry.value.id)
    showToast('已删除', 'success')
    router.push('/vault')
  } catch (e: any) {
    showToast(`删除失败：${e.message || e}`, 'danger')
    deleting.value = false
  }
}

// ---- nav ----
function goBack() {
  // 回列表：浏览器历史能回到 /vault 时用 back，否则 push
  if (window.history.length > 1) router.back()
  else router.push('/vault')
}

function goEdit() {
  if (!entry.value) return
  router.push(`/vault/${entry.value.id}/edit`)
}

// ---- lifecycle ----

async function load() {
  const id = route.params.id as string
  loading.value = true
  try {
    const e = await vaultStore.getEntry(id)
    entry.value = e
    if (isEdit.value) loadForm()
    startTotpTicker()
  } catch (err: any) {
    showToast(`加载失败：${err.message || err}`, 'danger')
    entry.value = null
  } finally {
    loading.value = false
  }
}

onMounted(load)

// 路由参数变化时重新加载（例如从列表点别的条目）
watch(() => route.params.id, () => { if (route.params.id) load() })

// 切到后台 / 卸载 / 路由离开 → 立即清空剪贴板
function teardown() {
  stopTotpTicker()
  if (toastTimer) { clearTimeout(toastTimer); toastTimer = null }
  clearCopyTimer()
  // 立即清空剪贴板（防用户复制完密码后切走）
  if (entry.value?.data.password && navigator?.clipboard?.writeText) {
    navigator.clipboard.writeText('').catch(() => { /* ignore */ })
  }
}

onBeforeUnmount(teardown)
onUnmounted(teardown)

// 同时监听 router 切换 —— 用户从密码箱详情跳别的页面也要清
const removeAfterEach = router.afterEach(() => {
  // currentRoute 已经不是密码箱页面了才执行
  if (!router.currentRoute.value.path.startsWith('/vault')) {
    teardown()
  }
})

onBeforeUnmount(() => { removeAfterEach() })

// 进入页面隐藏保险起见再次触发一次（处理 tab 切回）
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden' && copiedSlot.value) {
    // 切到后台立刻清空 + 取消倒计时（保险策略）
    navigator.clipboard.writeText('').catch(() => { /* ignore */ })
    showToast('🔒 已清空剪贴板（页面离开前台）', 'danger')
    clearCopyTimer()
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', () => undefined)
})
</script>

<style scoped>
.entry-detail, .edit-form { display: flex; flex-direction: column; gap: var(--space-4); padding-bottom: var(--space-6); }

.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }

.header-extra { display: inline-flex; align-items: center; }
.entry-icon { font-size: 18px; }

.entry-header {
  display: flex; align-items: center; gap: var(--space-3);
  background: var(--bg-card); border-radius: var(--radius-md);
  padding: var(--space-4) var(--space-5);
  border-left: 4px solid var(--brand-primary);
  box-shadow: var(--shadow-sm);
}
.entry-header.cat-card { border-left-color: #f59e0b; }
.entry-header.cat-note { border-left-color: #10b981; }
.entry-header.cat-identity { border-left-color: #8b5cf6; }
.header-icon { font-size: 28px; }
.header-title { font-size: 20px; font-weight: 700; margin: 0; color: var(--text-primary); flex: 1; word-break: break-word; }

.field-list {
  display: flex; flex-direction: column; gap: var(--space-2);
  background: var(--bg-card); border-radius: var(--radius-md);
  box-shadow: var(--shadow-sm); padding: var(--space-3) var(--space-4);
}
.field { padding: var(--space-3) 0; border-bottom: 1px solid var(--border); }
.field:last-child { border-bottom: none; }
.field-label {
  font-size: 12px; color: var(--text-muted); margin-bottom: var(--space-1);
  display: flex; align-items: center; justify-content: space-between;
}
.totp-hint { font-size: 11px; color: var(--text-muted); }
.field-row { display: flex; align-items: center; gap: var(--space-2); flex-wrap: nowrap; }
.field-value {
  flex: 1; min-width: 0;
  font-size: 14px; color: var(--text-primary);
  word-break: break-all; line-height: 1.5;
}
.field-value.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 15px; }
.field-value.link { color: var(--brand-primary); text-decoration: none; }
.field-value.notes { white-space: pre-wrap; }
.field-icon-btn {
  background: var(--bg-subtle); border: 1px solid var(--border);
  border-radius: var(--radius-sm); padding: 6px 10px;
  font-size: 13px; cursor: pointer; color: var(--text-primary);
  white-space: nowrap; flex-shrink: 0;
}
.field-icon-btn:active { opacity: 0.7; }
.field-icon-btn.primary-copy {
  background: var(--brand-primary); color: white; border-color: var(--brand-primary);
  font-weight: 500;
}
.field-icon-btn.countdown {
  background: var(--danger); color: white; border-color: var(--danger);
  font-weight: 600; cursor: default;
}

.totp-field .field-row { background: var(--bg-subtle); padding: var(--space-3); border-radius: var(--radius-sm); }
.totp-code { font-size: 22px; letter-spacing: 4px; font-weight: 700; color: var(--brand-primary); }
.totp-remaining {
  font-size: 12px; color: var(--text-muted);
  background: rgba(0,0,0,0.05); padding: 2px 8px; border-radius: var(--radius-full);
  font-variant-numeric: tabular-nums;
}
.totp-remaining.urgent { background: var(--danger); color: white; }

.meta {
  font-size: 12px; color: var(--text-muted); padding: 0 var(--space-2);
}

.actions { display: flex; gap: var(--space-3); padding-top: var(--space-2); }
.action-btn {
  flex: 1; padding: var(--space-3) var(--space-2);
  border-radius: var(--radius-md); border: 1px solid var(--border);
  background: var(--bg-card); font-size: 14px; font-weight: 500;
  cursor: pointer; color: var(--text-primary);
}
.action-btn:active { opacity: 0.7; }
.action-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.action-btn.primary {
  background: var(--brand-gradient); color: white; border: none;
}
.action-btn.ghost { background: var(--bg-subtle); }
.action-btn.danger { color: var(--danger); border-color: var(--danger); }

/* edit form */
.form-group { display: flex; flex-direction: column; gap: var(--space-1); margin-bottom: var(--space-3); }
.form-group label { font-size: 12px; color: var(--text-secondary); font-weight: 500; }
.form-group input, .form-group select, .form-group textarea {
  width: 100%; padding: var(--space-3);
  border-radius: var(--radius-md); border: 1px solid var(--border);
  background: var(--bg-card); color: var(--text-primary);
  font-size: 14px; box-sizing: border-box;
}
.form-group textarea { resize: vertical; min-height: 80px; font-family: inherit; }

/* toast */
.toast {
  position: fixed; bottom: calc(var(--space-6)); left: 50%;
  transform: translateX(-50%);
  background: var(--danger); color: white;
  padding: var(--space-3) var(--space-5);
  border-radius: var(--radius-md);
  font-size: 14px; font-weight: 500;
  box-shadow: var(--shadow-md, 0 4px 12px rgba(0,0,0,0.2));
  z-index: 100; max-width: 90vw; text-align: center;
}
.toast.success { background: #10b981; }
.toast-enter-active, .toast-leave-active { transition: opacity 0.2s, transform 0.2s; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translate(-50%, 10px); }
</style>
