package com.kaixuan.opencode.pocket.plugins;

import android.app.Activity;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import java.util.ArrayList;
import java.util.List;

/**
 * 按权限打开系统设置并定位到本应用。顺序与
 * frontend/src/composables/permission-settings.ts 的 androidSettingsPlan 对齐，
 * 中间插入国产 ROM 权限页，最后回退应用信息。
 */
final class PermissionSettingsLauncher {
    private PermissionSettingsLauncher() {}

    static boolean open(Context context, Activity activity, String name) {
        if (context == null) return false;
        String pkg = context.getPackageName();
        for (Intent intent : intentsFor(name == null ? "" : name, pkg)) {
            if (start(context, activity, intent)) return true;
        }
        return false;
    }

    static List<Intent> intentsFor(String name, String pkg) {
        List<Intent> out = new ArrayList<>();
        if ("notifications".equals(name)) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                out.add(notificationSettings(pkg));
            }
        } else if ("biometric".equals(name)) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                out.add(new Intent(Settings.ACTION_FINGERPRINT_ENROLL));
            }
            out.add(new Intent(Settings.ACTION_SECURITY_SETTINGS));
        } else {
            String group = permissionGroup(name);
            if (group != null) {
                out.add(manageAppPermission(pkg, group));
            }
        }
        out.add(manageAppPermissions(pkg));
        addOemPermissionEditors(out, pkg);
        out.add(applicationDetails(pkg));
        return out;
    }

    private static String permissionGroup(String name) {
        if ("microphone".equals(name)) return "android.permission-group.MICROPHONE";
        if ("camera".equals(name)) return "android.permission-group.CAMERA";
        if ("photos".equals(name)) return "android.permission-group.READ_MEDIA_VISUAL";
        if ("notifications".equals(name)) return "android.permission-group.NOTIFICATIONS";
        return null;
    }

    private static Intent notificationSettings(String pkg) {
        Intent intent = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS);
        intent.putExtra(Settings.EXTRA_APP_PACKAGE, pkg);
        intent.putExtra("android.provider.extra.APP_PACKAGE", pkg);
        intent.putExtra(Intent.EXTRA_PACKAGE_NAME, pkg);
        intent.putExtra("app_package", pkg);
        return intent;
    }

    private static Intent manageAppPermission(String pkg, String group) {
        Intent intent = new Intent("android.intent.action.MANAGE_APP_PERMISSION");
        intent.putExtra(Intent.EXTRA_PACKAGE_NAME, pkg);
        intent.putExtra("android.intent.extra.PERMISSION_GROUP_NAME", group);
        return intent;
    }

    private static Intent manageAppPermissions(String pkg) {
        Intent intent = new Intent("android.intent.action.MANAGE_APP_PERMISSIONS");
        intent.putExtra(Intent.EXTRA_PACKAGE_NAME, pkg);
        return intent;
    }

    private static Intent applicationDetails(String pkg) {
        Intent intent = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
        intent.setData(Uri.fromParts("package", pkg, null));
        intent.addCategory(Intent.CATEGORY_DEFAULT);
        return intent;
    }

    private static void addOemPermissionEditors(List<Intent> out, String pkg) {
        out.add(component(
                "com.vivo.permissionmanager",
                "com.vivo.permissionmanager.activity.SoftPermissionDetailActivity",
                pkg,
                "packagename"));
        out.add(component(
                "com.miui.securitycenter",
                "com.miui.permcenter.permissions.PermissionsEditorActivity",
                pkg,
                "extra_pkgname"));
        out.add(component(
                "com.huawei.systemmanager",
                "com.huawei.permissionmanager.ui.MainActivity",
                pkg,
                "packageName"));
        out.add(component(
                "com.coloros.safecenter",
                "com.coloros.safecenter.permission.PermissionManagerActivity",
                pkg,
                "packageName"));
    }

    private static Intent component(String pkg, String cls, String appPkg, String extraKey) {
        Intent intent = new Intent();
        intent.setComponent(new ComponentName(pkg, cls));
        intent.putExtra(extraKey, appPkg);
        intent.putExtra("packageName", appPkg);
        intent.putExtra("packagename", appPkg);
        return intent;
    }

    private static boolean start(Context context, Activity activity, Intent intent) {
        if (intent == null) return false;
        try {
            if (intent.resolveActivity(context.getPackageManager()) == null) return false;
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            if (activity != null) {
                activity.startActivity(intent);
            } else {
                context.startActivity(intent);
            }
            return true;
        } catch (Exception ignored) {
            return false;
        }
    }
}
