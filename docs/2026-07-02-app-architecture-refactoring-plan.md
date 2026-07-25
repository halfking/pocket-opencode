# 🏗️ OpenCode Pocket APP 整体架构重构规划

**版本**: v1.0.0  
**日期**: 2026-07-02  
**状态**: 架构规划方案  
**目标**: 公共模块抽取 + 设备交互统一 + 本地存储审计 + 高可用高可靠

---

## 📋 执行摘要

本文档针对 OpenCode Pocket 个人助理 APP 的**整体架构进行系统性规划**，重点解决：

1. **设备交互层统一化** — 录音、麦克风、语音识别、生物识别等外部设备的统一调度
2. **公共处理单元抽取** — 避免代码重复，建立可复用的核心模块
3. **本地存储审计与优化** — 数据库层面的性能、安全、可靠性审计
4. **高可用高可靠保证** — 错误处理、降级策略、数据一致性

**核心原则**：
- 简洁：避免过度抽象，每个抽象层必须服务于至少 3 个使用方
- 快速：关键路径延迟 < 500ms，启动时间 < 1.5s
- 可靠：离线可用、降级优雅、数据不丢失

---

## 🎯 架构分层全景

```
┌─────────────────────────────────────────────────────────────┐
│                   UI 层 (Vue 组件)                           │
│     notes/  meetings/  email/  vault/  tasks/  ...          │
├─────────────────────────────────────────────────────────────┤
│                状态层 (Pinia Stores)                         │
│     useNotesStore  useEmailStore  useVaultStore ...         │
├─────────────────────────────────────────────────────────────┤
│                  API 层 (统一封装)                           │
│     api/client.ts + notes.ts + email.ts + stt.ts ...       │
├─────────────────────────────────────────────────────────────┤
│            🔥 公共处理单元层 (本次重点)                      │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ 设备管理器  │  存储管理器  │  AI调度器  │  错误处理  │   │
│  │ DeviceHub  │  StorageHub │  AIHub    │  ErrorHub  │   │
│  └─────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────┤
│         🎙️ 设备交互层 (Capacitor 插件统一封装)              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ 音频  │ 语音识别 │ 生物识别 │ 存储 │ 网络 │ 通知    │   │
│  └─────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────┤
│              💾 本地存储层 (审计与优化)                      │
│     LocalDB (SQLCipher) + IndexedDB + FileSystem           │
├─────────────────────────────────────────────────────────────┤
│                   后端服务层                                 │
│     pocketd (Go) + kxmemory (Python FastAPI)               │
└─────────────────────────────────────────────────────────────┘
```

---

## 一、🎙️ 设备交互层统一架构

### 1.1 当前问题

- **分散调用**：各功能模块直接调用 Capacitor 插件，缺乏统一管理
- **权限混乱**：录音权限、麦克风权限在多处请求，无统一状态
- **错误处理不一致**：设备不支持、权限拒绝等错误各处理各的
- **资源竞争**：多个模块同时使用麦克风导致冲突

### 1.2 解决方案：DeviceHub 统一设备管理器

#### 架构设计

```typescript
// frontend/src/services/device-hub.ts

interface DeviceCapabilities {
  audio: {
    recording: boolean
    playback: boolean
    permissions: 'granted' | 'denied' | 'prompt'
  }
  stt: {
    local: boolean      // sherpa-onnx 可用
    cloud: boolean      // Groq 可用
    activeEngine: 'local' | 'cloud' | 'hybrid'
  }
  biometric: {
    available: boolean
    types: ('fingerprint' | 'face' | 'iris')[]
  }
  storage: {
    available: number   // 可用空间 MB
    encrypted: boolean
  }
  network: {
    online: boolean
    type: 'wifi' | '4g' | '5g' | 'offline'
  }
}

class DeviceHub {
  private capabilities: DeviceCapabilities
  private activeDevices: Map<string, DeviceHandle>
  
  // 统一权限请求
  async requestPermission(type: 'audio' | 'biometric' | 'storage'): Promise<boolean>
  
  // 设备能力检测（启动时调用一次，缓存结果）
  async detectCapabilities(): Promise<DeviceCapabilities>
  
  // 音频设备管理
  async acquireAudioDevice(consumer: string): Promise<AudioDevice>
  async releaseAudioDevice(consumer: string): Promise<void>
  
  // 设备分级（用于 STT 引擎选择）
  getDeviceTier(): 'high' | 'mid' | 'low'
  
  // 网络状态监听
  onNetworkChange(callback: (status: NetworkStatus) => void): void
}

export const deviceHub = new DeviceHub()
```

