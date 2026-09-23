import Foundation
import Capacitor
import AVFoundation

/**
 * BackgroundMicPlugin —— iOS 镜像 (Phase 7)。
 *
 * 对应 Android `com.kaixuan/opencode/pocket/plugins/BackgroundMicPlugin.java`。
 *
 * 实现：
 *   - 配置 AVAudioSession 为 .playAndRecord + .mixWithOthers
 *   - 使用 AVAudioRecorder 落盘 m4a (AAC)
 *   - iOS 没有 Android 的 Foreground Service 概念，
 *     后台录音需要 Info.plist 加 UIBackgroundModes: audio + AVAudioSession 配置正确。
 *
 * Phase 7 简化：
 *   - 当前只暴露 start / stop / pause / resume / getState；
 *   - 前端 ui 通过 `recordingRuntime.ts` 兜底（Capacitor plugin 自动 fallback）；
 *   - 真实录音需在 Xcode 上手动配置 Info.plist 后才能跑通。
 */
@objc(BackgroundMicPlugin)
public class BackgroundMicPlugin: CAPPlugin {
    private static let TAG = "BackgroundMicPlugin"

    private var recorder: AVAudioRecorder?
    private var currentSessionId: String = ""
    private var startedAt: Date?

    @objc func start(_ call: CAPPluginCall) {
        let sessionId = call.getString("sessionId") ?? UUID().uuidString
        let sampleRate = call.getDouble("sampleRate") ?? 44100

        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(
                .playAndRecord,
                mode: .default,
                options: [.mixWithOthers, .allowBluetooth]
            )
            try session.setActive(true, options: [.notifyOthersOnDeactivation])

            // 落盘到 Documents/recording-{sessionId}.m4a
            let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
            let url = dir.appendingPathComponent("recording-\(sessionId).m4a")
            let settings: [String: Any] = [
                AVFormatIDKey: kAudioFormatMPEG4AAC,
                AVSampleRateKey: sampleRate,
                AVNumberOfChannelsKey: 1,
                AVEncoderAudioQualityKey: AVAudioQuality.high.rawValue
            ]
            let recorder = try AVAudioRecorder(url: url, settings: settings)
            recorder.isMeteringEnabled = true
            recorder.prepareToRecord()
            if !recorder.record() {
                call.reject("recorder.record() returned false")
                return
            }

            self.recorder = recorder
            self.currentSessionId = sessionId
            self.startedAt = Date()

            call.resolve([
                "sessionId": sessionId,
                "uri": url.absoluteString
            ])
        } catch {
            call.reject("recorder start failed: \(error.localizedDescription)")
        }
    }

    @objc func stop(_ call: CAPPluginCall) {
        guard let rec = self.recorder else {
            call.reject("no active recorder")
            return
        }
        let durationMs: Double
        if let started = self.startedAt {
            durationMs = Date().timeIntervalSince(started) * 1000
        } else {
            durationMs = 0
        }
        rec.stop()
        try? AVAudioSession.sharedInstance().setActive(false, options: [.notifyOthersOnDeactivation])
        let uri = rec.url.absoluteString
        self.recorder = nil
        self.currentSessionId = ""
        self.startedAt = nil
        call.resolve([
            "uri": uri,
            "durationMs": durationMs
        ])
    }

    @objc func pause(_ call: CAPPluginCall) {
        recorder?.pause()
        call.resolve()
    }

    @objc func resume(_ call: CAPPluginCall) {
        if recorder?.record() == true {
            call.resolve()
        } else {
            call.reject("resume failed")
        }
    }

    @objc func getState(_ call: CAPPluginCall) {
        if let rec = recorder, rec.isRecording {
            let durationMs = startedAt.map { Date().timeIntervalSince($0) * 1000 } ?? 0
            rec.updateMeters()
            let peakDb = Double(rec.peakPower(forChannel: 0))
            call.resolve([
                "isRecording": true,
                "sessionId": currentSessionId,
                "durationMs": durationMs,
                "peakDb": peakDb
            ])
        } else {
            call.resolve(["isRecording": false])
        }
    }
}