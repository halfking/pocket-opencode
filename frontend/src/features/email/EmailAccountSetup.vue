<!--
  EmailAccountSetup — email account list + add wizard.
  Uses emailApi.addAccount / syncNow for the cloud-side happy path; falls back
  to localDB-backed emailsStore.saveAccount (when no backend) so it still works.

  Note: prompts 6 will add emailsStore.getAccount — current list relies on
  listAccounts() only.
-->
<template>
  <AppLayout>
    <div class="header-row">
      <h2 class="page-title">邮箱账户</h2>
      <button class="add-toggle" @click="showForm = !showForm">
        {{ showForm ? '收起' : '＋ 添加' }}
      </button>
    </div>

    <!-- 已有账户列表 -->
    <div v-if="loading" class="state">加载中…</div>
    <div v-else-if="accounts.length === 0 && !showForm" class="state">
      <p>尚未添加邮箱账户。</p>
      <p class="hint">点击右上角"＋ 添加"配置 IMAP。</p>
    </div>

    <div v-else class="account-list">
      <div v-for="a in accounts" :key="a.id" class="account-card">
        <div class="acct-main">
          <div class="acct-name">{{ a.displayName }}</div>
          <div class="acct-addr">{{ a.emailAddress }}</div>
          <div class="acct-meta">
            <span class="host">{{ a.imapHost }}:{{ a.imapPort }}</span>
            <span class="sep">·</span>
            <span class="sync">
              {{ a.lastSyncedAt ? `上次同步 ${formatTime(a.lastSyncedAt)}` : '尚未同步' }}
            </span>
          </div>
        </div>
        <div class="acct-actions">
          <button class="icon-btn" @click="editAccount(a)" aria-label="编辑">✎</button>
          <button class="icon-btn danger" @click="onDelete(a)" aria-label="删除">🗑</button>
        </div>
      </div>
    </div>

    <!-- 添加账户向导 -->
    <section v-if="showForm" class="wizard">
      <h3 class="form-title">{{ editId ? '编辑账户' : '添加账户' }}</h3>

      <div v-if="!editId" class="templates">
        <div class="templates-label">选择邮箱类型（预设 IMAP）</div>
        <div class="template-grid">
          <button
            v-for="t in templates"
            :key="t.id"
            class="tpl-btn"
            :class="{ selected: form.imapHost === t.host }"
            @click="applyTemplate(t)"
          >
            <div class="tpl-icon">{{ t.icon }}</div>
            <div class="tpl-name">{{ t.label }}</div>
            <div class="tpl-host">{{ t.host }}</div>
          </button>
        </div>
      </div>

      <div class="form-fields">
        <label class="field">
          <span class="field-label">显示名</span>
          <input
            v-model="form.displayName"
            placeholder="例如：工作邮箱"
            class="input"
            autocomplete="off"
          />
        </label>
        <label class="field">
          <span class="field-label">邮箱地址</span>
          <input
            v-model="form.emailAddress"
            type="email"
            placeholder="you@example.com"
            class="input"
            autocomplete="off"
          />
        </label>
        <label class="field">
          <span class="field-label">IMAP 主机</span>
          <input v-model="form.imapHost" class="input" autocomplete="off" />
        </label>
        <label class="field">
          <span class="field-label">端口</span>
          <input v-model.number="form.imapPort" type="number" class="input" />
        </label>
        <label class="field">
          <span class="field-label">
            密码 / 应用专用密码
            <span class="hint-inline">（明文传输，TLS 加密）</span>
          </span>
          <input
            v-model="form.credential"
            type="password"
            class="input"
            autocomplete="new-password"
          />
        </label>

        <div v-if="formError" class="error">{{ formError }}</div>
        <div v-if="testMsg" :class="['toast', testOk ? 'ok' : 'err']">{{ testMsg }}</div>

        <div class="form-actions">
          <button class="ghost-btn" @click="cancelEdit">取消</button>
          <button class="primary-btn" :disabled="testing" @click="testAndSave">
            {{ testing ? '测试连接中…' : '测试连接并保存' }}
          </button>
        </div>
      </div>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import AppLayout from '../../app/AppLayout.vue'
import * as emailsStore from './emails-store'
import type { EmailAccount } from './emails-store'
import { emailApi } from '../../api/email'
import type { EmailAccount as ApiEmailAccount } from '../../api/email'
import { ApiError } from '../../api/http'

interface ImapTemplate {
  id: string
  label: string
  icon: string
  host: string
  port: number
}

const templates: ImapTemplate[] = [
  { id: 'gmail', label: 'Gmail', icon: '📧', host: 'imap.gmail.com', port: 993 },
  { id: 'qq', label: 'QQ 邮箱', icon: '🐧', host: 'imap.qq.com', port: 993 },
  { id: '163', label: '163 邮箱', icon: '🟠', host: 'imap.163.com', port: 993 },
  { id: 'outlook', label: 'Outlook', icon: '🟦', host: 'outlook.office365.com', port: 993 },
]

