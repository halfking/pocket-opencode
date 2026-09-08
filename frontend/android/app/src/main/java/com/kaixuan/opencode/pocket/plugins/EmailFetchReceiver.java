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
    new Thread(() -> {
      try {
        EmailFetchRunner.run(context.getApplicationContext());
      } catch (Exception ignored) {
        /* 下次闹钟或前台再试 */
      }
    }, "email-fetch").start();
  }
}
