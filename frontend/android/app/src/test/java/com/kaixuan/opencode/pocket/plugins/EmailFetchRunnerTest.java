package com.kaixuan.opencode.pocket.plugins;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import android.content.Context;
import android.content.SharedPreferences;
import android.app.AlarmManager;
import android.app.PendingIntent;
import android.content.Intent;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;

/** Exercises the real native runner against Android state and a loopback server. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class EmailFetchRunnerTest {
  private Context context;
  private SharedPreferences prefs;
  private ServerSocket server;
  private Thread responder;
  private final AtomicInteger requests = new AtomicInteger();
  private volatile Runnable afterFirst = () -> {};

  @Before
  public void setup() throws Exception {
    context = RuntimeEnvironment.getApplication();
    prefs = context.getSharedPreferences(EmailFetchRunner.PREFS, Context.MODE_PRIVATE);
    prefs.edit().clear().commit();
    server = new ServerSocket(0, 8, InetAddress.getLoopbackAddress());
    responder = new Thread(() -> {
      while (!server.isClosed()) {
        try (Socket client = server.accept()) {
          BufferedReader reader = new BufferedReader(new InputStreamReader(client.getInputStream(), StandardCharsets.UTF_8));
          reader.readLine();
          String line;
          int bytes = 0;
          while ((line = reader.readLine()) != null && !line.isEmpty()) {
            if (line.toLowerCase().startsWith("content-length:")) bytes = Integer.parseInt(line.substring(15).trim());
          }
          while (bytes-- > 0) reader.read();
          if (requests.incrementAndGet() == 1) afterFirst.run();
          byte[] body = "{\"synced\":1,\"new\":1,\"classified\":1}".getBytes(StandardCharsets.UTF_8);
          OutputStream out = client.getOutputStream();
          out.write(("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " + body.length + "\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.UTF_8));
          out.write(body);
          out.flush();
        } catch (Exception ignored) {
          if (server.isClosed()) return;
        }
      }
    }, "email-policy-test-server");
    responder.start();
  }

  @After
  public void cleanup() throws Exception {
    EmailFetchRunner.disable(context);
    EmailFetchReceiver.cancel(context);
    prefs.edit().clear().commit();
    server.close();
    responder.join(2000);
  }

  private void credentials() {
    prefs.edit().putString("api_base", "http://127.0.0.1:" + server.getLocalPort())
        .putString("token", "isolated-test-credential").commit();
  }

  @Test
  public void oldCredentialsWithoutPolicyAreNotAnUploadGrant() throws Exception {
    credentials();
    try { EmailFetchRunner.run(context); } catch (IllegalStateException expected) { /* fail closed */ }
    assertEquals("An app upgrade must not keep an unapproved legacy background grant", 0, requests.get());
  }

  @Test
  public void persistedLocalModePreventsBothRequests() throws Exception {
    credentials();
    prefs.edit().putBoolean("enabled", false).commit();
    try { EmailFetchRunner.run(context); } catch (IllegalStateException expected) { /* fail closed */ }
    assertEquals(0, requests.get());
  }

  @Test
  public void disablingAfterSyncPreventsClassification() throws Exception {
    credentials();
    prefs.edit().putBoolean("enabled", true).commit();
    afterFirst = () -> prefs.edit().putBoolean("enabled", false).commit();
    try { EmailFetchRunner.run(context); } catch (IllegalStateException expected) { /* policy changed */ }
    assertEquals("Every native POST must recheck the grant", 1, requests.get());
  }

  @Test
  public void explicitCloudGrantAllowsBothRequests() throws Exception {
    EmailFetchRunner.save(context, "http://127.0.0.1:" + server.getLocalPort(), "account-a", true);
    assertEquals(1, EmailFetchRunner.run(context).getInt("classified"));
    assertEquals(2, requests.get());
  }

  @Test
  public void cancellationRemovesCredentialsAlarmAndBlocksBootReschedule() throws Exception {
    EmailFetchRunner.save(context, "http://127.0.0.1:" + server.getLocalPort(), "account-a", true);
    EmailFetchReceiver.schedule(context, 1000);
    AlarmManager alarms = (AlarmManager) context.getSystemService(Context.ALARM_SERVICE);
    assertEquals(1, Shadows.shadowOf(alarms).getScheduledAlarms().size());
    EmailFetchRunner.disable(context);
    EmailFetchReceiver.cancel(context);
    assertEquals("", prefs.getString("api_base", ""));
    assertEquals("", prefs.getString("token", ""));
    assertEquals(0, Shadows.shadowOf(alarms).getScheduledAlarms().size());
    assertEquals(null, PendingIntent.getBroadcast(context, 71, new Intent(context, EmailFetchReceiver.class), PendingIntent.FLAG_NO_CREATE | PendingIntent.FLAG_IMMUTABLE));
    new EmailFetchReceiver().onReceive(context, new Intent(Intent.ACTION_BOOT_COMPLETED));
    new EmailFetchReceiver().onReceive(context, new Intent(Intent.ACTION_MY_PACKAGE_REPLACED));
    assertEquals(0, Shadows.shadowOf(alarms).getScheduledAlarms().size());
    assertEquals(0, requests.get());
  }

  @Test
  public void cancelDuringSyncDisconnectsAndCannotClassifyWithOldCredentials() throws Exception {
    EmailFetchRunner.save(context, "http://127.0.0.1:" + server.getLocalPort(), "account-a", true);
    afterFirst = () -> EmailFetchRunner.disable(context);
    try { EmailFetchRunner.run(context); } catch (Exception expected) { /* in-flight request cancelled */ }
    assertEquals(1, requests.get());
    assertEquals(false, EmailFetchRunner.isEnabled(context));
  }

  @Test
  public void accountReplacementInvalidatesAnAlreadyRunningGrant() throws Exception {
    String base = "http://127.0.0.1:" + server.getLocalPort();
    EmailFetchRunner.save(context, base, "account-a", true);
    afterFirst = () -> EmailFetchRunner.save(context, base, "account-b", true);
    try { EmailFetchRunner.run(context); } catch (Exception expected) { /* old session cancelled */ }
    assertEquals(1, requests.get());
    EmailFetchRunner.run(context);
    assertEquals(3, requests.get());
  }

  @Test(timeout = 5000)
  public void cancellationDoesNotWaitForTheSyncResponseBeforeBlockingTheNextPost() throws Exception {
    EmailFetchRunner.save(context, "http://127.0.0.1:" + server.getLocalPort(), "account-a", true);
    CountDownLatch sent = new CountDownLatch(1);
    CountDownLatch respond = new CountDownLatch(1);
    afterFirst = () -> {
      sent.countDown();
      try { respond.await(2, TimeUnit.SECONDS); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
    };
    Thread fetch = new Thread(() -> {
      try { EmailFetchRunner.run(context); } catch (Exception expected) { /* cancelled */ }
    });
    fetch.start();
    assertTrue(sent.await(2, TimeUnit.SECONDS));
    long start = System.nanoTime();
    EmailFetchRunner.disable(context);
    assertTrue("Cancelling must not wait for a remote response", System.nanoTime() - start < TimeUnit.SECONDS.toNanos(1));
    respond.countDown();
    fetch.join(2000);
    assertEquals(false, fetch.isAlive());
    assertEquals(1, requests.get());
  }
}