const accounts = ref<EmailAccount[]>([])
const loading = ref(true)

const showForm = ref(false)
const testing = ref(false)
const formError = ref('')
const testMsg = ref('')
const testOk = ref(false)
const editId = ref<string | null>(null)

const form = reactive({
  displayName: '',
  emailAddress: '',
  imapHost: '',
  imapPort: 993 as number,
  credential: '',
})

async function loadList() {
  loading.value = true
  try {
    accounts.value = await emailsStore.listAccounts()
  } catch (e) {
    console.warn('[email] 列出本地账户失败，尝试云端:', e)
    try {
      const r = await emailApi.listAccounts()
      accounts.value = (r.accounts || []).map(toLocal)
    } catch (e2) {
      if (e2 instanceof ApiError && e2.status === 404) {
        accounts.value = []
      } else {
        console.warn('[email] 云端账户列表也失败:', e2)
        accounts.value = []
      }
    }
  } finally {
    loading.value = false
  }
}

function applyTemplate(t: ImapTemplate) {
  form.imapHost = t.host
  form.imapPort = t.port
  // 预设显示名（邮箱地址还没填就跳过）
  if (!form.displayName && form.emailAddress) {
    form.displayName = inferDisplayName(form.emailAddress)
  }
}

function inferDisplayName(addr: string) {
  if (!addr) return ''
  if (addr.includes('@gmail.com')) return 'Gmail'
  if (addr.includes('@qq.com')) return 'QQ 邮箱'
  if (addr.includes('@163.com')) return '163 邮箱'
  if (addr.includes('@outlook.com') || addr.includes('@hotmail.com')) return 'Outlook'
  return ''
}

function resetForm() {
  form.displayName = ''
  form.emailAddress = ''
  form.imapHost = ''
  form.imapPort = 993
  form.credential = ''
  formError.value = ''
  testMsg.value = ''
  testOk.value = false
  editId.value = null
}

function editAccount(a: EmailAccount) {
  editId.value = a.id
  form.displayName = a.displayName
  form.emailAddress = a.emailAddress
  form.imapHost = a.imapHost
  form.imapPort = a.imapPort
  form.credential = '' // 已有账户不反查密码
  formError.value = ''
  testMsg.value = ''
  showForm.value = true
}

function cancelEdit() {
  resetForm()
  showForm.value = false
}

function validate(): string | null {
  if (!form.emailAddress) return '请填写邮箱地址'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.emailAddress)) return '邮箱地址格式不正确'
  if (!form.imapHost) return '请选择 IMAP 模板或手动填写主机'
  if (!form.credential) return '请填写密码 / 应用专用密码'
  return null
}

async function testAndSave() {
  formError.value = ''
  testMsg.value = ''
  const err = validate()
  if (err) {
    formError.value = err
    return
  }
  testing.value = true
  try {
    if (editId.value) {
      // 编辑模式：调用云端 updateAccount
      await emailApi.updateAccount(editId.value, {
        displayName: form.displayName,
        emailAddress: form.emailAddress,
        imapHost: form.imapHost,
        imapPort: form.imapPort,
      } as Partial<ApiEmailAccount>)
      testOk.value = true
      testMsg.value = '已更新。'
    } else {
      // 新增：尝试云端 addAccount + syncNow，回落到本地 saveAccount
      try {
        await emailApi.addAccount({
          displayName: form.displayName,
          emailAddress: form.emailAddress,
          imapHost: form.imapHost,
          imapPort: form.imapPort,
          authType: 'password',
          syncIntervalMin: 15,
          enabled: true,
          credential: form.credential,
        })
        await emailApi.syncNow()
        testOk.value = true
        testMsg.value = '连接成功，已开始首次同步。'
      } catch (cloudErr) {
        // 后端 stub/暂未实现 → 落本地存储
        if (cloudErr instanceof ApiError && (cloudErr.status === 404 || cloudErr.status >= 500)) {
          await emailsStore.saveAccount({
            displayName: form.displayName,
            emailAddress: form.emailAddress,
            imapHost: form.imapHost,
            imapPort: form.imapPort,
            password: form.credential,
          })
          testOk.value = true
          testMsg.value = '已保存到本地（云端暂未实现）。'
        } else {
          throw cloudErr
        }
      }
    }
    await loadList()
    resetForm()
    showForm.value = false
  } catch (e) {
    testOk.value = false
    if (e instanceof ApiError) {
      testMsg.value = `连接失败：HTTP ${e.status} ${e.message}`
    } else if (e instanceof Error) {
      testMsg.value = `连接失败：${e.message}`
    } else {
      testMsg.value = '连接失败：未知错误'
    }
  } finally {
    testing.value = false
  }
}