#### 关键特性

1. **单例模式**：全局唯一实例，避免重复初始化
2. **能力缓存**：启动时检测一次，后续直接读缓存
3. **资源锁**：录音设备互斥访问（会议录音时笔记录音被阻塞）
4. **权限统一**：所有权限请求经过 DeviceHub，统一处理拒绝/降级
5. **设备分级**：根据 CPU/RAM 自动选择 STT 引擎（Tier 1/2/3）

### 1.3 音频录音统一封装

```typescript
// frontend/src/services/audio-recorder.ts

interface RecordingOptions {
  format: 'wav' | 'pcm'
  sampleRate: 16000 | 44100
  channels: 1 | 2
  maxDuration?: number  // 最大时长秒，防止内存溢出
}

class AudioRecorder {
  private deviceHub: DeviceHub
  private mediaRecorder: MediaRecorder | null
  
  async startRecording(options: RecordingOptions): Promise<void>
  async stopRecording(): Promise<AudioFile>
  async pauseRecording(): Promise<void>
  async resumeRecording(): Promise<void>
  
  // 实时音量监测（录音时显示波形）
  onVolumeChange(callback: (volume: number) => void): void
  
  // VAD 静音检测（会议录音分段用）
  onVADSegment(callback: (segment: AudioSegment) => void): void
}

export const audioRecorder = new AudioRecorder(deviceHub)
```

### 1.4 语音识别统一调度

```typescript
// frontend/src/services/stt-hub.ts

interface STTOptions {
  language: 'zh' | 'en' | 'auto'
  preferLocal: boolean
  fallbackCloud: boolean
  confidenceThreshold: number  // < 阈值自动回退云端
}

class STTHub {
  private deviceHub: DeviceHub
  private localEngine: SherpaEngine | null
  private cloudEngine: GroqEngine
  
  async transcribe(audio: AudioFile, options: STTOptions): Promise<STTResult>
  
  // 流式识别（会议录音用）
  async startStreaming(options: STTOptions): Promise<void>
  async stopStreaming(): Promise<STTResult>
  
  // 自动引擎选择
  private selectEngine(options: STTOptions): 'local' | 'cloud'
  
  // 降级策略
  private async fallback(audio: AudioFile, reason: string): Promise<STTResult>
}

export const sttHub = new STTHub(deviceHub)
```

**降级策略**：
1. 优先本地 sherpa-onnx（离线、快速、免费）
2. 低置信度（< 0.7）自动重试云端 Groq
3. 本地引擎崩溃 → 自动切云端
4. 网络断开 → 纯本地模式，提示用户"离线识别准确率较低"

---

## 二、🧩 公共处理单元抽取

### 2.1 StorageHub - 统一存储管理器

#### 当前问题

- **多入口混乱**：`localDB`、`IndexedDB`、`FileSystem` 各自为政
- **事务缺失**：笔记写入 + 向量写入非原子，可能不一致
- **缓存策略缺失**：重复查询无缓存，性能浪费
- **错误恢复弱**：写入失败后无重试、无回滚

#### 解决方案

```typescript
// frontend/src/services/storage-hub.ts

interface StorageOptions {
  cache?: boolean
  transaction?: boolean
  retry?: number
}

class StorageHub {
  private localDB: LocalDB
  private cache: LRUCache<string, any>
  private pendingWrites: Queue<WriteTask>
  
  // 统一读接口（自动缓存）
  async read<T>(key: string, opts?: StorageOptions): Promise<T | null>
  
  // 统一写接口（自动重试）
  async write<T>(key: string, value: T, opts?: StorageOptions): Promise<void>
  
  // 事务支持（多表原子写入）
  async transaction(operations: Operation[]): Promise<void>
  
  // 离线队列（网络断开时暂存，恢复后上传）
  async enqueueOfflineWrite(op: Operation): Promise<void>
  
  // 存储空间管理
  async getUsage(): Promise<{ used: number, available: number }>
  async cleanup(strategy: 'old' | 'unused' | 'audio'): Promise<number>
}

export const storageHub = new StorageHub(localDB)
```

