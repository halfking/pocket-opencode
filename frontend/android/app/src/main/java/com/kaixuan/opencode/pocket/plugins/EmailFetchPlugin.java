package com.kaixuan.opencode.pocket.plugins;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import org.json.JSONObject;

@CapacitorPlugin(name = "EmailFetch")
public class EmailFetchPlugin extends Plugin {
  @PluginMethod
  public void configure(PluginCall call) {
    String api = call.getString("apiBase", "");
    String token = call.getString("token", "");
    EmailFetchRunner.save(getContext(), api, token);
    call.resolve();
  }

  @PluginMethod
  public void schedule(PluginCall call) {
    Integer interval = call.getInt("intervalMs", 15 * 60 * 1000);
    EmailFetchReceiver.schedule(getContext(), interval == null ? 15 * 60 * 1000L : interval.longValue());
    call.resolve();
  }

  @PluginMethod
  public void runNow(PluginCall call) {
    new Thread(() -> {
      try {
        JSONObject r = EmailFetchRunner.run(getContext());
        JSObject ret = new JSObject();
        ret.put("synced", r.optInt("synced", 0));
        ret.put("newCount", r.optInt("newCount", 0));
        ret.put("classified", r.optInt("classified", 0));
        call.resolve(ret);
      } catch (Exception e) {
        call.reject(e.getMessage() == null ? "email fetch failed" : e.getMessage());
      }
    }, "email-fetch-now").start();
  }
}
