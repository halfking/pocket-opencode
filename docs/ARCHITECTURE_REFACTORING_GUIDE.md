# 🎯 APP 整体架构重构 - 实施指南

## 快速开始

本指南帮助您快速理解和实施 OpenCode Pocket 的架构重构方案。

---

## 📚 文档索引

1. **架构规划方案**（主文档）  
   `docs/2026-07-02-app-architecture-refactoring-plan.md`  
   → 完整的架构设计、分层说明、实施路线图

2. **核心公共模块**（代码实现）  
   - `frontend/src/services/device-hub.ts` - 设备管理器
   - `frontend/src/services/storage-hub.ts` - 存储管理器
   - `frontend/src/services/ai-hub.ts` - AI 调度器
   - `frontend/src/services/error-hub.ts` - 错误处理中心

3. **数据库优化**（SQL 脚本）  
   `frontend/src/native/schema-optimization.sql`  
   → 索引补充、触发器、视图、性能优化

---

## 🚀 实施步骤

### Phase 1: 初始化公共模块（第 1 天）

```typescript
// 1. 在 App.vue 中初始化所有 Hub
import { deviceHub } from '@/services/device-hub'
import { storageHub } from '@/services/storage-hub'
import { errorHub, setupGlobalErrorHandler } from '@/services/error-hub'

export default {
  async mounted() {
    // 初始化设备管理器
    await deviceHub.init()
    
    // 初始化存储管理器
    const dbSecret = await getDbSecret() // 从 Keystore 获取
    await storageHub.init(dbSecret)
    
    // 设置全局错误处理
    setupGlobalErrorHandler()
    
    console.log('架构初始化完成')
  }
}
```

### Phase 2: 执行数据库优化（第 1 天）

```bash
# 1. 备份现有数据库
cp frontend/data/lobster.db frontend/data/lobster.db.backup

# 2. 执行优化 SQL
# 在应用启动时自动执行，或手动执行：
sqlite3 frontend/data/lobster.db < frontend/src/native/schema-optimization.sql

# 3. 验证结果
sqlite3 frontend/data/lobster.db "PRAGMA integrity_check;"
```

### Phase 3: 迁移现有代码（第 2-3 天）

#### 示例：笔记创建流程迁移

**原代码**（分散调用）：
```typescript
// notes-store.ts
async createNote(note: Note) {
  // 直接调用 localDB
  await localDB.run('INSERT INTO local_notes ...', [])
  
  // 直接调用 fetch
  const embedding = await fetch('/api/embed', { body: note.content })
  
  // 错误处理缺失
}
```

**新代码**（使用 Hub）：
```typescript
// notes-store.ts
import { storageHub } from '@/services/storage-hub'
import { aiHub } from '@/services/ai-hub'
import { errorHub } from '@/services/error-hub'

async createNote(note: Note) {
  try {
    // 1. 写入本地存储（带缓存、重试、事务）
    await storageHub.write('local_notes', note.id, note, { cache: true })
    
    // 2. 异步生成向量（带降级、限流）
    const embeddings = await aiHub.batchEmbed([note.content])
    
    // 3. 事务写入向量
    await storageHub.transaction([
      { type: 'write', table: 'local_note_vectors', key: note.id, value: { note_id: note.id, embedding: embeddings[0] } }
    ])
    
  } catch (error: any) {
    // 4. 统一错误处理
    errorHub.handle(error, { module: 'notes', operation: 'create' })
    throw error
  }
}
```

### Phase 4: 音频录音迁移（第 3-4 天）

**原代码**（直接调用 MediaRecorder）：
```typescript
// 录音组件
const startRecording = async () => {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
  mediaRecorder = new MediaRecorder(stream)
  // ... 手动管理状态
}
```

**新代码**（使用 DeviceHub）：
```typescript
import { deviceHub } from '@/services/device-hub'

const startRecording = async () => {
  try {
    // 1. 请求音频设备（自动权限检查、互斥访问）
    const audioDevice = await deviceHub.acquireAudioDevice('notes-recorder')
    
    // 2. 开始录音
    // TODO: 使用 AudioRecorder 封装
    
    // 3. 释放设备
    onCleanup(() => audioDevice.release())
    
  } catch (error: any) {
    errorHub.handle(error, { module: 'notes', operation: 'record' })
  }
}
```

---

## 🔍 测试验证

