package com.kaixuan.opencode.pocket.plugins;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.media.AudioAttributes;
import android.media.AudioFormat;
import android.media.AudioRecord;
import android.media.MediaRecorder;
import android.media.projection.MediaProjection;
import android.media.projection.MediaProjectionManager;
import android.os.Build;
import android.util.Log;

import androidx.annotation.RequiresApi;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.ActivityCallback;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.PluginResult;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.util.UUID;

// API 29+ only imports
import android.media.AudioPlaybackCaptureConfiguration;

/**
 * SystemAudioPlugin - Cicada Phase B: 系统音频捕获
 * 
 * 功能：
 * - 捕获系统音频输出（会议软件声音）
 * - 支持三种模式：microphone / system / hybrid
 * - Android 10+ (API 29+) 支持
 * 
 * 技术：
 * - MediaProjection API
 * - AudioPlaybackCaptureConfiguration
 * - AudioRecord with capture config
 */
@CapacitorPlugin(name = "SystemAudio")
public class SystemAudioPlugin extends Plugin {
    private static final String TAG = "SystemAudioPlugin";
    private static final int SAMPLE_RATE = 16000;
    private static final int CHANNEL_CONFIG = AudioFormat.CHANNEL_IN_MONO;
    private static final int AUDIO_FORMAT = AudioFormat.ENCODING_PCM_16BIT;
    
    private MediaProjectionManager projectionManager;
    private MediaProjection mediaProjection;
    private AudioRecord systemRecorder;
    private AudioRecord micRecorder;
    private boolean isRecording = false;
    private Thread recordingThread;
    private String currentSessionId;
    private File outputFile;
    
    @Override
    public void load() {
        super.load();
        projectionManager = (MediaProjectionManager) getContext()
            .getSystemService(Context.MEDIA_PROJECTION_SERVICE);
    }
    
    /**
     * 检查系统音频捕获支持
     */
    @PluginMethod
    public void isSystemAudioSupported(PluginCall call) {
        JSObject ret = new JSObject();
        ret.put("supported", Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q);
        ret.put("apiLevel", Build.VERSION.SDK_INT);
        call.resolve(ret);
    }
    
