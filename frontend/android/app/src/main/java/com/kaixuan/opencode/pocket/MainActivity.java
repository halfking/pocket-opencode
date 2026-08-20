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
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.Plugin;
import com.getcapacitor.JSObject;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.kaixuan.opencode.pocket.plugins.SherpaPlugin;

public class MainActivity extends BridgeActivity {
    private static final int REQ_RECORD_AUDIO = 1001;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        registerPlugin(AppSettingsPlugin.class);
        registerPlugin(SherpaPlugin.class);
        super.onCreate(savedInstanceState);
        WebView.setWebContentsDebuggingEnabled(true);

        // 允许混合内容（仅开发环境使用）
        // 生产环境应该使用HTTPS后端
        if (getBridge() != null && getBridge().getWebView() != null) {
            WebSettings webSettings = getBridge().getWebView().getSettings();
            webSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
            // WebView 录音需要 JS 和 MediaPlayback 不受限
            webSettings.setJavaScriptEnabled(true);
            webSettings.setMediaPlaybackRequiresUserGesture(false);
            // 关键：批准 WebView 的 media（getUserMedia）权限请求。
            // Capacitor BridgeActivity 默认不处理 WebChromeClient.onPermissionRequest，
            // 导致 getUserMedia 即使 app 持有 RECORD_AUDIO 也会被拒（NotAllowedError）。
            getBridge().getWebView().setWebChromeClient(new WebChromeClient() {
                @Override
                public void onPermissionRequest(final PermissionRequest request) {
                    runOnUiThread(new Runnable() {
                        @Override
                        public void run() {
                            // 仅授权音视频资源（需 app 已声明并持有对应运行时权限）
                            request.grant(request.getResources());
                        }
                    });
                }
            });
        }

        // 主动申请录音运行时权限：会议/语音笔记需要麦克风。
        // WebView 的 getUserMedia 需要 app 已持有 RECORD_AUDIO，否则会静默失败。
        ensureRecordAudioPermission();
    }

    /** 若未授予 RECORD_AUDIO，发起系统授权请求。 */
    private void ensureRecordAudioPermission() {
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO)
                != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(
                    this,
                    new String[]{Manifest.permission.RECORD_AUDIO},
                    REQ_RECORD_AUDIO);
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