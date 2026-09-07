package com.kaixuan.opencode.pocket.plugins;

import android.media.AudioDeviceInfo;
import android.media.AudioManager;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/** 多麦排序：蓝牙 SCO > 有线/USB 耳机 > USB 设备 > 内置阵列。 */
final class AudioDeviceRank {
  private AudioDeviceRank() {}

  static int rank(AudioDeviceInfo d) {
    switch (d.getType()) {
      case AudioDeviceInfo.TYPE_BLUETOOTH_SCO:
      case AudioDeviceInfo.TYPE_BLE_HEADSET:
        return 0;
      case AudioDeviceInfo.TYPE_WIRED_HEADSET:
      case AudioDeviceInfo.TYPE_WIRED_HEADPHONES:
      case AudioDeviceInfo.TYPE_USB_HEADSET:
        return 1;
      case AudioDeviceInfo.TYPE_USB_DEVICE:
      case AudioDeviceInfo.TYPE_USB_ACCESSORY:
        return 2;
      case AudioDeviceInfo.TYPE_BUILTIN_MIC:
        return 3;
      default:
        return 4;
    }
  }

  static String kind(AudioDeviceInfo d) {
    switch (rank(d)) {
      case 0: return "bluetooth";
      case 1: return "headset";
      case 2: return "usb";
      case 3: return "builtin";
      default: return "unknown";
    }
  }

  static List<AudioDeviceInfo> listInputs(AudioManager am) {
    AudioDeviceInfo[] all = am.getDevices(AudioManager.GET_DEVICES_INPUTS);
    List<AudioDeviceInfo> out = new ArrayList<>();
    if (all == null) return out;
    for (AudioDeviceInfo d : all) {
      if (d.isSource()) out.add(d);
    }
    Collections.sort(out, Comparator.comparingInt(AudioDeviceRank::rank));
    return out;
  }

  static AudioDeviceInfo find(AudioManager am, String deviceId) {
    if (deviceId == null || deviceId.isEmpty()) {
      List<AudioDeviceInfo> ranked = listInputs(am);
      return ranked.isEmpty() ? null : ranked.get(0);
    }
    for (AudioDeviceInfo d : listInputs(am)) {
      if (String.valueOf(d.getId()).equals(deviceId)) return d;
    }
    return null;
  }

  static JSONArray toJson(List<AudioDeviceInfo> devices) throws JSONException {
    JSONArray arr = new JSONArray();
    for (AudioDeviceInfo d : devices) {
      JSONObject o = new JSONObject();
      CharSequence name = d.getProductName();
      o.put("deviceId", String.valueOf(d.getId()));
      o.put("label", name != null ? name.toString() : kind(d));
      o.put("kind", kind(d));
      arr.put(o);
    }
    return arr;
  }
}
