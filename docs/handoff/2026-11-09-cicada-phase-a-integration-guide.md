# Cicada 集成 Phase A 实施指南

> **快速上手**: 5 步完成 Phase A 集成  
> **预计时间**: 30 分钟  
> **前置条件**: 已完成 Phase A 代码生成

---

## 步骤 1: 验证文件完整性

```bash
# 赋予执行权限
chmod +x scripts/verify-cicada-integration.sh

# 运行验证脚本
./scripts/verify-cicada-integration.sh
```

**预期输出**: 所有核心文件检查通过（✓）

---

## 步骤 2: 前端集成

### 2.1 在 App.vue 中添加字幕组件

```vue
<!-- frontend/src/App.vue -->
<script setup lang="ts">
import { onMounted } from 'vue'
import LiveSubtitleOverlay from './components/LiveSubtitleOverlay.vue'
import { initLiveSubtitleIntegration } from './features/meetings/liveSubtitleIntegration'

// 初始化字幕集成
onMounted(() => {
  initLiveSubtitleIntegration()
})
</script>

<template>
  <div id="app">
    <!-- 现有内容... -->
    
    <!-- 实时字幕悬浮窗 -->
    <LiveSubtitleOverlay />
  </div>
</template>
```

### 2.2 在会议详情页添加字幕开关

```vue
<!-- frontend/src/pages/meetings/MeetingDetail.vue -->
<script setup lang="ts">
import { useLiveSubtitleStore } from '../../stores/liveSubtitle'

const subtitleStore = useLiveSubtitleStore()

function toggleSubtitle() {
  subtitleStore.visible = !subtitleStore.visible
}
</script>

<template>
  <!-- 在工具栏中添加字幕按钮 -->
  <button @click="toggleSubtitle" class="btn-icon">
    <span class="material-symbols-outlined">
      {{ subtitleStore.visible ? 'subtitles' : 'subtitles_off' }}
    </span>
  </button>
</template>
```

### 2.3 类型检查

```bash
cd frontend
npm run typecheck
```

**预期**: 0 errors

---

## 步骤 3: 后端集成

### 3.1 注册翻译路由

编辑 `backend/internal/server/server.go`:

```go
// 在 registerMeetingRoutes 函数中添加
func (s *Server) registerMeetingRoutes(g *echo.Group) {
    // ... 现有路由 ...
    
    // Cicada-inspired 实时翻译
    g.POST("/translate", s.meeting.HandleTranslate)
    g.POST("/translate-batch", s.meeting.HandleBatchTranslate)
}
```

### 3.2 确保 meeting Service 有 llmGateway 依赖

编辑 `backend/internal/meeting/service.go`:

```go
type Service struct {
    store      Store
    llmGateway *llmgateway.Client
}

func NewService(store Store, llmGateway *llmgateway.Client) *Service {
    return &Service{
        store:      store,
        llmGateway: llmGateway,
    }
}
```

### 3.3 编译检查

```bash
cd backend
go build ./...
```

**预期**: 编译成功，无错误

---

## 步骤 4: 本地测试

### 4.1 启动后端

```bash
cd backend
export POCKET_HTTP_PORT=8088
export POCKET_JWT_SECRET="test-secret"
export POCKET_DEV_AUTH=true
./pocketd
```

### 4.2 启动前端

```bash
cd frontend
npm run dev
```

### 4.3 测试翻译 API

```bash
# 测试单次翻译
curl -X POST http://localhost:8088/api/meetings/translate \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, how are you?",
    "sourceLang": "en",
    "targetLang": "zh"
  }'

# 预期响应:
# {"translation":"你好，你好吗？","detectedLang":"en","model":"gpt-4o-mini"}
```

### 4.4 测试字幕流程

1. 打开浏览器访问 http://localhost:5173
2. 进入会议页面
3. 点击"开始录音"
4. 观察字幕悬浮窗是否出现
5. 说话，观察转写片段是否实时显示
6. 点击"翻译"按钮，观察翻译是否出现
7. 拖拽悬浮窗，刷新页面，检查位置是否保持
8. 切换字号（小/中/大）
9. 点击"置顶"，观察边框是否高亮

