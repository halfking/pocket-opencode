// store.ts — 技能市场 / 智能体市场的 Pinia store。
//
// 三个独立 slice（skill / agent / workflow）共用同一个 store；视图按 kind
// 过滤显示。错误信息集中暴露给 UI（503 / 网络 / 解析）。

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { marketplaceApi } from './api'
import type {
  InstallPayload,
  InstallationRef,
  MarketplacePackage,
  PackageKind,
  PackageVersion,
  PublishPayload,
  RatePayload,
  ReleaseRef,
  ReviewPayload,
  RevokePayload,
  SubmitPayload,
} from './types'

export const useMarketplaceStore = defineStore('marketplace', () => {
  const packages = ref<MarketplacePackage[]>([])
  const releases = ref<ReleaseRef[]>([])
  const versionsByPackage = ref<Record<string, PackageVersion[]>>({})

  const loading = ref(false)
  const error = ref('')

  const isAvailable = computed(() => error.value === '' && packages.value !== null)

  async function loadPackages(kind?: PackageKind) {
    loading.value = true
    error.value = ''
    try {
      packages.value = await marketplaceApi.listPackages(kind)
    } catch (e: any) {
      error.value = humanizeError(e)
    } finally {
      loading.value = false
    }
  }

  async function loadReleases() {
    try {
      releases.value = await marketplaceApi.listReleases()
    } catch (e: any) {
      error.value = humanizeError(e)
    }
  }

  async function loadVersions(packageId: string): Promise<PackageVersion[]> {
    try {
      const list = await marketplaceApi.listVersions(packageId)
      versionsByPackage.value = { ...versionsByPackage.value, [packageId]: list }
      return list
    } catch (e: any) {
      error.value = humanizeError(e)
      return []
    }
  }

  async function submit(payload: SubmitPayload): Promise<PackageVersion | null> {
    try {
      const v = await marketplaceApi.submit(payload)
      // 本地缓存添加占位（等待下次 reload）
      const exists = packages.value.find((p) => p.package_id === v.package_id)
      if (!exists) {
        packages.value = [
          {
            package_id: v.package_id,
            workspace_id: v.workspace_id,
            name: payload.name,
            kind: payload.kind,
            publisher: payload.publisher || '',
            visibility: payload.visibility || 'workspace',
            created_at: new Date().toISOString(),
          },
          ...packages.value,
        ]
      }
      versionsByPackage.value = {
        ...versionsByPackage.value,
        [v.package_id]: [v, ...(versionsByPackage.value[v.package_id] || [])],
      }
      return v
    } catch (e: any) {
      error.value = humanizeError(e)
      return null
    }
  }

  async function review(payload: ReviewPayload): Promise<boolean> {
    try {
      await marketplaceApi.review(payload)
      return true
    } catch (e: any) {
      error.value = humanizeError(e)
      return false
    }
  }

  async function publish(payload: PublishPayload): Promise<ReleaseRef | null> {
    try {
      const rel = await marketplaceApi.publish(payload)
      releases.value = [rel, ...releases.value]
      return rel
    } catch (e: any) {
      error.value = humanizeError(e)
      return null
    }
  }

  async function install(payload: InstallPayload): Promise<InstallationRef | null> {
    try {
      const inst = await marketplaceApi.install(payload)
      return inst
    } catch (e: any) {
      error.value = humanizeError(e)
      return null
    }
  }

  async function revoke(payload: RevokePayload): Promise<boolean> {
    try {
      await marketplaceApi.revoke(payload)
      return true
    } catch (e: any) {
      error.value = humanizeError(e)
      return false
    }
  }

  async function rate(payload: RatePayload): Promise<boolean> {
    try {
      await marketplaceApi.rate(payload)
      return true
    } catch (e: any) {
      error.value = humanizeError(e)
      return false
    }
  }

  return {
    packages,
    releases,
    versionsByPackage,
    loading,
    error,
    isAvailable,
    loadPackages,
    loadReleases,
    loadVersions,
    submit,
    review,
    publish,
    install,
    revoke,
    rate,
  }
})

function humanizeError(e: any): string {
  const msg = e?.message || String(e)
  // 503：通常是 marketplace store 未配置（PG 缺失 / 离线模式）。
  if (/503/.test(msg) || /not configured/i.test(msg)) {
    return '市场服务暂未启用（PG 未配置或离线模式）'
  }
  if (/network|fetch/i.test(msg)) {
    return '网络异常，请稍后再试'
  }
  return msg
}