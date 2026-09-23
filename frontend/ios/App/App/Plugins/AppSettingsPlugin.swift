import Foundation
import Capacitor
import UIKit

/**
 * AppSettingsPlugin —— iOS 镜像 (Phase 7)。
 *
 * 对应 Android `com.kaixuan.opencode.pocket.plugins.AppSettingsPlugin`。
 * 范围：
 *   - openAppDetails({ name }) → 打开 iOS 设置 App 到本应用权限页
 *   - check({ name }) → 检查权限状态
 *   - request({ name }) → 申请权限（iOS 14+ 部分权限需用户主动设置）
 *
 * iOS 与 Android 差异：
 *   - iOS 没有「永久拒绝」（Android 才有），权限状态只是 granted/denied/prompt。
 *   - iOS 的 photos 权限（NSPhotoLibraryUsageDescription）首次会弹系统；
 *     microphone / camera 首次会弹系统。
 *   - 「打开应用设置页」iOS 通过 `UIApplication.openSettingsURLString`。
 */
@objc(AppSettingsPlugin)
public class AppSettingsPlugin: CAPPlugin {
    private static let TAG = "AppSettingsPlugin"

    @objc func openAppDetails(_ call: CAPPluginCall) {
        guard let name = call.getString("name") else {
            call.reject("name is required")
            return
        }
        DispatchQueue.main.async {
            guard let url = URL(string: UIApplication.openSettingsURLString) else {
                call.resolve(["opened": false])
                return
            }
            if UIApplication.shared.canOpenURL(url) {
                UIApplication.shared.open(url, options: [:]) { success in
                    call.resolve(["opened": success])
                }
            } else {
                call.resolve(["opened": false])
            }
        }
    }

    @objc func check(_ call: CAPPluginCall) {
        guard let name = call.getString("name") else {
            call.reject("name is required")
            return
        }
        let state = self.permissionState(name: name)
        call.resolve(["state": state])
    }

    @objc func request(_ call: CAPPluginCall) {
        // iOS 上权限请求通常在用户首次使用相应能力时由系统弹窗；
        // 这里只是占位（真正权限弹窗在 Camera.getPhoto / Recorder.start 等
        // 系统 API 触发时由 iOS 自动弹出）。
        call.resolve(["granted": true])
    }

    private func permissionState(name: String) -> String {
        // 简化：iOS 没有统一的权限查询 API（不像 Android 的 ContextCompat.checkSelfPermission）。
        // 真实使用时应通过具体能力 API（AVCaptureDevice.authorizationStatus 等）查询。
        // 这里返回 'prompt' 作为兜底，调用方根据行为判断。
        return "prompt"
    }
}