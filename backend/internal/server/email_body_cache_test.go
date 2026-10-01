package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// 正文缓存格式版本回归（2026-10-01 需求：详情不完整自愈）。
//
// 旧版本落盘的缓存没有格式字节，且部分内容是拍平展示文本——这类旧缓存
// 必须在读路径被判为未命中（触发回源 IMAP 重拉完整 MIME），否则升级后
// 详情页永远停留在旧的不完整正文上。

func newBodyCacheTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	crypto, err := email.NewCrypto(key)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}
	dir := t.TempDir()
	// bodyCacheDir() 会 MkdirAll，但伪造旧文件发生在调用它之前，先手动建好。
	if err := os.MkdirAll(filepath.Join(dir, bodyCacheDirName), 0700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	return &Server{emailCrypto: crypto, dataDir: dir}, dir
}

func TestBodyCacheLegacyFormatTreatedAsMiss(t *testing.T) {
	srv, dir := newBodyCacheTestServer(t)
	ctx := context.Background()

	// 伪造一份「旧版」缓存：8 字节 UID + 直接跟密文（无格式字节）。
	enc, err := srv.emailCrypto.EncryptString("拍平的旧正文")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	var legacy []byte
	legacy = binary.BigEndian.AppendUint64(legacy, 0) // UID=0（旧版写入时省略）
	legacy = append(legacy, enc...)
	if werr := os.WriteFile(filepath.Join(dir, bodyCacheDirName, "em-legacy.bin"), legacy, 0600); werr != nil {
		t.Fatalf("write legacy cache: %v", werr)
	}

	got, err := srv.readCachedEmailBody(ctx, "em-legacy", 0)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if got != nil {
		t.Fatalf("legacy 缓存必须按未命中处理（触发自愈回源），实际返回了 %d 字节", len(got))
	}
}

func TestBodyCacheVersionedFormatsHit(t *testing.T) {
	srv, _ := newBodyCacheTestServer(t)
	ctx := context.Background()

	for name, format := range map[string]byte{
		"mime": bodyCacheFormatMIME,
		"text": bodyCacheFormatText,
	} {
		if err := srv.writeCachedEmailBody(ctx, "em-"+name, []byte("正文 "+name), format); err != nil {
			t.Fatalf("write %s cache: %v", name, err)
		}
		got, err := srv.readCachedEmailBody(ctx, "em-"+name, 0)
		if err != nil {
			t.Fatalf("read %s cache: %v", name, err)
		}
		if got == nil || string(got) != "正文 "+name {
			t.Fatalf("v 格式缓存应命中，got=%q", got)
		}
	}
}

func TestBodyCacheGarbageHeaderIsMiss(t *testing.T) {
	srv, dir := newBodyCacheTestServer(t)
	// 截断到 8 字节（不足 9 字节头）也要按未命中处理。
	if werr := os.WriteFile(filepath.Join(dir, bodyCacheDirName, "em-short.bin"), []byte{0, 0, 0, 0, 0, 0, 0, 0}, 0600); werr != nil {
		t.Fatalf("write short cache: %v", werr)
	}
	got, err := srv.readCachedEmailBody(context.Background(), "em-short", 0)
	if err != nil || got != nil {
		t.Fatalf("short header = (%v, %v), want (nil, nil)", got, err)
	}
}
