/**
 * Phase 2.3: 云端优先级配置
 * 
 * 固定优先级策略，替代随机选择
 */

export interface CloudProvider {
  id: string
  score: number
  timeout: number
  enabled: boolean
}

/**
 * Phase 2: 云端供应商优先级列表
 * 按稳定性和准确率排序，取消随机选择
 */
export const CLOUD_PROVIDERS: CloudProvider[] = [
  {
    id: 'mimo-v2.5-asr',
    score: 100,
    timeout: 5000,
    enabled: true,
  },
  {
    id: 'groq/whisper-large-v3',
    score: 90,
    timeout: 3000,
    enabled: true,
  },
  {
    id: 'minimax-asr-1.0',
    score: 80,
    timeout: 5000,
    enabled: true,
  },
  {
    id: 'glm-asr',
    score: 50,
    timeout: 5000,
    enabled: true,
  },
]

/**
 * 获取下一个可用的云端供应商
 * Phase 2: 按优先级顺序返回，不再随机
 */
export function getNextCloudProvider(
  excludeIds: Set<string> = new Set()
): CloudProvider | null {
  for (const provider of CLOUD_PROVIDERS) {
    if (provider.enabled && !excludeIds.has(provider.id)) {
      return provider
    }
  }
  return null
}

/**
 * Phase 2: 快速失败策略
 * 单个供应商超时后立即尝试下一个
 */
export async function transcribeWithFallback(
  audioBlob: Blob,
  options: {
    maxRetries?: number
    onProviderFailed?: (providerId: string, error: Error) => void
  } = {}
): Promise<{ text: string; provider: string; confidence: number }> {
  const maxRetries = options.maxRetries ?? CLOUD_PROVIDERS.length
  const tried = new Set<string>()
  let lastError: Error | null = null

  for (let i = 0; i < maxRetries; i++) {
    const provider = getNextCloudProvider(tried)
    if (!provider) {
      break
    }

    tried.add(provider.id)

    try {
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), provider.timeout)

      const result = await fetch(`/api/stt/${provider.id}`, {
        method: 'POST',
        body: audioBlob,
        signal: controller.signal,
      })

      clearTimeout(timeoutId)

      if (!result.ok) {
        throw new Error(`HTTP ${result.status}`)
      }

      const data = await result.json()
      return {
        text: data.text || '',
        provider: provider.id,
        confidence: data.confidence || 0.8,
      }
    } catch (error) {
      lastError = error as Error
      options.onProviderFailed?.(provider.id, lastError)
      console.warn(`[stt] Provider ${provider.id} failed:`, lastError.message)
      // 立即尝试下一个供应商
      continue
    }
  }

  throw lastError || new Error('All cloud providers failed')
}
