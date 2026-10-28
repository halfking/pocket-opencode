package com.kaixuan.opencode.pocket.plugins;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Set;
import org.json.JSONObject;

/** 后台线程委托 pocketd 收信+归类；设备不直连 IMAP。 */
final class EmailFetchRunner {
  static final String PREFS = "email_fetch";
  static final String KEY_API = "api_base";
  static final String KEY_TOKEN = "token";
  private static final String KEY_ENABLED = "enabled";
  private static final String KEY_REVISION = "revision";
  static final Object POLICY_LOCK = new Object();
  private static final Set<HttpURLConnection> ACTIVE = new HashSet<>();

  private static final class Grant {
    final String api;
    final String token;
    final long revision;
    Grant(String api, String token, long revision) {
      this.api = api;
      this.token = token;
      this.revision = revision;
    }
  }

  static void save(Context ctx, String apiBase, String token, boolean enabled) {
    if (!enabled || apiBase == null || apiBase.isEmpty() || token == null || token.isEmpty()) {
      disable(ctx);
      return;
    }
    synchronized (POLICY_LOCK) {
      SharedPreferences p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
      if (p.getBoolean(KEY_ENABLED, false) && apiBase.equals(p.getString(KEY_API, ""))
          && token.equals(p.getString(KEY_TOKEN, ""))) return;
      if (!p.edit().putBoolean(KEY_ENABLED, true).putString(KEY_API, apiBase)
          .putString(KEY_TOKEN, token).putLong(KEY_REVISION, p.getLong(KEY_REVISION, 0) + 1).commit()) {
        disable(ctx);
        throw new IllegalStateException("email fetch permission could not be saved");
      }
      disconnectActive();
    }
  }

  static void disable(Context ctx) {
    synchronized (POLICY_LOCK) {
      SharedPreferences p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
      boolean saved = p.edit().putBoolean(KEY_ENABLED, false).remove(KEY_API).remove(KEY_TOKEN)
          .putLong(KEY_REVISION, p.getLong(KEY_REVISION, 0) + 1).commit();
      disconnectActive();
      EmailFetchReceiver.cancel(ctx);
      if (!saved) throw new IllegalStateException("email fetch denial could not be saved");
    }
  }

  private static void disconnectActive() {
    for (HttpURLConnection connection : ACTIVE) connection.disconnect();
    ACTIVE.clear();
  }

  static boolean isEnabled(Context ctx) {
    synchronized (POLICY_LOCK) {
      SharedPreferences p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
      return p.getBoolean(KEY_ENABLED, false) && !p.getString(KEY_API, "").isEmpty()
          && !p.getString(KEY_TOKEN, "").isEmpty();
    }
  }

  static JSONObject run(Context ctx) throws Exception {
    final Grant grant;
    synchronized (POLICY_LOCK) {
      if (!isEnabled(ctx)) throw new IllegalStateException("module_policy_local");
      SharedPreferences p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
      grant = new Grant(p.getString(KEY_API, ""), p.getString(KEY_TOKEN, ""), p.getLong(KEY_REVISION, 0));
    }
    JSONObject sync = post(ctx, grant, "/api/emails/sync", "{}");
    JSONObject cls = post(ctx, grant, "/api/emails/classify", "{\"limit\":20}");
    JSONObject out = new JSONObject();
    out.put("synced", sync.optInt("synced", 0));
    out.put("newCount", sync.optInt("new", 0));
    out.put("classified", cls.optInt("classified", 0));
    return out;
  }

  private static void requireGrant(Context ctx, Grant grant) {
    SharedPreferences p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    if (!isEnabled(ctx) || grant.revision != p.getLong(KEY_REVISION, 0)
        || !grant.api.equals(p.getString(KEY_API, "")) || !grant.token.equals(p.getString(KEY_TOKEN, ""))) {
      throw new IllegalStateException("module_policy_local");
    }
  }

  private static JSONObject post(Context ctx, Grant grant, String path, String body) throws Exception {
    HttpURLConnection c = (HttpURLConnection) new URL(grant.api + path).openConnection();
    try {
      c.setRequestMethod("POST");
      c.setInstanceFollowRedirects(false);
      c.setRequestProperty("Authorization", "Bearer " + grant.token);
      c.setRequestProperty("Content-Type", "application/json");
      c.setConnectTimeout(30_000);
      c.setReadTimeout(120_000);
      c.setDoOutput(true);
      byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
      c.setFixedLengthStreamingMode(bytes.length);
      synchronized (POLICY_LOCK) {
        // Denial/credential replacement and starting a POST share this lock.
        // Once cancel resolves, no later request can use this old grant.
        requireGrant(ctx, grant);
        ACTIVE.add(c);
        try (OutputStream os = c.getOutputStream()) { os.write(bytes); }
      }
      int code = c.getResponseCode();
      InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
      String text = readAll(in);
      if (code >= 300) {
        throw new IllegalStateException("HTTP " + code + " " + text);
      }
      return text.isEmpty() ? new JSONObject() : new JSONObject(text);
    } finally {
      synchronized (POLICY_LOCK) { ACTIVE.remove(c); }
      c.disconnect();
    }
  }

  private static String readAll(InputStream in) throws Exception {
    if (in == null) return "";
    ByteArrayOutputStream buf = new ByteArrayOutputStream();
    byte[] chunk = new byte[4096];
    int n;
    while ((n = in.read(chunk)) >= 0) buf.write(chunk, 0, n);
    in.close();
    return buf.toString(StandardCharsets.UTF_8.name());
  }
}
