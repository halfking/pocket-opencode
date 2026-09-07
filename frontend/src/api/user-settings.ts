import { ApiError, http } from './http'

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
  async list(): Promise<RemoteSetting[]> {
    const body = await http<{ settings?: RemoteSetting[] }>('/api/user-settings')
    return body.settings ?? []
  },

  async put(namespace: string, id: string, input: {
    payload: unknown
    updatedAt: number
    secret?: string
  }): Promise<SettingPutResult> {
    try {
      return await http<SettingPutResult>(
        `/api/user-settings/${encodeURIComponent(namespace)}/${encodeURIComponent(id)}`,
        { method: 'PUT', body: JSON.stringify(input) },
      )
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.body) {
        return err.body as SettingPutResult
      }
      throw err
    }
  },
}
