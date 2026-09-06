<!--
  EmailSettingsView — 邮箱设置（标题栏 ⚙ 入口落地页）。

  三个分区：
    1. 账户管理：列表 + 启用开关 + 立即同步 / 测试 SMTP / 删除；
       新增与完整编辑复用既有向导页（/email/accounts）。
    2. 过滤策略：每账户可视化规则编辑器（对齐后端 rules/engine.go 的
       6 类型 / 5 动作），保存序列化为新格式 JSON 写回账户。
    3. 处理逻辑：同步间隔、自动回复（vacation）、延迟动作队列说明。

  路由：/email/settings（hideAppHeader 自管页）
-->
<template>
  <div class="email-settings">
    <header class="page-head">
      <button class="back-btn" type="button" aria-label="返回" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h2 class="page-title">邮箱设置</h2>
      <button class="reload-btn" type="button" aria-label="刷新" @click="loadAll">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </header>

    <div v-if="loading" class="state">加载中…</div>
    <div v-else-if="loadError" class="state error">
      {{ loadError }}
      <button class="link-btn" @click="loadAll">重试</button>
    </div>

    <main v-else class="sections">
      <!-- ── 1. 账户管理 ── -->
      <section class="card">
        <div class="card-head">
          <h3>账户</h3>
          <button class="primary-btn" type="button" @click="router.push('/email/accounts')">
            + 新增 / 管理账户
          </button>
        </div>
        <EmptyState
          v-if="accounts.length === 0"
          icon="📧"
          title="尚未配置邮箱账户"
          hint="添加 IMAP 账户后即可同步邮件"
          size="sm"
          variant="inline"
        />
        <div v-for="a in accounts" :key="a.id" class="acct-row">
          <div class="acct-main">
            <div class="acct-name">{{ a.displayName }}</div>
            <div class="acct-sub">{{ a.emailAddress }} · {{ a.imapHost }}:{{ a.imapPort }}</div>
            <div class="acct-sub muted">
              同步间隔 {{ a.syncIntervalMin }} 分钟 · 上次同步 {{ fmtTime(a.lastSyncedAt) }}
            </div>
          </div>
          <div class="acct-actions">
            <label class="switch">
              <input
                type="checkbox"
                :checked="a.enabled"
                :aria-label="`${a.displayName} 启用`"
                @change="toggleEnabled(a, ($event.target as HTMLInputElement).checked)"
              />
              <span>{{ a.enabled ? '已启用' : '已停用' }}</span>
            </label>
            <button class="mini-btn" type="button" :disabled="syncingId === a.id" @click="syncNow(a)">
              {{ syncingId === a.id ? '同步中…' : '立即同步' }}
            </button>
            <button class="mini-btn" type="button" :disabled="testingId === a.id" @click="testSmtp(a)">
              {{ testingId === a.id ? '测试中…' : '测试 SMTP' }}
            </button>
            <button class="mini-btn danger" type="button" @click="removeAccount(a)">删除</button>
          </div>
        </div>
      </section>

      <!-- ── 2. 过滤策略 ── -->
      <section class="card">
        <div class="card-head">
          <h3>过滤策略</h3>
          <span class="head-hint">新邮件入库时按顺序匹配，命中即执行动作</span>
        </div>
        <div v-for="a in accounts" :key="a.id" class="rule-block">
          <div class="rule-block-head">
            <span class="rule-acct">{{ a.displayName }}</span>
            <button class="mini-btn" type="button" @click="addRule(a)">+ 添加规则</button>
          </div>
          <p v-if="isLegacyRules(a.rules)" class="legacy-hint">
            该账户仍在使用旧版黑白名单格式，保存后将转换为新版规则格式。
          </p>
          <div v-if="rulesOf(a).length === 0" class="rule-empty">暂无规则</div>
          <div v-for="(r, i) in rulesOf(a)" :key="i" class="rule-row">
            <select v-model="r.type" class="rule-select" aria-label="规则类型">
              <option v-for="t in RULE_TYPES" :key="t.value" :value="t.value">{{ t.label }}</option>
            </select>
            <input
              v-model="r.pattern"
              class="rule-pattern"
              placeholder="模式（支持 *@域名 通配 / 正则）"
              aria-label="规则模式"
            />
            <div class="rule-actions">
              <label v-for="act in RULE_ACTIONS" :key="act.value" class="rule-act">
                <input
                  type="checkbox"
                  :checked="hasAction(r, act.value)"
                  @change="toggleAction(r, act.value, ($event.target as HTMLInputElement).checked)"
                />
                {{ act.label }}
              </label>
              <template v-if="hasAction(r, 'label-category')">
                <select
                  class="rule-param"
                  aria-label="分类"
                  @change="setActionParam(r, 'label-category', 'category', ($event.target as HTMLSelectElement).value)"
                >
                  <option value="">分类…</option>
                  <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
                </select>
              </template>
              <template v-if="hasAction(r, 'route-folder')">
                <input
                  class="rule-param"
                  placeholder="目标文件夹"
                  aria-label="目标文件夹"
                  @change="setActionParam(r, 'route-folder', 'folder', ($event.target as HTMLInputElement).value)"
                />
              </template>
              <button class="mini-btn danger" type="button" :aria-label="`删除规则 ${i + 1}`" @click="rulesOf(a).splice(i, 1)">
                删除
              </button>
            </div>
          </div>
          <button
            class="primary-btn slim"
            type="button"
            :disabled="savingRulesId === a.id"
            @click="saveRules(a)"
          >
            {{ savingRulesId === a.id ? '保存中…' : '保存过滤策略' }}
          </button>
        </div>
      </section>

      <!-- ── 3. 处理逻辑 ── -->
      <section class="card">
        <div class="card-head">
          <h3>处理逻辑</h3>
        </div>

        <div v-for="a in accounts" :key="a.id" class="proc-block">
          <div class="proc-head">{{ a.displayName }}</div>

          <label class="proc-row">
            <span>同步间隔（分钟）</span>
            <input
              class="proc-input"
              type="number"
              min="1"
              :value="a.syncIntervalMin"
              :aria-label="`${a.displayName} 同步间隔`"
              @change="saveInterval(a, ($event.target as HTMLInputElement).value)"
            />
          </label>

          <div class="vacation">
            <div class="vac-head">
              自动回复（休假）
              <label class="switch">
                <input
                  type="checkbox"
                  :checked="vacationOf(a.id)?.enabled"
                  @change="toggleVacation(a, ($event.target as HTMLInputElement).checked)"
                />
                <span>{{ vacationOf(a.id)?.enabled ? '启用' : '停用' }}</span>
              </label>
            </div>
            <div v-if="vacationOf(a.id)" class="vac-form">
              <label class="proc-row">
                <span>开始</span>
                <input
                  class="proc-input"
                  type="datetime-local"
                  :value="toLocalInput(vacationOf(a.id)!.startAt)"
                  @change="patchVacation(a.id, { startAt: fromLocalInput(($event.target as HTMLInputElement).value) })"
                />
              </label>
              <label class="proc-row">
                <span>结束</span>
                <input
                  class="proc-input"
                  type="datetime-local"
                  :value="toLocalInput(vacationOf(a.id)!.endAt)"
                  @change="patchVacation(a.id, { endAt: fromLocalInput(($event.target as HTMLInputElement).value) })"
                />
              </label>
              <label class="proc-row">
                <span>主题</span>
                <input
                  class="proc-input wide"
                  :value="vacationOf(a.id)!.subject"
                  @change="patchVacation(a.id, { subject: ($event.target as HTMLInputElement).value })"
                />
              </label>
              <label class="proc-row">
                <span>正文</span>
                <textarea
                  class="proc-area"
                  rows="3"
                  :value="vacationOf(a.id)!.bodyText"
                  @change="patchVacation(a.id, { bodyText: ($event.target as HTMLTextAreaElement).value })"
                ></textarea>
              </label>
              <button class="primary-btn slim" type="button" :disabled="savingVacId === a.id" @click="saveVacation(a)">
                {{ savingVacId === a.id ? '保存中…' : '保存自动回复' }}
              </button>
            </div>
          </div>
        </div>

        <div class="queue-note">
          <h4>延迟动作队列说明</h4>
          <p>
            过滤策略中的「归档 / 移动到文件夹 / 触发自动回复」不会立即执行，而是写入
            <code>email_action_intents</code> 队列，由后端调度器约每分钟消费一次；
            失败的动作会保留在队列中等待人工处理。
          </p>
        </div>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { emailApi, type EmailAccount, type VacationReply } from '../../api/email'
