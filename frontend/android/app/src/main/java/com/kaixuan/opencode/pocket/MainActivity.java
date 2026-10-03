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

    /** 最近一次 IME insets 底边（CSS px）。见 injectSafeInsets 的注释——这是
        软键盘避让唯一的可靠信号源，不能靠 visualViewport 推。 */
    private float lastImeBottomCssPx = 0f;
    private boolean imeInsetKnown = false;

    private void injectSafeInsets() {
        if (lastSafeTopCssPx < 0 && lastSafeBottomCssPx < 0) return;
        if (getBridge() != null && getBridge().getWebView() != null) {
            // BUG-A 修复 (2026-09-22): onPageCommitVisible 早期回调触发时
            // document.documentElement 可能尚未就绪(WebView 126 回归),
            // 必须用 try/catch 兜底否则抛 "Cannot read properties of
            // null (reading 'style')" 干扰 Vue mount,导致全局点击事件
            // 不触发。Capacitor 8 SystemBars 已禁 insetsHandling=disable
            // (commit 90dfbd6),剩下的 path 就是 MainActivity 这里。
            String script = ""
                    + "try{"
                    + "document.documentElement.style.setProperty('--android-safe-top','"
                    + lastSafeTopCssPx + "px');"
                    + "document.documentElement.style.setProperty('--android-safe-bottom','"
                    + lastSafeBottomCssPx + "px');"
                    // 软键盘净高（CSS px，键盘不在场时 0）。
                    // 为什么必须来自原生：edge-to-edge + 未声明 adjustResize 时，
                    // Android 15 的 IME **既不缩小 WebView 视口、也不改
                    // visualViewport.height**（2026-10-03 模拟器 API 35 实测：
                    // 键盘弹起时 innerHeight 与 vv.height 恒为 915，与收起时
                    // 一模一样）。所以 JS 侧「baseline - min(innerHeight, vv.height)」
                    // 永远算出 0 —— 整条键盘避让机制不触发，聚焦输入框被键盘盖住。
                    // 真机 Android 15 是同一条 overlay 路径（见
                    // useKeyboardInset.ts 文件头），所以这不是模拟器特例，
                    // 而是这一整类设备上的真实缺陷。原生 insets 是唯一信号源。
                    + "document.documentElement.style.setProperty('--android-ime-inset','"
                    + lastImeBottomCssPx + "px');"
                    + "}catch(e){console.debug('[MainActivity] injectSafeInsets skipped:',(e&&e.message)||e)}";
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

    @Override
    public void onPostResume() {
        super.onPostResume();
        // insets 监听在第一次 setOnApplyWindowInsetsListener 之后才注册，若那一刻
        // 键盘已经开着（例如冷启动直接被输入框聚焦），WebView 不会主动来问一次。
        // 页面 ready 后补一次重放，让 --android-ime-inset 一定有初值。
        getBridge().getWebView().postDelayed(this::injectSafeInsets, 400);
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
        registerPlugin(com.kaixuan.opencode.pocket.plugins.AiStreamKeepalivePlugin.class);
        // Document：Android WebView 不渲染 PDF（真机实测全白），内置 PdfRenderer 逐页
        // 栅格化预览 + MediaStore 静默落盘。必须显式注册，否则前端 registerPlugin('Document')
        // 拿不到实现，会静默回落到 <iframe> 空白预览。
        registerPlugin(com.kaixuan.opencode.pocket.plugins.DocumentPlugin.class);
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
                // 软键盘 insets：必须显式取。原实现只处理 systemBars / gestures，
                // 于是 IME 弹出时 JS 侧拿不到任何信号（见 injectSafeInsets 注释）。
                androidx.core.graphics.Insets ime = insets.getInsets(
                        androidx.core.view.WindowInsetsCompat.Type.ime());
                float density = getResources().getDisplayMetrics().density;
                lastSafeTopCssPx = systemBars.top / density;
                lastSafeBottomCssPx = Math.max(systemBars.bottom, gestures.bottom) / density;
                lastImeBottomCssPx = ime.bottom / density;
                imeInsetKnown = true;
                injectSafeInsets();
                return insets;
            });
        }
        // 调试开关收口（原生顺滑度审计 A7/P0 #6）：WebView 远程调试仅 debug 构建开启
        if (BuildConfig.DEBUG) {
            WebView.setWebContentsDebuggingEnabled(true);
        }

        // 允许混合内容（仅开发环境使用）
        // 生产环境应该使用HTTPS后端
        if (getBridge() != null && getBridge().getWebView() != null) {
            WebSettings webSettings = getBridge().getWebView().getSettings();
            if (BuildConfig.DEBUG) {
                webSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
            } else {
                webSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
            }
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