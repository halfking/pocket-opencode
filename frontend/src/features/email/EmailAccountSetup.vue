<!--
  EmailAccountSetup — email account list + add wizard.
  Uses emailApi.addAccount / syncNow for the cloud-side happy path; falls back
  to localDB-backed emailsStore.saveAccount (when no backend) so it still works.

  Note: prompts 6 will add emailsStore.getAccount — current list relies on
  listAccounts() only.
-->
<template>
      <div class="header-row">
      <h2 class="page-title">邮箱账户</h2>
      <button class="add-toggle" @click="showForm = !showForm">
        {{ showForm ? '收起' : '＋ 添加' }}
      </button>
    </div>

    <!-- 已有账户列表 -->
    <div v-if="loading" class="state-wrap"><Skeleton :count="2" /></div>
    <EmptyState
      v-else-if="accounts.length === 0 && !showForm"
      icon="📬"
      title="尚未添加邮箱账户"
      hint="点击右上角「＋ 添加」配置 IMAP"
      size="sm"
      variant="inline"
      action-label="添加账户"
      @action="showForm = true"
    />

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
            <span class="hint-inline">{{ editId ? '（留空表示不变更）' : '（明文传输，TLS 加密）' }}</span>
          </span>
          <input
            v-model="form.credential"
            type="password"
            class="input"
            autocomplete="new-password"
          />
        </label>

        <!-- SMTP 出站配置：可选。留空则不配置发信，/test-smtp 会返回未配置。 -->
        <div class="section-divider">
          <span class="section-title">SMTP 发信（可选）</span>
          <span class="section-hint">仅在需要发信 / 假期自动回复时填写</span>
        </div>
        <label class="field">
          <span class="field-label">SMTP 主机</span>
          <input
            v-model="form.smtpHost"
            class="input"
            placeholder="例如：smtp.gmail.com"
            autocomplete="off"
          />
        </label>
        <label class="field">
          <span class="field-label">
            SMTP 端口
            <span class="hint-inline">（465 隐式 TLS / 587 STARTTLS）</span>
          </span>
          <input v-model.number="form.smtpPort" type="number" class="input" />
        </label>
        <label class="field">
          <span class="field-label">
            SMTP 密码
            <span class="hint-inline">
              {{ editId ? '（留空表示不变更）' : '（不填则不设置发信凭证）' }}
            </span>
          </span>
          <input
            v-model="form.smtpCredential"
            type="password"
            class="input"
            :disabled="form.clearSmtpCredential"
            autocomplete="new-password"
          />
        </label>
        <label v-if="editId && smtpConfigured" class="checkbox-field">
          <input v-model="form.clearSmtpCredential" type="checkbox" />
          <span>清空已保存的 SMTP 密码</span>
        </label>

        <div v-if="formError" class="error">{{ formError }}</div>
        <div v-if="testMsg" :class="['toast', testOk ? 'ok' : 'err']">{{ testMsg }}</div>

        <div class="form-actions">
          <button class="ghost-btn" @click="cancelEdit">取消</button>
          <!--
            /test-smtp 读的是库里已保存的配置，所以只有已存在的云端账户才能探测。
            新建账户请先保存，再回到编辑态测试。
          -->
          <button
            v-if="editId && isCloudAccount"
            class="ghost-btn"
            :disabled="testing || smtpTesting"
            @click="onTestSmtp"
          >
            {{ smtpTesting ? '探测中…' : '测试 SMTP' }}
          </button>
          <!--
            新建走 addAccount + syncNow（syncNow 会真正连一次 IMAP），所以叫"测试连接并保存"；
            编辑只是 PUT，不触发任何连接探测，标签如实反映为"保存"。
          -->
          <button class="primary-btn" :disabled="testing || smtpTesting" @click="testAndSave">
            {{ saveButtonLabel }}
          </button>
        </div>
      </div>
    </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Skeleton, EmptyState } from '../../components'
import * as emailsStore from './emails-store'
import type { EmailAccount } from './emails-store'
import { emailApi } from '../../api/email'
import type { EmailAccount as ApiEmailAccount, EmailCredentialInput } from '../../api/email'
import { ApiError } from '../../api/http'
import { useConfirm } from '../../composables/useConfirm'

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
const { confirm } = useConfirm()

const showForm = ref(false)
const testing = ref(false)
const formError = ref('')
const testMsg = ref('')
const testOk = ref(false)
const smtpTesting = ref(false)
const editId = ref<string | null>(null)

const form = reactive({
  displayName: '',
  emailAddress: '',
  imapHost: '',
  imapPort: 993 as number,
  credential: '',
  smtpHost: '',
  smtpPort: 587 as number,
  smtpCredential: '',
  clearSmtpCredential: false,
})