import type { EmailRuleActionName, EmailRuleActionSpec, EmailRuleEntry } from '../../api/email'
import { parseRules, isLegacyRules, serializeRules } from './rules-format'
import { syncAccountsFromServer } from './account-sync'
import { EmptyState } from '../../components'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'

const router = useRouter()
const toast = useToast()
const { confirm } = useConfirm()

const RULE_TYPES = [
  { value: 'sender-whitelist', label: '发件人白名单' },
  { value: 'sender-blacklist', label: '发件人黑名单' },
  { value: 'subject-keyword', label: '主题关键词' },
  { value: 'domain-match', label: '域名匹配' },
  { value: 'importance-min', label: '最低重要度' },
  { value: 'category-match', label: '分类匹配' },
] as const

const RULE_ACTIONS: { value: EmailRuleActionName; label: string }[] = [
  { value: 'mark-important', label: '标重要' },
  { value: 'label-category', label: '打分类' },
  { value: 'archive', label: '归档' },
  { value: 'route-folder', label: '移文件夹' },
  { value: 'trigger-autoreply', label: '自动回复' },
]

const CATEGORIES = ['work', 'bill', 'notification', 'personal', 'marketing', 'spam']

// ---- 数据 ----
const loading = ref(true)
const loadError = ref('')
const accounts = ref<EmailAccount[]>([])
const vacations = ref<VacationReply[]>([])
const syncingId = ref('')
const testingId = ref('')
const savingRulesId = ref('')
const savingVacId = ref('')

