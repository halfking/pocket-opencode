import UIKit
import Capacitor

@UIApplicationMain
class AppDelegate: UIResponder, UIApplicationDelegate {

    var window: UIWindow?

    /// M3（2026-09-09）：AI 流后台保活 — `beginBackgroundTaskWithName` 占位
    /// 系统给的约 30s 后台缓冲，足够 iOS WebView 在用户短暂切走时继续
    /// 推进 SSE / fetch；同时为后续接入 BGTaskScheduler 预留挂载点。
    private var backgroundTaskId: UIBackgroundTaskIdentifier = .invalid
    /// 后台进入时间戳：用于 audit / debug。
    private var backgroundEnteredAt: TimeInterval = 0

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        // Override point for customization after application launch.
        //
        // 隐藏/可见 事件已通过 Capacitor `@capacitor/app` 的 `appStateChange`
        // 推送给前端 `appLifecycleHub.start()`（见
        // frontend/src/native/appLifecycleHub.ts）。Capacitor 在
        // `applicationDidEnterBackground` / `applicationDidBecomeActive`
        // 内部已派发；AppDelegate 不需要再调 JS 桥。下方仅做 iOS 原生侧
        // 的 beginBackgroundTask 占位 + 时间戳记录。
        return true
    }

    func applicationWillResignActive(_ application: UIApplication) {
        // Sent when the application is about to move from active to inactive state. This can occur for certain types of temporary interruptions (such as an incoming phone call or SMS message) or when the user quits the application and it begins the transition to the background state.
        // Use this method to pause ongoing tasks, disable timers, and invalidate graphics rendering callbacks. Games should use this method to pause the game.
    }

    func applicationDidEnterBackground(_ application: UIApplication) {
        // M3：开始 30s 后台缓冲任务。Capacitor App 插件随后会通过
        // `appStateChange { isActive: false }` 通知前端 appLifecycleHub，
        // 由 aiStreamRuntime 暂停 120s watchdog。
        backgroundEnteredAt = Date().timeIntervalSince1970
        backgroundTaskId = application.beginBackgroundTask(withName: "ai-stream-buffer") { [weak self] in
            // expirationHandler：30s 用尽时回调，必须 endBackgroundTask
            // 否则系统会 crash。失败原因通常为 watchdog 暂停后又被
            // 触发，或网络已断。
            guard let self = self else { return }
            if self.backgroundTaskId != .invalid {
                application.endBackgroundTask(self.backgroundTaskId)
                self.backgroundTaskId = .invalid
            }
        }
    }

    func applicationWillEnterForeground(_ application: UIApplication) {
        // Called as part of the transition from the background to the active state; here you can undo many of the changes made on entering the background.
        //
        // M3：结束后台缓冲任务（Capacitor 会在 applicationDidBecomeActive
        // 派发 visible 事件，由前端 appLifecycleHub → aiStreamRuntime 续命）。
        if backgroundTaskId != .invalid {
            application.endBackgroundTask(backgroundTaskId)
            backgroundTaskId = .invalid
        }
        _ = backgroundEnteredAt // 留作未来埋点 / 监控
    }

    func applicationDidBecomeActive(_ application: UIApplication) {
        // Restart any tasks that were paused (or not yet started) while the application was inactive. If the application was previously in the background, optionally refresh the user interface.
    }

    func applicationWillTerminate(_ application: UIApplication) {
        // Called when the application is about to terminate. Save data if appropriate. See also applicationDidEnterBackground:.
        if backgroundTaskId != .invalid {
            application.endBackgroundTask(backgroundTaskId)
            backgroundTaskId = .invalid
        }
    }

    func application(_ app: UIApplication, open url: URL, options: [UIApplication.OpenURLOptionsKey: Any] = [:]) -> Bool {
        // Called when the app was launched with a url. Feel free to add additional processing here,
        // but if you want to use the App API to support tracking app url opens, make sure to keep this call
        return ApplicationDelegateProxy.shared.application(app, open: url, options: options)
    }

    func application(_ application: UIApplication, continue userActivity: NSUserActivity, restorationHandler: @escaping ([UIUserActivityRestoring]?) -> Void) -> Bool {
        // Called when the app was launched with an activity, including Universal Links. Feel free to add additional processing here, but if you want to use the App API to support tracking app url opens, make sure to keep this call
        return ApplicationDelegateProxy.shared.application(application, continue: userActivity, restorationHandler: restorationHandler)
    }

}
