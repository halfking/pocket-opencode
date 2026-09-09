package com.kaixuan.opencode.pocket.plugins;

import android.Manifest;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

/**
 * AiStreamKeepalive — AI 流后台保活的原生桥（M5/T2，2026-09-10）。
 *
 * JS 侧（aiStreamKeepalive.ts）在「AiStreamRuntime 有活跃流 && App 切后台」时调
 * start()，拉起 AiStreamService（dataSync 型前台服务 + ai_stream_sync 通知通道）；
 * 流结束或回前台时调 stop()。Web/iOS 上 JS 侧不注入本桥，调用全部 no-op。
 *
 * Android 13+ 通知属于运行时权限：start() 先确保 POST_NOTIFICATIONS，被拒也照常
 * 起服务（前台服务本身不依赖通知权限，只是通知不显示），把 result.permGranted
 * 告知 JS 以便提示用户。
 */
@CapacitorPlugin(
    name = "AiStreamKeepalive",
    permissions = {
      @Permission(alias = "notifications", strings = {Manifest.permission.POST_NOTIFICATIONS}),
    })
public class AiStreamKeepalivePlugin extends Plugin {

  @PluginMethod
  public void start(PluginCall call) {
    int activeCount = call.getInt("activeCount", 1);
    String text = call.getString("text");
    ensureNotificationPermission(
        call,
        () -> {
          launchStart(getContext(), activeCount, text);
          resolveState(call);
        });
  }

  @PluginMethod
  public void update(PluginCall call) {
    int activeCount = call.getInt("activeCount", 1);
    String text = call.getString("text");
    AiStreamService.update(getContext(), activeCount, text);
    resolveState(call);
  }

  @PluginMethod
  public void stop(PluginCall call) {
    Intent i = new Intent(getContext(), AiStreamService.class);
    i.setAction(AiStreamService.ACTION_STOP);
    getContext().startService(i);
    resolveState(call);
  }

  @PluginMethod
  public void isRunning(PluginCall call) {
    resolveState(call);
  }

  private void resolveState(PluginCall call) {
    JSObject ret = new JSObject();
    ret.put("running", AiStreamService.isRunning());
    ret.put("permGranted", notificationPermissionGranted());
    call.resolve(ret);
  }

  private boolean notificationPermissionGranted() {
    if (Build.VERSION.SDK_INT < 33) return true;
    return getPermissionState("notifications") == PermissionState.GRANTED;
  }

  private interface OnReady {
    void run();
  }

  /** 13+ 未授权时先弹系统窗；拒绝/授权都继续执行 after（服务照起，permGranted 如实上报）。 */
  private void ensureNotificationPermission(PluginCall call, OnReady after) {
    if (Build.VERSION.SDK_INT < 33 || notificationPermissionGranted()) {
      after.run();
      return;
    }
    if (getPermissionState("notifications") == PermissionState.PROMPT
        || getPermissionState("notifications") == PermissionState.PROMPT_WITH_RATIONALE) {
      requestPermissionForAlias("notifications", call, "completePermission");
      return;
    }
    // 已被永久拒绝（DENIED）：不再弹窗，直接起服务。
    after.run();
  }

  @PermissionCallback
  private void completePermission(PluginCall call) {
    // 回调无法区分 start/update 入口，这里只可能来自 start()；重走 start 主路径。
    launchStart(getContext(), call.getInt("activeCount", 1), call.getString("text"));
  }

  private static void launchStart(Context ctx, int activeCount, String text) {
    Intent i = new Intent(ctx, AiStreamService.class);
    i.setAction(AiStreamService.ACTION_START);
    i.putExtra(AiStreamService.EXTRA_ACTIVE, activeCount);
    if (text != null) i.putExtra(AiStreamService.EXTRA_TEXT, text);
    if (Build.VERSION.SDK_INT >= 26) {
      ctx.startForegroundService(i);
    } else {
      ctx.startService(i);
    }
  }
}