async function onDelete(a: EmailAccount) {
  const ok = confirm(`确认删除账户 ${a.displayName}（${a.emailAddress}）？`)
  if (!ok) return
  try {
    await emailApi.deleteAccount(a.id)
  } catch (e) {
    if (!(e instanceof ApiError) || e.status !== 404) {
      console.warn('[email] 云端删除失败，继续本地删除:', e)
    }
  }
  try {
    await emailsStore.deleteAccount(a.id)
  } catch (e) {
    console.warn('[email] 本地删除失败:', e)
  }
  await loadList()
}

function toLocal(a: ApiEmailAccount): EmailAccount {
  return {
    id: a.id,
    displayName: a.displayName,
    emailAddress: a.emailAddress,
    imapHost: a.imapHost,
    imapPort: a.imapPort,
    authType: a.authType,
    syncIntervalMin: a.syncIntervalMin,
    lastSyncedUid: null,
    lastSyncedAt: a.lastSyncedAt ? Date.parse(a.lastSyncedAt) : null,
    enabled: a.enabled,
    createdAt: Date.now(),
  }
}

function formatTime(ms: number | null) {
  if (!ms) return ''
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  const diff = Date.now() - ms
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}小时前`
  return `${d.getMonth() + 1}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

onMounted(loadList)
</script>

<style scoped>
.header-row {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: var(--space-3);
}
.page-title { font-size: 18px; font-weight: 600; margin: 0; color: var(--text-primary); }
.add-toggle {
  border: 1px solid var(--brand-primary);
  background: transparent;
  color: var(--brand-primary);
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  font-size: 13px;
  cursor: pointer;
}
.add-toggle:active { background: var(--brand-primary); color: var(--text-inverse); }

.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.hint { font-size: 12px; color: var(--text-muted); margin-top: var(--space-2); }

.account-list { display: flex; flex-direction: column; gap: var(--space-2); }
.account-card {
  display: flex; align-items: flex-start; justify-content: space-between;
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
  box-shadow: var(--shadow-sm);
  gap: var(--space-2);
}
.acct-main { flex: 1; min-width: 0; }
.acct-name { font-weight: 600; color: var(--text-primary); font-size: 14px; }
.acct-addr { font-size: 12px; color: var(--text-secondary); margin-top: 2px; }
.acct-meta { display: flex; gap: var(--space-1); align-items: center; font-size: 11px; color: var(--text-muted); margin-top: var(--space-1); }
.sep { color: var(--border-strong); }
.acct-actions { display: flex; gap: var(--space-1); }
.icon-btn {
  border: none; background: var(--bg-subtle);
  width: 32px; height: 32px; border-radius: var(--radius-sm);
  font-size: 14px; cursor: pointer; color: var(--text-secondary);
}
.icon-btn:active { background: var(--border); }
.icon-btn.danger { color: var(--danger); }

.wizard {
  margin-top: var(--space-4);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
}
.form-title { font-size: 16px; font-weight: 600; margin: 0 0 var(--space-3); color: var(--text-primary); }
.templates-label { font-size: 13px; color: var(--text-secondary); margin-bottom: var(--space-2); }
.template-grid { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-2); }
.tpl-btn {
  border: 1px solid var(--border);
  background: var(--bg-card);
  padding: var(--space-3) var(--space-2);
  border-radius: var(--radius-md);
  cursor: pointer;
  display: flex; flex-direction: column; align-items: center; gap: 2px;
}
.tpl-btn.selected { border-color: var(--brand-primary); background: rgba(102,126,234,0.08); }
.tpl-icon { font-size: 22px; }
.tpl-name { font-size: 13px; font-weight: 600; color: var(--text-primary); }
.tpl-host { font-size: 11px; color: var(--text-muted); }

.form-fields { display: flex; flex-direction: column; gap: var(--space-3); margin-top: var(--space-4); }
.field { display: flex; flex-direction: column; gap: var(--space-1); }
.field-label { font-size: 12px; color: var(--text-secondary); }
.hint-inline { font-size: 11px; color: var(--text-muted); margin-left: var(--space-1); }
.input {
  border: 1px solid var(--border);
  background: var(--bg-base);
  color: var(--text-primary);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  font-size: 14px;
  outline: none;
}
.input:focus { border-color: var(--brand-primary); }

.form-actions { display: flex; gap: var(--space-2); margin-top: var(--space-1); }
.ghost-btn {
  flex: 1;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  font-size: 14px;
  cursor: pointer;
}
.ghost-btn:active { background: var(--bg-subtle); }
.primary-btn {
  flex: 2;
  border: none;
  background: var(--brand-primary);
  color: var(--text-inverse);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}
.primary-btn:disabled { background: var(--text-muted); cursor: not-allowed; }

.error {
  font-size: 12px;
  color: var(--danger);
  padding: var(--space-2);
  background: rgba(239,68,68,0.08);
  border-radius: var(--radius-sm);
}
.toast {
  font-size: 12px;
  padding: var(--space-2);
  border-radius: var(--radius-sm);
}
.toast.ok { color: var(--success); background: rgba(16,185,129,0.08); }
.toast.err { color: var(--danger); background: rgba(239,68,68,0.08); }
</style>
