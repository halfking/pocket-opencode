<!--
  VaultListView — password vault list. Gated on the cap-keystore plugin;
  shows an unlock/setup screen until the vault is unlocked. See
  docs/2026-07-02-password-vault-design.md.
-->
<template>
  <div class="vault-page">
    <!-- Locked / setup state -->
    <div v-if="!unlocked" class="lock-screen">
      <div class="lock-icon">🔐</div>
      <p v-if="initError" class="error">{{ initError }}</p>
      <div v-else-if="!initialized" class="setup">
        <h2>设置主密码</h2>
        <input v-model="master" type="password" placeholder="主密码" />
        <button class="btn-primary" @click="setup">创建密码箱</button>
      </div>
      <div v-else class="unlock">
        <h2>解锁密码箱</h2>
        <button class="btn-bio" @click="unlockBio">指纹/面容解锁</button>
        <input v-model="master" type="password" placeholder="或输入主密码" />
        <button class="btn-primary" @click="unlockPwd">解锁</button>
      </div>
    </div>

    <!-- Unlocked: list -->
    <div v-else class="vault-unlocked">
      <ScrollChromePortal>
        <div class="toolbar">
          <button class="btn-ghost" @click="showAdd = !showAdd">➕ 新增</button>
          <button class="btn-ghost" @click="generate">🎲 生成密码</button>
          <button class="btn-ghost" @click="cloudSync" :disabled="syncing">
            {{ syncing ? '☁️ 同步中…' : '☁️ 云同步' }}
          </button>
          <button class="btn-ghost" @click="lock">🔒 锁定</button>
        </div>
      </ScrollChromePortal>

      <div class="vault-body">
      <div v-if="syncStatus" class="sync-status" :class="syncStatus.type">
        {{ syncStatus.msg }}
      </div>

      <!-- 新增表单 -->
      <div v-if="showAdd" class="add-form">
        <input v-model="newEntry.title" placeholder="标题（如 GitHub）" />
        <input v-model="newEntry.username" placeholder="用户名" />
        <input v-model="newEntry.url" placeholder="网址" />
        <select v-model="newEntry.category">
          <option value="login">登录</option>
          <option value="card">银行卡</option>
          <option value="note">安全笔记</option>
          <option value="identity">身份信息</option>
        </select>
        <input v-model="newEntry.password" type="password" placeholder="密码" />
        <textarea v-model="newEntry.notes" placeholder="备注（可选）"></textarea>
        <button class="btn-primary" @click="saveNew">保存</button>
      </div>

      <EmptyState
        v-if="entries.length === 0"
        icon="🔐"
        title="密码箱为空"
        hint="点击「新增」添加登录凭据，或使用「生成密码」"
        size="sm"
        variant="inline"
        action-label="新增条目"
        @action="showAdd = true"
      />
      <div v-else class="entry-list">
        <div v-for="e in entries" :key="e.id" class="entry-card" @click="open(e.id)">
          <span class="entry-icon">{{ categoryIcon(e.category) }}</span>
          <div class="entry-body">
            <div class="entry-title">{{ e.title }}</div>
            <div class="entry-user">{{ e.username }}</div>
          </div>
          <span class="arrow">›</span>
        </div>
      </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { EmptyState } from '../../components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'
import { keystore, isKeystoreAvailable, isNotImplementedError } from '../../native/keystore'
import * as vaultStore from './vault-store'
import * as syncStore from './sync-store'
import { isCryptoReady } from '../../native/crypto'
import type { VaultEntryMeta } from './vault-store'
import { useListScene } from '../../composables/use-list-scene'
import { useToast } from '../../composables/useToast'
import { useApiError } from '../../composables/useApiError'

defineOptions({ name: 'VaultListView' })

const initialized = ref(false)
const unlocked = ref(false)
const initError = ref('')
const apiError = useApiError()
const master = ref('')
const entries = ref<VaultEntryMeta[]>([])
const showAdd = ref(false)
const syncing = ref(false)
const syncStatus = ref<{ type: 'ok' | 'err'; msg: string } | null>(null)

