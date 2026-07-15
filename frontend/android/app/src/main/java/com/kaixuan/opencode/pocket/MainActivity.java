package com.kaixuan.opencode.pocket;

import android.os.Bundle;
import android.webkit.WebView;
import com.getcapacitor.BridgeActivity;
import com.kaixuan.opencode.pocket.plugins.SherpaPlugin;

public class MainActivity extends BridgeActivity {
  @Override
  public void onCreate(Bundle savedInstanceState) {
    registerPlugin(SherpaPlugin.class);
    super.onCreate(savedInstanceState);
    WebView.setWebContentsDebuggingEnabled(true);
  }
}
