<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChatAgentStore, departmentLabel } from '../../stores/chatAgentStore'
import { renderMarkdown } from '../../utils/markdown'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const route = useRoute()
const router = useRouter()
const agentStore = useChatAgentStore()

const agentId = computed(() => route.params.agentId as string)
const agent = computed(() => agentStore.getAgent(agentId.value))

const showFullPrompt = ref(false)

// 部门标签
const departmentLabelText = computed(() =>
  agent.value ? departmentLabel(agent.value.department) : ''
)

// Markdown 渲染（截取前 500 字符预览）
const promptPreview = computed(() => {
  if (!agent.value) return ''
  const prompt = agent.value.system_prompt
  const preview = showFullPrompt.value ? prompt : prompt.slice(0, 500)
  return renderMarkdown(preview + (!showFullPrompt.value && prompt.length > 500 ? '...' : ''))
})

const shouldShowToggle = computed(() => {
  return agent.value && agent.value.system_prompt.length > 500
})

onMounted(async () => {
  if (agentStore.agents.length === 0) {
    await agentStore.loadAgents()
  }
})

function goToEdit() {
  router.push(`/agents/${agentId.value}/edit`)
}
</script>

<template>
  <div class="agent-detail-view">
    <!-- 标题栏右侧操作经 Portal 注入壳层 top-bar，页面不再自绘 header。
         内置角色同样可编辑——专家库允许维护性修改。 -->
    <HeaderActionsPortal>
      <button
        v-if="agent"
        type="button"
        aria-label="编辑角色"
        @click="goToEdit"
      >
        <span class="material-symbols-outlined">edit</span>
      </button>
    </HeaderActionsPortal>

    <div v-if="!agent" class="loading">
      {{ agentStore.loading ? '加载中...' : '角色不存在' }}
    </div>

    <main v-else class="detail-content">
      <!-- 角色基本信息 -->
      <div class="role-header">
        <div class="role-emoji">{{ agent.emoji || '👤' }}</div>
        <div class="role-info">
          <div class="role-name-row">
            <h2 class="role-name">{{ agent.name }}</h2>
            <span v-if="!agent.is_builtin" class="custom-badge">自定义</span>
            <span v-else class="custom-badge builtin-badge">内置</span>
          </div>
          <div class="role-dept">{{ departmentLabelText }}</div>
        </div>
      </div>

      <!-- 角色简介 -->
      <section class="section">
        <p class="role-desc">{{ agent.description }}</p>
      </section>

      <!-- System Prompt 预览 -->
      <section class="section">
        <div class="section-header">
          <h3 class="section-title">System Prompt</h3>
          <span class="section-hint">对话时自动注入</span>
        </div>
        <div class="prompt-preview" v-html="promptPreview"></div>
        <button
          v-if="shouldShowToggle"
          class="toggle-btn"
          @click="showFullPrompt = !showFullPrompt"
        >
          {{ showFullPrompt ? '收起' : '展开全部' }}
        </button>
      </section>

      <!-- 元信息 -->
      <section class="section meta-section">
        <div class="meta-row">
          <span class="meta-label">角色 ID</span>
          <span class="meta-value mono">{{ agent.id }}</span>
        </div>
        <div class="meta-row">
          <span class="meta-label">部门</span>
          <span class="meta-value">{{ departmentLabelText }}</span>
        </div>
        <div v-if="agent.color" class="meta-row">
          <span class="meta-label">主题色</span>
          <span class="meta-value">{{ agent.color }}</span>
        </div>
        <div class="meta-row">
          <span class="meta-label">类型</span>
          <span class="meta-value">{{ agent.is_builtin ? '内置角色' : '用户自定义' }}</span>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.agent-detail-view {
  min-height: 100%;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}

.loading {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
  font-size: 14px;
}

.detail-content {
  flex: 1;
  overflow-y: auto;
  padding: 20px 16px;
}

.role-header {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 24px;
  padding: 20px;
  background: var(--bg-card);
  border-radius: 12px;
}

.role-emoji {
  font-size: 48px;
  line-height: 1;
  flex-shrink: 0;
}

.role-info {
  flex: 1;
  min-width: 0;
}

.role-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.role-name {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: var(--text-primary);
}

.custom-badge {
  font-size: 11px;
  padding: 3px 8px;
  background: var(--brand-primary);
  color: white;
  border-radius: 4px;
}

/* 内置角色徽标（专家库原生角色，可维护） */
.builtin-badge {
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.role-dept {
  font-size: 13px;
  color: var(--text-secondary);
}

.section {
  margin-bottom: 24px;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.section-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}

.section-hint {
  font-size: 11px;
  color: var(--text-muted);
}

.role-desc {
  margin: 0;
  font-size: 14px;
  color: var(--text-secondary);
  line-height: 1.6;
  padding: 16px;
  background: var(--bg-card);
  border-radius: 10px;
}

.prompt-preview {
  padding: 16px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-primary);
  overflow-x: auto;
}

.prompt-preview :deep(h1),
.prompt-preview :deep(h2),
.prompt-preview :deep(h3) {
  margin: 12px 0 8px;
  font-weight: 600;
}

.prompt-preview :deep(h1) { font-size: 16px; }
.prompt-preview :deep(h2) { font-size: 15px; }
.prompt-preview :deep(h3) { font-size: 14px; }

.prompt-preview :deep(p) {
  margin: 8px 0;
}

.prompt-preview :deep(ul),
.prompt-preview :deep(ol) {
  margin: 8px 0;
  padding-left: 20px;
}

.prompt-preview :deep(li) {
  margin: 4px 0;
}

.prompt-preview :deep(code) {
  background: var(--bg-base);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'SF Mono', Menlo, monospace;
  font-size: 12px;
}

.prompt-preview :deep(strong) {
  font-weight: 600;
}

.toggle-btn {
  width: 100%;
  margin-top: 8px;
  padding: 10px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 13px;
  color: var(--brand-primary);
  cursor: pointer;
}

.meta-section {
  padding: 16px;
  background: var(--bg-card);
  border-radius: 10px;
}

.meta-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 0;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}

.meta-row:last-child {
  border-bottom: none;
}

.meta-label {
  color: var(--text-secondary);
}

.meta-value {
  color: var(--text-primary);
}

.mono {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: 12px;
}
</style>
