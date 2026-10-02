package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// apk_download_test.go 钉住 /api/app/download 的**失败路径**。
//
// ## 要防的回归
//
// 原实现先设 Content-Type/Content-Disposition，再交给 http.ServeFile。
// APK 文件不在时 ServeFile 回 404，而 http.Error 会覆盖 Content-Type、
// 却**不会删掉** Content-Disposition，于是响应变成：
//
//	404
//	Content-Length: 19
//	Content-Disposition: attachment; filename=opencode-pocket.apk
//	Content-Type: text/plain; charset=utf-8
//
// 客户端照字面执行，存下一个 19 字节、名字叫 .apk 的文件；用户装的时候才报
// 解析失败，而真实原因（服务器上没部署 APK）被完全掩盖。404 长��"文件不在"，
// 不该长得像"下载成功但文件坏了"。
//
// ## 为什么必须真的打 handler
//
// 这个问题在 handler 之外任何一层都看不出来：路由对不对、鉴权过不过，
// 都与「404 响应带不带 attachment 头」无关。只断言状态码会全绿。
//
// ## 负控
//
// 把「先 stat 再设头」改回「先设头再 ServeFile」，第一个用例必须转红 ——
// 状态码仍是 404，只有 Content-Disposition 变了。这正是本文件唯一要守的不变量。

// mustNotLookLikeADownload 断言这个响应不会被客户端当成一次 APK 下载。
func mustNotLookLikeADownload(t *testing.T, rr *httptest.ResponseRecorder, why string) {
	t.Helper()
	if got := rr.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("%s：404/失败响应仍带 Content-Disposition=%q；"+
			"客户端会把它存成 .apk，真实原因被掩盖", why, got)
	}
	if ct := rr.Header().Get("Content-Type"); strings.Contains(ct, "android.package-archive") {
		t.Errorf("%s：失败响应的 Content-Type=%q 宣称自己是 APK", why, ct)
	}
}

func TestAPKDownload_MissingFileDoesNotLookLikeADownload(t *testing.T) {
	srv := &Server{cfg: config.Config{APKDownloadPath: filepath.Join(t.TempDir(), "absent.apk")}}

	rr := httptest.NewRecorder()
	srv.handleDownloadAPK(rr, httptest.NewRequest(http.MethodGet, "/api/app/download", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，want 404（文件不存在）", rr.Code)
	}
	mustNotLookLikeADownload(t, rr, "文件缺失")
}

// 目录形态：路径配错成目录时也必须走同一条失败路径，而不是 ServeFile 去
// 列目录或返回 200。
func TestAPKDownload_DirectoryIsNotServed(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{cfg: config.Config{APKDownloadPath: dir}}

	rr := httptest.NewRecorder()
	srv.handleDownloadAPK(rr, httptest.NewRequest(http.MethodGet, "/api/app/download", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，want 404（路径是目录）", rr.Code)
	}
	mustNotLookLikeADownload(t, rr, "路径是目录")
}

// 对照组：文件真的在时，下载头**必须**存在，且内容正确。
//
// 没有这条，修复可以退化成「一律不设头」，测试照样绿 —— 那是把功能改坏，
// 不是修好。
func TestAPKDownload_PresentFileStillServes(t *testing.T) {
	dir := t.TempDir()
	apk := filepath.Join(dir, "app.apk")
	want := "PK\x03\x04 pretend this is an apk"
	if err := os.WriteFile(apk, []byte(want), 0o600); err != nil {
		t.Fatalf("准备 APK 文件失败: %v", err)
	}

	srv := &Server{cfg: config.Config{APKDownloadPath: apk}}
	rr := httptest.NewRecorder()
	srv.handleDownloadAPK(rr, httptest.NewRequest(http.MethodGet, "/api/app/download", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，want 200（文件存在）；body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Disposition"); !strings.Contains(got, "opencode-pocket.apk") {
		t.Errorf("Content-Disposition = %q，want 含 opencode-pocket.apk", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "android.package-archive") {
		t.Errorf("Content-Type = %q，want android.package-archive", ct)
	}
	if rr.Body.String() != want {
		t.Errorf("响应体 = %q，want %q（必须原样服务文件，不能被改写）", rr.Body.String(), want)
	}
}

// 路径必须来自配置，而不是又写死一次。这条守的是「搬进配置」这件事本身
// 没白做：把 handler 改回硬编码路径，本用例立刻转红。
func TestAPKDownload_PathComesFromConfig(t *testing.T) {
	dir := t.TempDir()
	apk := filepath.Join(dir, "from-config.apk")
	if err := os.WriteFile(apk, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备 APK 文件失败: %v", err)
	}

	srv := &Server{cfg: config.Config{APKDownloadPath: apk}}
	rr := httptest.NewRecorder()
	srv.handleDownloadAPK(rr, httptest.NewRequest(http.MethodGet, "/api/app/download", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，want 200 —— handler 忽略了配置里的路径", rr.Code)
	}
}

// 空路径退回默认常量，而不是去服务空串（那会变成服务当前目录）。
func TestAPKDownload_EmptyPathFallsBackToDefault(t *testing.T) {
	srv := &Server{cfg: config.Config{APKDownloadPath: "   "}}

	rr := httptest.NewRecorder()
	srv.handleDownloadAPK(rr, httptest.NewRequest(http.MethodGet, "/api/app/download", nil))

	// 默认路径在本机不存在，所以是 404；关键是**不能** 200（服务了空路径/目录）。
	if rr.Code == http.StatusOK {
		t.Fatalf("空配置路径竟返回 200，body=%q", rr.Body.String())
	}
	mustNotLookLikeADownload(t, rr, "空路径退回默认")
}

// 默认常量必须与原硬编码值一致：搬进配置是为了让它**可改**，不是为了
// 悄悄换掉某台机器正在依赖的路径。
func TestDefaultAPKDownloadPathUnchanged(t *testing.T) {
	const want = "/data/www/pocket.kxpms.cn/downloads/opencode-pocket-latest.apk"
	if config.DefaultAPKDownloadPath != want {
		t.Fatalf("DefaultAPKDownloadPath = %q，want %q（默认值不得擅自改动）",
			config.DefaultAPKDownloadPath, want)
	}
}
