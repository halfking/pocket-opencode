// Package stt — sniff.go
//
// 音频容器的 magic-bytes 嗅探。
//
// 为什么需要（2026-10-05 网关音频端点轮实测）：会议转写端点
// （/api/meetings/{id}/transcribe 与 /api/stt/transcribe-full 的默认值）
// 直接读 raw body，没有文件名线索，filename 被硬编码成 "meeting.wav"。
// 前端 MediaRecorder 的真实产物是 webm/opus——webm 字节顶着 .wav 的
// 文件名上传，网关按 wav 推断 input_audio.format，小米按 wav 解码
// webm 字节直接 400（实测 "invalid audio format"）。嗅探给出真实容器，
// 错误至少不再错位；配合前端 webm→wav 转码后这条路径整体打通。
package stt

// DetectAudioFormat 按容器魔数推断音频格式（扩展名口径，小写）。
// 识别不了返回空串，调用方回退到原文件名/默认值。
func DetectAudioFormat(data []byte) string {
	switch {
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		return "wav"
	case len(data) >= 4 && string(data[0:4]) == "fLaC":
		return "flac"
	case len(data) >= 4 && string(data[0:4]) == "OggS":
		return "ogg"
	// WebM/EBML：0x1A45DFA3
	case len(data) >= 4 && data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3:
		return "webm"
	// MP4/M4A：ftyp box，偏移 4
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return "m4a"
	// MP3：ID3 标签或 MPEG 帧同步（0xFFEx / 0xFFFx）
	case len(data) >= 3 && string(data[0:3]) == "ID3":
		return "mp3"
	case len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0:
		return "mp3"
	default:
		return ""
	}
}

// FilenameForAudio 按「已知格式 → 规范文件名；未知 → 原样」的口径给
// 转写调用一个不撒谎的 filename。defaultName 是调用方原有的默认值
// （如 "meeting.wav"）。
func FilenameForAudio(data []byte, defaultName string) string {
	if f := DetectAudioFormat(data); f != "" {
		return "audio." + f
	}
	return defaultName
}