    /**
     * 获取可用的捕获模式
     */
    @PluginMethod
    public void getAvailableCaptureModes(PluginCall call) {
        JSObject ret = new JSObject();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ret.put("modes", new String[]{"microphone", "system", "hybrid"});
        } else {
            ret.put("modes", new String[]{"microphone"});
        }
        call.resolve(ret);
    }
    
    /**
     * 启动系统音频捕获（仅系统音频）
     */
    @PluginMethod
    public void startSystemAudio(PluginCall call) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            call.reject("System audio capture requires Android 10+");
            return;
        }
        
        // 请求 MediaProjection 权限
        Intent intent = projectionManager.createScreenCaptureIntent();
        startActivityForResult(call, intent, "systemAudioPermissionCallback");
    }
    
    /**
     * 启动混合模式（系统音频 + 麦克风）
     */
    @PluginMethod
    public void startHybridMode(PluginCall call) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            call.reject("Hybrid mode requires Android 10+");
            return;
        }
        
        Intent intent = projectionManager.createScreenCaptureIntent();
        startActivityForResult(call, intent, "hybridModePermissionCallback");
    }
    
    /**
     * 停止录音
     */
    @PluginMethod
    public void stopRecording(PluginCall call) {
        stopRecordingInternal();
        
        JSObject ret = new JSObject();
        ret.put("sessionId", currentSessionId);
        ret.put("uri", outputFile != null ? outputFile.getAbsolutePath() : null);
        call.resolve(ret);
    }
    
    /**
     * MediaProjection 权限回调 - 系统音频模式
     */
    @ActivityCallback
    private void systemAudioPermissionCallback(PluginCall call, com.getcapacitor.PluginCall savedCall, int resultCode, Intent data) {
        if (resultCode != Activity.RESULT_OK) {
            call.reject("User denied screen capture permission");
            return;
        }
        
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startSystemAudioCapture(call, data, false);
        }
    }
    
    /**
     * MediaProjection 权限回调 - 混合模式
     */
    @ActivityCallback
    private void hybridModePermissionCallback(PluginCall call, com.getcapacitor.PluginCall savedCall, int resultCode, Intent data) {
        if (resultCode != Activity.RESULT_OK) {
            call.reject("User denied screen capture permission");
            return;
        }
        
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startSystemAudioCapture(call, data, true);
        }
    }
    
    /**
     * 启动系统音频捕获
     * @param includeMic 是否同时捕获麦克风
     */
    @RequiresApi(api = Build.VERSION_CODES.Q)
    private void startSystemAudioCapture(PluginCall call, Intent resultData, boolean includeMic) {
        try {
            // 1. 创建 MediaProjection
            mediaProjection = projectionManager.getMediaProjection(
                Activity.RESULT_OK, 
                resultData
            );
            
            if (mediaProjection == null) {
                call.reject("Failed to create MediaProjection");
                return;
            }
            
            // 2. 配置系统音频捕获
            AudioPlaybackCaptureConfiguration.Builder configBuilder = 
                new AudioPlaybackCaptureConfiguration.Builder(mediaProjection);
            
            // 匹配媒体和语音通信类型（会议软件常用）
            configBuilder.addMatchingUsage(AudioAttributes.USAGE_MEDIA);
            configBuilder.addMatchingUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION);
            configBuilder.addMatchingUsage(AudioAttributes.USAGE_UNKNOWN);
            
            AudioPlaybackCaptureConfiguration captureConfig = configBuilder.build();
            
            // 3. 创建 AudioRecord 用于系统音频
            int bufferSize = AudioRecord.getMinBufferSize(
                SAMPLE_RATE, 
                CHANNEL_CONFIG, 
                AUDIO_FORMAT
            );
            
            AudioRecord.Builder systemRecorderBuilder = new AudioRecord.Builder()
                .setAudioFormat(new AudioFormat.Builder()
                    .setEncoding(AUDIO_FORMAT)
                    .setSampleRate(SAMPLE_RATE)
                    .setChannelMask(CHANNEL_CONFIG)
                    .build())
                .setBufferSizeInBytes(bufferSize)
                .setAudioPlaybackCaptureConfig(captureConfig);
            
            systemRecorder = systemRecorderBuilder.build();
            
            // 4. 如果是混合模式，同时创建麦克风 AudioRecord
            if (includeMic) {
                micRecorder = new AudioRecord(
                    MediaRecorder.AudioSource.MIC,
                    SAMPLE_RATE,
                    CHANNEL_CONFIG,
                    AUDIO_FORMAT,
                    bufferSize
                );
            }
            
            // 5. 准备输出文件
            currentSessionId = UUID.randomUUID().toString();
            File cacheDir = getContext().getCacheDir();
            outputFile = new File(cacheDir, "recording-" + currentSessionId + ".pcm");
            
            // 6. 启动录音
            systemRecorder.startRecording();
            if (micRecorder != null) {
                micRecorder.startRecording();
            }
            
            isRecording = true;
            startRecordingThread(includeMic);
            
            // 7. 返回成功
            JSObject ret = new JSObject();
            ret.put("sessionId", currentSessionId);
            ret.put("mode", includeMic ? "hybrid" : "system");
            ret.put("uri", outputFile.getAbsolutePath());
            call.resolve(ret);
            
        } catch (Exception e) {
            Log.e(TAG, "Failed to start system audio capture", e);
            call.reject("Failed to start capture: " + e.getMessage());
        }
    }
    
    /**
     * 启动录音线程
     */
    private void startRecordingThread(boolean mixMode) {
        recordingThread = new Thread(() -> {
            FileOutputStream fos = null;
            try {
                fos = new FileOutputStream(outputFile);
                byte[] buffer = new byte[1024];
                byte[] mixBuffer = mixMode ? new byte[1024] : null;
                
                while (isRecording) {
                    // 读取系统音频
                    int systemRead = systemRecorder.read(buffer, 0, buffer.length);
                    
                    if (mixMode && micRecorder != null) {
                        // 混合模式：读取麦克风音频
                        int micRead = micRecorder.read(mixBuffer, 0, mixBuffer.length);
                        
                        // 简单混音：平均两个信号
                        int minLen = Math.min(systemRead, micRead);
                        for (int i = 0; i < minLen - 1; i += 2) {
                            short systemSample = (short) ((buffer[i + 1] << 8) | (buffer[i] & 0xFF));
                            short micSample = (short) ((mixBuffer[i + 1] << 8) | (mixBuffer[i] & 0xFF));
                            short mixed = (short) ((systemSample + micSample) / 2);
                            buffer[i] = (byte) (mixed & 0xFF);
                            buffer[i + 1] = (byte) ((mixed >> 8) & 0xFF);
                        }
                        
                        if (minLen > 0) {
                            fos.write(buffer, 0, minLen);
                            
                            // 通知前端（可选）
                            notifyAudioData(buffer, minLen);
                        }
                    } else {
                        // 仅系统音频
                        if (systemRead > 0) {
                            fos.write(buffer, 0, systemRead);
                            notifyAudioData(buffer, systemRead);
                        }
                    }
                    
                    if (systemRead < 0) {
                        Log.w(TAG, "AudioRecord read error: " + systemRead);
                        break;
                    }
                }
            } catch (IOException e) {
                Log.e(TAG, "Recording thread error", e);
                notifyError("Recording failed: " + e.getMessage());
            } finally {
                if (fos != null) {
                    try {
                        fos.close();
                    } catch (IOException e) {
                        Log.e(TAG, "Failed to close output stream", e);
                    }
                }
            }
        });
        recordingThread.start();
    }
    
    /**
     * 停止录音
     */
    private void stopRecordingInternal() {
        isRecording = false;
        
        if (systemRecorder != null) {
            try {
                systemRecorder.stop();
                systemRecorder.release();
            } catch (Exception e) {
                Log.e(TAG, "Error stopping system recorder", e);
            }
            systemRecorder = null;
        }
        
        if (micRecorder != null) {
            try {
                micRecorder.stop();
                micRecorder.release();
            } catch (Exception e) {
                Log.e(TAG, "Error stopping mic recorder", e);
            }
            micRecorder = null;
        }
        
        if (mediaProjection != null) {
            mediaProjection.stop();
            mediaProjection = null;
        }
        
        if (recordingThread != null) {
            try {
                recordingThread.join(1000);
            } catch (InterruptedException e) {
                Log.w(TAG, "Recording thread interrupted", e);
            }
            recordingThread = null;
        }
    }
    
    /**
     * 通知前端音频数据（用于实时处理）
     */
    private void notifyAudioData(byte[] data, int length) {
        // 降低事件频率，避免性能问题
        if (System.currentTimeMillis() % 100 == 0) {
            JSObject ret = new JSObject();
            ret.put("sessionId", currentSessionId);
            ret.put("length", length);
            // 可选：Base64 编码数据
            // ret.put("data", Base64.encodeToString(data, 0, length, Base64.NO_WRAP));
            notifyListeners("audioData", ret);
        }
    }
    
    /**
     * 通知前端错误
     */
    private void notifyError(String message) {
        JSObject ret = new JSObject();
        ret.put("error", message);
        notifyListeners("captureError", ret);
    }
    
    @Override
    protected void handleOnDestroy() {
        stopRecordingInternal();
        super.handleOnDestroy();
    }
}
