package agent

// helpers.go — transport 通用 helper

import (
	"crypto/tls"
	"io"
)

// insecureSkipVerifyTLSConfig 返回跳过证书校验的 TLS 配置。
//
// 仅用于开发/内网部署（agent 自签证书）。生产应使用正式证书。
func insecureSkipVerifyTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
	}
}

// readBody 读完整 body 并截断到 maxLen（用于错误消息）。
func readBody(r io.Reader, maxLen int) string {
	if r == nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(r, int64(maxLen)))
	if err != nil {
		return ""
	}
	return string(b)
}

// readAll 读完整 body（无限长）。
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// getString 从 map 安全取 string（type assertion + 默认值）。
func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt 从 map 安全取 int（type assertion + 默认值）。
func getInt(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		if i, ok := v.(int); ok {
			return i
		}
		// 尝试 float64（JSON unmarshal 数字默认类型）
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

// getBool 从 map 安全取 bool（type assertion + 默认值）。
func getBool(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

