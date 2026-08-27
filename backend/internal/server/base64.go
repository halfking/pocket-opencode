package server

import "encoding/base64"

// base64StdDecode 是 URL-safe base64 解码后的标准 base64 解码入口。
// 单独抽到独立文件便于 server_biometric.go 通过包级函数名复用，避免
// 集中 import 在 handler 文件里造成视觉噪音。
func base64StdDecode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
