<!--
  新增邮箱账户：选服务商 → 填邮箱/授权码 → 保存并验证 IMAP+SMTP。
  授权码在官方网页生成，本页只打开说明并接收粘贴。
-->
<template>
  <div class="add-page">
    <header class="page-head">
      <button class="back-btn" type="button" aria-label="返回" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h2 class="page-title">新增邮箱账户</h2>
    </header>

    <p class="steps">{{ stepLabel }}</p>

    <section v-if="step === 1" class="panel">
      <h3>选择邮箱服务商</h3>
      <p class="hint">从服务商开始添加，不要从已有账户里选。</p>
      <button
        v-for="p in EMAIL_PROVIDERS"
        :key="p.id"
        type="button"
        class="prov"
        @click="pickProvider(p.id)"
      >
        <strong>{{ p.label }}</strong>
        <span>{{ p.hint }}</span>
      </button>
    </section>

    <section v-else-if="step === 2" class="panel">
      <button type="button" class="link" @click="step = 1">← 重选服务商（{{ provider.label }}）</button>
      <label class="field">
        <span>邮箱地址</span>
        <input v-model="email" type="email" class="input" autocomplete="username" :placeholder="emailPlaceholder" />
      </label>
      <label class="field">
        <span>显示名（可选）</span>
        <input v-model="displayName" class="input" :placeholder="provider.label" />
      </label>
      <label class="field">
        <span>{{ provider.authCodeRequired ? '客户端授权码 / 专用密码' : 'IMAP 密码' }}</span>
        <input v-model="credential" type="password" class="input" autocomplete="new-password" />
      </label>

      <div v-if="provider.authCodeRequired" class="auth-box">
        <p>这不是网页登录密码。请到服务商网页开启 IMAP 并生成授权码：</p>
        <ol>
          <li v-for="(s, i) in provider.authCodeSteps" :key="i">{{ s }}</li>
        </ol>
        <a
          v-if="provider.authCodeUrl"
          class="ghost"
          :href="provider.authCodeUrl"
          target="_blank"
          rel="noopener noreferrer"
        >打开官方说明 / 设置页</a>
      </div>

      <button
        v-if="!showAdvanced && provider.id !== 'other'"
        type="button"
        class="link"
        @click="showAdvanced = true"
      >改服务器（高级）</button>
      <div v-if="showAdvanced || provider.id === 'other'" class="adv">
        <p class="adv-title">高级：IMAP / SMTP</p>
        <div class="host-port">
          <label class="field">
            <span>IMAP 主机</span>
            <input v-model="imapHost" class="input" placeholder="imap.example.com" autocomplete="off" />
          </label>
          <label class="field">
            <span>IMAP 端口</span>
            <input v-model.number="imapPort" type="number" class="input" min="1" max="65535" />
          </label>
        </div>
        <div class="host-port">
          <label class="field">
            <span>SMTP 主机</span>
            <input v-model="smtpHost" class="input" placeholder="smtp.example.com" autocomplete="off" />
          </label>
          <label class="field">
            <span>SMTP 端口</span>
            <input v-model.number="smtpPort" type="number" class="input" min="1" max="65535" />
          </label>
        </div>
      </div>

      <p v-if="formError" class="err">{{ formError }}</p>
      <button type="button" class="primary" :disabled="busy" @click="saveAndVerify">
        {{ busy ? '正在测试…' : '保存并测试收发' }}
      </button>
    </section>

    <section v-else class="panel">
      <h3>{{ resultOk ? '已添加' : '未完成' }}</h3>
      <p>{{ resultMsg }}</p>
      <p v-if="imapMsg" :class="imapOk ? 'ok' : 'err'">IMAP：{{ imapMsg }}</p>
      <p v-if="smtpMsg" :class="smtpOk ? 'ok' : 'err'">SMTP：{{ smtpMsg }}</p>
      <button type="button" class="primary" @click="goBack">返回邮箱设置</button>
      <button v-if="!resultOk" type="button" class="ghost" @click="step = 2">返回修改</button>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { emailApi } from '../../api/email'
import { ApiError } from '../../api/http'
import {
  EMAIL_PROVIDERS, type EmailProviderId, inferProviderId, providerById,
} from './providers'

const router = useRouter()
const step = ref<1 | 2 | 3>(1)
const providerId = ref<EmailProviderId>('qq')
const provider = computed(() => providerById(providerId.value))
const email = ref('')
const displayName = ref('')
const credential = ref('')
const imapHost = ref('')
const imapPort = ref(993)
const smtpHost = ref('')
const smtpPort = ref(465)
const showAdvanced = ref(false)
const busy = ref(false)
const formError = ref('')
const resultOk = ref(false)
const resultMsg = ref('')
const imapOk = ref(false)
const imapMsg = ref('')
const smtpOk = ref(false)
const smtpMsg = ref('')

const stepLabel = computed(() => ['', '1/3 选择服务商', '2/3 填写并获取授权码', '3/3 测试结果'][step.value])
const emailPlaceholder = computed(() => provider.value.domains[0] ? `you@${provider.value.domains[0]}` : 'you@example.com')

