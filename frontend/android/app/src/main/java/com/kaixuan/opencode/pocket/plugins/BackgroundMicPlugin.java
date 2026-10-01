package com.kaixuan.opencode.pocket.plugins;

import android.Manifest;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.media.AudioManager;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.util.Base64;
import androidx.core.content.ContextCompat;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import org.json.JSONArray;

/**
 * 后台麦克风插件：把「开始录音」变成一个**有结论**的 Promise。
 *
 * 为什么不能 startForegroundService 完就 resolve（2026-10-01 修复）：
 * 旧实现调完 startForegroundService 立刻 call.resolve()，于是 JS 侧
 * nativeMode=true，切后台不再挂 visibilitychange 告警。但服务可能**当场就死**——
 * RECORD_AUDIO 未授予时 startForeground(..., TYPE_MICROPHONE) 抛 SecurityException、
 * 麦克风被电话/其他应用占用时 AudioRecord 状态异常——两种情况都不会有人通知 JS，
 * 表现是「显示正在录音、切后台也不提示、一整场没有声音」。
 * 这属于典型的静默失效：UI 显示成功，实际功能是空的。
 *
 * 现在 start() 会等 MeetingRecordService 真正开始采音后回报（reportStart(true)），
 * 失败则 reject；另有 3 秒兜底，避免服务在回报前崩溃导致 JS 永久等待。
 */
@CapacitorPlugin(name = "BackgroundMic")
public class BackgroundMicPlugin extends Plugin {
  private static BackgroundMicPlugin instance;

  /** 服务对「本次启动」的结论。null = 还没有结论。 */
  private static volatile Boolean startOutcome;
  private static volatile String startMessage;

  private static final long START_TIMEOUT_MS = 3000L;

  private final Handler main = new Handler(Looper.getMainLooper());
  private PluginCall pendingStart;

  @Override
  public void load() {
    instance = this;
  }

  @Override
  protected void handleOnDestroy() {
    instance = null;
    pendingStart = null;
    super.handleOnDestroy();
  }

  @PluginMethod
  public void listInputs(PluginCall call) {
    AudioManager am = (AudioManager) getContext().getSystemService(Context.AUDIO_SERVICE);
    if (am == null) {
      call.reject("AudioManager unavailable");
      return;
    }
    try {
      JSONArray devices = AudioDeviceRank.toJson(AudioDeviceRank.listInputs(am));
      JSObject ret = new JSObject();
      ret.put("devices", devices);
      call.resolve(ret);
    } catch (Exception e) {
      call.reject(e.getMessage());
    }
  }

  @PluginMethod
  public void start(PluginCall call) {
    if (pendingStart != null) {
      call.reject("已有一场录音正在启动，请稍候");
      return;
    }
    // Android 14+ 的 microphone 前台服务要求「使用中」权限此刻真实授予。
    // 提前查一次，把「权限没给」变成一条明确的 reject；否则它会在服务的
    // startForeground 里抛 SecurityException，表现为无声崩溃。
    if (ContextCompat.checkSelfPermission(getContext(), Manifest.permission.RECORD_AUDIO)
        != PackageManager.PERMISSION_GRANTED) {
      call.reject("麦克风权限未授予，无法启动后台录音服务");
      return;
    }

    String meetingId = call.getString("meetingId", "");
    String deviceId = call.getString("deviceId");
    startOutcome = null;
    startMessage = null;
    pendingStart = call;

    Intent i = new Intent(getContext(), MeetingRecordService.class);
    i.setAction(MeetingRecordService.ACTION_START);
    i.putExtra(MeetingRecordService.EXTRA_MEETING, meetingId);
    if (deviceId != null) i.putExtra(MeetingRecordService.EXTRA_DEVICE, deviceId);
    Context ctx = getContext();
    try {
      if (Build.VERSION.SDK_INT >= 26) {
        ctx.startForegroundService(i);
      } else {
        ctx.startService(i);
      }
    } catch (Exception e) {
      pendingStart = null;
      call.reject("无法拉起前台录音服务：" + describe(e));
      return;
    }

    // 兜底：服务若在回报之前就崩了（OOM、进程被杀），也不能让 JS 无限等下去。
    main.postDelayed(new Runnable() {
      @Override
      public void run() {
        settlePendingStart();
      }
    }, START_TIMEOUT_MS);
  }

  @PluginMethod
  public void stop(PluginCall call) {
    Intent i = new Intent(getContext(), MeetingRecordService.class);
    i.setAction(MeetingRecordService.ACTION_STOP);
    try {
      getContext().startService(i);
    } catch (Exception e) {
      // 服务已经不在了也算停止成功，不该因此报错。
    }
    call.resolve();
  }

  /** 服务侧回报启动结论。 */
  static void reportStart(boolean ok, String message) {
    startOutcome = Boolean.valueOf(ok);
    startMessage = message;
    final BackgroundMicPlugin p = instance;
    if (p == null) return;
    p.main.post(new Runnable() {
      @Override
      public void run() {
        p.settlePendingStart();
      }
    });
  }

  private void settlePendingStart() {
    if (pendingStart == null) return;
    PluginCall call = pendingStart;
    pendingStart = null;
    Boolean outcome = startOutcome;
    if (outcome == null) {
      call.reject("后台录音服务未在 " + (START_TIMEOUT_MS / 1000) + " 秒内回报启动结果，本次录音未开始");
    } else if (Boolean.TRUE.equals(outcome)) {
      call.resolve();
    } else {
      call.reject(startMessage != null ? startMessage : "后台录音服务启动失败");
    }
  }

  static void emitPart(int seq, byte[] wav, long startMs, long endMs) {
    final BackgroundMicPlugin p = instance;
    if (p == null) return;
    final JSObject data = new JSObject();
    data.put("seq", seq);
    data.put("mimeType", "audio/wav");
    data.put("dataBase64", Base64.encodeToString(wav, Base64.NO_WRAP));
    data.put("startMs", startMs);
    data.put("endMs", endMs);
    // 统一回主线程：worker 线程是连续调用的，投递到同一个 Handler 队列即可保持
    // 先后顺序，同时避免从后台线程碰 UI/桥接状态。
    p.main.post(new Runnable() {
      @Override
      public void run() {
        p.notifyListeners("partReady", data);
      }
    });
  }

  static void emitError(String message) {
    final BackgroundMicPlugin p = instance;
    if (p == null) return;
    final JSObject data = new JSObject();
    data.put("message", message != null ? message : "录音失败");
    p.main.post(new Runnable() {
      @Override
      public void run() {
        p.notifyListeners("error", data);
      }
    });
  }

  private static String describe(Throwable t) {
    if (t == null) return "未知错误";
    String m = t.getMessage();
    return (m != null && !m.isEmpty()) ? m : t.getClass().getSimpleName();
  }
}
