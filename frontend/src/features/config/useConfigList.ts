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
import { useApiError } from '../../composables/useApiError'

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
  const apiError = useApiError()

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      instances.value = await api.getInstances()
    } catch (e) {
      error.value = apiError(e, 'errors.loadSettingsFailed')
      // ⚠ 2026-10-18（§845）：原来是无条件 `instances.value = []`。
      //   成功路径会整体赋值，失败路径**根本不该碰 instances** ——
      //   碰了就是把已加载的行抹掉，而「有行时点重试失败」正是这个场景
      //   （§845 给本页加了重试入口，这条就从「无害」变成「有害」了）。
      // ⇒ 与 §843/§844 同一口径：失败只挂提示，不动数据。
      console.error('[config-list] 读取实例失败：', e)
    } finally {
      loading.value = false
    }
  }

  return { instances, loading, error, load }
}

export type { Instance }
