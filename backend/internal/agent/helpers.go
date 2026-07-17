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
