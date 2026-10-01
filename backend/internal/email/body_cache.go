package email

// body_cache.go — 整封邮件原文（RFC 5322 字节）的加密落盘缓存。
//
// 为什么需要它（BUG-AV 的一半修复）：
//
// POP3 降级路径 syncPOP3Fallback 落库时**手上就有完整原文**，但它没存。
// 于是事后再想拿原文时只能走 `FetchMessageRaw(uid)`，而 POP3 路径给的
// `UID` 是**位置序号**不是 IMAP UID，拿去 `UID FETCH` 会取到毫不相干的
// 另一封邮件（见 invoice_harvest.go 里 isPOP3SourcedEmail 的守卫）。
// 结果就是：IMAP 一旦不可用而 POP3 接管（实测 QQ 上 POP3 是主路径，
// 444 封里 284 封），**这些邮件的发票永远采不到**，而原文其实曾经拿到过。
//
// 所以：POP3 同步时把原文存下来，采集器直接读缓存，不再依赖 IMAP UID。
//
// 格式与 server 层既有的 email-bodies 缓存保持一致（8 字节大端 UID 前缀 +
// AES-GCM 密文），但**放在独立子目录** email-bodies-raw：两边的 UID 语义
// 根本不同（一个是 IMAP UID，一个是 POP3 位置序号），混在一个文件里必然互相
// 误命中。

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bodyCacheRawDirName 原文缓存目录。与 server 层的 "email-bodies" 分开：
// 后者按 IMAP UID 判失效，这里按 POP3 位置序号，语义不同不能共用文件。
const bodyCacheRawDirName = "email-bodies-raw"

// BodyCache 存/取整封邮件原文。为 nil 时相关路径整体跳过（不缓存）。
type BodyCache interface {
	// Put 写入原文，返回相对路径（供 emails.body_path 记录）。
	Put(emailID string, uid int64, raw []byte) (relPath string, err error)
	// Get 读回原文；未命中 / 损坏 / UID 不匹配一律返回 (nil, nil)。
	Get(emailID string, uid int64) ([]byte, error)
}

// FileBodyCache 是 BodyCache 的落盘实现：<dataDir>/email-bodies-raw/<id>.bin。
type FileBodyCache struct {
	Dir    string
	Crypto *Crypto
}

var _ BodyCache = (*FileBodyCache)(nil)

// NewFileBodyCache 构造原文缓存。crypto 为 nil 时返回 nil（调用方按 nil 处理）。
func NewFileBodyCache(dataDir string, crypto *Crypto) *FileBodyCache {
	if dataDir == "" || crypto == nil {
		return nil
	}
	return &FileBodyCache{Dir: dataDir, Crypto: crypto}
}

func (c *FileBodyCache) dir() (string, error) {
	if c == nil || c.Dir == "" {
		return "", fmt.Errorf("body cache: dir not configured")
	}
	d := filepath.Join(c.Dir, bodyCacheRawDirName)
	if err := os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	return d, nil
}

// bodyCachePathSafe 报告 email ID 是否可安全用于拼路径。
//
// 与 server 层 emailIDPathSafe 同规则：客户端推送路径允许自带 ID，
// 含路径分隔符或 ".." 的 ID 会把缓存读写引到目录之外。
func bodyCachePathSafe(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`)
}

func (c *FileBodyCache) Put(emailID string, uid int64, raw []byte) (string, error) {
	if !bodyCachePathSafe(emailID) {
		return "", fmt.Errorf("body cache: unsafe email id")
	}
	dir, err := c.dir()
	if err != nil {
		return "", err
	}
	enc, err := c.Crypto.EncryptString(string(raw))
	if err != nil {
		return "", err
	}
	buf := make([]byte, 8, 8+len(enc))
	binary.BigEndian.PutUint64(buf, uint64(uid))
	buf = append(buf, enc...)

	final := filepath.Join(dir, emailID+".bin")
	tmp, err := os.CreateTemp(dir, ".raw-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // rename 成功后这里是无害的 no-op
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// 原子替换：reader 不会撞上半截文件。
	if err := os.Rename(tmpPath, final); err != nil {
		return "", err
	}
	return filepath.Join(bodyCacheRawDirName, emailID+".bin"), nil
}

func (c *FileBodyCache) Get(emailID string, uid int64) ([]byte, error) {
	if !bodyCachePathSafe(emailID) {
		return nil, nil
	}
	dir, err := c.dir()
	if err != nil {
		return nil, nil // 目录都建不出来 = 没有缓存，静视为未命中
	}
	data, err := os.ReadFile(filepath.Join(dir, emailID+".bin"))
	if err != nil {
		return nil, nil
	}
	if len(data) < 8 {
		return nil, nil
	}
	if uid > 0 && int64(binary.BigEndian.Uint64(data[:8])) != uid {
		return nil, nil // 旧缓存，视为未命中
	}
	body, derr := c.Crypto.DecryptString(string(data[8:]))
	if derr != nil {
		return nil, nil // 损坏视为未命中
	}
	return []byte(body), nil
}
