package com.kaixuan.opencode.pocket.plugins;

import android.app.AlarmManager;
import android.app.PendingIntent;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.SystemClock;

/** 进程被杀后仍能按间隔委托 pocketd 收信。 */
public class EmailFetchReceiver extends BroadcastReceiver {
  private static final int REQ = 71;

  /** 与前端 GAP_MS（15 分钟）一致。 */
  static final long DEFAULT_INTERVAL_MS = 15L * 60 * 1000;

  /**
   * 这些广播到达时必须重排闹钟，否则后台收信会静默停掉。
   *
   * <p>schedule() 用的是 {@code setInexactRepeating(ELAPSED_REALTIME_WAKEUP, ...)}，
   * 而 <b>ELAPSED_REALTIME 基准的闹钟在设备重启后会被系统清空</b>。原实现只在
   * 前台 {@code bindNative()} 时重排，手机重启一次就再也不会有后台收信——
   * 需求 1「每天定时进行邮件接收」直接失效，而现象是「偶发不收信」，
   * 极难归因。
   *
   * <p>MY_PACKAGE_REPLACED 同理：应用升级也会清空闹钟。
   *
   * <p>抽成静态纯函数是为了能用普通 JUnit 测（Robolectric 未接入，
   * 测不了 AlarmManager 的真实交互）。
   */
  static boolean shouldReschedule(String action) {
    return Intent.ACTION_BOOT_COMPLETED.equals(action)
        || Intent.ACTION_MY_PACKAGE_REPLACED.equals(action);
  }

  static void schedule(Context ctx, long intervalMs) {
    long gap = Math.max(intervalMs, AlarmManager.INTERVAL_FIFTEEN_MINUTES);
    Intent i = new Intent(ctx, EmailFetchReceiver.class);
    PendingIntent pi = PendingIntent.getBroadcast(
        ctx, REQ, i, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    AlarmManager am = (AlarmManager) ctx.getSystemService(Context.ALARM_SERVICE);
    if (am == null) return;
    am.setInexactRepeating(
        AlarmManager.ELAPSED_REALTIME_WAKEUP,
        SystemClock.elapsedRealtime() + gap,
        gap,
        pi);
  }

  @Override
  public void onReceive(Context context, Intent intent) {
    // 开机/升级后先重排，再顺手补一次（设备关着的那段时间收不到邮件）。
    // setInexactRepeating 覆盖同 PendingIntent 的旧闹钟，不会堆叠。
    if (shouldReschedule(intent == null ? null : intent.getAction())) {
      schedule(context, DEFAULT_INTERVAL_MS);
    }
    new Thread(() -> {
      try {
        EmailFetchRunner.run(context.getApplicationContext());
      } catch (Exception ignored) {
        /* 下次闹钟或前台再试 */
      }
    }, "email-fetch").start();
  }
}
