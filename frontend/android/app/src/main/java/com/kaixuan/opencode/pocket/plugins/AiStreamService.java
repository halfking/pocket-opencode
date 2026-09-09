package com.kaixuan.opencode.pocket.plugins;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import androidx.core.app.NotificationCompat;
import com.kaixuan.opencode.pocket.MainActivity;
import com.kaixuan.opencode.pocket.R;

/**
 * AI 流前台服务（M5/T2，2026-09-10）：AiStreamRuntime 有活跃流且 App 切后台时，
 * 由 AiStreamKeepalivePlugin 拉起本服务，以 dataSync 型前台服务 + 常驻通知
 * 保住进程优先级，防 Doze 冻结 WebView 的 SSE fetch。
 * 设计见 docs/design/2026-09-09-ai-async-background-survival.md §D5。
 *
 * 生命周期约定：START / UPDATE 可反复调用（幂等更新通知文案）；STOP 释放
 * WakeLock 并停服。startForegroundService 要求 5s 内 startForeground，
 * onStartCommand 首行即满足。
 */
public class AiStreamService extends Service {
  public static final String ACTION_START = "com.kaixuan.opencode.pocket.AI_STREAM_START";
  public static final String ACTION_STOP = "com.kaixuan.opencode.pocket.AI_STREAM_STOP";
  public static final String ACTION_UPDATE = "com.kaixuan.opencode.pocket.AI_STREAM_UPDATE";
  public static final String EXTRA_ACTIVE = "activeCount";
  public static final String EXTRA_TEXT = "text";
  public static final String CHANNEL_ID = "ai_stream_sync";
  private static final int NOTIF_ID = 43;
  /** WakeLock 上限：与设计文档"后台 30 分钟测试矩阵"同量级，UPDATE 时续期。 */
  private static final long WAKELOCK_MS = 30 * 60 * 1000L;

  private static AiStreamService instance;
  private PowerManager.WakeLock wakeLock;

  public static boolean isRunning() {
    return instance != null;
  }

  /** 已在跑时刷新通知文案；未在跑则 no-op（由 start 流程负责首建）。 */
  public static void update(Context ctx, int activeCount, String text) {
    AiStreamService svc = instance;
    if (svc == null) return;
    NotificationManager nm = (NotificationManager) ctx.getSystemService(Context.NOTIFICATION_SERVICE);
    Notification notif = svc.buildNotification(nm, activeCount, text);
    nm.notify(NOTIF_ID, notif);
    svc.renewWakeLock();
  }

  @Override
  public IBinder onBind(Intent intent) {
    return null;
  }

  @Override
  public void onCreate() {
    super.onCreate();
    instance = this;
  }

  @Override
  public int onStartCommand(Intent intent, int flags, int startId) {
    if (intent != null && ACTION_STOP.equals(intent.getAction())) {
      stopKeepalive();
      return START_NOT_STICKY;
    }
    int active = intent != null ? intent.getIntExtra(EXTRA_ACTIVE, 1) : 1;
    String text = intent != null ? intent.getStringExtra(EXTRA_TEXT) : null;
    NotificationManager nm = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
    startForegroundCompat(buildNotification(nm, active, text));
    renewWakeLock();
    return START_STICKY;
  }

  private Notification buildNotification(NotificationManager nm, int activeCount, String text) {
    if (Build.VERSION.SDK_INT >= 26 && nm != null) {
      NotificationChannel ch = new NotificationChannel(
          CHANNEL_ID, "AI 任务后台同步", NotificationManager.IMPORTANCE_LOW);
      ch.setDescription("AI 对话/生成任务在后台继续运行");
      ch.setShowBadge(false);
      nm.createNotificationChannel(ch);
    }
    String body = text != null && !text.isEmpty()
        ? text
        : (activeCount > 1 ? activeCount + " 个 AI 任务在后台运行" : "AI 任务在后台运行中");
    Intent launch = new Intent(this, MainActivity.class);
    launch.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
    int piFlags = PendingIntent.FLAG_UPDATE_CURRENT;
    if (Build.VERSION.SDK_INT >= 23) piFlags |= PendingIntent.FLAG_IMMUTABLE;
    PendingIntent pi = PendingIntent.getActivity(this, 0, launch, piFlags);
    return new NotificationCompat.Builder(this, CHANNEL_ID)
        .setContentTitle("AI 任务进行中")
        .setContentText(body)
        .setSmallIcon(R.mipmap.ic_launcher)
        .setOngoing(true)
        .setOnlyAlertOnce(true)
        .setContentIntent(pi)
        .build();
  }

  private void startForegroundCompat(Notification notif) {
    if (Build.VERSION.SDK_INT >= 29) {
      startForeground(NOTIF_ID, notif, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
    } else {
      startForeground(NOTIF_ID, notif);
    }
  }

  private void renewWakeLock() {
    if (wakeLock != null && wakeLock.isHeld()) {
      // 已持有：PowerManager 不支持延迟续期，先释放再重新计满时长。
      try { wakeLock.release(); } catch (Exception ignored) {}
    }
    PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
    if (pm == null) return;
    wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "openpocket:ai_stream");
    wakeLock.setReferenceCounted(false);
    wakeLock.acquire(WAKELOCK_MS);
  }

  private void stopKeepalive() {
    if (wakeLock != null && wakeLock.isHeld()) {
      try { wakeLock.release(); } catch (Exception ignored) {}
    }
    wakeLock = null;
    stopForeground(true);
    stopSelf();
  }

  @Override
  public void onDestroy() {
    if (wakeLock != null && wakeLock.isHeld()) {
      try { wakeLock.release(); } catch (Exception ignored) {}
    }
    wakeLock = null;
    instance = null;
    super.onDestroy();
  }
}
