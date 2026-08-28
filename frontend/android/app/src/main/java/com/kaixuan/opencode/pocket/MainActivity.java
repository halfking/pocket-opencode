package com.kaixuan.opencode.pocket;

import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.webkit.PermissionRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebChromeClient;
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.Plugin;
import com.getcapacitor.JSObject;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.kaixuan.opencode.pocket.plugins.SherpaPlugin;

public class MainActivity extends BridgeActivity {
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
            // 批准 WebView 的 media（getUserMedia）权限请求。Capacitor BridgeActivity
            // 默认不处理 onPermissionRequest，会导致 getUserMedia 始终回落 NotAllowedError。
            // 当 JS 调 getUserMedia 且 app 尚未持权时，WebView 会触发系统权限弹窗。
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

        // 麦克风权限：由前端 useMicPermission() 在首次录音前懒申请，
        // 避免冷启动弹窗打断跳入。
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