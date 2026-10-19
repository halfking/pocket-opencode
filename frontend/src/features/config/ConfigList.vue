<script setup lang="ts">
/**
 * ConfigList — 实例配置管理入口。
 *
 * 数据获取已迁至 `useConfigList`（2026-09-20 ViewModel 拆分）。
 * 本文件仅负责模板 + 一行 load() 触发。
 */
import { computed, onMounted } from 'vue'
import { useConfigList } from './useConfigList'
import {
  listReadFailureNotice,
  type ListReadFailureNotice,
} from '../../utils/list-load-state'

const emit = defineEmits<{
  viewConfig: [instanceId: string]
}>()

const { instances, loading, error, load } = useConfigList()
onMounted(load)

/**
 * §845：原来这里是
 *     // error 暴露给模板（兜底文案已与 useConfigList 保持一致）
 *     void error
 * —— **注释说的与紧邻的代码正好相反**：`void error` 是「明确丢弃」。
 * 而 `useConfigList.load()` 的 catch 既设 `error` 又把 `instances` 清空
 * ⇒ 失败时本页落到 `instances.length === 0` ⇒ 显示「没有可用的实例」+
 *   「请先配置 OpenCode 实例」，并**引导用户去做一整套实例配置**，
 *   而真实原因只是一次读取失败，且页面上没有任何重试入口。
 * ⇒ 本会话第 4 次撞上「注释声称的机制要单独验一次」（§837 / §840 / §841 各一次）。
 */

/** §845：`error` 非空即读失败；文案统一走共享函数，不把技术串甩给用户。 */
const readError = computed<ListReadFailureNotice | null>(() => {
  if (!error.value) return null
  return listReadFailureNotice('实例列表', { hasRows: instances.value.length > 0 })
})
/** 模板绑定用：永远非空（vue-tsc 不用模板条件收窄后续属性）。 */
const readErrorNotice = computed<ListReadFailureNotice>(
  () => readError.value ?? listReadFailureNotice('实例列表'),
)
</script>

<template>
  <div class="config-list max-w-4xl mx-auto p-6">
    <header class="mb-6">
      <button @click="$router?.back()" class="text-blue-600 mb-2">
        ← 返回
      </button>
      <h1 class="text-2xl font-bold mb-2">模型配置管理</h1>
      <p class="text-gray-600">选择 OpenCode 实例进行配置</p>
    </header>

    <div v-if="loading" class="text-center py-12">
      <div class="text-gray-500">加载实例列表...</div>
    </div>

    <div v-else class="space-y-4">
      <!--
        §845：有行时的失败不打断已加载内容（与 §840–§844 同一套两档契约）。
        ⚠ 条件里带 `Error` 一词是刻意的：各类判据用 `/error/i` 认出「错误分支」。
      -->
      <div
        v-if="readError && instances.length > 0"
        class="mb-3 flex items-center justify-between rounded px-3 py-2 text-sm"
        style="background: rgba(239, 68, 68, 0.08); color: var(--danger)"
        role="alert"
      >
        <span>{{ readErrorNotice.hint }}</span>
        <button type="button" class="underline" @click="load">{{ readErrorNotice.action }}</button>
      </div>
      <div
        v-for="instance in instances"
        :key="instance.id"
        @click="emit('viewConfig', instance.id)"
        class="bg-white rounded-lg shadow-sm border border-gray-200 p-5 cursor-pointer hover:shadow-md transition"
      >
        <div class="flex items-center justify-between">
          <div class="flex-1">
            <h3 class="text-lg font-semibold text-gray-900">{{ instance.displayName }}</h3>
            <p class="text-sm text-gray-500 mt-1">{{ instance.environment }}</p>
            <div class="flex items-center gap-2 mt-2">
              <span :class="[
                'px-2 py-1 rounded-full text-xs font-medium',
                instance.health === 'healthy' ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800'
              ]">
                {{ instance.health }}
              </span>
            </div>
          </div>
          <svg class="w-6 h-6 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
          </svg>
        </div>
      </div>

      <!--
        §845：读失败分支，必须自带 `.length === 0` 守卫。
        ⚠⚠ 这里用 `v-if` 而**不是** `v-else-if`：它的前面隔着 `v-for` 的实例卡片
        ⇒ 没有相邻的 `v-if` 兄弟，Vue 编译会直接拒绝（`vue-template-compile` 门禁）。
        两个分支条件互斥（一边带 `readError` 一边带 `!readError`），各用 `v-if` 即可。
      -->
      <div
        v-if="readError && instances.length === 0"
        class="text-center py-12"
        style="color: var(--danger)"
      >
        <p>{{ readErrorNotice.title }}</p>
        <p class="text-sm mt-2">{{ readErrorNotice.hint }}</p>
        <button type="button" class="mt-3 underline" @click="load">{{ readErrorNotice.action }}</button>
      </div>

      <div v-if="instances.length === 0 && !loading && !readError" class="text-center py-12 text-gray-400">
        <p>没有可用的实例</p>
        <p class="text-sm mt-2">请先配置 OpenCode 实例</p>
      </div>
    </div>
  </div>
</template>