function goBack() {
  router.push('/email/settings')
}

function applyProvider(id: EmailProviderId) {
  const p = providerById(id)
  providerId.value = id
  imapHost.value = p.imapHost
  imapPort.value = p.imapPort
  smtpHost.value = p.smtpHost
  smtpPort.value = p.smtpPort
  showAdvanced.value = id === 'other'
}

function pickProvider(id: EmailProviderId) {
  applyProvider(id)
  step.value = 2
}

function validate(): string | null {
  if (!email.value.trim()) return '请填写邮箱地址'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value.trim())) return '邮箱地址格式不正确'
  if (!credential.value.trim()) return '请填写授权码或密码'
  if (!imapHost.value.trim()) return '请填写 IMAP 主机'
  return null
}

async function saveAndVerify() {
  formError.value = ''
  const guessed = inferProviderId(email.value)
  if (guessed !== 'other' && guessed !== providerId.value) {
    applyProvider(guessed)
  }
  const err = validate()
  if (err) { formError.value = err; return }
  busy.value = true
  try {
    const created = await emailApi.addAccount({
      displayName: displayName.value.trim() || provider.value.label,
      emailAddress: email.value.trim(),
      imapHost: imapHost.value.trim(),
      imapPort: imapPort.value,
      smtpHost: smtpHost.value.trim() || undefined,
      smtpPort: smtpHost.value.trim() ? smtpPort.value : undefined,
      authType: 'password',
      syncIntervalMin: 15,
      enabled: true,
      password: credential.value.trim(),
      smtpPassword: credential.value.trim(),
    })
    try {
      const sync = await emailApi.syncNow(created.id)
      imapOk.value = true
      imapMsg.value = `同步成功，新邮件 ${sync.new ?? 0} 封`
    } catch (e) {
      imapOk.value = false
      imapMsg.value = e instanceof ApiError ? e.message : (e instanceof Error ? e.message : 'IMAP 失败')
    }
    if (smtpHost.value.trim()) {
      try {
        const smtp = await emailApi.testSmtp(created.id)
        smtpOk.value = true
        smtpMsg.value = smtp.smtp
      } catch (e) {
        smtpOk.value = false
        smtpMsg.value = e instanceof ApiError ? e.message : (e instanceof Error ? e.message : 'SMTP 失败')
      }
    } else {
      smtpOk.value = true
    }
    resultOk.value = imapOk.value && smtpOk.value
    resultMsg.value = resultOk.value
      ? `已保存并验证 ${created.emailAddress}`
      : `已保存 ${created.emailAddress}，但连接未全部通过`
    step.value = 3
  } catch (e) {
    formError.value = e instanceof ApiError ? `保存失败：${e.message}` : (e instanceof Error ? e.message : '保存失败')
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.add-page {
  flex: 1; min-height: 0; height: 100%;
  overflow-y: auto; -webkit-overflow-scrolling: touch;
  padding: var(--space-3) var(--space-4) var(--space-6);
  box-sizing: border-box;
}
.page-head {
  position: sticky; top: 0; z-index: 2;
  display: flex; align-items: center; gap: var(--space-2);
  margin: calc(-1 * var(--space-3)) calc(-1 * var(--space-4)) var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-base);
}
.back-btn { border: 0; background: transparent; color: var(--text-primary); padding: 4px; }
.page-title { margin: 0; font-size: 18px; }
.steps { color: var(--text-muted); font-size: 12px; }
.panel { display: flex; flex-direction: column; gap: var(--space-2); }
.hint, .auth-box p { margin: 0; color: var(--text-secondary); font-size: 13px; }
.prov {
  text-align: left; border: 1px solid var(--border); background: var(--bg-card);
  border-radius: var(--radius-md); padding: var(--space-3); cursor: pointer;
}
.prov strong { display: block; }
.prov span { font-size: 12px; color: var(--text-muted); }
.field { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--text-secondary); }
.input { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: var(--space-2); background: var(--bg-base); color: var(--text-primary); }
.auth-box { background: var(--bg-subtle); border-radius: var(--radius-md); padding: var(--space-3); font-size: 13px; }
.auth-box ol { margin: var(--space-2) 0; padding-left: 1.2rem; }
.adv { display: flex; flex-direction: column; gap: var(--space-2); font-size: 13px; }
.adv-title { margin: 0; font-weight: 600; color: var(--text-primary); }
.host-port { display: grid; grid-template-columns: 1fr 5.5rem; gap: var(--space-2); }
.link, .ghost, .primary { border-radius: var(--radius-md); padding: var(--space-2) var(--space-3); cursor: pointer; }
.link { border: 0; background: none; color: var(--brand-primary); text-align: left; }
.ghost { border: 1px solid var(--border); background: var(--bg-card); color: inherit; text-decoration: none; display: inline-block; }
.primary { border: 0; background: var(--brand-primary); color: var(--text-inverse); font-weight: 600; }
.primary:disabled { background: var(--text-muted); }
.err { color: var(--danger); }
.ok { color: var(--success); }
</style>
