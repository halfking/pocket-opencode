<!--
  VaultListView — password vault list. Gated on the cap-keystore plugin;
  shows an unlock/setup screen until the vault is unlocked. See
  docs/2026-07-02-password-vault-design.md.
-->
<template>
  <AppLayout>
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
    <div v-else>
      <div class="toolbar">
        <button class="btn-ghost" @click="showAdd = !showAdd">➕ 新增</button>
        <button class="btn-ghost" @click="generate">🎲 生成密码</button>
        <button class="btn-ghost" @click="cloudSync" :disabled="syncing">
          {{ syncing ? '☁️ 同步中…' : '☁️ 云同步' }}
        </button>
        <button class="btn-ghost" @click="lock">🔒 锁定</button>
      </div>

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

      <div v-if="entries.length === 0" class="state">密码箱为空</div>
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
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { keystore } from '../../native/keystore'
import * as vaultStore from './vault-store'
import * as syncStore from './sync-store'
import { isCryptoReady } from '../../native/crypto'
import type { VaultEntryMeta } from './vault-store'

const initialized = ref(false)
const unlocked = ref(false)
const initError = ref('')
const master = ref('')
const entries = ref<VaultEntryMeta[]>([])
const showAdd = ref(false)
const syncing = ref(false)
const syncStatus = ref<{ type: 'ok' | 'err'; msg: string } | null>(null)

const router = useRouter()

const newEntry = reactive({
  title: '', username: '', url: '', category: 'login', password: '', notes: '',
})

async function probe() {
  try {
    initialized.value = await keystore.isVaultInitialized()
  } catch {
    // cap-keystore 不可用（Web/dev）：用本地 crypto 降级
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
    initError.value = e.message
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
    initError.value = e.message
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
    alert('已生成并复制（30秒后剪贴板自动清空）')
  } catch {
    // cap-keystore 不可用：用 Web Crypto 生成
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*'
    const arr = new Uint32Array(20)
    crypto.getRandomValues(arr)
    const pwd = Array.from(arr, (n) => chars[n % chars.length]).join('')
    await navigator.clipboard.writeText(pwd).catch(() => {})
    alert('已生成并复制（30秒后剪贴板自动清空）')
  }
}

async function saveNew() {
  if (!newEntry.title) { alert('请输入标题'); return }
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
    syncStatus.value = { type: 'err', msg: `同步失败: ${e.message || e}` }
  } finally {
    syncing.value = false
  }
}

const categoryIcon = (c?: string | null) =>
  ({ login: '🔑', card: '💳', note: '🗒', identity: '🪪' }[c || 'login'] || '🔑')

onMounted(probe)
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
.btn-primary { background: var(--brand-gradient); color: white; border: none; padding: var(--space-3); border-radius: var(--radius-md); font-weight: 600; cursor: pointer; }
.btn-bio { background: var(--bg-card); color: var(--brand-primary); border: 1px solid var(--brand-primary); padding: var(--space-3); border-radius: var(--radius-md); font-weight: 600; cursor: pointer; }
.error { color: var(--danger); font-size: 13px; text-align: center; }
.add-form {
  display: flex; flex-direction: column; gap: var(--space-2);
  margin-bottom: var(--space-3); padding: var(--space-3);
  background: var(--bg-elevated); border-radius: var(--radius-md);
}
.add-form input, .add-form select, .add-form textarea {
  padding: var(--space-2); border-radius: var(--radius-sm);
  border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary); font-size: 14px;
}
.add-form textarea { resize: vertical; min-height: 60px; }
.sync-status {
  margin-bottom: var(--space-3); padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm); font-size: 13px;
}
.sync-status.ok { background: rgba(34,197,94,0.12); color: #16a34a; }
.sync-status.err { background: rgba(239,68,68,0.12); color: var(--danger); }
.toolbar { display: flex; gap: var(--space-2); margin-bottom: var(--space-3); }
.btn-ghost { background: var(--bg-card); border: 1px solid var(--border); color: var(--text-primary); padding: var(--space-2) var(--space-3); border-radius: var(--radius-md); font-size: 13px; cursor: pointer; }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.entry-list { display: flex; flex-direction: column; gap: var(--space-2); }
.entry-card { display: flex; align-items: center; gap: var(--space-3); background: var(--bg-card); padding: var(--space-3); border-radius: var(--radius-md); cursor: pointer; box-shadow: var(--shadow-sm); }
.entry-icon { font-size: 22px; }
.entry-body { flex: 1; }
.entry-title { font-weight: 600; font-size: 14px; }
.entry-user { color: var(--text-secondary); font-size: 12px; }
.arrow { color: var(--text-muted); }
</style>
