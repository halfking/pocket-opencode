<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChatAgentStore, departmentLabel } from '../../stores/chatAgentStore'
import { useToast } from '../../composables/useToast'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const route = useRoute()
const router = useRouter()
const agentStore = useChatAgentStore()
const toast = useToast()

const isEditMode = computed(() => route.params.agentId && route.params.agentId !== 'new')
const editingAgent = computed(() => {
  if (!isEditMode.value) return null
  return agentStore.getAgent(route.params.agentId as string)
})

// 专家人头像快选（职业化 emoji，也可在输入框自定义）
const AVATAR_EMOJIS = [
  '👤', '👨‍💼', '👩‍💼', '🧑‍💼', '👨‍💻', '👩‍💻', '🧑‍💻',
  '👨‍⚕️', '👩‍⚕️', '🧑‍⚕️', '👨‍🏫', '👩‍🏫', '🧑‍🏫',
  '👨‍🔬', '👩‍🔬', '🧑‍🔬', '🧑‍🎨', '🧑‍⚖️', '🧑‍🔧',
  '🧑‍🌾', '🕵️', '🧑‍🚀', '🧑‍✈️', '🧙', '🦸',
]

// 部门选项：实际存在的部门（动态计算）+ 兜底当前值（自定义角色可能用了新部门）
const departmentOptions = computed(() => {
  const list = agentStore.departments.map((d) => ({ key: d.key, label: d.label }))
  if (form.value.department && !list.some((d) => d.key === form.value.department)) {
    list.unshift({ key: form.value.department, label: departmentLabel(form.value.department) })
  }
  return list
})

// 表单字段
const form = ref({
  name: '',
  description: '',
  department: 'engineering',
  emoji: '👤',
  color: 'blue',
  system_prompt: '',
})

const saving = ref(false)

onMounted(async () => {
  if (agentStore.agents.length === 0) {
    await agentStore.loadAgents()
  }

  if (isEditMode.value && editingAgent.value) {
    form.value = {
      name: editingAgent.value.name,
      description: editingAgent.value.description,
      department: editingAgent.value.department,
      emoji: editingAgent.value.emoji || '👤',
      color: editingAgent.value.color || 'blue',
      system_prompt: editingAgent.value.system_prompt,
    }
  }
})

function insertTemplate() {
  // 快速填充模板
  form.value.system_prompt = `# 角色名称

你是**${form.value.name || '[角色名称]'}**，一位专业的 [领域] 专家。

## 你的身份与记忆

- **角色**：[具体角色定位]
- **个性**：[性格特征]
- **经验**：[领域经验]

## 核心使命

### 职责一
- 要点 1
- 要点 2

### 职责二
- 要点 1
- 要点 2

## 关键规则

- 规则 1
- 规则 2

## 技术交付物

[输出格式/代码示例等]
`
}

