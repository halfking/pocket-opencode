import { ref } from 'vue'

/** 页面可临时覆盖壳层顶栏标题（如列表录音时长）。离开页面必须清掉。 */
export const headerTitleOverride = ref<string | null>(null)

export function setHeaderTitle(title: string | null): void {
  headerTitleOverride.value = title
}
