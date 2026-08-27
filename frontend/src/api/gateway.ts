/**
 * Gateway API — llm-gateway-go 运维控制面。
 *
 * 所有请求都打 pocketd 的白名单代理（/api/llm-gateway/nodes/...），由后端
 * 持有网关 admin JWT。前端从不接触网关凭据。
 *
 * 后端代理表见 backend/internal/server/llm_gateway_nodes_handler.go 的
 * gatewayProxyRoutes —— 这里的每个方法都对应其中一条已登记路由。
 */
import { http } from './http'

// ─────────────────────────────────────────────────────────────────────────────
// 节点
// ─────────────────────────────────────────────────────────────────────────────

export type GatewayHealthStatus = 'unknown' | 'ok' | 'error'

export interface GatewayNode {
  id: number
  workspaceId: string
  name: string
  baseURL: string
  adminUsername: string
  /** 后端只告诉我们"已配置"，密码本身永不下发。 */
  adminPasswordSet: boolean
  dataApiKeySet: boolean
  enabled: boolean
  healthStatus: GatewayHealthStatus
  healthError?: string
  /** 上次探测时网关返回的角色。非 super_admin 时供应商页/探测会 403。 */
  healthRole?: string
  healthAt?: string
  createdAt: string
  updatedAt: string
}

export interface GatewayNodeListResponse {
  nodes: GatewayNode[]
  total: number
  /** 后端是否允许私网目标（POCKET_LLM_GATEWAY_ALLOW_PRIVATE）。 */
  allowPrivateHosts: boolean
}

export interface GatewayNodeInput {
  name?: string
  baseURL?: string
  adminUsername?: string
  /** 留空 = 保留现有密码（更新时）。 */
  adminPassword?: string
  dataApiKey?: string
  enabled?: boolean
}

export interface GatewayProbeResult {
  ok: boolean
  status: GatewayHealthStatus
  role: string
  error?: string
  warning?: string
}

export function listNodes(): Promise<GatewayNodeListResponse> {
  return http('/api/llm-gateway/nodes')
}

export function getNode(id: number): Promise<GatewayNode> {
  return http(`/api/llm-gateway/nodes/${id}`)
}

export function createNode(input: GatewayNodeInput): Promise<GatewayNode> {
  return http('/api/llm-gateway/nodes', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export function updateNode(id: number, input: GatewayNodeInput): Promise<GatewayNode> {
  return http(`/api/llm-gateway/nodes/${id}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  })
}

export function deleteNode(id: number): Promise<{ ok: boolean }> {
  return http(`/api/llm-gateway/nodes/${id}`, { method: 'DELETE' })
}

export function probeNode(id: number): Promise<GatewayProbeResult> {
  return http(`/api/llm-gateway/nodes/${id}/probe`, { method: 'POST' })
}

// ─────────────────────────────────────────────────────────────────────────────
// 供应商
// ─────────────────────────────────────────────────────────────────────────────

export interface GatewayProvider {
  id: number
  code: string
  display_name: string
  category: string
  protocol: string
  base_url: string
  enabled: boolean
  manual_disabled: boolean
  vendor_name: string
  active_credential_count: number
  healthy_credential_count: number
  warning_credential_count: number
  unreachable_credential_count: number
  routable_binding_count: number
  total_binding_count: number
  health_status: string
  health_checked_at?: string
  quality_fix_mode: string
}

/** 翻转供应商的 enabled 状态（flip，非 set）。需要 admin 角色 + 网关 super_admin。 */
export function toggleProvider(nodeId: number, providerId: number) {
  return http(`/api/llm-gateway/nodes/${nodeId}/providers/${providerId}/toggle`, {
    method: 'POST',
  })
}

export function listProviders(nodeId: number, search?: string): Promise<{ providers: GatewayProvider[] }> {
  const q = search ? `?search=${encodeURIComponent(search)}` : ''
  return http(`/api/llm-gateway/nodes/${nodeId}/providers${q}`)
}

// ─────────────────────────────────────────────────────────────────────────────
// 凭据 × 模型
// ─────────────────────────────────────────────────────────────────────────────

/** 派生的 5 状态，优先级 manual > probe > offer > binding > available。 */
export type ModelEffectiveState =
  | 'available'
  | 'manual_disabled'
  | 'probe_broken'
  | 'offer_missing'
  | 'binding_missing'

export interface CredentialModelStatus {
  raw_model_name: string
  offer_available: boolean
  offer_unavailable_reason?: string
  binding_available: boolean
  binding_unavailable_reason?: string
  /** 'broken_confirmed' | 'healthy_confirmed' | 'recovering' | 'unknown' */
  probe_state: string
  probe_last_status?: string
  probe_last_attempt_at?: string
  recent_success_rate?: number
  recent_samples: number
  p95_latency_ms?: number
  avg_latency_ms?: number
  /** 'bg' | 'live' | 'no_data' —— P95 的来源 */
  p95_source: string
  /** 'live'（24h 内被调用过）| 'declared'（从未调用） */
  data_source: string
  last_used_at?: string
  total_calls: number
  effective_state: ModelEffectiveState
  model_disabled_reason?: string
}

export interface GatewayCredential {
  id: number
  provider_id: number
  provider_name: string
  label: string
  status: string
  availability_state: string
  health_status: string
  quota_state: string
  concurrency_limit?: number
  concurrency_limit_auto?: number
  effective_concurrency: number
  manual_disabled: boolean
  consecutive_failures: number
  availability_recover_at?: string
  state_reason_code?: string
  state_reason_detail?: string
  health_checked_at?: string
  total_requests: number
  model_total?: number
  model_available?: number
  broken_model_count?: number
  /** 仅 detail 模式（带 credential_id）返回。 */
  models?: CredentialModelStatus[]
  aggregated_success_rate?: number
}

export interface CredentialListResponse {
  credentials: GatewayCredential[]
  count: number
  meta?: {
    cache_hit: boolean
    generated_at: string
    expires_at: string
    ttl_seconds: number
    server_duration_ms: number
  }
}

export function listCredentials(nodeId: number, providerId?: number): Promise<CredentialListResponse> {
  const q = providerId ? `?provider_id=${providerId}` : ''
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials${q}`)
}

/** 详情模式：返回的 credentials[0].models 是 per-model 明细。 */
export function getCredential(nodeId: number, credentialId: number): Promise<CredentialListResponse> {
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/${credentialId}`)
}

export function getCredentialHistory(
  nodeId: number,
  credentialId: number,
  rawModel?: string,
  limit = 50,
): Promise<any> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (rawModel) params.set('raw_model', rawModel)
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/${credentialId}/history?${params}`)
}

// ── 写操作（需要 pocket admin 角色）──

export function promoteCredential(nodeId: number, credentialId: number, reason?: string) {
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/promote`, {
    method: 'POST',
    body: JSON.stringify({ credential_id: credentialId, reason }),
  })
}

export function demoteCredential(nodeId: number, credentialId: number, reason?: string) {
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/demote`, {
    method: 'POST',
    body: JSON.stringify({ credential_id: credentialId, reason }),
  })
}

