<template>
  <div class="server-select-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <h1>选择服务器</h1>
      <button class="logout-btn" @click="handleLogout">退出</button>
    </div>

    <!-- 服务器列表 -->
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
          <span class="status-dot"></span>
          {{ server.statusText }}
        </div>
      </div>
    </div>

    <!-- 底部提示 -->
    <div class="footer-hint">
      <p>💡 选择一个服务器节点以查看 OpenCode 实例</p>
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
    statusText: '在线'
  },
  {
    id: 'nps-252',
    name: 'NPS 252 服务器',
    url: 'https://code.itestu.cn',
    description: '备用服务器 (115.29.212.252)',
    status: 'online',
    statusText: '在线'
  }
])

function selectServer(server: Server) {
  // 保存选择的服务器
  localStorage.setItem('selected_server', JSON.stringify(server))
  
  // 跳转到实例列表
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
  min-height: 100vh;
  background: #f5f7fa;
  display: flex;
  flex-direction: column;
}

.top-bar {
  background: white;
  padding: 16px 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
}

.top-bar h1 {
  font-size: 20px;
  font-weight: 600;
  color: #333;
  margin: 0;
}

.logout-btn {
  padding: 8px 16px;
  font-size: 14px;
  color: #667eea;
  background: transparent;
  border: 1px solid #667eea;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.3s;
}

.logout-btn:active {
  background: #667eea;
  color: white;
}

.server-list {
  flex: 1;
  padding: 20px;
  overflow-y: auto;
}

.server-card {
  background: white;
  border-radius: 16px;
  padding: 20px;
  margin-bottom: 16px;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
  display: flex;
  align-items: center;
  gap: 16px;
  cursor: pointer;
  transition: all 0.3s;
  position: relative;
}

.server-card:active {
  transform: scale(0.98);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}

.server-icon {
  font-size: 40px;
  width: 60px;
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border-radius: 12px;
  flex-shrink: 0;
}

.server-info {
  flex: 1;
}

.server-info h3 {
  font-size: 18px;
  font-weight: 600;
  color: #333;
  margin: 0 0 4px 0;
}

.server-url {
  font-size: 13px;
  color: #667eea;
  margin: 0 0 4px 0;
  font-family: monospace;
}

.server-desc {
  font-size: 13px;
  color: #999;
  margin: 0;
}

.server-status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 500;
  padding: 6px 12px;
  border-radius: 20px;
}

.server-status.online {
  background: #d4f4dd;
  color: #2a8a4e;
}

.server-status.offline {
  background: #fee;
  color: #c33;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
}

.footer-hint {
  padding: 20px;
  text-align: center;
  color: #999;
  font-size: 14px;
}

.footer-hint p {
  margin: 0;
}
</style>