---

## 步骤 5: 真机验证

### 5.1 构建 APK

```bash
cd frontend
npm run build
npx cap sync android
cd android
./gradlew assembleDebug
```

### 5.2 安装到真机

```bash
adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

### 5.3 真机测试清单

- [ ] 字幕悬浮窗显示正常
- [ ] 拖拽流畅，不卡顿
- [ ] 翻译延迟 < 2 秒
- [ ] 后台切换后字幕继续工作
- [ ] 旋转屏幕后位置自动调整
- [ ] 长时间录音（10 分钟）不崩溃
- [ ] 导出 SRT 文件成功

---

## 故障排查

### 问题 1: TypeScript 类型错误

**症状**: `useLiveSubtitleStore is not defined`

**解决**: 确保 `frontend/src/stores/liveSubtitle.ts` 存在并正确导出

### 问题 2: 翻译 API 返回 502

**症状**: `translation failed: llm gateway error`

**检查**:
1. LLM 网关是否正常运行
2. `POCKET_LLM_GATEWAY_URL` 环境变量是否正确
3. API Key 是否有效

### 问题 3: 字幕不显示

**症状**: 录音开始但字幕窗口不出现

**检查**:
1. `initLiveSubtitleIntegration()` 是否在 `App.vue` 中调用
2. `<LiveSubtitleOverlay />` 是否添加到模板
3. 浏览器控制台是否有错误

### 问题 4: 拖拽不工作

**症状**: 无法拖动字幕窗口

**检查**:
1. CSS `touch-action: none` 是否生效
2. 移动端使用 `touchstart/touchmove/touchend`
3. PC 端使用 `mousedown/mousemove/mouseup`

---

## 性能优化建议

### 1. 翻译缓存

在 `liveSubtitle.ts` 中添加:

```typescript
const translationCache = new Map<string, string>()

function getCachedTranslation(text: string, targetLang: string): string | null {
  const key = `${text}:${targetLang}`
  return translationCache.get(key) || null
}

function setCachedTranslation(text: string, targetLang: string, translation: string) {
  const key = `${text}:${targetLang}`
  translationCache.set(key, translation)
  // 限制缓存大小
  if (translationCache.size > 500) {
    const firstKey = translationCache.keys().next().value
    translationCache.delete(firstKey)
  }
}
```

### 2. 批量翻译优化

开启翻译时，使用批量 API:

```typescript
async function translateAllPending() {
  const pending = segments.value.filter(s => !s.translation && !s.translating)
  if (pending.length === 0) return
  
  // 分批处理（每批最多 50 条）
  const BATCH_SIZE = 50
  for (let i = 0; i < pending.length; i += BATCH_SIZE) {
    const batch = pending.slice(i, i + BATCH_SIZE)
    const texts = batch.map(s => s.text)
    
    const result = await api.post('/api/meetings/translate-batch', {
      texts,
      targetLang: targetLang.value,
    })
    
    // 更新翻译
    batch.forEach((seg, idx) => {
      seg.translation = result.translations[idx]
    })
  }
}
```

---

## 下一步: Phase B 准备

Phase A 完成后，准备 Phase B（系统音频捕获）:

1. **Android 研究** (2d)
   - MediaProjection API 文档
   - AudioPlaybackCaptureConfiguration 示例
   - 权限请求流程

2. **iOS 研究** (2d)
   - ReplayKit 文档
   - RPScreenRecorder 示例
   - 音频格式转换

3. **架构设计** (1d)
   - 混合模式设计
   - 降级策略
   - 性能测试计划

---

## 参考资料

- [Cicada 项目](https://github.com/wquguru/cicada)
- [设计文档](../docs/design/2026-11-09-cicada-inspired-meeting-enhancement.md)
- [实施总结](../docs/design/2026-11-09-cicada-integration-summary.md)
- [Android MediaProjection](https://developer.android.com/guide/topics/media/av-capture)
- [iOS ReplayKit](https://developer.apple.com/documentation/replaykit)

---

**文档版本**: v1.0  
**最后更新**: 2026-11-09  
**维护者**: OpenPocket Team
