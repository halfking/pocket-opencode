// api.ts — 技能市场 / 智能体市场的 HTTP API 客户端。
//
// 所有方法都返回强类型结果；对后端的"裸 JSON" 做最小包装。
// 503 (marketplace store not configured) 由调用方识别并降级到空态 UI。

import { http } from '../../api/http'
import type {
  InstallPayload,
  InstallationRef,
  MarketplacePackage,
  PackageVersion,
  PublishPayload,
  RatePayload,
  ReleaseRef,
  ReviewPayload,
  RevokePayload,
  SubmitPayload,
} from './types'

const base = '/api/marketplace'

function unwrap<T>(body: T | { [key: string]: T }): T {
  if (body && typeof body === 'object' && !Array.isArray(body)) {
    const obj = body as Record<string, unknown>
    // 约定：服务端返回 { packages|releases|versions|... : [] } 形态
    for (const key of ['packages', 'releases', 'versions']) {
      if (key in obj) return obj[key] as T
    }
  }
  return body as T
}

export const marketplaceApi = {
  async listPackages(kind?: string): Promise<MarketplacePackage[]> {
    const query = kind ? `?kind=${encodeURIComponent(kind)}` : ''
    return unwrap<MarketplacePackage[]>(await http(`${base}/packages${query}`))
  },

  async listReleases(): Promise<ReleaseRef[]> {
    return unwrap<ReleaseRef[]>(await http(`${base}/releases`))
  },

  async listVersions(packageId: string): Promise<PackageVersion[]> {
    return unwrap<PackageVersion[]>(
      await http(`${base}/packages/${encodeURIComponent(packageId)}/versions`),
    )
  },

  async submit(payload: SubmitPayload): Promise<PackageVersion> {
    return http<PackageVersion>(`${base}/submit`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  async review(payload: ReviewPayload): Promise<{ reviewed: boolean }> {
    return http(`${base}/review`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  async publish(payload: PublishPayload): Promise<ReleaseRef> {
    return http<ReleaseRef>(`${base}/publish`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  async install(payload: InstallPayload): Promise<InstallationRef> {
    return http<InstallationRef>(`${base}/install`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  async revoke(payload: RevokePayload): Promise<{ revoked: boolean }> {
    return http(`${base}/revoke`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  async rate(payload: RatePayload): Promise<{ recorded: boolean }> {
    return http(`${base}/rate`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },
}