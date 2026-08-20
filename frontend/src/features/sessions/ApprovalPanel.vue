<script setup lang="ts">
/**
 * ApprovalPanel — human-in-the-loop 审批面板（权限批准/拒绝 + 问答回答）。
 *
 * 通过 useApprovalStore 周期性拉取 /api/mobile/approvals（近实时），
 * 以独立区块渲染在会话视图底部输入区之上，不阻塞、不破坏既有聊天/SSE 流。
 *
 * 仅在有待审批项时显示（v-if="store.hasPending"）。
 */
import { onMounted, onBeforeUnmount, watch, reactive, computed } from 'vue'
import { useApprovalStore } from '../../stores/approval'

const props = defineProps<{
  instanceId: string
  sessionId: string
}>()

const store = useApprovalStore()

// 每个权限请求的附言（可选）
const permMessage = reactive<Record<string, string>>({})
// 每个问题请求：按子问题索引保存已选 label 列表
const qSelected = reactive<Record<string, string[][]>>({})
// 每个问题请求：按子问题索引保存自定义文本
const qCustom = reactive<Record<string, string[]>>({})

// 正在提交中的请求 id（防重复点击）
const submitting = reactive<Record<string, boolean>>({})

function ensureQState(reqId: string, count: number) {
  if (!qSelected[reqId]) {
    qSelected[reqId] = Array.from({ length: count }, () => [] as string[])
  }
  if (!qCustom[reqId]) {
    qCustom[reqId] = Array.from({ length: count }, () => '')
  }
}

function isSelected(reqId: string, qi: number, label: string): boolean {
  return (qSelected[reqId]?.[qi] ?? []).includes(label)
}

function toggleOption(reqId: string, qi: number, label: string, multiple: boolean) {
  if (!multiple) {
    // 单选：直接替换为当前 label
    qSelected[reqId][qi] = [label]
    return
  }
  const arr = qSelected[reqId][qi]
  const idx = arr.indexOf(label)
  if (idx >= 0) arr.splice(idx, 1)
  else arr.push(label)
}

function hasAnyAnswer(reqId: string, count: number): boolean {
  for (let i = 0; i < count; i++) {
    if ((qSelected[reqId]?.[i]?.length ?? 0) > 0) return true
    if ((qCustom[reqId]?.[i] ?? '').trim().length > 0) return true
  }
  return false
}

function buildAnswers(reqId: string, count: number): string[][] {
  const out: string[][] = []
  for (let i = 0; i < count; i++) {
    const sel = qSelected[reqId]?.[i] ?? []
    const cus = (qCustom[reqId]?.[i] ?? '').trim()
    if (sel.length) out.push(sel)
    else if (cus) out.push([cus])
    else out.push([])
  }
  return out
}

const permissionCount = computed(() => store.permissions.length)
const questionCount = computed(() => store.questions.length)

async function onApprove(p: { id: string }) {
  if (submitting[p.id]) return
  submitting[p.id] = true
  try {
    await store.approvePermission(p.id, permMessage[p.id]?.trim() || undefined)
  } finally {
    submitting[p.id] = false
  }
}

async function onDeny(p: { id: string }) {
  if (submitting[p.id]) return
  submitting[p.id] = true
  try {
    await store.denyPermission(p.id, permMessage[p.id]?.trim() || undefined)
  } finally {
    submitting[p.id] = false
  }
}

async function onAnswer(q: { id: string; questions: any[] }) {
  if (submitting[q.id]) return
  ensureQState(q.id, q.questions.length)
  submitting[q.id] = true
  try {
    await store.answerQuestion(q.id, buildAnswers(q.id, q.questions.length))
  } finally {
    submitting[q.id] = false
  }
}

async function onSkip(q: { id: string }) {
  if (submitting[q.id]) return
  submitting[q.id] = true
  try {
    await store.skipQuestion(q.id)
  } finally {
    submitting[q.id] = false
  }
}

function refreshScope() {
  if (props.instanceId && props.sessionId) {
    store.setScope(props.instanceId, props.sessionId)
  }
}

onMounted(refreshScope)

// 会话切换（同路由组件复用）时更新作用域 + 重新轮询
watch(
  () => [props.instanceId, props.sessionId],
  () => refreshScope(),
)

onBeforeUnmount(() => {
  store.stopPolling()
})
</script>

