package com.kaixuan.opencode.pocket.plugins;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

/**
 * cap-sherpa — sherpa-onnx 本地 ASR + ECAPA 声纹插件骨架。
 *
 * Sprint 3 占位：方法签名与 frontend/src/native/sherpa.ts 对齐。
 * 集成步骤见 docs/2026-07-02-android-stt-evaluation.md §4.1。
 *
 * TODO: 引入 sherpa-onnx Android AAR，实现 Paraformer 流式 + VAD + ECAPA embedding。
 */
@CapacitorPlugin(name = "Sherpa")
public class SherpaPlugin extends Plugin {

  private static final String NOT_READY = "sherpa-onnx AAR not integrated (Phase 4)";

  @PluginMethod
  public void preload(PluginCall call) {
    call.reject(NOT_READY);
  }

  @PluginMethod
  public void transcribe(PluginCall call) {
    call.reject(NOT_READY);
  }

  @PluginMethod
  public void extractEmbedding(PluginCall call) {
    call.reject(NOT_READY);
  }

  @PluginMethod
  public void startListening(PluginCall call) {
    call.reject(NOT_READY);
  }

  @PluginMethod
  public void stopListening(PluginCall call) {
    call.reject(NOT_READY);
  }

  @PluginMethod
  public void addListener(PluginCall call) {
    // Capacitor 事件监听由 JS 侧 Plugin.addListener 处理；此方法仅为接口对齐
    JSObject ret = new JSObject();
    ret.put("remove", "");
    call.resolve(ret);
  }
}
