// types.ts — 技能市场 / 智能体市场的共享类型定义。
//
// 与 backend/internal/marketplace/marketplace.go 中的字段对齐；JSON 字段
// 名一致以便直接复用 http 客户端的反序列化结果。

export type PackageKind = 'skill' | 'agent' | 'workflow'
export type Visibility = 'private' | 'workspace' | 'org' | 'public'
export type VersionStatus =
  | 'draft'
  | 'submitted'
  | 'reviewing'
  | 'approved'
  | 'rejected'
  | 'published'
  | 'revoked'

export interface MarketplacePackage {
  package_id: string
  workspace_id: string
  name: string
  kind: PackageKind
  publisher: string
  visibility: Visibility
  created_at: string
}

export interface MarketplaceManifest {
  version: string
  description?: string
  digest: string
  licenses?: string[]
  dependencies?: Array<{ package_id: string; version: string }>
  permissions?: string[]
  compatibility?: Record<string, string>
  runtime?: string
}

export interface PackageVersion {
  version_id: string
  package_id: string
  workspace_id: string
  version: string
  digest: string
  manifest: MarketplaceManifest
  status: VersionStatus
  signature?: string
  reviewer?: string
  submitted_at: string
  published_at?: string
}

export interface ReleaseRef {
  release_id: string
  version_id: string
  channel: string
  published_at: string
}

export interface InstallationRef {
  installation_id: string
  release_id: string
  installed_at: string
}

export interface SubmitPayload {
  package_id?: string
  name: string
  kind: PackageKind
  version: string
  digest: string
  manifest: MarketplaceManifest
  publisher?: string
  signature?: string
  visibility?: Visibility
}

export interface ReviewPayload {
  version_id: string
  approved: boolean
  comment?: string
}

export interface PublishPayload {
  version_id: string
  channel?: string
}

export interface InstallPayload {
  release_id: string
  target_env?: string
}

export interface RevokePayload {
  release_id: string
  reason: string
}

export interface RatePayload {
  release_id: string
  score: number
  comment?: string
}