import { webview } from '@kit.ArkWeb'

const HARMONY_RUNTIME_MARKER = JSON.stringify({
  version: 1,
  host: 'arkts-webview',
  capabilities: {
    camera: false,
    notificationPermission: false,
    localNotifications: false,
    statusBar: false,
    biometric: false,
    encryptedLocalDb: false,
    appLifecycle: false,
    backButton: false,
    appSettings: false
  }
})

@Entry
@Component
struct Index {
  private controller: webview.WebviewController = new webview.WebviewController()

  build() {
    Column() {
      Web({ src: $rawfile('index.html'), controller: this.controller })
        .javaScriptAccess(true)
        .domStorageAccess(true)
        .onPageEnd(() => {
          // Phase A starts as the existing Web fallback. This marker arrives
          // after page load and only identifies the host for future bridge calls;
          // it enables no native capability until that capability has evidence.
          this.controller.runJavaScript(
            `Object.defineProperty(window, '__OPENCODE_POCKET_HARMONY__', { value: ${HARMONY_RUNTIME_MARKER}, configurable: false, writable: false });`
          ).catch((error: Error) => console.error(`Harmony runtime marker failed: ${error.message}`))
        })
    }
    .width('100%')
    .height('100%')
  }
}
