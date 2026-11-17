/**
 * VAD 设置组件 - 用于真机测试对比
 */
<template>
  <div class="vad-settings">
    <div class="setting-header">
      <h3>🎤 语音检测方案</h3>
      <span class="badge">实验性</span>
    </div>

    <div class="vad-options">
      <label 
        class="vad-option"
        :class="{ active: currentType === 'energy' }"
      >
        <input 
          type="radio" 
          name="vad-type" 
          value="energy"
          v-model="currentType"
          @change="onVadTypeChange"
        />
        <div class="option-content">
          <div class="option-title">
            <span class="icon">⚡</span>
            <span>能量检测</span>
            <span v-if="currentType === 'energy'" class="current-badge">当前</span>
          </div>
          <div class="option-desc">
            基于音频能量阈值，启动快速，内存占用小
          </div>
          <div class="option-metrics">
            <span class="metric">延迟: ~1550ms</span>
            <span class="metric">内存: ~10MB</span>
          </div>
        </div>
      </label>

      <label 
        class="vad-option"
        :class="{ active: currentType === 'silero' }"
      >
        <input 
          type="radio" 
          name="vad-type" 
          value="silero"
          v-model="currentType"
          @change="onVadTypeChange"
        />
        <div class="option-content">
          <div class="option-title">
            <span class="icon">🧠</span>
            <span>Silero VAD</span>
            <span v-if="currentType === 'silero'" class="current-badge">当前</span>
            <span class="new-badge">新</span>
          </div>
          <div class="option-desc">
            基于深度学习，检测更准确，延迟更低
          </div>
          <div class="option-metrics">
            <span class="metric">延迟: ~500ms</span>
            <span class="metric">内存: ~30MB</span>
            <span class="metric warn">需下载 ~16MB</span>
          </div>
        </div>
      </label>
    </div>

    <div class="setting-note">
      <div class="note-icon">💡</div>
      <div class="note-content">
        <p><strong>测试提示</strong></p>
        <ul>
          <li>切换后需重新开始录音才能生效</li>
          <li>Silero VAD 首次使用需下载模型文件</li>
          <li>建议在 Wi-Fi 环境下测试</li>
        </ul>
      </div>
    </div>

    <!-- 性能统计 -->
    <div v-if="stats" class="vad-stats">
      <h4>本次录音统计</h4>
      <div class="stats-grid">
        <div class="stat-item">
          <span class="stat-label">VAD 方案</span>
          <span class="stat-value">{{ stats.vadType === 'energy' ? '能量检测' : 'Silero VAD' }}</span>
        </div>
        <div class="stat-item">
          <span class="stat-label">分段数量</span>
          <span class="stat-value">{{ stats.segmentCount }}</span>
        </div>
        <div class="stat-item">
          <span class="stat-label">平均延迟</span>
          <span class="stat-value">{{ stats.avgLatency.toFixed(0) }}ms</span>
        </div>
        <div class="stat-item">
          <span class="stat-label">总时长</span>
          <span class="stat-value">{{ (stats.totalDuration / 1000).toFixed(1) }}s</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getPreferredVadType, setPreferredVadType, type VadType } from '@/native/vad-factory'

const currentType = ref<VadType>('energy')
const stats = ref<{
  vadType: VadType
  segmentCount: number
  avgLatency: number
  totalDuration: number
} | null>(null)

onMounted(() => {
  currentType.value = getPreferredVadType()
  loadStats()
})

function onVadTypeChange() {
  setPreferredVadType(currentType.value)
  console.log(`[vad-settings] VAD type changed to: ${currentType.value}`)
  
  // 显示提示
  alert(`已切换到${currentType.value === 'energy' ? '能量检测' : 'Silero VAD'}方案\n\n请重新开始录音以应用新设置`)
}

function loadStats() {
  try {
    const stored = localStorage.getItem('vad-stats')
    if (stored) {
      stats.value = JSON.parse(stored)
    }
  } catch (err) {
    console.warn('[vad-settings] Failed to load stats:', err)
  }
}

// 供外部调用以更新统计
defineExpose({
  updateStats(newStats: typeof stats.value) {
    stats.value = newStats
    try {
      localStorage.setItem('vad-stats', JSON.stringify(newStats))
    } catch (err) {
      console.warn('[vad-settings] Failed to save stats:', err)
    }
  }
})
</script>

<style scoped>
.vad-settings {
  padding: 16px;
}

.setting-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.setting-header h3 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.badge {
  padding: 4px 8px;
  background: #fef3c7;
  color: #92400e;
  font-size: 12px;
  border-radius: 4px;
  font-weight: 500;
}

.vad-options {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 20px;
}

.vad-option {
  display: flex;
  gap: 12px;
  padding: 16px;
  border: 2px solid #e5e7eb;
  border-radius: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.vad-option:hover {
  border-color: #3b82f6;
  background: #eff6ff;
}

.vad-option.active {
  border-color: #3b82f6;
  background: #dbeafe;
}

.vad-option input[type="radio"] {
  width: 20px;
  height: 20px;
  margin-top: 2px;
  cursor: pointer;
}

.option-content {
  flex: 1;
}

.option-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-weight: 600;
  font-size: 16px;
}

.option-title .icon {
  font-size: 20px;
}

.current-badge {
  padding: 2px 8px;
  background: #3b82f6;
  color: white;
  font-size: 11px;
  border-radius: 4px;
  font-weight: 500;
}

.new-badge {
  padding: 2px 8px;
  background: #10b981;
  color: white;
  font-size: 11px;
  border-radius: 4px;
  font-weight: 500;
}

.option-desc {
  color: #6b7280;
  font-size: 14px;
  margin-bottom: 8px;
  line-height: 1.4;
}

.option-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.metric {
  padding: 4px 8px;
  background: #f3f4f6;
  border-radius: 4px;
  font-size: 12px;
  color: #374151;
}

.metric.warn {
  background: #fef3c7;
  color: #92400e;
}

.setting-note {
  display: flex;
  gap: 12px;
  padding: 16px;
  background: #eff6ff;
  border-radius: 8px;
  border-left: 4px solid #3b82f6;
  margin-bottom: 20px;
}

.note-icon {
  font-size: 24px;
  flex-shrink: 0;
}

.note-content {
  flex: 1;
}

.note-content p {
  margin: 0 0 8px 0;
  font-weight: 600;
  color: #1e40af;
}

.note-content ul {
  margin: 0;
  padding-left: 20px;
  color: #1e40af;
}

.note-content li {
  margin-bottom: 4px;
  font-size: 14px;
}

.vad-stats {
  padding: 16px;
  background: #f9fafb;
  border-radius: 8px;
}

.vad-stats h4 {
  margin: 0 0 12px 0;
  font-size: 16px;
  font-weight: 600;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.stat-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.stat-label {
  font-size: 12px;
  color: #6b7280;
}

.stat-value {
  font-size: 18px;
  font-weight: 600;
  color: #111827;
}
</style>