/**
 * 云端账户快照，按 id 索引。
 *
 * 本地 store 的 EmailAccount 没有 SMTP 字段（SMTP 只在服务端生效），所以编辑
 * 态要预填 smtpHost/smtpPort 必须拿云端那份。同时它也用来判断某个账户是不是
 * 云端账户——只有云端账户能调 PUT / test-smtp。
 */
const cloudAccounts = ref<Map<string, ApiEmailAccount>>(new Map())

const isCloudAccount = computed(() => !!editId.value && cloudAccounts.value.has(editId.value))
const smtpConfigured = computed(() => {
  if (!editId.value) return false
  return !!cloudAccounts.value.get(editId.value)?.smtpHost
})
const saveButtonLabel = computed(() => {
  if (testing.value) return editId.value ? '保存中…' : '测试连接中…'
  return editId.value ? '保存' : '测试连接并保存'
})

/**
 * 合并云端与本地账户。
 *
 * 之前这里是「本地优先，失败才查云端」，但 listAccounts() 在本地库为空时返回
 * []（不抛异常），而云端创建的账户从不写本地库——只有 addAccount 失败后的本地
 * 回落路径才写。结果云端账户永远不出现在列表里，也就没有编辑入口，SMTP 编辑
 * 态对真正支持 SMTP 的那批账户完全不可达。
 *
 * 现在两边都取并按 id 合并，云端优先（服务端才是云账户的权威来源，且只有它
 * 带 SMTP 字段）。
 */
async function loadList() {
  loading.value = true
  await refreshCloudSnapshot()
  const merged = new Map<string, EmailAccount>()
  try {
    for (const a of await emailsStore.listAccounts()) {
      merged.set(a.id, a)
    }
  } catch (e) {
    console.warn('[email] 列出本地账户失败，仅显示云端账户:', e)
  }
  for (const a of cloudAccounts.value.values()) {
    merged.set(a.id, toLocal(a))
  }
  // 按创建时间排序，避免顺序随两个来源的合并次序漂移。
  accounts.value = [...merged.values()].sort((a, b) => a.createdAt - b.createdAt)
  loading.value = false
}