/** 精确到 (credential, raw_model) 的手工上下线。 */
export function toggleCredentialModel(
  nodeId: number,
  credentialId: number,
  rawModel: string,
  action: 'online' | 'offline',
  reason?: string,
) {
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/model-toggle`, {
    method: 'POST',
    body: JSON.stringify({ credential_id: credentialId, raw_model: rawModel, action, reason }),
  })
}

export function setManualDisabled(nodeId: number, credentialId: number, reason?: string) {
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/set-manual-disabled`, {
    method: 'POST',
    body: JSON.stringify({ credential_id: credentialId, reason }),
  })
}

export function clearManualDisabled(nodeId: number, credentialId: number) {
  return http(`/api/llm-gateway/nodes/${nodeId}/credentials/clear-manual-disabled`, {
    method: 'POST',
    body: JSON.stringify({ credential_id: credentialId }),
  })
}

// ─────────────────────────────────────────────────────────────────────────────
// 模型树 / 路由
// ─────────────────────────────────────────────────────────────────────────────

export interface ModelTreeCredential {
  credential_id: number
  credential_label: string
  credential_status: string
  provider_id: number
  provider_name: string
  available: boolean
  tier: number
  weight: number
  unit_price_in_per_1m?: number
  unit_price_out_per_1m?: number
  currency?: string
  success_rate: number
  p95_latency_ms: number
  runtime_routable: boolean
  runtime_block_reason?: string
}

export interface ModelTreeVariant {
  variant: string
  canonical_name: string
  tags: string[]
  credentials: ModelTreeCredential[]
}

export interface ModelTreeGeneration {
  generation: string
  variants: ModelTreeVariant[]
}

export interface ModelTreeFamily {
  family: string
  generations: ModelTreeGeneration[]
}

export function getModelTree(nodeId: number, featuredOnly = false): Promise<any> {
  const q = featuredOnly ? '?featured_only=true' : ''
  return http(`/api/llm-gateway/nodes/${nodeId}/models${q}`)
}

export function getRoutingHealth(nodeId: number): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/routing/health`)
}

/** 真实发一次请求验活，会产生上游调用与费用。需要 admin 角色 + 网关 super_admin。 */
export function probeModel(nodeId: number, model: string, maxTokens = 20): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/routing/probe`, {
    method: 'POST',
    body: JSON.stringify({ model, max_tokens: maxTokens }),
  })
}

// ─────────────────────────────────────────────────────────────────────────────
// 汇总
// ─────────────────────────────────────────────────────────────────────────────

export interface GatewayOverview {
  node: GatewayNode
  generatedAt: string
  board?: any
  routingHealth?: any
  credentials?: CredentialListResponse
  /** 部分块失败时只标记该块，其余照常返回。 */
  errors?: Record<string, string>
}

export function getOverview(nodeId: number, days = 1): Promise<GatewayOverview> {
  return http(`/api/llm-gateway/nodes/${nodeId}/overview?days=${days}`)
}

