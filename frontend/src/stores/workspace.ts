/**
 * workspace.ts — S0-A workspace 状态管理。
 *
 * 持有当前选中的 workspace_id（其他 store 通过它来 scope），以及成员/设备列表。
 * workspace_id 选择影响所有后续 API 调用的隔离边界。
 *
 * 设计：登录后由 F0.7 的扩展登录流程自动 setWorkspaceId。多 workspace
 * 用户可在 UI 切换（v1 仅显示，不做切换器；切换器留给 S1）。
 */
import { defineStore } from 'pinia'
import { identityApi, type Workspace, type WorkspaceMember, type Device } from '../api/identity'

const ACTIVE_WS_KEY = 'pocket_active_ws'

export const useWorkspaceStore = defineStore('workspace', {
  state: () => ({
    /** 当前激活的 workspace id。所有后续业务 API 隐式以此为隔离边界。 */
    activeId: localStorage.getItem(ACTIVE_WS_KEY) || 'default',
    workspaces: [] as Workspace[],
    members: [] as WorkspaceMember[],
    devices: [] as Device[],
    loading: false,
  }),
  getters: {
    hasMultiple: (s) => s.workspaces.length > 1,
    current: (s) => s.workspaces.find((w) => w.id === s.activeId) || null,
    isOwner: (s) =>
      s.members.some((m) => m.user_id === s.activeId && m.role === 'owner'),
  },
  actions: {
    /** 登录后或切换 workspace 后调用。 */
    setActiveWorkspace(id: string) {
      this.activeId = id
      localStorage.setItem(ACTIVE_WS_KEY, id)
    },

    async loadWorkspaces() {
      this.loading = true
      try {
        this.workspaces = await identityApi.listWorkspaces()
        // 若当前 activeId 不在列表里（如刚登录），自动选第一个。
        if (!this.workspaces.some((w) => w.id === this.activeId)) {
          const first = this.workspaces[0]
          if (first) this.setActiveWorkspace(first.id)
        }
      } finally {
        this.loading = false
      }
    },

    async loadMembers() {
      if (!this.activeId) return
      this.members = await identityApi.listMembers(this.activeId)
    },

    async loadDevices() {
      if (!this.activeId) return
      this.devices = await identityApi.listDevices(this.activeId)
    },

    async invite(userId: string, expiresInDays?: number) {
      await identityApi.inviteMember(this.activeId, userId, expiresInDays)
      await this.loadMembers()
    },

    async removeMember(userId: string) {
      await identityApi.removeMember(this.activeId, userId)
      await this.loadMembers()
    },

    async revokeDevice(deviceId: string) {
      await identityApi.revokeDevice(this.activeId, deviceId)
      await this.loadDevices()
    },
  },
})
