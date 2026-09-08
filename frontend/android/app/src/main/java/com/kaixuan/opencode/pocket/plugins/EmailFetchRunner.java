package com.kaixuan.opencode.pocket.plugins;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import org.json.JSONObject;

/** 后台线程委托 pocketd 收信+归类；设备不直连 IMAP。 */
final class EmailFetchRunner {
  static final String PREFS = "email_fetch";
  static final String KEY_API = "api_base";
  static final String KEY_TOKEN = "token";

  static void save(Context ctx, String apiBase, String token) {
    ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        .edit()
        .putString(KEY_API, apiBase == null ? "" : apiBase)
        .putString(KEY_TOKEN, token == null ? "" : token)
        .apply();
  }

  static JSONObject run(Context ctx) throws Exception {
    SharedPreferences p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    String api = p.getString(KEY_API, "");
    String token = p.getString(KEY_TOKEN, "");
    if (api == null || api.isEmpty() || token == null || token.isEmpty()) {
      throw new IllegalStateException("email fetch not configured");
    }
    JSONObject sync = post(api + "/api/emails/sync", token, "{}");
    JSONObject cls = post(api + "/api/emails/classify", token, "{\"limit\":20}");
    JSONObject out = new JSONObject();
    out.put("synced", sync.optInt("synced", 0));
    out.put("newCount", sync.optInt("new", 0));
    out.put("classified", cls.optInt("classified", 0));
    return out;
  }

  private static JSONObject post(String url, String token, String body) throws Exception {
    HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
    try {
      c.setRequestMethod("POST");
      c.setRequestProperty("Authorization", "Bearer " + token);
      c.setRequestProperty("Content-Type", "application/json");
      c.setConnectTimeout(30_000);
      c.setReadTimeout(120_000);
      c.setDoOutput(true);
      byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
      c.setFixedLengthStreamingMode(bytes.length);
      OutputStream os = c.getOutputStream();
      os.write(bytes);
      os.close();
      int code = c.getResponseCode();
      InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
      String text = readAll(in);
      if (code >= 400) {
        throw new IllegalStateException("HTTP " + code + " " + text);
      }
      return text.isEmpty() ? new JSONObject() : new JSONObject(text);
    } finally {
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
