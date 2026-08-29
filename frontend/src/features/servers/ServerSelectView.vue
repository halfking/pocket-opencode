<template>
  <div class="server-select-view">
    <div class="top-bar">
      <h1>选择服务器</h1>
      <button class="logout-btn" @click="handleLogout">退出</button>
    </div>

    <div class="server-list">
      <div
        v-for="server in servers"
        :key="server.id"
        class="server-card"
        @click="selectServer(server)"
      >
        <div class="server-icon">🌐</div>
        <div class="server-info">
          <h3>{{ server.name }}</h3>
          <p class="server-url">{{ server.url }}</p>
          <p class="server-desc">{{ server.description }}</p>
        </div>
        <div class="server-status" :class="server.status">
          <span class="status-dot" />
          {{ server.statusText }}
        </div>
      </div>
    </div>

    <div class="footer-hint">
      <p>选择一个服务器节点以查看 OpenCode 实例</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()

interface Server {
  id: string
  name: string
  url: string
  description: string
  status: 'online' | 'offline'
  statusText: string
}

const servers = ref<Server[]>([
  {
    id: 'nps-56',
    name: 'NPS 56 服务器',
    url: 'https://code.kxpms.cn',
    description: '主服务器 (14.103.169.56)',
    status: 'online',
    statusText: '在线',
  },
  {
    id: 'nps-252',
    name: 'NPS 252 服务器',
    url: 'https://code.itestu.cn',
    description: '备用服务器 (115.29.212.252)',
    status: 'online',
    statusText: '在线',
  },
])

function selectServer(server: Server) {
  localStorage.setItem('selected_server', JSON.stringify(server))
  router.push('/instances')
}

function handleLogout() {
  localStorage.removeItem('pocket_user')
  localStorage.removeItem('selected_server')
  router.push('/login')
}
</script>

<style scoped>
.server-select-view {
  min-height: 100%;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}

.top-bar {
  background: var(--bg-card);
  padding: var(--space-3) var(--space-4);
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid var(--border);
}

.top-bar h1 {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.logout-btn {
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  color: var(--brand-primary);
  background: transparent;
  border: 1px solid var(--brand-primary);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.logout-btn:active {
  background: var(--brand-primary);
  color: var(--text-inverse);
}

.server-list {
  flex: 1;
  padding: var(--space-3);
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: var(--spacing-list-gap);
}

.server-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  display: flex;
  align-items: center;
  gap: var(--space-3);
  cursor: pointer;
  transition: background 120ms;
  min-height: 56px;
  max-height: 66px;
}

.server-card:active {
  background: var(--bg-subtle);
}

.server-icon {
  font-size: 20px;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--brand-gradient);
  border-radius: var(--radius-sm);
  flex-shrink: 0;
}

.server-info {
  flex: 1;
  min-width: 0;
}

.server-info h3 {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-url {
  font-size: var(--text-xs);
  color: var(--brand-primary);
  margin: 0;
  font-family: monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-desc {
  font-size: var(--text-xs);
  color: var(--text-muted);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-status {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  flex-shrink: 0;
}

.server-status.online {
  background: var(--success-bg);
  color: var(--success);
}

.server-status.offline {
  background: var(--danger-bg);
  color: var(--danger);
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.footer-hint {
  padding: var(--space-4);
  text-align: center;
  color: var(--text-muted);
  font-size: var(--text-sm);
  border-top: 1px solid var(--border);
}

.footer-hint p {
  margin: 0;
}
</style>