const router = useRouter()
const toast = useToast()

const newEntry = reactive({
  title: '', username: '', url: '', category: 'login', password: '', notes: '',
})

// BUG-AT（2026-10-01 真机取证）：Android 模块里**没有** KeystorePlugin.java，
// Capacitor.Plugins 19 个插件里也没有 Keystore，所有方法 reject
// `"Keystore" plugin is not implemented on android`。
//
// 原实现只对**探针那一个方法**做了 crypto 降级，initialized 因此为真、界面照常显示
// 「解锁密码箱 / 指纹·面容解锁」，而其余 11 个方法仍直接抛错——
// 用户点一下就撞原始英文技术错误。更糟的是另一条降级路径把原因说成
// 「主密码尚未设置」，与真实原因（插件不存在）完全无关，把人和排查都带偏。
//
// 现在：先问「这个平台到底能不能用」，不能用就**如实说不可用**，
// 宁可少一个入口，也不给一个必然失败的操作。
const UNSUPPORTED_MSG = '当前平台未提供密码箱原生插件，功能不可用。'

const supported = ref<boolean | null>(null)

async function probe() {
  if (!(await isKeystoreAvailable())) {
    supported.value = false
    initialized.value = false
    unlocked.value = false
    // 注意：这里绝不能写「主密码尚未设置」——那与真实原因无关。
    initError.value = UNSUPPORTED_MSG
    return
  }
  supported.value = true
  try {
    initialized.value = await keystore.isVaultInitialized()
  } catch {
    // 插件在、但探针失败：这时才轮到本地 crypto 降级
    initialized.value = isCryptoReady()
    if (!initialized.value) {
      initError.value = '主密码尚未设置（登录后自动初始化）'
    }
  }
}

async function setup() {
  if (!master.value) return
  await keystore.setupMasterPassword(master.value)
  master.value = ''
  await probe()
  await load()
}

async function unlockBio() {
  try {
    await keystore.unlockWithBiometric()
    unlocked.value = true
    await load()
  } catch (e: any) {
    // BUG-AT：插件缺失时 e.message 是 `"Keystore" plugin is not implemented on android`
    // 这种原始英文技术错误，直接显示等于把内部实现扔给用户。
    initError.value = isNotImplementedError(e) ? UNSUPPORTED_MSG : '解锁失败，请重试'
  }
}

async function unlockPwd() {
  // 本地降级模式：crypto 已初始化（登录时）直接解锁
  if (isCryptoReady()) {
    unlocked.value = true
    master.value = ''
    await load()
    return
  }
  initError.value = '密码箱未初始化'
}

async function load() {
  try {
    entries.value = await vaultStore.listEntries()
  } catch (e: any) {
    initError.value = isNotImplementedError(e) ? UNSUPPORTED_MSG : '读取密码箱失败'
  }
}

async function lock() {
  try { await keystore.lock() } catch { /* 本地模式忽略 */ }
  unlocked.value = false
  entries.value = []
}

async function generate() {
  try {
    const pwd = await keystore.generatePassword({ length: 20, upper: true, lower: true, digits: true, symbols: true })
    await navigator.clipboard.writeText(pwd).catch(() => {})
    toast.success('已生成并复制（30秒后剪贴板自动清空）')
  } catch {
    // cap-keystore 不可用：用 Web Crypto 生成
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*'
    const arr = new Uint32Array(20)
    crypto.getRandomValues(arr)
    const pwd = Array.from(arr, (n) => chars[n % chars.length]).join('')
    await navigator.clipboard.writeText(pwd).catch(() => {})
    toast.success('已生成并复制（30秒后剪贴板自动清空）')
  }
}

async function saveNew() {
  if (!newEntry.title) { toast.error('请输入标题'); return }
  await vaultStore.saveEntry({
    title: newEntry.title,
    username: newEntry.username || undefined,
    url: newEntry.url || undefined,
    category: newEntry.category,
    data: { password: newEntry.password, notes: newEntry.notes },
  })
  // 清空表单
  newEntry.title = ''; newEntry.username = ''; newEntry.url = ''
  newEntry.password = ''; newEntry.notes = ''; newEntry.category = 'login'
  showAdd.value = false
  await load()
}

