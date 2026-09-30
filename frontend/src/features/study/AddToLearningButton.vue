<!--
  AddToLearningButton —— 「一键加入学习」入口（docs/学习muse/05-实施路线图.md §3 / P2）。

  放在笔记、邮件、RSS、会议详情页，让"读到有用的东西"当场就能进记忆回路。
  只发 {sourceKind, sourceId}：标题与摘要由服务端解析（见
  backend/internal/learning/sources），所以这里不需要先读一遍详情再拼请求体。

  三种状态的取舍：
  - Learning Core 不可用（503 / 离线）→ 按钮直接隐藏，而不是点了报错。
    学习是可选能力，不该在没装 PG 的部署里变成一个坏按钮。
  - 重复点击是幂等的（服务端按来源唯一），所以成功后禁用而不是报错。
  - 失败给 toast 提示，但不改变按钮状态：用户可能只是暂时网络不通。
-->
<template>
  <button
    v-if="available"
    class="add-learning"
    :class="{ done: added }"
    type="button"
    :disabled="busy || added"
    :data-testid="`add-learning-${sourceKind}`"
    :aria-label="label"
    @click.stop.prevent="add"
  >
    <!-- 图标名必须以字面量出现在模板里：字体子集脚本的正则只认
         material-symbols-outlined"…>([a-z_]+)<，写在插值表达式里会缺字。 -->
    <span v-if="added" class="material-symbols-outlined" aria-hidden="true">check</span>
    <span v-else class="material-symbols-outlined" aria-hidden="true">psychology</span>
    <span class="label">{{ label }}</span>
  </button>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'
import * as learningApi from '../../services/learning'
import type { LearningSourceKind } from '../../types/learning'

const props = defineProps<{
  sourceKind: LearningSourceKind
  sourceId: string
  /** 会议一次会把多个 action item 转成任务，用不同的文案。 */
  asTask?: boolean
  /** 任务分类，只在 asTask 时生效。 */
  taskType?: string
}>()

/**
 * 能转成工作项的来源只有四类。chat / manual 是合法的**学习**来源，
 * 但没有可回链的来源行，所以不提供"转为任务"。
 * 这与服务端 handleTaskFromSource 的 sourceOriginKinds 一一对应。
 */
const TASK_SOURCE_KINDS = ['note', 'email', 'rss', 'meeting'] as const
type TaskSourceKind = (typeof TASK_SOURCE_KINDS)[number]

function asTaskSourceKind(kind: LearningSourceKind): TaskSourceKind | null {
  return (TASK_SOURCE_KINDS as readonly string[]).includes(kind)
    ? (kind as TaskSourceKind)
    : null
}

const { t } = useI18n()
const toast = useToast()

const busy = ref(false)
const added = ref(false)
const available = ref(true)

const label = computed(() => {
  if (props.asTask) return added.value ? t('study.capture.taskDone') : t('study.capture.toTask')
  return added.value ? t('study.capture.done') : t('study.capture.add')
})

onMounted(async () => {
  if (!props.sourceId) {
    available.value = false
    return
  }
  if (props.asTask && !asTaskSourceKind(props.sourceKind)) {
    // 没有可回链的来源行，点了必然 400，不如不渲染。
    available.value = false
    return
  }
  // 探测 Learning Core 是否就绪；不可用就不渲染这个按钮。
  try {
    await learningApi.fetchDueSummary()
  } catch {
    available.value = false
  }
})

async function add() {
  if (busy.value || added.value) return
  const taskKind = props.asTask ? asTaskSourceKind(props.sourceKind) : null
  if (props.asTask && !taskKind) return
  busy.value = true
  try {
    if (taskKind) {
      await api.createTaskFromSource({
        sourceKind: taskKind,
        sourceId: props.sourceId,
        type: props.taskType,
      })
    } else {
      await learningApi.captureItem({
        sourceKind: props.sourceKind,
        sourceId: props.sourceId,
      })
    }
    added.value = true
  } catch (e) {
    toast.error(t('study.capture.failed'))
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.add-learning {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 6px 10px;
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  min-height: 32px;
  white-space: nowrap;
}

.add-learning .material-symbols-outlined {
  font-size: 16px;
  color: var(--brand-primary, #4c8dff);
}

.add-learning.done {
  border-color: var(--brand-primary);
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  cursor: default;
}

.add-learning:disabled {
  opacity: 0.7;
  cursor: default;
}
</style>
