package com.kaixuan.opencode.pocket;

import android.Manifest;
import android.content.pm.PackageManager;
import android.os.Bundle;
import android.webkit.PermissionRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebChromeClient;
import androidx.core.app.ActivityCompat;
import androidx.core.content.ContextCompat;
import java.util.ArrayList;
import androidx.core.view.WindowCompat;
import com.getcapacitor.BridgeActivity;
import com.kaixuan.opencode.pocket.plugins.AppSettingsPlugin;
import com.kaixuan.opencode.pocket.plugins.SherpaPlugin;
import com.kaixuan.opencode.pocket.plugins.BiometricAuthPlugin;

public class MainActivity extends BridgeActivity {
    private static final int REQ_PERMISSIONS = 1001;
    private static final String WEB_AUDIO_CAPTURE = "android.webkit.resource.AUDIO_CAPTURE";
    private static final String WEB_VIDEO_CAPTURE = "android.webkit.resource.VIDEO_CAPTURE";

    /** 最近一次系统栏 insets（CSS px）。insets 在 WebView 加载前就会派发一次，
        那次 evaluateJavascript 会随页面加载丢失，所以缓存下来在窗口获得焦点时重放。 */
    private float lastSafeTopCssPx = -1f;
    private float lastSafeBottomCssPx = -1f;

    private void injectSafeInsets() {
        if (lastSafeTopCssPx < 0 && lastSafeBottomCssPx < 0) return;
        if (getBridge() != null && getBridge().getWebView() != null) {
            String script = ""
                    + "document.documentElement.style.setProperty('--android-safe-top','"
                    + lastSafeTopCssPx + "px');"
                    + "document.documentElement.style.setProperty('--android-safe-bottom','"
                    + lastSafeBottomCssPx + "px')";
            getBridge().getWebView().evaluateJavascript(script, null);
        }
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        // BridgeActivity#onResume 是 final；用窗口焦点回调兜底：页面就绪获得焦点时
        // 重放 insets，消除首启动 evaluateJavascript 早于页面加载而丢失的竞态。
        if (hasFocus) injectSafeInsets();
    }

    /** 等待系统权限回调时挂起的 WebView 请求；grant 后需要 resume() 它 */
    private PermissionRequest pendingPermissionRequest = null;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        registerPlugin(AppSettingsPlugin.class);
        registerPlugin(SherpaPlugin.class);
        registerPlugin(BiometricAuthPlugin.class);
        registerPlugin(com.kaixuan.opencode.pocket.plugins.BackgroundMicPlugin.class);
        registerPlugin(com.kaixuan.opencode.pocket.plugins.EmailFetchPlugin.class);
        super.onCreate(savedInstanceState);
        // edge-to-edge：让 WebView 内容延伸至状态栏之下。Android WebView 不提供
        // env(safe-area-inset-top)（iOS 才有），所以这里把系统 insets 换算成 CSS px
        // 注入 --android-safe-top，styles.css 用 max(env(...), var(...)) 兜底，
        // 否则顶栏（≡ 菜单按钮等）会被状态栏遮住且无法点击。
        WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
        android.view.View contentView = findViewById(android.R.id.content);
        if (contentView != null) {
            androidx.core.view.ViewCompat.setOnApplyWindowInsetsListener(contentView, (v, insets) -> {
                androidx.core.graphics.Insets systemBars = insets.getInsets(
                        androidx.core.view.WindowInsetsCompat.Type.systemBars());
                androidx.core.graphics.Insets gestures = insets.getInsets(
                        androidx.core.view.WindowInsetsCompat.Type.mandatorySystemGestures());
                float density = getResources().getDisplayMetrics().density;
                lastSafeTopCssPx = systemBars.top / density;
                lastSafeBottomCssPx = Math.max(systemBars.bottom, gestures.bottom) / density;
                injectSafeInsets();
                return insets;
            });
        }
        WebView.setWebContentsDebuggingEnabled(true);

        // 允许混合内容（仅开发环境使用）
        // 生产环境应该使用HTTPS后端
        if (getBridge() != null && getBridge().getWebView() != null) {
            WebSettings webSettings = getBridge().getWebView().getSettings();
            webSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
            // WebView 录音需要 JS 和 MediaPlayback 不受限
            webSettings.setJavaScriptEnabled(true);
            webSettings.setMediaPlaybackRequiresUserGesture(false);
            // 拦截 WebChromeClient.onPermissionRequest：getUserMedia 时若 app 未持
            // RECORD_AUDIO / CAMERA 会直接 NotAllowedError。grant 前先申请对应运行时权限。
            getBridge().getWebView().setWebChromeClient(new WebChromeClient() {
                @Override
                public void onPermissionRequest(final PermissionRequest request) {
                    runOnUiThread(() -> {
                        String[] needed = androidPermissionsFor(request);
                        if (needed.length > 0) {
                            pendingPermissionRequest = request;
                            ActivityCompat.requestPermissions(MainActivity.this, needed, REQ_PERMISSIONS);
                        } else {
                            request.grant(request.getResources());
                        }
                    });
                }
            });
        }
    }

    private boolean hasWebResource(PermissionRequest request, String resource) {
        for (String res : request.getResources()) {
            if (resource.equals(res)) return true;
        }
        return false;
    }

    private boolean hasAndroidPermission(String permission) {
        return ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED;
    }

    /** WebView 资源对应的、尚未授予的 Android 运行时权限。 */
    private String[] androidPermissionsFor(PermissionRequest request) {
        ArrayList<String> needed = new ArrayList<>();
        if (hasWebResource(request, WEB_AUDIO_CAPTURE) && !hasAndroidPermission(Manifest.permission.RECORD_AUDIO)) {
            needed.add(Manifest.permission.RECORD_AUDIO);
        }
        if (hasWebResource(request, WEB_VIDEO_CAPTURE) && !hasAndroidPermission(Manifest.permission.CAMERA)) {
            needed.add(Manifest.permission.CAMERA);
        }
        return needed.toArray(new String[0]);
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == REQ_PERMISSIONS && pendingPermissionRequest != null) {
            if (androidPermissionsFor(pendingPermissionRequest).length == 0) {
                pendingPermissionRequest.grant(pendingPermissionRequest.getResources());
            } else {
                pendingPermissionRequest.deny();
            }
            pendingPermissionRequest = null;
        }
    }
}