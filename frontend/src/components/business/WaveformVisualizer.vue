<template>
  <div class="waveform-visualizer" :class="containerClasses">
    <!-- Canvas 绘制波形 -->
    <canvas
      ref="canvasRef"
      class="waveform-canvas"
      :width="canvasWidth"
      :height="canvasHeight"
    />
    
    <!-- 播放进度条（仅在播放模式下显示） -->
    <div
      v-if="showProgress && duration > 0"
      class="waveform-progress"
      :style="{ width: `${progressPercent}%` }"
    />
    
    <!-- 时间显示 -->
    <div v-if="showTime" class="waveform-time">
      <span class="current-time">{{ formattedCurrentTime }}</span>
      <span class="separator">/</span>
      <span class="total-time">{{ formattedDuration }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'

export interface WaveformVisualizerProps {
  /** 波形数据数组（0-1 之间的归一化值） */
  waveformData?: number[]
  /** 是否正在录音 */
  isRecording?: boolean
  /** 是否正在播放 */
  isPlaying?: boolean
  /** 当前播放时间（秒） */
  currentTime?: number
  /** 总时长（秒） */
  duration?: number
  /** 波形颜色 */
  color?: string
  /** 波形宽度 */
  barWidth?: number
  /** 波形间距 */
  barGap?: number
  /** 是否显示进度 */
  showProgress?: boolean
  /** 是否显示时间 */
  showTime?: boolean
  /** 容器宽度 */
  width?: number
  /** 容器高度 */
  height?: number
}

const props = withDefaults(defineProps<WaveformVisualizerProps>(), {
  waveformData: () => [],
  isRecording: false,
  isPlaying: false,
  currentTime: 0,
  duration: 0,
  color: 'var(--color-primary)',
  barWidth: 3,
  barGap: 2,
  showProgress: true,
  showTime: true,
  width: 300,
  height: 60,
})

const emit = defineEmits<{
  (e: 'click', time: number): void
}>()

const canvasRef = ref<HTMLCanvasElement>()
const canvasWidth = computed(() => props.width)
const canvasHeight = computed(() => props.height)

// 实时录音波形数据
const realtimeData = ref<number[]>([])
let animationFrameId: number | null = null
let analyserNode: AnalyserNode | null = null
let audioContext: AudioContext | null = null

// 计算属性
const containerClasses = computed(() => ({
  'waveform-visualizer--recording': props.isRecording,
  'waveform-visualizer--playing': props.isPlaying,
}))

const progressPercent = computed(() => {
  if (props.duration <= 0) return 0
  return (props.currentTime / props.duration) * 100
})

const formattedCurrentTime = computed(() => formatTime(props.currentTime))
const formattedDuration = computed(() => formatTime(props.duration))

// 格式化时间
function formatTime(seconds: number): string {
  const mins = Math.floor(seconds / 60)
  const secs = Math.floor(seconds % 60)
  return `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`
}

// 绘制波形
function drawWaveform() {
  const canvas = canvasRef.value
  if (!canvas) return
  
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  
  const width = canvas.width
  const height = canvas.height
  const centerY = height / 2
  
  // 清空画布
  ctx.clearRect(0, 0, width, height)
  
  // 获取波形数据
  const data = props.isRecording ? realtimeData.value : props.waveformData
  if (data.length === 0) {
    // 绘制空状态
    drawEmptyState(ctx, width, height)
    return
  }
  
  // 计算每个波形条的位置
  const totalBarWidth = props.barWidth + props.barGap
  const barsCount = Math.floor(width / totalBarWidth)
  const step = data.length / barsCount
  
  // 获取计算后的颜色
  const computedColor = getComputedColor()
  
  for (let i = 0; i < barsCount; i++) {
    const dataIndex = Math.floor(i * step)
    const value = data[dataIndex] || 0
    
    // 计算波形条高度（最小高度为 2px）
    const barHeight = Math.max(2, value * height * 0.8)
    
    // 计算位置
    const x = i * totalBarWidth
    const y = centerY - barHeight / 2
    
    // 绘制波形条
    ctx.fillStyle = computedColor
    ctx.beginPath()
    ctx.roundRect(x, y, props.barWidth, barHeight, 1)
    ctx.fill()
  }
  
  // 绘制播放进度遮罩
  if (props.showProgress && props.isPlaying && props.duration > 0) {
    const progressX = (props.currentTime / props.duration) * width
    ctx.fillStyle = 'rgba(0, 0, 0, 0.3)'
    ctx.fillRect(progressX, 0, width - progressX, height)
  }
}

// 绘制空状态
function drawEmptyState(ctx: CanvasRenderingContext2D, width: number, height: number) {
  const centerY = height / 2
  const computedColor = getComputedColor()
  
  // 绘制一条细线
  ctx.strokeStyle = computedColor
  ctx.lineWidth = 1
  ctx.globalAlpha = 0.3
  ctx.beginPath()
  ctx.moveTo(0, centerY)
  ctx.lineTo(width, centerY)
  ctx.stroke()
  ctx.globalAlpha = 1
}

// 获取计算后的颜色值
function getComputedColor(): string {
  const canvas = canvasRef.value
  if (!canvas) return props.color
  
  // 如果是 CSS 变量，通过计算样式获取实际值
  if (props.color.startsWith('var(')) {
    const computedStyle = getComputedStyle(canvas)
    const varName = props.color.replace('var(', '').replace(')', '')
    return computedStyle.getPropertyValue(varName).trim() || '#667eea'
  }
  
  return props.color
}

// 初始化录音波形监听
async function initRecordingVisualization() {
  try {
    audioContext = new AudioContext()
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    const source = audioContext.createMediaStreamSource(stream)
    
    analyserNode = audioContext.createAnalyser()
    analyserNode.fftSize = 256
    source.connect(analyserNode)
    
    // 开始更新波形
    updateRealtimeWaveform()
  } catch (error) {
    console.error('无法初始化音频可视化:', error)
    // 使用模拟数据
    startMockVisualization()
  }
}

// 更新实时波形
function updateRealtimeWaveform() {
  if (!analyserNode) return
  
  const bufferLength = analyserNode.frequencyBinCount
  const dataArray = new Uint8Array(bufferLength)
  
  function update() {
    if (!props.isRecording) {
      cancelAnimationFrame(animationFrameId!)
      return
    }
    
    analyserNode!.getByteFrequencyData(dataArray)
    
    // 归一化数据到 0-1
    const normalizedData = Array.from(dataArray).map(value => value / 255)
    realtimeData.value = normalizedData
    
    // 绘制波形
    drawWaveform()
    
    animationFrameId = requestAnimationFrame(update)
  }
  
  update()
}

// 模拟录音可视化（用于测试或权限不足时）
function startMockVisualization() {
  function update() {
    if (!props.isRecording) {
      cancelAnimationFrame(animationFrameId!)
      return
    }
    
    // 生成模拟波形数据
    const mockData = Array.from({ length: 64 }, () => Math.random() * 0.5 + 0.1)
    realtimeData.value = mockData
    
    drawWaveform()
    
    animationFrameId = requestAnimationFrame(update)
  }
  
  update()
}

// 停止录音可视化
function stopRecordingVisualization() {
  if (animationFrameId) {
    cancelAnimationFrame(animationFrameId)
    animationFrameId = null
  }
  
  if (audioContext) {
    audioContext.close()
    audioContext = null
    analyserNode = null
  }
  
  // 清空实时数据
  realtimeData.value = []
}

// 处理点击事件
function handleClick(event: MouseEvent) {
  const canvas = canvasRef.value
  if (!canvas || props.duration <= 0) return
  
  const rect = canvas.getBoundingClientRect()
  const x = event.clientX - rect.left
  const clickPercent = x / rect.width
  const clickTime = clickPercent * props.duration
  
  emit('click', clickTime)
}

// 监听 props 变化
watch(
  () => [props.waveformData, props.currentTime, props.isPlaying],
  () => {
    if (!props.isRecording) {
      drawWaveform()
    }
  },
  { deep: true }
)

watch(
  () => props.isRecording,
  (isRecording) => {
    if (isRecording) {
      initRecordingVisualization()
    } else {
      stopRecordingVisualization()
    }
  }
)

// 生命周期
onMounted(() => {
  drawWaveform()
  
  if (props.isRecording) {
    initRecordingVisualization()
  }
})

onUnmounted(() => {
  stopRecordingVisualization()
})

// 暴露方法
defineExpose({
  /** 获取当前波形数据 */
  getWaveformData: () => [...realtimeData.value],
  /** 清空波形 */
  clear: () => {
    realtimeData.value = []
    drawWaveform()
  },
})
</script>

<style scoped>
.waveform-visualizer {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  user-select: none;
}

.waveform-canvas {
  display: block;
  cursor: pointer;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
}

.waveform-visualizer--recording .waveform-canvas {
  animation: recording-pulse 2s ease-in-out infinite;
}

@keyframes recording-pulse {
  0%, 100% {
    box-shadow: 0 0 0 0 rgba(var(--color-primary-rgb, 102, 126, 234), 0.2);
  }
  50% {
    box-shadow: 0 0 0 4px rgba(var(--color-primary-rgb, 102, 126, 234), 0);
  }
}

.waveform-progress {
  position: absolute;
  top: 0;
  left: 0;
  height: 100%;
  background: linear-gradient(
    90deg,
    rgba(var(--color-primary-rgb, 102, 126, 234), 0.1),
    rgba(var(--color-primary-rgb, 102, 126, 234), 0.2)
  );
  border-radius: var(--radius-md);
  pointer-events: none;
}

.waveform-time {
  display: flex;
  justify-content: center;
  gap: var(--space-1);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  color: var(--color-text-secondary);
  font-variant-numeric: tabular-nums;
}

.waveform-time .separator {
  color: var(--color-text-tertiary);
}

.waveform-time .current-time {
  color: var(--color-text-primary);
}

.waveform-visualizer--recording .waveform-time .current-time {
  color: var(--color-error);
  animation: blink 1s ease-in-out infinite;
}

@keyframes blink {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.5;
  }
}
</style>