function open(id: string) { router.push(`/vault/${id}`) }

/** 云同步：加密本地数据 → 上传到 pocketd（零知识，服务端只见密文）*/
async function cloudSync() {
  syncing.value = true
  syncStatus.value = null
  try {
    const result = await syncStore.smartSync()
    if (result.action === 'upload') {
      syncStatus.value = { type: 'ok', msg: `☁️ 已上传 ${result.entries} 条到云端（v${result.version}）` }
    } else if (result.action === 'download') {
      syncStatus.value = { type: 'ok', msg: `☁️ 已从云端恢复 ${result.entries} 条（v${result.version}）` }
      await load()
    } else {
      syncStatus.value = { type: 'ok', msg: '本地和云端均为空，无需同步' }
    }
  } catch (e: any) {
    syncStatus.value = { type: 'err', msg: apiError(e, '同步失败') }
  } finally {
    syncing.value = false
  }
}

const categoryIcon = (c?: string | null) =>
  ({ login: '🔑', card: '💳', note: '🗒', identity: '🪪' }[c || 'login'] || '🔑')

onMounted(probe)
/* KeepAlive 现场保持：新增表单开关/解锁态保留；条目被编辑或删除过才刷新 */
useListScene('vault', load)
</script>

<style scoped>
.lock-screen { display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 60vh; gap: var(--space-3); }
.lock-icon { font-size: 56px; }
.setup, .unlock { display: flex; flex-direction: column; gap: var(--space-3); width: 80%; max-width: 320px; }
input {
  padding: var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
}
.btn-primary { background: var(--brand-gradient); color: var(--text-inverse); border: none; padding: var(--space-3); border-radius: var(--radius-md); font-weight: var(--font-weight-semibold); cursor: pointer; }
.btn-bio { background: var(--bg-card); color: var(--brand-primary); border: 1px solid var(--brand-primary); padding: var(--space-3); border-radius: var(--radius-md); font-weight: 600; cursor: pointer; }
.error { color: var(--danger); font-size: 13px; text-align: center; }
.add-form {
  display: flex; flex-direction: column; gap: var(--space-2);
  margin-bottom: var(--space-3); padding: var(--space-3);
  background: var(--bg-elevated); border-radius: var(--radius-md);
}
.add-form input, .add-form select, .add-form textarea {
  padding: var(--space-2); border-radius: var(--radius-sm);
  border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary); font-size: var(--text-base);
}
.add-form textarea { resize: vertical; min-height: 60px; }
.sync-status {
  margin-bottom: var(--space-3); padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm); font-size: 13px;
}
.sync-status.ok { background: var(--success-bg); color: var(--success); }
.sync-status.err { background: var(--danger-bg); color: var(--danger); }
.toolbar { display: flex; gap: var(--space-2); padding: var(--space-3); flex-wrap: wrap; }
.vault-page { height: 100%; min-height: 0; }
.vault-unlocked { height: 100%; min-height: 0; display: flex; flex-direction: column; }
.vault-body { flex: 1; min-height: 0; }
.btn-ghost { background: var(--bg-card); border: 1px solid var(--border); color: var(--text-primary); padding: var(--space-2) var(--space-3); border-radius: var(--radius-md); font-size: 13px; cursor: pointer; }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.entry-list { display: flex; flex-direction: column; gap: var(--space-2); }
.entry-card {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  background: var(--bg-card);
  padding: var(--spacing-card-padding);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  cursor: pointer;
  min-height: 52px;
  max-height: 66px;
}
.entry-icon { font-size: 22px; }
.entry-body { flex: 1; }
.entry-title { font-weight: 600; font-size: var(--text-base); }
.entry-user { color: var(--text-secondary); font-size: var(--text-sm); }
.arrow { color: var(--text-muted); }
</style>