/** 编辑态规则（账户 id → 规则列表）；加载时从账户 rules 解析。 */
const rulesByAccount = ref<Record<string, EmailRuleEntry[]>>({})
/** 编辑态自动回复（增量 patch 用）。 */
const vacDrafts = ref<Record<string, Partial<VacationReply>>>({})

onMounted(loadAll)

async function loadAll() {
  loading.value = true
  loadError.value = ''
  try {
    // LWW：先做服务端→本地镜像（含 updated_at 对齐），再列服务端账户作为权威 UI 数据源。
    const sync = await syncAccountsFromServer()
    const [accRes, vacRes] = await Promise.all([
      emailApi.listAccounts(),
      emailApi.listVacations(),
    ])
    accounts.value = accRes.accounts ?? []
    vacations.value = vacRes.vacations ?? []
    const next: Record<string, EmailRuleEntry[]> = {}
    for (const a of accounts.value) next[a.id] = parseRules(a.rules)
    rulesByAccount.value = next
    vacDrafts.value = {}
    if (sync.error) {
      // 服务端拉取失败时只提示，不影响列表（云端拿不到就吃离线列表，但 settings 必走云端）。
      loadError.value = sync.error
    }
  } catch (e: any) {
    loadError.value = e?.message || '加载邮箱设置失败'
  } finally {
    loading.value = false
  }
}

// ---- 过滤策略（格式转换见 ./rules-format.ts，含旧格式兼容） ----

function rulesOf(a: EmailAccount): EmailRuleEntry[] {
  return rulesByAccount.value[a.id] ?? []
}

function addRule(a: EmailAccount) {
  rulesOf(a).push({ type: 'subject-keyword', pattern: '', actions: [] })
}

function hasAction(r: EmailRuleEntry, name: EmailRuleActionName): boolean {
  return r.actions.some((a) => (typeof a === 'string' ? a === name : a.name === name))
}

function toggleAction(r: EmailRuleEntry, name: EmailRuleActionName, on: boolean) {
  const others = r.actions.filter((a) => (typeof a === 'string' ? a !== name : a.name !== name))
  if (on) {
    if (name === 'label-category') r.actions = [...others, { name, category: 'work' }]
    else if (name === 'route-folder') r.actions = [...others, { name, folder: '' }]
    else r.actions = [...others, name]
  } else {
    r.actions = others
  }
}

function setActionParam(r: EmailRuleEntry, name: EmailRuleActionName, key: 'category' | 'folder', value: string) {
  r.actions = r.actions.map((a) =>
    typeof a === 'object' && a.name === name ? ({ ...a, [key]: value } as EmailRuleActionSpec) : a,
  )
}

async function saveRules(a: EmailAccount) {
  const payload = serializeRules(rulesOf(a))
  savingRulesId.value = a.id
  try {
    const updated = await emailApi.updateAccount(a.id, { rules: payload } as any)
    // 同步本地账户（避免重新拉取后编辑态丢失）
    Object.assign(a, updated)
    toast.success(`已保存「${a.displayName}」过滤策略（${payload.rules.length} 条）`)
  } catch (e: any) {
    toast.error(e?.message || '保存过滤策略失败')
  } finally {
    savingRulesId.value = ''
  }
}

// ---- 账户 ----