async function refreshCloudSnapshot() {
  try {
    const r = await emailApi.listAccounts()
    cloudAccounts.value = new Map((r.accounts || []).map((a) => [a.id, a]))
  } catch (e) {
    // 404 = 后端未实现该路由（纯本地模式），静默降级；其它错误留个警告。
    if (!(e instanceof ApiError && e.status === 404)) {
      console.warn('[email] 拉取云端账户失败，SMTP 预填不可用:', e)
    }
    cloudAccounts.value = new Map()
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
  form.smtpHost = ''
  form.smtpPort = 587
  form.smtpCredential = ''
  form.clearSmtpCredential = false
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
  // SMTP host/port 可以回显（非敏感）；凭证不回显，与 IMAP 密码同样处理。
  const cloud = cloudAccounts.value.get(a.id)
  form.smtpHost = cloud?.smtpHost ?? ''
  form.smtpPort = cloud?.smtpPort || 587
  form.smtpCredential = ''
  form.clearSmtpCredential = false
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
  if (!Number.isInteger(form.imapPort) || form.imapPort < 1 || form.imapPort > 65535) {
    return 'IMAP 端口需在 1-65535 之间'
  }
  // 编辑已有账户时 credential 可选（保持原密码不变）。
  // 新增账户必须输入密码或应用专用密码——这是后端的强制契约。
  if (!editId.value && !form.credential) return '请填写密码 / 应用专用密码'
  // SMTP 是可选块：填了 host 才校验 port；反过来只填 port/密码而没 host，
  // 后端会拒绝（smtpHost required），所以这里提前拦住给出更清楚的提示。
  if (form.smtpHost) {
    if (!Number.isInteger(form.smtpPort) || form.smtpPort < 1 || form.smtpPort > 65535) {
      return 'SMTP 端口需在 1-65535 之间'
    }
  } else if (form.smtpCredential) {
    return '填写 SMTP 密码前请先填写 SMTP 主机'
  }
  return null
}

/**
 * 构造 PUT 的 SMTP 部分。后端约定：只有携带 smtpHost 才会写 SMTP 列。
 *
 *   - host 非空                → 写 host+port，凭证按下面规则
 *   - host 清空且原本配过      → 传 ''，把 SMTP 配置置空
 *   - host 清空且原本也没配    → 完全不带 SMTP 字段，避免无意义写入
 *
 * 凭证：勾了"清空"传 ''（后端识别为清空），填了新值传新值，都没有则省略以保留原凭证。
 */
function buildSmtpPatch(): Partial<ApiEmailAccount> & EmailCredentialInput {
  const host = form.smtpHost.trim()
  if (!host) {
    return smtpConfigured.value ? { smtpHost: '', smtpPort: 0, smtpPassword: '' } : {}
  }
  const patch: Partial<ApiEmailAccount> & EmailCredentialInput = {
    smtpHost: host,
    smtpPort: form.smtpPort,
  }
  if (form.clearSmtpCredential) {
    patch.smtpPassword = ''
  } else if (form.smtpCredential) {
    patch.smtpPassword = form.smtpCredential
  }
  return patch
}

function describeSaveResult(): string {
  const parts: string[] = [form.credential ? '已更新（含新 IMAP 凭证）' : '已更新（IMAP 密码未改动）']
  const host = form.smtpHost.trim()
  if (!host && smtpConfigured.value) {
    parts.push('已清空 SMTP 配置')
  } else if (host) {
    if (form.clearSmtpCredential) parts.push('已清空 SMTP 密码')
    else if (form.smtpCredential) parts.push('已更新 SMTP 密码')
    else parts.push('SMTP 主机/端口已保存')
  }
  return `${parts.join('；')}。`
}

/**
 * 探测 SMTP。/test-smtp 读的是库里已保存的配置，所以先保存再探测，
 * 否则用户改了输入框却测到旧配置，结果具有误导性。
 */
async function onTestSmtp() {
  if (!editId.value) return
  formError.value = ''
  testMsg.value = ''
  const err = validate()
  if (err) {
    formError.value = err
    return
  }
  if (!form.smtpHost.trim()) {
    formError.value = '请先填写 SMTP 主机再测试'
    return
  }
  smtpTesting.value = true
  try {
    await emailApi.updateAccount(editId.value, buildSmtpPatch())
    await refreshCloudSnapshot()
    const r = await emailApi.testSmtp(editId.value)
    testOk.value = true
    testMsg.value = `SMTP 连接成功：${r.smtp}`
    form.smtpCredential = ''
    form.clearSmtpCredential = false
  } catch (e) {
    testOk.value = false
    testMsg.value = e instanceof ApiError
      ? `SMTP 测试失败：HTTP ${e.status} ${e.message}`
      : `SMTP 测试失败：${e instanceof Error ? e.message : '未知错误'}`
  } finally {
    smtpTesting.value = false
  }
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
      // 编辑模式：仅当用户输入了新密码才附带；其它元数据走 PATCH 语义。
      const patch: Partial<ApiEmailAccount> & EmailCredentialInput = {
        displayName: form.displayName,
        emailAddress: form.emailAddress,
        imapHost: form.imapHost,
        imapPort: form.imapPort,
      }
      if (form.credential) {
        patch.password = form.credential
      }
      Object.assign(patch, buildSmtpPatch())
      await emailApi.updateAccount(editId.value, patch)
      testOk.value = true
      testMsg.value = describeSaveResult()
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
          password: form.credential,
          // SMTP 可选；后端要求「有 port/密码必须有 host」，validate() 已保证。
          ...(form.smtpHost
            ? {
                smtpHost: form.smtpHost,
                smtpPort: form.smtpPort,
                ...(form.smtpCredential ? { smtpPassword: form.smtpCredential } : {}),
              }
            : {}),
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
  const ok = await confirm({ title: '删除账户', message: `确认删除账户 ${a.displayName}（${a.emailAddress}）？`, confirmText: '删除', danger: true })
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
    authType: String(a.authType), // API 是 AuthType enum，本地 store 是 string
    syncIntervalMin: a.syncIntervalMin,
    lastSyncedUid: null, // 本地 store 字段，API 无此字段
    // 后端发的是 Unix 秒；本地 store 与 formatTime 都用毫秒。
    // 之前这里按 ISO 字符串 Date.parse 解析，对数字必然得到 NaN——只是云端账户
    // 此前从不出现在列表里，这个 bug 一直没显形。
    lastSyncedAt: a.lastSyncedAt ? a.lastSyncedAt * 1000 : null,
    enabled: a.enabled,
    createdAt: a.createdAt ? a.createdAt * 1000 : Date.now(),
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
.tpl-btn.selected { border-color: var(--brand-primary); background: var(--brand-bg); }
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
.input:disabled { opacity: 0.5; cursor: not-allowed; }

.section-divider {
  display: flex; flex-direction: column; gap: 2px;
  padding-top: var(--space-2);
  border-top: 1px solid var(--border);
}
.section-title { font-size: 13px; font-weight: 600; color: var(--text-primary); }
.section-hint { font-size: 11px; color: var(--text-muted); }
.checkbox-field {
  display: flex; align-items: center; gap: var(--space-2);
  font-size: 12px; color: var(--text-secondary);
}

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
  font-size: var(--text-sm);
  color: var(--danger);
  padding: var(--space-2);
  background: var(--danger-bg);
  border-radius: var(--radius-sm);
}
.toast {
  font-size: var(--text-sm);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
}
.toast.ok { color: var(--success); background: var(--success-bg); }
.toast.err { color: var(--danger); background: var(--danger-bg); }
.state-wrap { padding: var(--space-2) 0; }
</style>
