/**
 * identity.ts — S0-A Identity Core API client.
 *
 * 对接后端 /api/workspaces 子树：
 *   GET    /api/workspaces                  列出我加入的 workspace
 *   POST   /api/workspaces                  创建 workspace
 *   GET    /api/workspaces/{id}             workspace 详情
 *   GET    /api/workspaces/{id}/members     成员列表
 *   POST   /api/workspaces/{id}/members     邀请成员（owner-only）
 *   DELETE /api/workspaces/{id}/members/{uid}
 *   GET    /api/workspaces/{id}/devices     设备列表
 *   POST   /api/workspaces/{id}/devices     注册/刷新设备
 *   DELETE /api/workspaces/{id}/devices/{did}
 */
import { http } from './http'

export interface Workspace {
  id: string
  owner_id: string
  name: string
  type: 'default' | 'shadow'
  created_at: number
}

export interface WorkspaceMember {
  workspace_id: string
  user_id: string
  role: 'owner' | 'invitee'
  invited_at: number
  expires_at: number
}

export interface Device {
  id: string
  user_id: string
  workspace_id: string
  fingerprint: string
  push_token?: string
  os?: string
  last_seen_at: number
  created_at: number
}

export const identityApi = {
  listWorkspaces: () =>
    http<{ workspaces: Workspace[] }>('/api/workspaces').then((r) => r.workspaces),

  createWorkspace: (input: { name: string; type?: 'default' | 'shadow' }) =>
    http<Workspace>('/api/workspaces', {
      method: 'POST',
      body: JSON.stringify(input),
    }),

  getWorkspace: (id: string) => http<Workspace>(`/api/workspaces/${id}`),

  listMembers: (wsId: string) =>
    http<{ members: WorkspaceMember[] }>(`/api/workspaces/${wsId}/members`).then(
      (r) => r.members,
    ),

  inviteMember: (wsId: string, userId: string, expiresInDays?: number) =>
    http<WorkspaceMember>(`/api/workspaces/${wsId}/members`, {
      method: 'POST',
      body: JSON.stringify({
        user_id: userId,
        expires_in_seconds: expiresInDays ? expiresInDays * 86400 : undefined,
      }),
    }),

  removeMember: (wsId: string, userId: string) =>
    http<{ removed: string }>(`/api/workspaces/${wsId}/members/${userId}`, {
      method: 'DELETE',
    }),

  listDevices: (wsId: string) =>
    http<{ devices: Device[] }>(`/api/workspaces/${wsId}/devices`).then((r) => r.devices),

  upsertDevice: (
    wsId: string,
    input: { id: string; fingerprint: string; push_token?: string; os?: string },
  ) =>
    http<Device>(`/api/workspaces/${wsId}/devices`, {
      method: 'POST',
      body: JSON.stringify(input),
    }),

  revokeDevice: (wsId: string, deviceId: string) =>
    http<{ revoked: string }>(`/api/workspaces/${wsId}/devices/${deviceId}`, {
      method: 'DELETE',
    }),
}
