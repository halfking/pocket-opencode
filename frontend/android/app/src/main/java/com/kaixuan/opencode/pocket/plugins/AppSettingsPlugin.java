package com.kaixuan.opencode.pocket.plugins;

import android.Manifest;
import android.os.Build;
import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

/**
 * AppSettings — 打开对应系统权限页（定位本应用），并检查/申请麦克风、通知、相机、相册。
 *
 * 第一次拒绝仍弹系统窗；永久禁止则 openAppDetails({ name }) 打开该权限的系统设置。
 */
@CapacitorPlugin(
        name = "AppSettings",
        permissions = {
            @Permission(alias = "microphone", strings = {Manifest.permission.RECORD_AUDIO}),
            @Permission(alias = "notifications", strings = {Manifest.permission.POST_NOTIFICATIONS}),
            @Permission(alias = "camera", strings = {Manifest.permission.CAMERA}),
            @Permission(alias = "photos", strings = {Manifest.permission.READ_MEDIA_IMAGES}),
            @Permission(alias = "photosLegacy", strings = {Manifest.permission.READ_EXTERNAL_STORAGE})
        })
public class AppSettingsPlugin extends Plugin {
    @PluginMethod
    public void openAppDetails(PluginCall call) {
        String name = call.getString("name", "");
        boolean opened = PermissionSettingsLauncher.open(getContext(), getActivity(), name);
        JSObject ret = new JSObject();
        ret.put("opened", opened);
        call.resolve(ret);
    }

    @PluginMethod
    public void check(PluginCall call) {
        String name = aliasOf(call);
        if (name == null) {
            call.reject("name must be microphone, notifications, camera, or photos");
            return;
        }
        call.resolve(statePayload(name));
    }

    @PluginMethod
    public void request(PluginCall call) {
        String name = aliasOf(call);
        if (name == null) {
            call.reject("name must be microphone, notifications, camera, or photos");
            return;
        }
        if ("notifications".equals(name) && Build.VERSION.SDK_INT < 33) {
            call.resolve(grantedPayload("notifications"));
            return;
        }
        requestPermissionForAlias(resolvedAlias(name), call, "completeRequest");
    }

    @PermissionCallback
    private void completeRequest(PluginCall call) {
        String name = aliasOf(call);
        if (name == null) {
            call.reject("name must be microphone, notifications, camera, or photos");
            return;
        }
        call.resolve(statePayload(name));
    }

    private static String aliasOf(PluginCall call) {
        String name = call.getString("name", "");
        if ("microphone".equals(name)
                || "notifications".equals(name)
                || "camera".equals(name)
                || "photos".equals(name)) {
            return name;
        }
        return null;
    }

    private static String resolvedAlias(String name) {
        if ("photos".equals(name)) {
            return Build.VERSION.SDK_INT >= 33 ? "photos" : "photosLegacy";
        }
        return name;
    }

    private JSObject statePayload(String alias) {
        if ("notifications".equals(alias) && Build.VERSION.SDK_INT < 33) {
            return grantedPayload(alias);
        }
        PermissionState state = getPermissionState(resolvedAlias(alias));
        if (state == null) {
            state = PermissionState.PROMPT;
        }
        JSObject ret = new JSObject();
        ret.put("name", alias);
        ret.put("status", state.toString());
        boolean canRequestAgain =
                state == PermissionState.PROMPT || state == PermissionState.PROMPT_WITH_RATIONALE;
        ret.put("canRequestAgain", canRequestAgain);
        return ret;
    }

    private static JSObject grantedPayload(String alias) {
        JSObject ret = new JSObject();
        ret.put("name", alias);
        ret.put("status", PermissionState.GRANTED.toString());
        ret.put("canRequestAgain", false);
        return ret;
    }
}