### 1. 设备能力检测

```typescript
import { deviceHub } from '@/services/device-hub'

// 检查设备分级
const tier = deviceHub.getDeviceTier()
console.log(`设备分级: ${tier}`) // high / mid / low

// 检查 STT 能力
const capabilities = deviceHub.getCapabilities()
console.log(`本地 STT: ${capabilities.stt.local}`)
console.log(`云端 STT: ${capabilities.stt.cloud}`)
```

### 2. 存储性能测试

```typescript
import { storageHub } from '@/services/storage-hub'

// 测试写入性能
const start = Date.now()
for (let i = 0; i < 100; i++) {
  await storageHub.write('local_notes', `note-${i}`, { title: 'Test', content: 'Content' })
}
console.log(`100 次写入耗时: ${Date.now() - start}ms`) // 目标 < 500ms

// 测试缓存命中率
await storageHub.read('local_notes', 'note-1') // 第一次从数据库
await storageHub.read('local_notes', 'note-1') // 第二次从缓存（应该更快）
```

### 3. AI 调用测试

```typescript
import { aiHub } from '@/services/ai-hub'

// 测试分类
const result = await aiHub.classify('今天开会讨论了项目进度', ['work', 'life', 'study'])
console.log(`分类结果: ${result.category}, 置信度: ${result.confidence}`)

// 测试降级
// 1. 断网后调用，应该自动降级
// 2. 检查 result.fallback === true
```

### 4. 错误处理测试

```typescript
import { errorHub } from '@/services/error-hub'

// 订阅错误事件
const unsubscribe = errorHub.onError((report) => {
  console.log(`错误类型: ${report.type}, 严重程度: ${report.severity}`)
  console.log(`自动恢复: ${report.recovered}`)
})

// 模拟各种错误
try {
  throw new Error('Network request failed')
} catch (error: any) {
  errorHub.handle(error, { module: 'test', operation: 'simulate' })
}
```

---

## 📊 性能指标

| 指标 | 目标 | 当前 | 测量方法 |
|------|------|------|---------|
| 应用启动时间 | < 1.5s | TBD | `performance.now()` 测量到首屏渲染 |
| 数据库查询 | < 50ms | TBD | 笔记列表查询（100 条） |
| 存储写入 | < 100ms | TBD | 单条笔记写入 |
| AI 分类 | < 2s | TBD | 单次分类请求 |
| 离线可用率 | > 95% | TBD | 核心功能离线可用比例 |

---

## ⚠️ 注意事项

### 1. 数据迁移

- 执行 SQL 优化前**务必备份数据库**
- 触发器会影响写入性能，监控实际影响
- 视图不占用额外存储，可放心使用

### 2. 向后兼容

- 旧代码逐步迁移，不强制一次性替换
- Hub 模块可与现有代码并存
- 优先迁移高频路径（笔记创建、列表查询）

### 3. 性能监控

- 开发环境启用性能日志：`_perf_log` 表
- 生产环境监控关键指标：启动时间、查询延迟
- 定期执行 `VACUUM` 和 `ANALYZE`

### 4. 错误处理

- 所有异步操作必须用 `try-catch` 包裹
- 错误必须经过 `errorHub.handle()` 处理
- 用户提示必须友好，避免技术术语

---

## 🔗 相关文档

- [本地存储设计](./2026-07-02-lobster-local-storage-design.md)
- [Android STT 评估](./2026-07-02-android-stt-evaluation.md)
- [个人助理 APP 方案](./2026-07-02-android-personal-assistant-plan.md)

---

## 💬 问题反馈

遇到问题请：
1. 检查浏览器控制台的 `[DeviceHub]` / `[StorageHub]` 日志
2. 查看 `errorHub.getStats()` 错误统计
3. 执行 `PRAGMA integrity_check` 检查数据库完整性

---

## ✅ 检查清单

完成以下检查确保架构迁移成功：

- [ ] Hub 模块全部初始化成功
- [ ] 数据库优化 SQL 执行完成
- [ ] 至少一个功能模块（如笔记）完成迁移
- [ ] 错误处理覆盖所有异步操作
- [ ] 性能测试通过（启动 < 1.5s，查询 < 50ms）
- [ ] 离线模式测试通过
- [ ] 设备权限请求正常
- [ ] 数据完整性检查通过

---

**祝重构顺利！有任何问题随时反馈。** 🎉
