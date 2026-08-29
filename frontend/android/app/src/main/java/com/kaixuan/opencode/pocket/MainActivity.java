package com.kaixuan.opencode.pocket;

import android.Manifest;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Bundle;
import android.webkit.PermissionRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebChromeClient;
import androidx.core.app.ActivityCompat;
import androidx.core.content.ContextCompat;
import androidx.core.view.WindowCompat;
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.Plugin;
import com.getcapacitor.JSObject;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.kaixuan.opencode.pocket.plugins.SherpaPlugin;

public class MainActivity extends BridgeActivity {
    private static final int REQ_PERMISSIONS = 1001;
    private static final String[] WEB_MIC_RESOURCES = {"android.webkit.resource.AUDIO_CAPTURE"};

    /** 等待系统权限回调时挂起的 WebView 请求；grant 后需要 resume() 它 */
    private PermissionRequest pendingPermissionRequest = null;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        registerPlugin(AppSettingsPlugin.class);
        registerPlugin(SherpaPlugin.class);
        super.onCreate(savedInstanceState);
        // edge-to-edge：让 WebView 内容延伸至状态栏之下，配合 styles.css:32 body 的
        // env(safe-area-inset-top) padding 兑现顶部留白，避免双倍下移。
        WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
        WebView.setWebContentsDebuggingEnabled(true);

        // 允许混合内容（仅开发环境使用）
        // 生产环境应该使用HTTPS后端
        if (getBridge() != null && getBridge().getWebView() != null) {
            WebSettings webSettings = getBridge().getWebView().getSettings();
            webSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
            // WebView 录音需要 JS 和 MediaPlayback 不受限
            webSettings.setJavaScriptEnabled(true);
            webSettings.setMediaPlaybackRequiresUserGesture(false);
            // 拦截 WebChromeClient.onPermissionRequest：getUserMedia 时若 app 未持 RECORD_AUDIO
            // 会直接 NotAllowedError。这里在 grant 前先确保 RECORD_AUDIO 已获授权，
            // 已被系统拒绝时让 WebView 走失败分支（UI 跳系统设置）。
            getBridge().getWebView().setWebChromeClient(new WebChromeClient() {
                @Override
                public void onPermissionRequest(final PermissionRequest request) {
                    runOnUiThread(() -> {
                        if (needsAudioCapturePermission(request) && !hasRecordAudioPermission()) {
                            pendingPermissionRequest = request;
                            ActivityCompat.requestPermissions(
                                MainActivity.this,
                                new String[]{Manifest.permission.RECORD_AUDIO},
                                REQ_PERMISSIONS);
                        } else {
                            request.grant(request.getResources());
                        }
                    });
                }
            });
        }
    }

    /** WebView 的 AUDIO_CAPTURE 是否属于"需要 RECORD_AUDIO"的场景 */
    private boolean needsAudioCapturePermission(PermissionRequest request) {
        for (String res : request.getResources()) {
            for (String mic : WEB_MIC_RESOURCES) {
                if (mic.equals(res)) return true;
            }
        }
        return false;
    }

    private boolean hasRecordAudioPermission() {
        return ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO)
                == PackageManager.PERMISSION_GRANTED;
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == REQ_PERMISSIONS && pendingPermissionRequest != null) {
            boolean audioGranted = false;
            for (int i = 0; i < permissions.length; i++) {
                if (Manifest.permission.RECORD_AUDIO.equals(permissions[i])
                        && grantResults[i] == PackageManager.PERMISSION_GRANTED) {
                    audioGranted = true;
                }
            }
            if (audioGranted) {
                // 用户刚刚授权：补发 WebView 的请求，避免 JS 端再次触发 getUserMedia。
                pendingPermissionRequest.grant(pendingPermissionRequest.getResources());
            }
            // 未授权：什么都不做，WebView 会因未 grant 而自动走 NotAllowedError，UI 引导去系统设置。
            pendingPermissionRequest = null;
        }
    }
}

@CapacitorPlugin(name = "AppSettings")
class AppSettingsPlugin extends Plugin {
    @PluginMethod
    public void openAppDetails(PluginCall call) {
        Intent intent = new Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
        intent.setData(Uri.parse("package:" + getContext().getPackageName()));
        getContext().startActivity(intent);
        call.resolve(new JSObject());
    }
}