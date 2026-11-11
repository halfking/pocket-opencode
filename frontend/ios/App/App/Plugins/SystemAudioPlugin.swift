import Foundation
import Capacitor
import ReplayKit
import AVFoundation

/**
 * SystemAudioPlugin - Cicada Phase B: iOS 系统音频捕获
 *
 * 功能：
 * - 捕获系统音频（会议软件声音）
 * - 支持混合模式（系统音频 + 麦克风）
 * - iOS 14+ 支持
 *
 * 技术：
 * - ReplayKit (RPScreenRecorder)
 * - CMSampleBuffer 音频处理
 * - AVAudioEngine 混音
 */
@objc(SystemAudioPlugin)
public class SystemAudioPlugin: CAPPlugin {
    private static let TAG = "SystemAudioPlugin"
    
    private let recorder = RPScreenRecorder.shared()
    private var isRecording = false
    private var currentSessionId: String?
    private var outputFileURL: URL?
    private var audioFile: AVAudioFile?
    private var audioEngine: AVAudioEngine?
    private var mixerNode: AVAudioMixerNode?
    
    /**
     * 检查系统音频捕获支持
     */
    @objc func isSystemAudioSupported(_ call: CAPPluginCall) {
        let supported = recorder.isAvailable
        call.resolve([
            "supported": supported,
            "iOSVersion": ProcessInfo.processInfo.operatingSystemVersionString
        ])
    }
    
    /**
     * 获取可用的捕获模式
     */
    @objc func getAvailableCaptureModes(_ call: CAPPluginCall) {
        if recorder.isAvailable {
            call.resolve(["modes": ["microphone", "system", "hybrid"]])
        } else {
            call.resolve(["modes": ["microphone"]])
        }
    }
    
    /**
     * 启动系统音频捕获（仅系统音频）
     */
    @objc func startSystemAudio(_ call: CAPPluginCall) {
        guard recorder.isAvailable else {
            call.reject("ReplayKit not available on this device")
            return
        }
        
        // 仅系统音频，不捕获麦克风
        recorder.isMicrophoneEnabled = false
        
        startCapture(call: call, mode: "system")
    }
    
    /**
     * 启动混合模式（系统音频 + 麦克风）
     */
    @objc func startHybridMode(_ call: CAPPluginCall) {
        guard recorder.isAvailable else {
            call.reject("ReplayKit not available on this device")
            return
        }
        
        // 启用麦克风
        recorder.isMicrophoneEnabled = true
        
        // 设置音频会话
        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.playAndRecord, mode: .default, options: [.mixWithOthers, .allowBluetooth])
            try session.setActive(true)
        } catch {
            CAPLog.print("Failed to configure audio session: \(error.localizedDescription)")
        }
        
        startCapture(call: call, mode: "hybrid")
    }
    
    /**
     * 停止录音
     */
    @objc func stopRecording(_ call: CAPPluginCall) {
        stopCaptureInternal()
        
        call.resolve([
            "sessionId": currentSessionId ?? "",
            "uri": outputFileURL?.absoluteString ?? ""
        ])
    }
    
    /**
     * 启动捕获
     */
    private func startCapture(call: CAPPluginCall, mode: String) {
        // 生成会话 ID
        currentSessionId = UUID().uuidString
        
        // 准备输出文件
        let documentsPath = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
        outputFileURL = documentsPath.appendingPathComponent("recording-\(currentSessionId!).caf")
        
        // 配置音频文件
        let audioSettings: [String: Any] = [
            AVFormatIDKey: kAudioFormatLinearPCM,
            AVSampleRateKey: 16000.0,
            AVNumberOfChannelsKey: 1,
            AVLinearPCMBitDepthKey: 16,
            AVLinearPCMIsFloatKey: false,
            AVLinearPCMIsBigEndianKey: false
        ]
        
        do {
            audioFile = try AVAudioFile(forWriting: outputFileURL!, settings: audioSettings)
        } catch {
            call.reject("Failed to create audio file: \(error.localizedDescription)")
            return
        }
        
        // 启动捕获
        recorder.startCapture(handler: { [weak self] (sampleBuffer, bufferType, error) in
            guard let self = self else { return }
            
            if let error = error {
                CAPLog.print("Capture error: \(error.localizedDescription)")
                self.notifyError(error.localizedDescription)
                return
            }
            
            // 处理音频采样
            switch bufferType {
            case .audioApp:
                // 系统音频（应用输出）
                self.processSampleBuffer(sampleBuffer, type: "system")
                
            case .audioMic:
                // 麦克风音频
                self.processSampleBuffer(sampleBuffer, type: "microphone")
                
            default:
                break
            }
            
        }, completionHandler: { [weak self] error in
            if let error = error {
                call.reject("Failed to start capture: \(error.localizedDescription)")
            } else {
                self?.isRecording = true
                call.resolve([
                    "sessionId": self?.currentSessionId ?? "",
                    "mode": mode,
                    "uri": self?.outputFileURL?.absoluteString ?? ""
                ])
            }
        })
    }
    
    /**
     * 处理音频采样缓冲
     */
    private func processSampleBuffer(_ sampleBuffer: CMSampleBuffer, type: String) {
        guard let audioFile = audioFile else { return }
        
        // 转换 CMSampleBuffer 到 AVAudioPCMBuffer
        guard let formatDescription = CMSampleBufferGetFormatDescription(sampleBuffer) else {
            return
        }
        
        let audioStreamBasicDescription = CMAudioFormatDescriptionGetStreamBasicDescription(formatDescription)?.pointee
        
        guard let blockBuffer = CMSampleBufferGetDataBuffer(sampleBuffer) else {
            return
        }
        
        var length: Int = 0
        var dataPointer: UnsafeMutablePointer<Int8>?
        let status = CMBlockBufferGetDataPointer(blockBuffer, atOffset: 0, lengthAtOffsetOut: nil, totalLengthOut: &length, dataPointerOut: &dataPointer)
        
        guard status == kCMBlockBufferNoErr, let data = dataPointer else {
            return
        }
        
        // 创建 AVAudioPCMBuffer
        if let format = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16000, channels: 1, interleaved: false),
           let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: UInt32(length / 2)) {
            
            buffer.frameLength = buffer.frameCapacity
            
            // 复制数据
            if let channelData = buffer.int16ChannelData {
                let channelDataPointer = channelData[0]
                memcpy(channelDataPointer, data, length)
            }
            
            // 写入文件
            do {
                try audioFile.write(from: buffer)
                
                // 通知前端（降低频率）
                if Int.random(in: 0..<100) == 0 {
                    notifyAudioData(length: length, type: type)
                }
            } catch {
                CAPLog.print("Failed to write audio buffer: \(error.localizedDescription)")
            }
        }
    }
    
    /**
     * 停止捕获
     */
    private func stopCaptureInternal() {
        recorder.stopCapture { [weak self] error in
            if let error = error {
                CAPLog.print("Error stopping capture: \(error.localizedDescription)")
            }
            
            self?.isRecording = false
            self?.audioFile = nil
        }
    }
    
    /**
     * 通知前端音频数据
     */
    private func notifyAudioData(length: Int, type: String) {
        notifyListeners("audioData", data: [
            "sessionId": currentSessionId ?? "",
            "length": length,
            "type": type
        ])
    }
    
    /**
     * 通知前端错误
     */
    private func notifyError(_ message: String) {
        notifyListeners("captureError", data: [
            "error": message
        ])
    }
    
    deinit {
        stopCaptureInternal()
    }
}
