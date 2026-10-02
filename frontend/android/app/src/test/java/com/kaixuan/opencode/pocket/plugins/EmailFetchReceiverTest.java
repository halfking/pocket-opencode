package com.kaixuan.opencode.pocket.plugins;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import android.content.Intent;
import org.junit.Test;

/**
 * EmailFetchReceiver 的重排判定。
 *
 * <p>背景：schedule() 用 setInexactRepeating(ELAPSED_REALTIME_WAKEUP, ...)，
 * 而该基准的闹钟在设备重启与应用升级后会被系统清空。原实现在 manifest 里
 * 没有 BOOT_COMPLETED intent-filter，只在前台 bindNative() 时才重排——
 * 手机重启一次，需求 1「每天定时收信」就永久失效。
 *
 * <p>Robolectric 未接入，测不了 AlarmManager 的真实交互，所以把判定抽成
 * 静态纯函数 here。
 */
public class EmailFetchReceiverTest {

  @Test
  public void bootCompletedMustReschedule() {
    assertTrue(
        "开机广播必须重排闹钟，否则重启后定时收信永久停止",
        EmailFetchReceiver.shouldReschedule(Intent.ACTION_BOOT_COMPLETED));
  }

  @Test
  public void packageReplacedMustReschedule() {
    assertTrue(
        "应用升级同样会清空 ELAPSED_REALTIME 闹钟",
        EmailFetchReceiver.shouldReschedule(Intent.ACTION_MY_PACKAGE_REPLACED));
  }

  @Test
  public void alarmFireItselfDoesNotReschedule() {
    // 闹钟自己触发时不需要重排：setInexactRepeating 已经在重复。
    assertFalse(EmailFetchReceiver.shouldReschedule(""));
    assertFalse(EmailFetchReceiver.shouldReschedule(null));
  }

  @Test
  public void unrelatedBroadcastsDoNotReschedule() {
    assertFalse(EmailFetchReceiver.shouldReschedule(Intent.ACTION_SCREEN_ON));
    assertFalse(EmailFetchReceiver.shouldReschedule(Intent.ACTION_POWER_CONNECTED));
    assertFalse(EmailFetchReceiver.shouldReschedule("com.example.SOMETHING"));
  }

  @Test
  public void defaultIntervalMatchesFrontendGap() {
    // 前端 email-fetch-host.ts 的 GAP_MS = 15 分钟；两边不一致会导致
    // 「前端显示的频率」与「实际闹钟频率」对不上。
    assertEquals(15L * 60 * 1000, EmailFetchReceiver.DEFAULT_INTERVAL_MS);
  }
}