// ─────────────────────────────────────────────────────────────────────────────
// 模型目录（按家族/版本，含 modality）— 供"按模态默认模型"与目录页使用
// ─────────────────────────────────────────────────────────────────────────────

export type ModelModality = 'text' | 'vision' | 'audio' | 'video' | 'multimodal' | 'embedding'

export interface AvailableModelVersion {
  canonical_name: string
  display_name?: string
  modality?: ModelModality | string
  context_window?: number
  parameters_b?: number
  aliases?: string[]
  raw_names?: string[]
  provider_count?: number
  featured?: boolean
  tags?: string[]
}

export interface AvailableModelFamily {
  family: string
  versions?: AvailableModelVersion[]
}

export interface AvailableModelsResponse {
  families?: AvailableModelFamily[]
}

/** 可用模型目录（含模态/上下文窗口/精选标记）。需要网关节点已配置 admin 凭据。 */
export function getAvailableModels(nodeId: number): Promise<AvailableModelsResponse> {
  return http(`/api/llm-gateway/nodes/${nodeId}/catalog/available-models`)
}

// ─────────────────────────────────────────────────────────────────────────────
// 任务识别（work_types）：8 个 L1 任务类型及其模型路由
// ─────────────────────────────────────────────────────────────────────────────

export interface WorkTypeRoute {
  model?: string
  canonical_model?: string
  weight?: number
  [k: string]: unknown
}

export interface WorkType {
  key: string
  name?: string
  description?: string
  enabled?: boolean
  routes?: WorkTypeRoute[]
  [k: string]: unknown
}

export function getWorkTypes(nodeId: number): Promise<{ work_types?: WorkType[]; [k: string]: unknown }> {
  return http(`/api/llm-gateway/nodes/${nodeId}/work-types`)
}

export function getWorkTypeStats(nodeId: number): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/work-types/stats`)
}

/** 整体替换某任务类型的模型路由（上游 PUT /api/admin/work-types/{key}/routes）。 */
export function replaceWorkTypeRoutes(nodeId: number, key: string, routes: WorkTypeRoute[]) {
  return http(`/api/llm-gateway/nodes/${nodeId}/work-types/${key}/routes`, {
    method: 'PUT',
    body: JSON.stringify({ routes }),
  })
}

/** 更新任务类型配置（描述/启用等，局部字段）。 */
export function updateWorkType(nodeId: number, key: string, patch: Record<string, unknown>) {
  return http(`/api/llm-gateway/nodes/${nodeId}/work-types/${key}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  })
}

// ─────────────────────────────────────────────────────────────────────────────
// 任务默认路由（task_type × profile × tier → canonical_model）
// ─────────────────────────────────────────────────────────────────────────────

export interface TaskDefaultRouting {
  id: number
  task_type: string
  profile?: string
  tier?: 'primary' | 'secondary' | 'fallback' | string
  canonical_model: string
  tenant_id?: string
  priority?: number
  reason?: string
  expires_at?: string
  created_at?: string
  updated_at?: string
}

export function listTaskDefaults(nodeId: number, query?: { active?: string; task_type?: string; profile?: string }) {
  const q = new URLSearchParams()
  if (query?.active) q.set('active', query.active)
  if (query?.task_type) q.set('task_type', query.task_type)
  if (query?.profile) q.set('profile', query.profile)
  const qs = q.toString()
  return http<{ defaults?: TaskDefaultRouting[]; [k: string]: unknown }>(
    `/api/llm-gateway/nodes/${nodeId}/auto-route/defaults${qs ? '?' + qs : ''}`,
  )
}

export function createTaskDefault(
  nodeId: number,
  input: { task_type: string; canonical_model: string; profile?: string; tier?: string; priority?: number; reason?: string },
) {
  return http(`/api/llm-gateway/nodes/${nodeId}/auto-route/defaults`, {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export function updateTaskDefault(nodeId: number, id: number, patch: Record<string, unknown>) {
  return http(`/api/llm-gateway/nodes/${nodeId}/auto-route/defaults/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  })
}

export function deleteTaskDefault(nodeId: number, id: number) {
  return http(`/api/llm-gateway/nodes/${nodeId}/auto-route/defaults/${id}`, { method: 'DELETE' })
}

// ─────────────────────────────────────────────────────────────────────────────
// 路由策略 / 精选模型 / 评分权重 / 默认限流
// ─────────────────────────────────────────────────────────────────────────────

export function getRoutingPolicy(nodeId: number): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/routing/policy`)
}

export function getFeaturedModels(nodeId: number): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/routing/featured`)
}

export function setFeaturedModels(nodeId: number, featured_models: string[]) {
  return http(`/api/llm-gateway/nodes/${nodeId}/routing/featured`, {
    method: 'POST',
    body: JSON.stringify({ featured_models }),
  })
}

export function getScoringWeights(nodeId: number): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/routing/scoring-weights`)
}

export function getDefaultLimits(nodeId: number): Promise<any> {
  return http(`/api/llm-gateway/nodes/${nodeId}/config/default-limits`)
}