#### 关键优化

1. **LRU 缓存**：热数据常驻内存，减少 SQLite 查询
2. **写合并**：100ms 内多次写同一 key 合并为一次
3. **离线队列**：网络断开时写操作入队，恢复后批量同步
4. **自动清理**：音频文件转写后自动删除，节省空间

### 2.2 AIHub - AI 调用统一调度

#### 当前问题

- **重复代码**：笔记分类、邮件分类、会议纪要都调 LLM，逻辑雷同
- **无降级**：kxmemory 挂了就全挂
- **无限流**：并发 10 个 LLM 请求可能被 rate limit
- **prompt 分散**：各处硬编码 prompt，难以统一优化

#### 解决方案

```typescript
// frontend/src/services/ai-hub.ts

interface AITask {
  type: 'classify' | 'summarize' | 'extract' | 'embed'
  input: string
  options?: Record<string, any>
}

class AIHub {
  private requestQueue: PQueue  // 限流队列
  private promptLibrary: Map<string, string>
  
  // 统一 AI 调用入口
  async execute(task: AITask): Promise<AIResult>
  
  // 批量嵌入（笔记向量化）
  async batchEmbed(texts: string[]): Promise<number[][]>
  
  // 分类（笔记/邮件共用）
  async classify(text: string, categories: string[]): Promise<ClassifyResult>
  
  // 总结（笔记/邮件/会议共用）
  async summarize(text: string, maxLength: number): Promise<string>
  
  // 降级策略
  private async fallback(task: AITask, error: Error): Promise<AIResult>
}

export const aiHub = new AIHub()
```

**降级策略**：
- kxmemory 不可用 → 直接调 Groq/OpenAI API（绕过中间层）
- LLM 超时 → 返回空分类 + 提示用户"AI 处理中，稍后刷新"
- Rate limit → 入队重试，指数退避

### 2.3 ErrorHub - 统一错误处理

#### 当前问题

- **静默失败**：很多地方 `catch(e) { console.log(e) }`，用户无感知
- **无上报**：错误未收集，无法分析线上问题
- **无恢复**：网络错误、权限错误无引导用户修复

#### 解决方案

```typescript
// frontend/src/services/error-hub.ts

interface ErrorContext {
  module: string
  operation: string
  userId?: string
  deviceInfo?: DeviceCapabilities
}

class ErrorHub {
  private errorQueue: ErrorReport[]
  
  // 统一错误处理
  handle(error: Error, context: ErrorContext): void
  
  // 错误分类
  private classify(error: Error): 'network' | 'permission' | 'storage' | 'ai' | 'unknown'
  
  // 用户提示
  private notify(error: Error, type: string): void
  
  // 错误上报（批量，避免打爆后端）
  private async report(errors: ErrorReport[]): Promise<void>
  
  // 自动恢复
  private async autoRecover(error: Error): Promise<boolean>
}

export const errorHub = new ErrorHub()
```

**错误分类与处理**：

| 错误类型 | 用户提示 | 自动恢复 | 上报 |
|---------|---------|---------|------|
| 网络错误 | "网络不可用，已保存到本地" | 离线队列 | ❌ |
| 权限拒绝 | "需要麦克风权限，去设置开启？" | 引导跳转 | ✅ |
| 存储满 | "存储空间不足，清理旧音频？" | 自动清理 | ✅ |
| AI 超时 | "AI 处理中，稍后查看" | 后台重试 | ✅ |
| 崩溃 | "应用异常，已自动恢复" | 重启模块 | ✅ |

---

## 三、💾 本地存储审计与优化

### 3.1 数据库性能审计

#### 审计项

1. **索引覆盖率**
   - ✅ 已有索引：`local_notes` 的 `domain`、`updated_at`、`workspace_id`
   - ⚠️ 缺失索引：`local_emails` 的 `category`、`importance` 需要索引
   - ⚠️ FTS5 索引：`local_notes_fts` 触发器已建立，但未测试性能

2. **查询性能**
   - 笔记列表查询（按 domain 分组）：需添加复合索引 `(domain, updated_at)`
   - 邮件未读数统计：`is_read = 0` 已有部分索引，但需优化

