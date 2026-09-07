package com.kaixuan.opencode.pocket.plugins;

import android.Manifest;
import android.content.Intent;
import android.net.Uri;
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
 * AppSettings — 打开系统应用详情，并支持麦克风/通知运行时权限的检查与再次申请。
 *
 * 禁止后必须还能再次 request：第一次拒绝是 prompt-with-rationale，
 * 仍弹系统窗；只有「不再询问」/二次拒绝（denied）才引导去系统设置。
 */
@CapacitorPlugin(
        name = "AppSettings",
        permissions = {
            @Permission(alias = "microphone", strings = {Manifest.permission.RECORD_AUDIO}),
            @Permission(alias = "notifications", strings = {Manifest.permission.POST_NOTIFICATIONS})
        })
public class AppSettingsPlugin extends Plugin {
    @PluginMethod
    public void openAppDetails(PluginCall call) {
        Intent intent = new Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
        intent.setData(Uri.parse("package:" + getContext().getPackageName()));
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        getContext().startActivity(intent);
        call.resolve(new JSObject());
    }

    @PluginMethod
    public void check(PluginCall call) {
        String name = aliasOf(call);
        if (name == null) {
            call.reject("name must be microphone or notifications");
            return;
        }
        call.resolve(statePayload(name));
    }

    @PluginMethod
    public void request(PluginCall call) {
        String name = aliasOf(call);
        if (name == null) {
            call.reject("name must be microphone or notifications");
            return;
        }
        if ("notifications".equals(name) && Build.VERSION.SDK_INT < 33) {
            call.resolve(grantedPayload("notifications"));
            return;
        }
        requestPermissionForAlias(name, call, "completeRequest");
    }

    @PermissionCallback
    private void completeRequest(PluginCall call) {
        String name = aliasOf(call);
        if (name == null) {
            call.reject("name must be microphone or notifications");
            return;
        }
        call.resolve(statePayload(name));
    }

    private static String aliasOf(PluginCall call) {
        String name = call.getString("name", "");
        if ("microphone".equals(name) || "notifications".equals(name)) {
            return name;
        }
        return null;
    }

    private JSObject statePayload(String alias) {
        if ("notifications".equals(alias) && Build.VERSION.SDK_INT < 33) {
            return grantedPayload(alias);
        }
        PermissionState state = getPermissionState(alias);
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
