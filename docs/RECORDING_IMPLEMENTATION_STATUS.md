# 录音转写功能实施进度

## 已完成 ✅

### 1. 技术调研与方案设计
- [x] GitHub高星项目调研（VAD、录音、ASR、说话人分离）
- [x] 技术选型文档（`docs/RECORDING_TECH_SELECTION.md`）
- [x] 实施计划（`docs/RECORDING_OPTIMIZATION_PLAN.md`）
- [x] 核心技术栈确定：
  - VAD: FireRedVAD / Silero VAD
  - 录音: OmRecorder + rnnoise-kmp
  - ASR: FunASR (服务端) + sherpa-onnx (端侧)
  - 说话人: pyannote-audio + WhisperX

### 2. Android原生录音（已有实现）
- [x] BackgroundMicPlugin完整实现（前台服务、权限管理）
- [x] SherpaPlugin完整实现（678行，transcribe/startListening/stopListening）
- [x] sherpa-onnx AAR配置（v1.13.8，自动下载）
- [x] 模型注册表（zipformer流式、sensevoice整段）
- [x] AudioRecord采集 + VAD端点检测

### 3. 后端基础设施（已有）
- [x] 完整STT基础设施（单次/全量/增量转写）
- [x] 多provider支持（OpenAI兼容、MiniMax、智谱）
- [x] 自动目标解析（resolveSTTTarget）
- [x] 会议元数据管理（PostgreSQL）
- [x] kxmemory集成基础

### 4. 新增模块
- [x] 说话人分离客户端（`backend/internal/diarization/client.go`）
- [x] pyannote-audio服务（`backend/scripts/diarization_service.py`）
- [x] 启动脚本（`backend/scripts/start_diarization_service.sh`）
- [x] 动态参谋模块（`backend/internal/advisor/advisor.go`）

## 进行中 🚧

### 5. 集成与联调
- [ ] sherpa-onnx模型下载与验证
- [ ] pyannote服务部署与测试（需要HF_TOKEN）
- [ ] 后端API集成（说话人分离端点）
- [ ] 动态参谋kxmemory实际对接

## 待实施 📋

### 6. 核心功能完善
- [ ] 说话人标注UI（Android/前端）
- [ ] 本地声纹库（SQLite存储）
- [ ] 实时建议推送（WebSocket）
- [ ] 精转与自动总结pipeline
- [ ] 多语言并行转写+翻译
- [ ] 任务提取与智能体关联

### 7. 性能优化
- [ ] 离线/在线自动切换策略
- [ ] 模型懒加载
- [ ] 音频流式传输优化
- [ ] 内存占用优化

### 8. 测试与部署
- [ ] Android端到端测试
- [ ] 服务端集成测试
- [ ] 性能基准测试
- [ ] 文档完善

## 技术债务

1. **192.168.31.30服务器**
   - 是macOS系统，无Docker
   - 有Python 3.9环境
   - 可部署pyannote服务
   - 需要安装依赖包

2. **llm.itestu.cn网关**
   - 确认无ASR provider
   - 仅提供LLM服务
   - 需要配置独立ASR端点

3. **WhisperX集成**
   - diarization_service.py中标记为pending
   - 需要安装whisperx包
   - 词级对齐功能暂未实现

## 下一步行动

1. **立即执行**（今天）
   - 在192.168.31.30部署pyannote服务
   - 测试说话人分离基本功能
   - 验证sherpa-onnx AAR下载

2. **本周完成**
   - 后端说话人分离API集成
   - 动态参谋kxmemory对接
   - Android端测试录音+转写流程

3. **下周完成**
   - 精转与自动总结
   - 多语言支持
   - 端到端测试

## 风险评估

| 风险 | 状态 | 缓解措施 |
|------|------|----------|
| pyannote需要GPU | 中 | CPU版本可用，速度慢2-3倍 |
| HF_TOKEN获取 | 低 | 文档已说明申请方式 |
| sherpa模型较大 | 已缓解 | 配置为首次使用时下载 |
| 192.168.31.30资源 | 待确认 | 可回退到本地开发机 |

---

**更新时间**: 2026-11-14  
**负责人**: AI Development Team
