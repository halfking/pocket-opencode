<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, type Instance } from '../../api/client'

const emit = defineEmits<{
  viewConfig: [instanceId: string]
}>()

const instances = ref<Instance[]>([])
const loading = ref(true)

onMounted(async () => {
  try {
    instances.value = await api.getInstances()
  } catch (e: any) {
    console.error('Failed to load instances:', e)
  } finally {
    loading.value = false
  }
})
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

      <div v-if="instances.length === 0" class="text-center py-12 text-gray-400">
        <p>没有可用的实例</p>
        <p class="text-sm mt-2">请先配置 OpenCode 实例</p>
      </div>
    </div>
  </div>
</template>
