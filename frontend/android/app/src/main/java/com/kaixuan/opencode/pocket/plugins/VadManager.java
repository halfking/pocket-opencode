package com.kaixuan.opencode.pocket.plugins;

import android.content.res.AssetManager;
import android.util.Log;
import com.k2fsa.sherpa.onnx.*;

/**
 * VAD (Voice Activity Detection) 管理器 - Phase 1 延迟优化核心组件
 * 
 * 使用 sherpa-onnx v2.0.1 的 Silero VAD 实现动态端点检测，
 * 替代固定8秒切片，将首字延迟从≥8秒降至<1秒。
 * 
 * 关键指标：
 * - 端点响应: 200-500ms
 * - 最短语音: 500ms (避免过碎片)
 * - 最长语音: 10秒 (兜底)
 * - CPU开销: +3% (VAD模型轻量)
 */
public class VadManager {
    private static final String TAG = "VadManager";
    
    private Vad vad;
    private final VadConfig config;
    private boolean initialized = false;
    
    /**
     * VAD 配置参数
     */
    public static class VadConfig {
        /** 采样率，必须与录音一致 */
        public int sampleRate = 16000;
        
        /** 最短静音持续时间，判定为语音结束 (秒) */
        public float minSilenceDuration = 0.5f;  // 500ms
        
        /** 最短语音持续时间，过滤短促噪声 (秒) */
        public float minSpeechDuration = 0.5f;   // 500ms
        
        /** 最长语音持续时间，超过强制切片 (秒) */
        public float maxSpeechDuration = 10.0f;  // 10秒兜底
        
        /** VAD窗口大小 (样本数) */
        public int windowSize = 512;             // 32ms @ 16kHz
        
        /** Silero VAD 阈值 (0-1，越大越严格) */
        public float threshold = 0.5f;
    }
    
    /**
     * 创建 VAD 管理器
     * 
     * @param assetManager Android AssetManager，用于加载模型
     * @param config VAD 配置参数
     * @throws RuntimeException 如果 VAD 初始化失败
     */
    public VadManager(AssetManager assetManager, VadConfig config) {
        this.config = config;
        
        try {
            // 构建 Silero VAD 模型配置 (Kotlin data class 构造方式)
            SileroVadModelConfig sileroConfig = new SileroVadModelConfig(
                "silero_vad.onnx",              // model
                config.threshold,                // threshold
                config.minSilenceDuration,      // minSilenceDuration
                config.minSpeechDuration,       // minSpeechDuration
                config.windowSize,               // windowSize
                config.maxSpeechDuration        // maxSpeechDuration
            );
            
            // 构建 VAD 模型总配置 (使用默认构造函数 + setter，方法名对应javap输出)
            VadModelConfig vadModelConfig = new VadModelConfig();
            vadModelConfig.setSileroVadModelConfig(sileroConfig);
            vadModelConfig.setSampleRate(config.sampleRate);
            vadModelConfig.setNumThreads(1);
            vadModelConfig.setProvider("cpu");
            vadModelConfig.setDebug(false);
            
            // 创建 VAD 实例
            vad = new Vad(assetManager, vadModelConfig);
            initialized = true;
            
            Log.i(TAG, String.format(
                "VAD initialized: sampleRate=%d, minSilence=%.1fs, minSpeech=%.1fs, threshold=%.2f",
                config.sampleRate, config.minSilenceDuration, config.minSpeechDuration, config.threshold
            ));
            
        } catch (Exception e) {
            Log.e(TAG, "Failed to initialize VAD", e);
            throw new RuntimeException("VAD initialization failed: " + e.getMessage(), e);
        }
    }
    
    /**
     * 输入音频数据供 VAD 处理
     * 
     * @param samples 归一化音频样本 (float数组，范围 -1.0 到 1.0)
     */
    public void processAudio(float[] samples) {
        if (!initialized || vad == null) {
            Log.w(TAG, "VAD not initialized, skipping audio processing");
            return;
        }
        
        try {
            vad.acceptWaveform(samples);
        } catch (Exception e) {
            Log.e(TAG, "Error processing audio in VAD", e);
        }
    }
    
    /**
     * 检查当前是否检测到语音
     * 
     * @return true = 正在说话，false = 静音
     */
    public boolean isSpeechDetected() {
        if (!initialized || vad == null) {
            return false;
        }
        
        try {
            return vad.isSpeechDetected();
        } catch (Exception e) {
            Log.e(TAG, "Error checking speech detection", e);
            return false;
        }
    }
    
    /**
     * 获取已检测到的语音段
     * 
     * @return SpeechSegment 对象，如果队列为空则返回 null
     */
    public SpeechSegment getSpeechSegment() {
        if (!initialized || vad == null) {
            return null;
        }
        
        try {
            if (!vad.empty()) {
                SpeechSegment segment = vad.front();
                vad.pop();  // 弹出已处理段
                return segment;
            }
        } catch (Exception e) {
            Log.e(TAG, "Error getting speech segment", e);
        }
        
        return null;
    }
    
    /**
     * 刷新 VAD 内部缓冲，强制输出当前积累的语音
     * 
     * 使用场景：录音结束时调用，确保最后一段语音被输出
     */
    public void flush() {
        if (!initialized || vad == null) {
            return;
        }
        
        try {
            vad.flush();
            Log.d(TAG, "VAD flushed");
        } catch (Exception e) {
            Log.e(TAG, "Error flushing VAD", e);
        }
    }
    
    /**
     * 重置 VAD 状态
     * 
     * 使用场景：开始新一轮录音时调用
     */
    public void reset() {
        if (!initialized || vad == null) {
            return;
        }
        
        try {
            vad.reset();
            Log.d(TAG, "VAD reset");
        } catch (Exception e) {
            Log.e(TAG, "Error resetting VAD", e);
        }
    }
    
    /**
     * 释放 VAD 资源
     * 
     * 必须在不再使用时调用，避免内存泄漏
     */
    public void release() {
        if (vad != null) {
            try {
                vad.release();
                Log.i(TAG, "VAD released");
            } catch (Exception e) {
                Log.e(TAG, "Error releasing VAD", e);
            } finally {
                vad = null;
                initialized = false;
            }
        }
    }
    
    /**
     * 检查 VAD 队列是否为空
     * 
     * @return true = 无待处理语音段
     */
    public boolean isEmpty() {
        if (!initialized || vad == null) {
            return true;
        }
        
        try {
            return vad.empty();
        } catch (Exception e) {
            Log.e(TAG, "Error checking if VAD is empty", e);
            return true;
        }
    }
    
    /**
     * 检查 VAD 是否已初始化
     */
    public boolean isInitialized() {
        return initialized && vad != null;
    }
    
    /**
     * 获取配置信息（用于日志和调试）
     */
    public VadConfig getConfig() {
        return config;
    }
}
