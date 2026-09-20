/**
 * useConfigList — ConfigList.vue 的视图模型（2026-09-20 ViewModel 拆分）。
 *
 * 原 `ConfigList.vue` 直接 `import { api } from '../../api/client'` 并在 onMounted 内
 * 一次性拉取实例；抽到本 composable 后：
 *  - Vue 文件薄到只剩模板 + props/emit；
 *  - 状态 `instances / loading / error` 归一为响应式；
 *  - `load()` 可在调用方主动重试（例如错误后 retry）；
 *  - `Instance` 类型显式重导出供上层消费。
 */
import { ref, type Ref } from 'vue'
import { api, type Instance } from '../../api/client'

export interface UseConfigListReturn {
  instances: Ref<Instance[]>
  loading: Ref<boolean>
  error: Ref<string>
  load: () => Promise<void>
}

export function useConfigList(): UseConfigListReturn {
  const instances = ref<Instance[]>([])
  const loading = ref(true)
  const error = ref('')

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      instances.value = await api.getInstances()
    } catch (e) {
      error.value = e instanceof Error ? e.message : '加载实例失败'
      instances.value = []
    } finally {
      loading.value = false
    }
  }

  return { instances, loading, error, load }
}

export type { Instance }