async function toggleEnabled(a: EmailAccount, enabled: boolean) {
  try {
    const updated = await emailApi.updateAccount(a.id, { enabled })
    Object.assign(a, updated)
    toast.success(enabled ? `已启用 ${a.displayName}` : `已停用 ${a.displayName}`)
  } catch (e: any) {
    toast.error(e?.message || '更新失败')
  }
}

async function syncNow(a: EmailAccount) {
  syncingId.value = a.id
  try {
    const r = await emailApi.syncNow(a.id)
    toast.success(`同步完成：新邮件 ${r.new ?? 0} 封`)
  } catch (e: any) {
    toast.error(e?.message || '同步失败')
  } finally {
    syncingId.value = ''
  }
}

async function testSmtp(a: EmailAccount) {
  testingId.value = a.id
  try {
    const r = await emailApi.testSmtp(a.id)
    toast.success(`SMTP 测试通过：${r.smtp}`)
  } catch (e: any) {
    toast.error(e?.message || 'SMTP 测试失败')
  } finally {
    testingId.value = ''
  }
}

async function removeAccount(a: EmailAccount) {
  if (
    !(await confirm({
      title: '删除邮箱账户',
      message: `删除「${a.displayName}」？已同步的邮件与规则将一并移除，此操作不可撤销。`,
      confirmText: '删除',
      danger: true,
    }))
  ) {
    return
  }
  try {
    await emailApi.deleteAccount(a.id)
    accounts.value = accounts.value.filter((x) => x.id !== a.id)
    toast.success('已删除')
  } catch (e: any) {
    toast.error(e?.message || '删除失败')
  }
}

// ---- 处理逻辑 ----

async function saveInterval(a: EmailAccount, raw: string) {
  const v = Number(raw)
  if (!Number.isFinite(v) || v < 1) {
    toast.error('同步间隔必须 ≥ 1 分钟')
    return
  }
  try {
    const updated = await emailApi.updateAccount(a.id, { syncIntervalMin: v })
    Object.assign(a, updated)
    toast.success('已保存同步间隔')
  } catch (e: any) {
    toast.error(e?.message || '保存失败')
  }
}

function vacationOf(accountId: string): VacationReply | null {
  return vacations.value.find((v) => v.accountId === accountId) ?? null
}

function toLocalInput(sec: number): string {
  const d = new Date(sec * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function fromLocalInput(value: string): number {
  return Math.floor(new Date(value).getTime() / 1000)
}

function draftVac(a: EmailAccount): VacationReply {
  const existing = vacationOf(a.id)
  const draft = vacDrafts.value[a.id] ?? {}
  const base: VacationReply = existing ?? {
    accountId: a.id,
    enabled: false,
    startAt: Math.floor(Date.now() / 1000),
    endAt: Math.floor(Date.now() / 1000) + 7 * 86400,
    subject: '自动回复：暂不方便查收邮件',
    bodyText: '您好，我目前休假中，将于近期回复您的邮件。',
  }
  return { ...base, ...draft } as VacationReply
}

async function toggleVacation(a: EmailAccount, enabled: boolean) {
  const v = draftVac(a)
  await doSaveVacation(a, { ...v, enabled })
}

function patchVacation(accountId: string, patch: Partial<VacationReply>) {
  vacDrafts.value[accountId] = { ...(vacDrafts.value[accountId] ?? {}), ...patch }
}

async function saveVacation(a: EmailAccount) {
  await doSaveVacation(a, draftVac(a))
}

async function doSaveVacation(a: EmailAccount, v: VacationReply) {
  savingVacId.value = a.id
  try {
    const saved = await emailApi.upsertVacation(v)
    const idx = vacations.value.findIndex((x) => x.accountId === a.id)
    if (idx >= 0) vacations.value[idx] = saved
    else vacations.value.push(saved)
    delete vacDrafts.value[a.id]
    toast.success(`已保存「${a.displayName}」自动回复`)
  } catch (e: any) {
    toast.error(e?.message || '保存自动回复失败')
  } finally {
    savingVacId.value = ''
  }
}

function fmtTime(sec?: number): string {
  if (!sec) return '从未'
  return new Date(sec * 1000).toLocaleString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/email')
}
</script>

<style scoped>
.email-settings {
  min-height: 100%;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}
.page-head {
  position: sticky;
  top: 0;
  z-index: 5;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  padding-top: calc(var(--space-3) + env(safe-area-inset-top, 0px));
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.page-title { flex: 1; font-size: 17px; font-weight: 600; margin: 0; }
.back-btn, .reload-btn {
  width: 40px; height: 40px;
  display: flex; align-items: center; justify-content: center;
  border: none; border-radius: var(--radius-md);
  background: transparent; color: var(--text-secondary); cursor: pointer;
}
.state { text-align: center; color: var(--text-secondary); padding: var(--space-8); }
.state.error { color: var(--danger); }
.link-btn { background: none; border: none; color: var(--brand-primary); cursor: pointer; font-size: 14px; }

.sections {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-4);
  padding-bottom: calc(var(--space-8) + env(safe-area-inset-bottom, 0px));
  max-width: 760px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
}
.card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
}
.card-head {
  display: flex; align-items: center; justify-content: space-between;
  gap: var(--space-2); margin-bottom: var(--space-3);
}
.card-head h3 { margin: 0; font-size: 15px; font-weight: 600; }
.head-hint { font-size: 11px; color: var(--text-muted); }

.primary-btn {
  border: none; border-radius: var(--radius-md);
  background: var(--brand-primary); color: var(--text-inverse);
  padding: var(--space-2) var(--space-3);
  font-size: 13px; font-weight: 600; cursor: pointer;
}
.primary-btn.slim { margin-top: var(--space-2); }
.primary-btn:disabled { opacity: 0.5; cursor: not-allowed; }

.acct-row {
  display: flex; flex-direction: column; gap: var(--space-2);
  padding: var(--space-3) 0;
  border-bottom: 1px solid var(--border-subtle);
}
.acct-row:last-child { border-bottom: none; }
.acct-name { font-size: 14px; font-weight: 600; }
.acct-sub { font-size: 12px; color: var(--text-secondary); }
.acct-sub.muted { color: var(--text-muted); }
.acct-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }

.switch { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-secondary); cursor: pointer; }
.mini-btn {
  border: 1px solid var(--border); border-radius: var(--radius-md);
  background: var(--bg-subtle); color: var(--text-primary);
  padding: 6px 10px; font-size: 12px; cursor: pointer;
}
.mini-btn.danger { color: var(--danger); border-color: var(--danger); }
.mini-btn:disabled { opacity: 0.5; cursor: not-allowed; }

/* 过滤策略 */
.rule-block { padding: var(--space-3) 0; border-bottom: 1px solid var(--border-subtle); }
.rule-block:last-child { border-bottom: none; }
.rule-block-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: var(--space-2); }
.rule-acct { font-size: 13px; font-weight: 600; }
.legacy-hint { margin: 0 0 var(--space-2); font-size: 11px; color: var(--warning); }
.rule-empty { font-size: 12px; color: var(--text-muted); padding: var(--space-2) 0; }
.rule-row {
  display: flex; flex-direction: column; gap: var(--space-2);
  padding: var(--space-2); margin-bottom: var(--space-2);
  background: var(--bg-subtle); border-radius: var(--radius-md);
}
.rule-select, .rule-pattern, .rule-param, .proc-input {
  padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--radius-md);
  background: var(--bg-card); color: var(--text-primary); font-size: 13px;
}
.rule-pattern { width: 100%; box-sizing: border-box; }
.rule-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
.rule-act { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-secondary); cursor: pointer; }
.rule-param { width: 110px; }

/* 处理逻辑 */
.proc-block { padding: var(--space-3) 0; border-bottom: 1px solid var(--border-subtle); }
.proc-block:last-of-type { border-bottom: none; }
.proc-head { font-size: 13px; font-weight: 600; margin-bottom: var(--space-2); }
.proc-row {
  display: flex; align-items: center; justify-content: space-between; gap: var(--space-3);
  margin-bottom: var(--space-2); font-size: 13px; color: var(--text-secondary);
}
.proc-input { width: 170px; }
.proc-input.wide { flex: 1; }
.proc-area {
  flex: 1; padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--radius-md);
  background: var(--bg-card); color: var(--text-primary); font-size: 13px; font-family: inherit;
  box-sizing: border-box;
}
.vacation { margin-top: var(--space-2); padding: var(--space-3); background: var(--bg-subtle); border-radius: var(--radius-md); }
.vac-head {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 13px; font-weight: 600; margin-bottom: var(--space-2);
}
.vac-form .proc-row { margin-bottom: var(--space-2); }
.queue-note {
  margin-top: var(--space-3); padding: var(--space-3);
  background: var(--bg-subtle); border-radius: var(--radius-md);
  font-size: 12px; color: var(--text-secondary); line-height: 1.6;
}
.queue-note h4 { margin: 0 0 4px; font-size: 12px; }
.queue-note code { background: var(--bg-card); padding: 1px 4px; border-radius: 4px; }
</style>
