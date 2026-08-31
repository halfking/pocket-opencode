<!--
  GatewayNodeListView — 已注册网关节点列表。

  路由：/gateway
  - 列出节点 + 健康灯 + 上次探测角色
  - 探测（验证凭据）/ 新增 / 编辑 / 删除
  - 点节点名进汇总页
-->
<template>
  <div class="gw-view">
    <HeaderActionsPortal>
      <button type="button" class="gw-add-btn" @click="openCreate">+ 新增</button>
    </HeaderActionsPortal>

    <div v-if="status" :class="['status-bar', `status-${status.kind}`]">{{ status.text }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>

      <div v-else-if="nodes.length === 0" class="state">
        <p>还没有注册网关</p>
        <p class="hint">新增一个 llm-gateway-go 节点即可查看供应商、凭据与实时请求流</p>
      </div>

      <div v-else class="node-list">
        <div v-for="n in nodes" :key="n.id" class="node-card">
          <div class="node-head" @click="openNode(n)">
            <span :class="['health-dot', `health-${n.healthStatus}`]" />
            <div class="node-name">
              {{ n.name }}
              <span v-if="!n.enabled" class="chip chip-muted">已停用</span>
            </div>
            <span class="material-symbols-outlined chevron">chevron_right</span>
          </div>

          <div class="node-url">{{ n.baseURL }}</div>

          <div class="node-meta">
            <span v-if="!n.adminPasswordSet" class="chip chip-warn">待补录账号</span>
            <span v-else-if="n.healthRole" :class="['chip', n.healthRole === 'super_admin' ? 'chip-ok' : 'chip-warn']">
              {{ n.healthRole }}
            </span>
            <span v-if="n.healthAt" class="time">{{ relTime(n.healthAt) }}探测</span>
          </div>

          <p v-if="n.healthStatus === 'error' && n.healthError" class="node-error">{{ n.healthError }}</p>

          <div class="node-actions">
            <button class="btn-ghost" :disabled="probing === n.id" @click="probe(n)">
              {{ probing === n.id ? '探测中…' : '探测' }}
            </button>
            <button class="btn-ghost" @click="openEdit(n)">编辑</button>
            <button class="btn-ghost danger" @click="confirmDelete(n)">删除</button>
          </div>
        </div>
      </div>

      <p v-if="!allowPrivateHosts && nodes.length > 0" class="footnote">
        后端未开启私网访问（POCKET_LLM_GATEWAY_ALLOW_PRIVATE），内网地址的节点探测会失败。
      </p>
    </main>

    <!-- 新增/编辑弹层 -->
    <div v-if="editing" class="sheet-mask" @click.self="editing = null">
      <div class="sheet">
        <h2 class="sheet-title">{{ editing.id ? '编辑节点' : '新增节点' }}</h2>

        <label class="form-label">名称 *</label>
        <input v-model="form.name" class="form-input" placeholder="prod / 154 / 测试环境" />

        <label class="form-label">Base URL *</label>
        <input
          v-model="form.baseURL"
          class="form-input"
          placeholder="https://llmgo.kxpms.cn"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
        />
        <div class="form-hint">网关根地址（不带 /v1；带了会自动剥掉）</div>

        <label class="form-label">Admin 用户名 *</label>
        <input v-model="form.adminUsername" class="form-input" autocapitalize="off" spellcheck="false" />
        <div class="form-hint">供应商列表与模型探测需要网关 super_admin 角色</div>

        <label class="form-label">Admin 密码 {{ editing.id ? '' : '*' }}</label>
        <input
          v-model="form.adminPassword"
          class="form-input"
          type="password"
          :placeholder="editing.id && editing.adminPasswordSet ? '留空 = 保留现有密码' : ''"
        />

        <label class="checkbox-row">
          <input v-model="form.enabled" type="checkbox" />
          <span>启用</span>
        </label>

        <div class="sheet-actions">
          <button class="btn-ghost" @click="editing = null">取消</button>
          <button class="btn-primary" :disabled="saving" @click="save">
            {{ saving ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import * as gw from '../../api/gateway'
import type { GatewayNode } from '../../api/gateway'
import { useConfirm } from '../../composables/useConfirm'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const router = useRouter()
const { confirm } = useConfirm()

const nodes = ref<GatewayNode[]>([])
const allowPrivateHosts = ref(true)
const loading = ref(true)
const saving = ref(false)
const probing = ref<number | null>(null)
const status = ref<{ kind: 'ok' | 'err' | 'warn'; text: string } | null>(null)

const editing = ref<Partial<GatewayNode> | null>(null)
const form = reactive({
  name: '',
  baseURL: '',
  adminUsername: '',
  adminPassword: '',
  enabled: true,
})

function flash(kind: 'ok' | 'err' | 'warn', text: string) {
  status.value = { kind, text }
  window.setTimeout(() => {
    if (status.value?.text === text) status.value = null
  }, 6000)
}

async function load() {
  loading.value = true
  try {
    const res = await gw.listNodes()
    nodes.value = res.nodes
    allowPrivateHosts.value = res.allowPrivateHosts
  } catch (e: any) {
    flash('err', e?.message || '加载节点失败')
  } finally {
    loading.value = false
  }
}

function openNode(n: GatewayNode) {
  router.push(`/gateway/${n.id}`)
}

function openCreate() {
  editing.value = {}
  Object.assign(form, { name: '', baseURL: '', adminUsername: '', adminPassword: '', enabled: true })
}

function openEdit(n: GatewayNode) {
  editing.value = n
  Object.assign(form, {
    name: n.name,
    baseURL: n.baseURL,
    adminUsername: n.adminUsername,
    adminPassword: '',
    enabled: n.enabled,
  })
}

async function save() {
  saving.value = true
  try {
    const payload: gw.GatewayNodeInput = {
      name: form.name,
      baseURL: form.baseURL,
      adminUsername: form.adminUsername,
      enabled: form.enabled,
    }
    // 空密码在更新时意为"保留"，创建时后端会因必填而报错。
    if (form.adminPassword) payload.adminPassword = form.adminPassword

    if (editing.value?.id) {
      await gw.updateNode(editing.value.id, payload)
      flash('ok', '已保存')
    } else {
      await gw.createNode(payload)
      flash('ok', '已新增，点"探测"验证凭据')
    }
    editing.value = null
    await load()
  } catch (e: any) {
    flash('err', e?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function probe(n: GatewayNode) {
  probing.value = n.id
  try {
    const res = await gw.probeNode(n.id)
    if (res.ok) {
      flash(res.warning ? 'warn' : 'ok', res.warning || `连通正常（角色 ${res.role || '未知'}）`)
    } else {
      flash('err', res.error || '探测失败')
    }
    await load()
  } catch (e: any) {
    flash('err', e?.message || '探测失败')
  } finally {
    probing.value = null
  }
}

async function confirmDelete(n: GatewayNode) {
  if (!(await confirm({
    title: '删除节点',
    message: `删除节点「${n.name}」？只影响 pocket 侧的注册信息，不会改动网关本身。`,
    confirmText: '删除',
    danger: true,
  }))) {
    return
  }
  try {
    await gw.deleteNode(n.id)
    flash('ok', '已删除')
    await load()
  } catch (e: any) {
    flash('err', e?.message || '删除失败')
  }
}

function relTime(iso: string) {
  const diff = Date.now() - new Date(iso).getTime()
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}

onMounted(load)
</script>

<style scoped>
.gw-view {
  min-height: 100%;
  background: var(--bg-base);
}
/* Portal 注入到壳层标题栏：颜色用品牌色（保留原 .add-btn 视觉权重） */
.gw-add-btn {
  color: var(--brand-primary);
}
.status-bar {
  padding: var(--space-2) var(--space-3);
  font-size: 13px;
}
.status-ok {
  background: color-mix(in srgb, var(--success) 15%, transparent);
  color: var(--success);
}
.status-err {
  background: color-mix(in srgb, var(--danger) 15%, transparent);
  color: var(--danger);
}
.status-warn {
  background: color-mix(in srgb, var(--warning) 18%, transparent);
  color: var(--warning);
}
.body {
  padding: var(--space-3);
}
.state {
  padding: 40px 20px;
  text-align: center;
  color: var(--text-secondary);
  font-size: 14px;
}
.hint {
  font-size: 12px;
  margin-top: 8px;
}
.node-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
}
.node-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  padding: var(--space-3);
}
.node-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.node-name {
  flex: 1;
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}
.chevron {
  color: var(--text-secondary);
  font-size: 20px;
}
.health-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  flex: none;
}
.health-ok {
  background: var(--success);
}
.health-error {
  background: var(--danger);
}
.health-unknown {
  background: var(--text-secondary);
}
.node-url {
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 4px;
  word-break: break-all;
}
.node-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  flex-wrap: wrap;
}
.chip {
  font-size: 11px;
  padding: 2px 7px;
  border-radius: var(--radius-full, 999px);
  background: var(--bg-subtle);
  color: var(--text-secondary);
}
.chip-ok {
  color: var(--success);
}
.chip-warn {
  color: var(--warning);
}
.chip-muted {
  color: var(--text-secondary);
}
.time {
  font-size: 11px;
  color: var(--text-secondary);
}
.node-error {
  font-size: 12px;
  color: var(--danger);
  margin: 8px 0 0;
  word-break: break-word;
}
.node-actions {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}
.btn-ghost {
  flex: 1;
  padding: 7px;
  font-size: 13px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--text-primary);
}
.btn-ghost.danger {
  color: var(--danger);
}
.btn-primary {
  flex: 1;
  padding: 10px;
  font-size: 14px;
  background: var(--primary, #4c8dff);
  border: none;
  border-radius: var(--radius-sm, 8px);
  color: var(--text-inverse);
}
.footnote {
  margin-top: var(--space-3);
  font-size: 11px;
  color: var(--warning);
}
.sheet-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-end;
  z-index: var(--z-base);
}
.sheet {
  width: 100%;
  max-height: 88vh;
  overflow-y: auto;
  background: var(--bg-card);
  border-radius: 14px 14px 0 0;
  padding: var(--space-4) var(--space-3) var(--space-5);
}
.sheet-title {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 var(--space-3);
}
.form-label {
  display: block;
  font-size: 12px;
  color: var(--text-secondary);
  margin: var(--space-2-5) 0 4px;
}
.form-input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-subtle);
  color: var(--text-primary);
  font-size: 14px;
  box-sizing: border-box;
}
.form-hint {
  font-size: 11px;
  color: var(--text-secondary);
  margin-top: 4px;
}
.checkbox-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: var(--space-3);
  font-size: 14px;
}
.sheet-actions {
  display: flex;
  gap: 10px;
  margin-top: var(--space-4);
}
</style>