3. **事务一致性**
   - ❌ **严重问题**：笔记写入 + 向量写入非原子，崩溃会不一致
   - 解决：`storageHub.transaction()` 包裹多表写入

#### 优化建议

```sql
-- 补充索引
CREATE INDEX IF NOT EXISTS idx_emails_category ON local_emails(category, date DESC);
CREATE INDEX IF NOT EXISTS idx_notes_domain_updated ON local_notes(domain, updated_at DESC) WHERE deleted_at IS NULL;

-- 查询优化：邮件未读数
-- 原查询：SELECT COUNT(*) FROM local_emails WHERE is_read = 0
-- 优化：CREATE TABLE local_email_stats (unread_count INTEGER); 用触发器维护

-- 向量表优化：定期 VACUUM
```

### 3.2 数据安全审计

#### 审计项

1. **加密覆盖率**
   - ✅ 数据库整体加密：SQLCipher AES-256
   - ✅ 敏感字段二次加密：`credential_encrypted`、`entry_ciphertext`
   - ⚠️ 音频文件未加密：`audio_path` 指向明文 WAV 文件

2. **密钥管理**
   - ✅ 主密钥由 Keystore 保护
   - ⚠️ `dbSecret` 运行时内存持有，可能被内存 dump
   - ⚠️ 向量嵌入请求发明文片段给 pocketd，隐私风险中等

3. **备份与恢复**
   - ❌ **严重缺失**：无备份机制，手机丢失/损坏数据全丢
   - 解决：定期加密导出到云端（E2EE）

#### 优化建议

```typescript
// frontend/src/services/backup-hub.ts

class BackupHub {
  // 增量备份（仅备份变更数据）
  async createBackup(): Promise<EncryptedBlob>
  
  // 恢复
  async restoreBackup(blob: EncryptedBlob): Promise<void>
  
  // 自动备份策略（每日凌晨 WiFi 下）
  async scheduleAutoBackup(): Promise<void>
}
```

### 3.3 数据一致性审计

#### 审计项

1. **外键约束**
   - ✅ 已启用：`local_note_vectors` → `local_notes` 级联删除
   - ⚠️ 孤儿数据风险：`local_smart_links` 的 `target_note_id` 可能指向已删笔记

2. **软删除一致性**
   - ⚠️ `deleted_at` 不一致：笔记软删除后，向量未删除（占用空间）
   - 解决：触发器自动清理关联数据

3. **并发写冲突**
   - ❌ **严重问题**：Vue 组件直接调 `localDB.run()`，无并发控制
   - 解决：所有写操作必须经过 `storageHub`，内部加锁

#### 优化建议

```sql
-- 软删除触发器：自动清理关联数据
CREATE TRIGGER IF NOT EXISTS local_notes_soft_delete 
AFTER UPDATE OF deleted_at ON local_notes
WHEN new.deleted_at IS NOT NULL
BEGIN
    DELETE FROM local_note_vectors WHERE note_id = new.id;
    DELETE FROM local_smart_links WHERE source_note_id = new.id OR target_note_id = new.id;
END;
```

---

## 四、🚀 高可用高可靠保证

### 4.1 离线优先架构

```
写操作流程：
1. 立即写本地 SQLite（用户无感知延迟）
2. 入离线队列（待同步）
3. 网络可用时批量同步到 pocketd/kxmemory
4. 同步成功后移出队列

读操作流程：
1. 优先读本地缓存
2. 缓存未命中读 SQLite
3. 后台异步从服务端拉取更新
```

### 4.2 降级策略矩阵

| 服务 | 正常 | 降级 Level 1 | 降级 Level 2 |
|------|------|-------------|-------------|
| **STT** | 本地 sherpa-onnx | 云端 Groq | 纯本地 Vosk（低准确率）|
| **AI 分类** | kxmemory LLM | 直接调 Groq | 本地规则分类 |
| **向量检索** | 向量 + FTS 混合 | 仅 FTS5 全文 | 仅标题匹配 |
| **邮件抓取** | 后台 15min | 手动刷新 | 仅显示缓存 |
| **云同步** | 实时同步 | WiFi 下同步 | 禁用同步 |

### 4.3 错误恢复机制

