# Phase 2 实施计划：Paraformer集成

## ✅ 可行性验证结果

### sherpa-onnx v2.0.1 Paraformer支持
- ✅ `OfflineParaformerModelConfig.class` - 离线模式
- ✅ `OnlineParaformerModelConfig.class` - 在线流式模式
- ✅ API完整，可直接集成

### Paraformer配置参数
```java
public class OfflineParaformerModelConfig {
  public OfflineParaformerModelConfig(String model);
  public final String getModel();
  public final void setModel(String);
}
```

---

## 实施方案

### Step 1: 下载Paraformer模型

**模型选择**：
- **paraformer-zh-small**: ~50MB，适合移动端
- **paraformer-zh**: ~120MB，高精度（推荐）
- **paraformer-zh-large**: ~220MB，最高精度

**下载源**：
```bash
# 从sherpa-onnx官方releases获取
https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/paraformer-zh.tar.bz2
```

### Step 2: 更新SherpaPlugin配置

**文件**: `frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/SherpaPlugin.java`

**当前配置**（zipformer）：
```java
// 当前使用zipformer
OfflineRecognizerConfig config = ...
```

**修改为**（Paraformer）：
```java
// Phase 2: 切换到Paraformer高精度模式
OfflineParaformerModelConfig paraformerConfig = 
    new OfflineParaformerModelConfig(
        modelsDir + "/paraformer-zh/model.int8.onnx"
    );

OfflineModelConfig modelConfig = new OfflineModelConfig();
modelConfig.setParaformer(paraformerConfig);
modelConfig.setTokens(modelsDir + "/paraformer-zh/tokens.txt");
modelConfig.setNumThreads(2);
modelConfig.setDebug(false);

OfflineRecognizerConfig config = new OfflineRecognizerConfig();
config.setOfflineModelConfig(modelConfig);
config.setDecodingMethod("greedy_search");
config.setMaxActivePaths(4);
```

### Step 3: 模型下载自动化

**文件**: `frontend/android/app/build.gradle`

```gradle
// Phase 2: Paraformer模型下载
def paraformerModel = file('src/main/assets/paraformer-zh.tar.bz2')
tasks.register('downloadParaformer') {
    onlyIf { !paraformerModel.exists() }
    doLast {
        // 下载paraformer模型包
        def url = 'https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/paraformer-zh.tar.bz2'
        // ... 下载逻辑
    }
}

tasks.named('preBuild') { 
    dependsOn 'downloadSherpaAar', 'downloadSileroVad', 'downloadParaformer' 
}
```

### Step 4: 测试验证

**测试集**：100条中文语音
- 短句（<5秒）：30条
- 中句（5-10秒）：40条  
- 长句（>10秒）：30条

**评估指标**：
- 字准率（CER）：目标>95%
- 标点还原率：目标>90%
- RTF：目标<0.2
- 内存占用：目标<500MB

---

## 实施时间线

- **Day 1**: 模型下载与集成（3小时）
- **Day 2**: 测试调优（4小时）
- **Day 3**: 文档与验收（2小时）

---

## 成功标准

- [ ] Paraformer模型成功加载
- [ ] 编译通过，APK生成
- [ ] 字准率≥95%
- [ ] 标点还原正常
- [ ] RTF<0.2（中端设备）
- [ ] 无崩溃，无内存泄漏

---

**状态**: 可行性验证完成，待实施
