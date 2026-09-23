import Foundation
import Capacitor
import LocalAuthentication

/**
 * BiometricAuthPlugin —— iOS 镜像 (Phase 7)。
 *
 * 对应 Android `com.kaixuan.opencode.pocket.plugins.BiometricAuthPlugin`。
 *
 * 实现：
 *   - isAvailable: 用 LAContext.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics) 检测
 *   - authenticate: LAContext.evaluatePolicy(.deviceOwnerAuthentication, ...) 弹 Face ID / Touch ID
 *
 * 与 Android 差异：
 *   - iOS 没有 AndroidKeyStore 的 AES-GCM CryptoObject；
 *     「加密」依赖 Keychain (kSecAttrAccessibleWhenUnlockedThisDeviceOnly) 替代。
 *   - 这里只提供认证门；加密操作由 native 之外的 WebCrypto 走 user 提供的密钥。
 *
 * Phase 7 简化：
 *   - 本插件只暴露 isAvailable / authenticate 两个方法（与 Android 对齐）；
 *   - encrypt/decrypt 走 Keychain 的版本在 Phase 7.1 增量。
 */
@objc(BiometricAuthPlugin)
public class BiometricAuthPlugin: CAPPlugin {
    private static let TAG = "BiometricAuthPlugin"

    @objc func isAvailable(_ call: CAPPluginCall) {
        let context = LAContext()
        var error: NSError?
        let canEvaluate = context.canEvaluatePolicy(
            .deviceOwnerAuthenticationWithBiometrics,
            error: &error
        )
        var biometryType = "none"
        if canEvaluate {
            switch context.biometryType {
            case .faceID: biometryType = "face"
            case .touchID: biometryType = "fingerprint"
            case .opticID: biometryType = "iris"
            default: biometryType = "none"
            }
        }
        call.resolve([
            "available": canEvaluate,
            "biometryType": biometryType,
            "code": error?.code ?? 0
        ])
    }

    @objc func authenticate(_ call: CAPPluginCall) {
        guard let reason = call.getString("reason") else {
            call.reject("reason is required")
            return
        }
        let title = call.getString("title") ?? "OpenPocket"
        let context = LAContext()
        context.localizedReason = reason
        context.localizedFallbackTitle = title
        // iOS 生物识别必须在主线程弹窗
        DispatchQueue.main.async {
            context.evaluatePolicy(
                .deviceOwnerAuthentication,
                localizedReason: reason
            ) { success, error in
                if success {
                    call.resolve(["ok": true])
                } else {
                    call.resolve([
                        "ok": false,
                        "error": error?.localizedDescription ?? "authentication failed"
                    ])
                }
            }
        }
    }
}