```typescript
// frontend/src/services/recovery-hub.ts

class RecoveryHub {
  // 启动时检查未完成任务
  async checkPendingTasks(): Promise<void>
  
  // 恢复中断的录音
  async recoverRecording(): Promise<AudioFile | null>
  
  // 恢复离线队列
  async syncOfflineQueue(): Promise<void>
  
  // 数据库完整性检查
  async verifyDatabaseIntegrity(): Promise<boolean>
}
```

### 4.4 性能指标监控

```typescript
// frontend/src/services/performance-hub.ts

interface PerformanceMetrics {
  appStartTime: number
  dbQueryTime: number
  sttLatency: number
  aiLatency: number
}

class PerformanceHub {
  private metrics: PerformanceMetrics
  
  // 关键路径计时
  startTimer(operation: string): void
  endTimer(operation: string): number
  
  // 性能报告（开发环境打印，生产环境上报）
  report(): PerformanceMetrics
}
```

---

## 五、📅 实施路线图

### Phase 1：基础设施（第 1 周）

- [ ] 建立 `DeviceHub` 统一设备管理
- [ ] 建立 `StorageHub` 统一存储接口
- [ ] 补充数据库索引
- [ ] 添加软删除触发器

### Phase 2：公共模块（第 2 周）

- [ ] 实现 `AIHub` 统一 AI 调度
- [ ] 实现 `ErrorHub` 统一错误处理
- [ ] 实现 `STTHub` 语音识别调度
- [ ] 实现 `AudioRecorder` 录音封装

### Phase 3：存储优化（第 3 周）

- [ ] 事务包裹多表写入
- [ ] LRU 缓存层
- [ ] 离线队列实现
- [ ] 自动备份机制

### Phase 4：降级策略（第 4 周）

- [ ] 各服务降级矩阵实现
- [ ] 自动恢复机制
- [ ] 网络状态监听与切换
- [ ] 性能监控埋点

### Phase 5：审计与测试（第 5 周）

- [ ] 数据库完整性测试
- [ ] 并发写冲突测试
- [ ] 崩溃恢复测试
- [ ] 性能基准测试

---

## 六、🎯 成功指标

| 指标 | 目标 | 测量方式 |
|------|------|---------|
| **启动时间** | < 1.5s | 首屏渲染完成 |
| **数据库查询** | < 50ms | 笔记列表查询 |
| **STT 延迟** | < 3s | 10s 音频转写 |
| **离线可用率** | > 95% | 核心功能离线可用 |
| **数据丢失率** | 0% | 崩溃后数据完整 |
| **错误恢复率** | > 90% | 自动恢复成功率 |

---

## 七、🔗 与现有文档的关系

本文档是对现有设计文档的**架构层整合与优化**：

- `2026-07-02-lobster-local-storage-design.md` → 本文第三章"本地存储审计"
- `2026-07-02-android-stt-evaluation.md` → 本文第一章"设备交互层"的 STT 部分
- `2026-07-02-android-personal-assistant-plan.md` → 本文是其架构实施前置步骤

**核心改进**：
1. 从功能模块视角 → 架构分层视角
2. 从单点设计 → 全局统一
3. 从实现方案 → 可靠性保证

---

## 八、⚠️ 风险与缓解

| 风险 | 影响 | 概率 | 缓解 |
|------|------|------|------|
| 过度抽象导致性能下降 | 中 | 中 | 性能测试验证，必要时直接调用 |
| DeviceHub 成为单点故障 | 高 | 低 | 错误隔离，单个设备失败不影响其他 |
| 离线队列无限增长 | 中 | 中 | 队列大小限制 + LRU 淘汰 |
| 数据库迁移破坏现有数据 | 高 | 低 | 先备份 + 灰度测试 |

---

## 🏁 总结

本架构规划的核心价值：

1. **统一**：设备交互、存储、AI 调用、错误处理全部统一入口
2. **可靠**：离线优先、降级策略、自动恢复、数据不丢
3. **简洁**：避免过度抽象，每个 Hub 解决明确问题
4. **可演进**：分阶段实施，不阻塞业务开发

**下一步行动**：
- 与团队评审本方案
- 确定 Phase 1-2 的优先级
- 建立性能基线测试
- 开始 DeviceHub 和 StorageHub 实现
