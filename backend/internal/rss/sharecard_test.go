package rss

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"
)

// TestRenderShareCardPNG 校验渲染产物是合法 PNG，且尺寸正确。
func TestRenderShareCardPNG(t *testing.T) {
	it := Item{
		ID:          "it_test_1",
		Title:       "Hello world — OpenPocket launches RSS subscriptions",
		URL:         "https://example.com/posts/hello-world",
		Author:      "Author",
		Summary:     "A short summary of an RSS item used for share-card rendering tests.",
		PublishedAt: timePtr(time.Now()),
	}
	src := &Source{
		ID:    "src_test_1",
		URL:   "https://example.com/feed.xml",
		Title: "Example Feed",
	}
	png, err := RenderShareCard(context.Background(), it, src, "light")
	if err != nil {
		t.Fatalf("render light: %v", err)
	}
	if len(png) < 1024 {
		t.Fatalf("expected >1KB PNG, got %d bytes", len(png))
	}
	// PNG 魔数 89 50 4E 47 0D 0A 1A 0A
	magic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if !bytes.HasPrefix(png, magic) {
		t.Fatalf("not a PNG; first 8 bytes = % x", png[:8])
	}
	// IHDR width/height: bytes 16-23 是大端 uint32
	if len(png) < 24 {
		t.Fatalf("PNG too short: %d bytes", len(png))
	}
	w := binary.BigEndian.Uint32(png[16:20])
	h := binary.BigEndian.Uint32(png[20:24])
	if w != 1080 || h != 1350 {
		t.Fatalf("unexpected dimensions: %dx%d", w, h)
	}

	// dark theme 也得能渲染。
	png2, err := RenderShareCard(context.Background(), it, src, "dark")
	if err != nil {
		t.Fatalf("render dark: %v", err)
	}
	if len(png2) < 1024 {
		t.Fatalf("dark PNG too small: %d bytes", len(png2))
	}
}

// TestRenderShareCardCJKGlyphFallback 验证 CJK 字符被替换为 '?' 而不 panic。
func TestRenderShareCardCJKGlyphFallback(t *testing.T) {
	it := Item{Title: "阮一峰: 网络日志 - 最新文章"}
	src := &Source{Title: "阮一峰的网络日志"}
	png, err := RenderShareCard(context.Background(), it, src, "light")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.HasPrefix(png, []byte{0x89, 0x50}) {
		t.Fatalf("expected PNG")
	}
}