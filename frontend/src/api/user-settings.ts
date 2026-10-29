import { ApiError, http, type HttpOptions } from './http'

export interface RemoteSetting {
  userId: string
  workspaceId: string
  namespace: string
  id: string
  payload: unknown
  updatedAt: number
  hasSecret?: boolean
}

export interface SettingPutResult {
  applied: boolean
  conflict: boolean
  record: RemoteSetting
}

export const userSettingsApi = {
  async list(options: HttpOptions = {}): Promise<RemoteSetting[]> {
    const body = await http<{ settings?: RemoteSetting[] }>('/api/user-settings', options)
    return body.settings ?? []
  },

  async put(namespace: string, id: string, input: {
    payload: unknown
    updatedAt: number
    secret?: string
  }, options: HttpOptions = {}): Promise<SettingPutResult> {
    try {
      return await http<SettingPutResult>(
        `/api/user-settings/${encodeURIComponent(namespace)}/${encodeURIComponent(id)}`,
        { ...options, method: 'PUT', body: JSON.stringify(input) },
      )
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.body?.conflict === true && err.body.record) {
        return err.body as SettingPutResult
      }
      throw err
    }
  },
}
