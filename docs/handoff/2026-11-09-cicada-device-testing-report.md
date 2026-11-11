# Cicada 集成真机测试报告

> **测试日期**: 2026-11-09  
> **测试设备**: Xiaomi 2411DRN47C  
> **Android 版本**: 16 (API 36)  
> **APK 版本**: 158MB (Debug Build)

---

## 测试环境

### 设备信息
- **型号**: 2411DRN47C
- **Android 版本**: 16
- **API 级别**: 36
- **系统音频支持**: ✅ 是（API 36 > 29）

### 构建信息
- **前端构建**: 成功（4.37s）
- **Android 构建**: 成功（4s）
- **APK 大小**: 158MB
- **安装**: 成功

---

## 测试项目

### 1. Phase A：实时字幕与翻译

#### 1.1 基础功能
- [ ] 字幕悬浮窗显示
- [ ] 拖拽定位
- [ ] 置顶功能
- [ ] 字号调整（小/中/大）
- [ ] 翻译开关

#### 1.2 翻译功能
- [ ] 中文→英文翻译
- [ ] 英文→中文翻译
- [ ] 其他语言翻译（日/韩/法/德/西）
- [ ] 自动语言检测
- [ ] 翻译延迟测试（目标 < 2s）

#### 1.3 导出功能
- [ ] SRT 格式导出
- [ ] TXT 格式导出
- [ ] 分享功能

### 2. Phase B：系统音频捕获

#### 2.1 插件注册
- [x] SystemAudioPlugin 已注册到 MainActivity
- [x] 编译通过
- [ ] 运行时加载成功

#### 2.2 API 可用性
- [ ] `isSystemAudioSupported()` 返回 true
- [ ] `getAvailableCaptureModes()` 包含 system/hybrid
- [ ] MediaProjection 权限请求流程

#### 2.3 捕获模式
- [ ] 仅麦克风模式（baseline）
- [ ] 仅系统音频模式
- [ ] 混合模式（麦克风 + 系统音频）

### 3. 性能测试

#### 3.1 资源占用
- [ ] 内存占用（idle / 录音中）
- [ ] CPU 占用
- [ ] 电池消耗
- [ ] 发热情况

#### 3.2 稳定性
- [ ] 长时间录音（10 分钟+）
- [ ] 切换应用后台
- [ ] 接听电话后恢复
- [ ] 旋转屏幕

---

## 测试步骤

### Phase A 测试流程

1. **启动应用**
   ```bash
   adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
   ```

2. **进入会议页面**
   - 导航到会议列表
   - 创建新会议或选择现有会议

3. **开始录音**
   - 点击录音按钮
   - 观察字幕悬浮窗是否出现

4. **测试字幕功能**
   - 说话，观察转写是否实时显示
   - 拖拽悬浮窗到不同位置
   - 测试置顶功能
   - 切换字号

5. **测试翻译功能**
   - 开启翻译
   - 说中文，观察英文翻译
   - 说英文，观察中文翻译
   - 记录翻译延迟

6. **测试导出功能**
   - 停止录音
   - 导出 SRT 文件
   - 导出 TXT 文件
   - 使用分享功能

### Phase B 测试流程

1. **检查插件加载**
   ```bash
   adb logcat | grep SystemAudioPlugin
   ```

2. **测试 API 调用**（需要前端集成）
   - 打开浏览器控制台（Chrome DevTools）
   - 执行：
   ```javascript
   const plugin = await import('/src/plugins/system-audio')
   const result = await plugin.default.isSystemAudioSupported()
   console.log('System audio supported:', result.supported)
   ```

3. **测试系统音频捕获**（需要 UI 集成）
   - 播放音乐或视频
   - 开始录音（系统音频模式）
   - 验证是否捕获到系统声音

---

## 预期结果

### Phase A
- ✅ 字幕悬浮窗流畅显示
- ✅ 拖拽响应迅速（< 16ms）
- ✅ 翻译延迟 < 2s
- ✅ 导出文件格式正确

### Phase B
- ✅ 插件成功加载
- ✅ API 29+ 设备返回 supported: true
- ✅ MediaProjection 权限请求正常
- ✅ 系统音频捕获成功

---

## 已知问题

### 编译问题（已修复）
1. ❌ `AudioPlaybackCapture` 导入错误
   - **原因**: 错误的类路径
   - **修复**: 使用 `AudioPlaybackCaptureConfiguration`

2. ❌ `ActivityResult` 类型错误
   - **原因**: Capacitor 回调签名错误
   - **修复**: 使用 `(PluginCall, PluginCall, int, Intent)` 签名

### 待验证
- ⏳ 前端 SystemAudio 插件是否能正常调用
- ⏳ MediaProjection 权限流程是否完整
- ⏳ 混音质量是否满足要求

---

## 下一步行动

### 立即执行
1. **手动测试 Phase A**
   - 在真机上测试字幕和翻译功能
   - 记录性能数据
   - 截图关键界面

2. **集成 Phase B 前端**
   - 创建 `CaptureModeSelector.vue` 组件
   - 扩展 `recordingRuntime.ts`
   - 添加模式切换 UI

3. **性能测试**
   - 使用 Android Profiler 监控资源
   - 测试长时间录音稳定性

### 后续工作
1. 完善测试用例
2. 性能优化
3. Bug 修复
4. 用户反馈收集

---

**报告状态**: 🔄 测试进行中  
**完成度**: 构建完成 100% | 功能测试 0% | 性能测试 0%  
**更新时间**: 2026-11-09 19:05
