package email

// body_cache_test.go — POP3 原文缓存：存得下、取得回、取不回的几种情况都能分开。
//
// 这是 BUG-AV 的功能侧修复：POP3 降级路径的 UID 是位置序号，事后不能拿去
// IMAP FETCH，所以同步时必须把原文留下来（见 body_cache.go）。

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestBodyCache(t *testing.T) *FileBodyCache {
	t.Helper()
	c := NewFileBodyCache(t.TempDir(), testCrypto(t))
	if c == nil {
		t.Fatal("NewFileBodyCache returned nil")
	}
	return c
}

// testCrypto 造一个临时 master key 的 Crypto。
func testCrypto(t *testing.T) *Crypto {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	return c
}

func TestFileBodyCache_RoundTrip(t *testing.T) {
	c := newTestBodyCache(t)
	raw := []byte("From: a@b.com\r\nSubject: 发票\r\n\r\nbody\r\n")
	rel, err := c.Put("em-1", 264, raw)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if rel == "" {
		t.Fatal("Put 返回空相对路径")
	}
	got, err := c.Get("em-1", 264)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("取回的原文与写入不一致:\n got=%q\nwant=%q", got, raw)
	}
}

// TestFileBodyCache_UIDMismatchIsMiss 钉住「UID 不符视为未命中」：
// POP3 位置序号会随邮件增删漂移，缓存串号比不命中更危险。
func TestFileBodyCache_UIDMismatchIsMiss(t *testing.T) {
	c := newTestBodyCache(t)
	if _, err := c.Put("em-2", 264, []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.Get("em-2", 999)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("UID 不匹配应视为未命中，却取回了 %q", got)
	}
}

func TestFileBodyCache_MissOnAbsentAndCorrupt(t *testing.T) {
	c := newTestBodyCache(t)

	if got, _ := c.Get("never-written", 1); got != nil {
		t.Fatalf("不存在的缓存应返回 nil，却拿到 %q", got)
	}

	if _, err := c.Put("em-3", 7, []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	dir := filepath.Join(c.Dir, bodyCacheRawDirName)
	if err := os.WriteFile(filepath.Join(dir, "em-3.bin"), []byte("not-a-valid-cache"), 0600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	got, err := c.Get("em-3", 7)
	if err != nil {
		t.Fatalf("损坏应静视为未命中，却返回 err=%v", err)
	}
	if got != nil {
		t.Fatalf("损坏缓存应返回 nil，却拿到 %q", got)
	}
}

// TestFileBodyCache_RejectsUnsafeIDs 路径穿越防护：email ID 允许客户端自带，
// 含分隔符/".." 的 ID 会把缓存写到目录之外。
func TestFileBodyCache_RejectsUnsafeIDs(t *testing.T) {
	c := newTestBodyCache(t)
	for _, id := range []string{"", ".", "..", `..\..\escape`, "a/b"} {
		if _, err := c.Put(id, 1, []byte("x")); err == nil {
			t.Fatalf("Put(%q) 应该被拒绝（路径穿越风险）", id)
		}
		if got, err := c.Get(id, 1); err != nil || got != nil {
			t.Fatalf("Get(%q) 应返回 nil,nil，实际 got=%q err=%v", id, got, err)
		}
	}
}

func TestNewFileBodyCache_NilWhenDepsMissing(t *testing.T) {
	if c := NewFileBodyCache("", testCrypto(t)); c != nil {
		t.Fatal("dataDir 为空时应返回 nil（调用方按 nil 跳过缓存）")
	}
	if c := NewFileBodyCache(t.TempDir(), nil); c != nil {
		t.Fatal("crypto 为 nil 时应返回 nil")
	}
}