<template>
  <section v-if="store.hasPending" class="approval-panel">
    <div class="approval-header">
      <span class="approval-title">
        <span class="material-symbols-outlined">gpp_maybe</span>
        待处理审批
      </span>
      <span class="approval-counts">
        <span v-if="permissionCount" class="badge permission">权限 {{ permissionCount }}</span>
        <span v-if="questionCount" class="badge question">问答 {{ questionCount }}</span>
      </span>
    </div>

    <div class="approval-list">
      <!-- 权限请求 -->
      <div
        v-for="p in store.permissions"
        :key="'perm-' + p.id"
        class="approval-card permission-card"
      >
        <div class="card-head">
          <span class="card-kind permission">权限请求</span>
          <span class="card-id">{{ p.id }}</span>
        </div>
        <div class="card-body">
          <div class="row">
            <span class="label">操作</span>
            <span class="value action">{{ p.action || '—' }}</span>
          </div>
          <div v-if="p.resources && p.resources.length" class="row">
            <span class="label">资源</span>
            <span class="value">
              <code v-for="(r, i) in p.resources" :key="i" class="res">{{ r }}</code>
            </span>
          </div>
        </div>
        <textarea
          v-model="permMessage[p.id]"
          class="msg-input"
          rows="1"
          placeholder="附言（可选）"
        />
        <div class="card-actions">
          <button
            class="btn approve"
            :disabled="submitting[p.id]"
            @click="onApprove(p)"
          >
            批准
          </button>
          <button
            class="btn deny"
            :disabled="submitting[p.id]"
            @click="onDeny(p)"
          >
            拒绝
          </button>
        </div>
      </div>

      <!-- 问题请求 -->
      <div
        v-for="q in store.questions"
        :key="'ques-' + q.id"
        class="approval-card question-card"
      >
        <div class="card-head">
          <span class="card-kind question">问答请求</span>
          <span class="card-id">{{ q.id }}</span>
        </div>
        <div class="card-body">
          <div
            v-for="(sub, qi) in q.questions"
            :key="qi"
            class="sub-q"
          >
            <div class="sub-q-text">{{ sub.question }}</div>

            <div v-if="sub.options && sub.options.length" class="options">
              <button
                v-for="(opt, oi) in sub.options"
                :key="oi"
                type="button"
                class="option"
                :class="{ active: isSelected(q.id, qi, opt.label) }"
                @click="ensureQState(q.id, q.questions.length); toggleOption(q.id, qi, opt.label, !!sub.multiple)"
              >
                {{ opt.label }}
                <span v-if="opt.description" class="opt-desc">{{ opt.description }}</span>
              </button>
            </div>

            <input
              v-if="sub.custom"
              :value="qCustom[q.id]?.[qi] ?? ''"
              class="custom-input"
              type="text"
              placeholder="自定义回答（可选）"
              @input="(e: any) => { ensureQState(q.id, q.questions.length); qCustom[q.id][qi] = e.target.value }"
            />
          </div>
        </div>
        <div class="card-actions">
          <button
            class="btn approve"
            :disabled="submitting[q.id] || !hasAnyAnswer(q.id, q.questions.length)"
            @click="onAnswer(q)"
          >
            回答
          </button>
          <button
            class="btn skip"
            :disabled="submitting[q.id]"
            @click="onSkip(q)"
          >
            跳过
          </button>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.approval-panel {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  max-height: 40dvh;
  background: var(--bg-card);
  border-top: 1px solid var(--border);
  border-bottom: 1px solid var(--border);
}

.approval-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-4);
  color: var(--text-secondary);
}
.approval-title {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}
.approval-counts {
  display: flex;
  gap: var(--space-1);
}
.badge {
  font-size: var(--text-xs);
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-weight: var(--font-weight-medium);
}
.badge.permission { background: var(--info-bg); color: var(--info); }
.badge.question { background: var(--warning-bg, #4b3a00); color: var(--warning, #ffb300); }

.approval-list {
  overflow-y: auto;
  padding: 0 var(--space-3) var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.approval-card {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  padding: var(--space-3);
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-2);
}
.card-kind {
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
}
.card-kind.permission { background: var(--info-bg); color: var(--info); }
.card-kind.question { background: var(--warning-bg, #4b3a00); color: var(--warning, #ffb300); }
.card-id {
  font-size: var(--text-xs);
  color: var(--text-muted);
  font-family: monospace;
}

.card-body { display: flex; flex-direction: column; gap: var(--space-1); }
.row {
  display: flex;
  gap: var(--space-2);
  font-size: var(--text-sm);
  align-items: flex-start;
}
.label {
  flex: 0 0 auto;
  color: var(--text-muted);
  width: 36px;
}
.value { color: var(--text-primary); word-break: break-word; }
.value.action { font-family: monospace; font-weight: var(--font-weight-semibold); }
.res {
  display: inline-block;
  font-family: monospace;
  font-size: var(--text-xs);
  background: var(--overlay-subtle);
  border-radius: var(--radius-sm);
  padding: 1px 6px;
  margin: 0 4px 4px 0;
  word-break: break-all;
}

.msg-input,
.custom-input {
  width: 100%;
  margin-top: var(--space-2);
  padding: var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  font-family: inherit;
  background: var(--bg-card);
  color: var(--text-primary);
  outline: none;
}
.msg-input:focus,
.custom-input:focus { border-color: var(--brand-primary); }

.sub-q { padding: var(--space-2) 0; border-top: 1px dashed var(--border); }
.sub-q:first-child { border-top: none; padding-top: 0; }
.sub-q-text {
  font-size: var(--text-sm);
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}
.options {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}
.option {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: 4px var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all 150ms;
}
.option.active {
  border-color: var(--brand-primary);
  background: var(--brand-primary);
  color: var(--text-inverse);
}
.opt-desc { font-size: var(--text-xs); opacity: 0.7; }

.card-actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
.btn {
  flex: 1 1 auto;
  padding: var(--space-2);
  border: none;
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
  transition: all 150ms;
}
.btn:disabled { cursor: not-allowed; opacity: 0.5; }
.btn.approve { background: var(--brand-primary); color: var(--text-inverse); }
.btn.deny { background: var(--danger); color: var(--text-inverse); }
.btn.skip { flex: 0 0 auto; background: var(--bg-subtle); color: var(--text-secondary); border: 1px solid var(--border); }
.btn:not(:disabled):active { transform: scale(0.97); }

.material-symbols-outlined {
  font-family: 'Material Symbols Outlined', 'Material Icons';
  font-weight: normal;
  font-style: normal;
  font-size: 18px;
  line-height: 1;
}
</style>
