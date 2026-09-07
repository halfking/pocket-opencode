package com.kaixuan.opencode.pocket.plugins;

import android.content.Context;
import android.content.Intent;
import android.media.AudioManager;
import android.os.Build;
import android.util.Base64;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import org.json.JSONArray;

@CapacitorPlugin(name = "BackgroundMic")
public class BackgroundMicPlugin extends Plugin {
  private static BackgroundMicPlugin instance;

  @Override
  public void load() {
    instance = this;
  }

  @Override
  protected void handleOnDestroy() {
    instance = null;
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
    String meetingId = call.getString("meetingId", "");
    String deviceId = call.getString("deviceId");
    Intent i = new Intent(getContext(), MeetingRecordService.class);
    i.setAction(MeetingRecordService.ACTION_START);
    i.putExtra(MeetingRecordService.EXTRA_MEETING, meetingId);
    if (deviceId != null) i.putExtra(MeetingRecordService.EXTRA_DEVICE, deviceId);
    Context ctx = getContext();
    if (Build.VERSION.SDK_INT >= 26) {
      ctx.startForegroundService(i);
    } else {
      ctx.startService(i);
    }
    call.resolve();
  }

  @PluginMethod
  public void stop(PluginCall call) {
    Intent i = new Intent(getContext(), MeetingRecordService.class);
    i.setAction(MeetingRecordService.ACTION_STOP);
    getContext().startService(i);
    call.resolve();
  }

  static void emitPart(int seq, byte[] wav, long startMs, long endMs) {
    if (instance == null) return;
    JSObject data = new JSObject();
    data.put("seq", seq);
    data.put("mimeType", "audio/wav");
    data.put("dataBase64", Base64.encodeToString(wav, Base64.NO_WRAP));
    data.put("startMs", startMs);
    data.put("endMs", endMs);
    instance.notifyListeners("partReady", data);
  }

  static void emitError(String message) {
    if (instance == null) return;
    JSObject data = new JSObject();
    data.put("message", message != null ? message : "录音失败");
    instance.notifyListeners("error", data);
  }
}