async function handleSave() {
  // 验证
  if (!form.value.name.trim()) {
    toast.error('请输入角色名称')
    return
  }
  if (!form.value.description.trim()) {
    toast.error('请输入角色简介')
    return
  }
  if (!form.value.system_prompt.trim()) {
    toast.error('请输入 System Prompt')
    return
  }

  saving.value = true
  try {
    if (isEditMode.value && editingAgent.value) {
      // 更新
      const updated = await agentStore.updateAgent(editingAgent.value.id, {
        name: form.value.name.trim(),
        description: form.value.description.trim(),
        department: form.value.department,
        emoji: form.value.emoji,
        color: form.value.color,
        system_prompt: form.value.system_prompt.trim(),
      })
      toast.success('已更新角色')
      router.push(`/agents/${updated.id}`)
    } else {
      // 创建
      const created = await agentStore.createAgent({
        name: form.value.name.trim(),
        description: form.value.description.trim(),
        department: form.value.department,
        emoji: form.value.emoji,
        color: form.value.color,
        system_prompt: form.value.system_prompt.trim(),
      })
      toast.success('已创建角色')
      router.push(`/agents/${created.id}`)
    }
  } catch (err: any) {
    toast.error(`保存失败：${err.message || err}`)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="agent-edit-view">
    <!-- 顶部操作经 Portal 注入壳层 top-bar；标题与返回由 AppLayout 统一渲染 -->
    <HeaderActionsPortal>
      <button
        type="button"
        class="primary-text-btn"
        :disabled="saving"
        @click="handleSave"
      >
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </HeaderActionsPortal>

    <main class="edit-content">
      <!-- 编辑内置角色提示：修改全局生效 -->
      <div v-if="isEditMode && editingAgent?.is_builtin" class="builtin-hint">
        正在编辑内置专家角色，保存后对全部用户生效。
      </div>

      <!-- 基本信息 -->
      <section class="section">
        <label class="field">
          <span class="field-label">角色名称 <span class="required">*</span></span>
          <input
            v-model="form.name"
            type="text"
            class="input"
            placeholder="如：AI 工程师"
            maxlength="50"
          />
        </label>

        <label class="field">
          <span class="field-label">角色简介 <span class="required">*</span></span>
          <textarea
            v-model="form.description"
            class="input"
            rows="2"
            placeholder="一句话描述这个角色的核心能力"
            maxlength="200"
          ></textarea>
        </label>

        <label class="field">
          <span class="field-label">头像 Emoji</span>
          <div class="emoji-picker" role="listbox" aria-label="选择头像">
            <button
              v-for="e in AVATAR_EMOJIS"
              :key="e"
              type="button"
              :class="['emoji-option', { active: form.emoji === e }]"
              :aria-label="`头像 ${e}`"
              @click="form.emoji = e"
            >
              {{ e }}
            </button>
          </div>
          <input
            v-model="form.emoji"
            type="text"
            class="input emoji-input"
            maxlength="8"
            placeholder="👤"
          />
        </label>

        <label class="field">
          <span class="field-label">部门 <span class="required">*</span></span>
          <select v-model="form.department" class="input">
            <option v-for="dept in departmentOptions" :key="dept.key" :value="dept.key">
              {{ dept.label }}
            </option>
          </select>
        </label>

        <label class="field">
          <span class="field-label">主题色</span>
          <select v-model="form.color" class="input">
            <option value="blue">蓝色</option>
            <option value="purple">紫色</option>
            <option value="green">绿色</option>
            <option value="orange">橙色</option>
            <option value="red">红色</option>
            <option value="pink">粉色</option>
            <option value="gray">灰色</option>
          </select>
        </label>
      </section>

      <!-- System Prompt -->
      <section class="section">
        <div class="section-header">
          <span class="field-label">System Prompt <span class="required">*</span></span>
          <button class="template-btn" @click="insertTemplate">插入模板</button>
        </div>
        <textarea
          v-model="form.system_prompt"
          class="input prompt-input"
          rows="12"
          placeholder="定义角色的身份、专业领域、核心使命、关键规则等。支持 Markdown。"
        ></textarea>
        <div class="field-hint">
          建议包含：身份与记忆、核心使命、关键规则、技术交付物
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.agent-edit-view {
  min-height: 100%;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}

/* Portal 注入的文字主按钮外观（左侧颜色块由 AppLayout 的 :deep 规则管热区尺寸） */
.primary-text-btn {
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}

.edit-content {
  flex: 1;
  overflow-y: auto;
  padding: 20px 16px;
}

.section {
  margin-bottom: 24px;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.field {
  display: block;
  margin-bottom: 16px;
}

.field-row {
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 12px;
}

/* 内置角色编辑提示 */
.builtin-hint {
  margin-bottom: 16px;
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--warning-bg);
  color: var(--warning);
  font-size: 12px;
}

/* 头像快选 */
.emoji-picker {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}

.emoji-option {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg-base);
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-out),
    background var(--duration-fast) var(--ease-out);
}

.emoji-option.active {
  border-color: var(--brand-primary);
  background: var(--brand-bg);
}

.field-label {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 6px;
}

.required {
  color: var(--danger);
}

.input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 14px;
  background: var(--bg-base);
  color: var(--text-primary);
  font-family: inherit;
}

.input:focus {
  outline: none;
  border-color: var(--brand-primary);
}

.emoji-input {
  font-size: 20px;
  text-align: center;
}

.prompt-input {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: 13px;
  line-height: 1.6;
  resize: vertical;
}

.field-hint {
  margin-top: 6px;
  font-size: 11px;
  color: var(--text-muted);
}

.template-btn {
  padding: 4px 12px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
  color: var(--brand-primary);
  cursor: pointer;
}
</style>
