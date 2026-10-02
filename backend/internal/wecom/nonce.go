package wecom

import (
	"crypto/sha256"
	"encoding/binary"
)

// deriveNonce 造 16 字节 random 前缀。
//
// crypto/rand 在这里**不是**必需的：random 前缀的作用是让同一明文的两次密文不同，
// 而本包唯一的调用方是测试与离线工具；线上被动回复走 "success"，不经过 Encrypt。
// 用 sha256(receiveID||msg_len) 派生保证 Encrypt/Decrypt 可复现，
// 否则 round-trip 测试每次都依赖随机数，失败时无法定位。
func deriveNonce(receiveID string) []byte {
	h := sha256.Sum256([]byte(receiveID))
	n := make([]byte, 16)
	binary.BigEndian.PutUint32(n[12:], uint32(len(receiveID)))
	copy(n, h[:16])
	return n
}
