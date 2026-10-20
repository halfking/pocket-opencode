package server

// response_encode_test.go — §946 判据：**响应体编码失败**必须可见。
//
// # 这道判据在量什么
//
//	一句话：**「值不可 JSON 编码」这件事，客户端必须能看见，服务端必须留痕。**
//
// # ⚠ 症状是**实测**出来的（不是推理）
//
//	§946 实测（2026-10-21，旧实现）：
//
//	    writeJSON(rec, 200, map[string]any{"x": math.NaN()})
//	    ⇒ status = 200、bodyLen = 0（**空体**，不是截断！）
//
//	原因：`json.Encoder.Encode` **先把值 marshal 进内部 buffer、成功后才写**
//	⇒ ⇒⇒ 编码失败时**响应体是空的**；而旧写法**先 WriteHeader 了**
//	⇒ ⇒⇒⇒ 客户端拿到「200 OK + 空体」⇒ ⇒⇒⇒ 服务端**一个字都没留**
//	⇒ ⇒⇒⇒ 前端只看到「Unexpected end of JSON input」。
//
//	⇒ 所以判据钉的是**行为**，不是源码形态：
//	① 编码失败 ⇒ **500**（不是 200）
//	② 响应体**非空**、且是**合法 JSON**、且带**稳定错误码**
//	③ 正常路径的响应体**字节与旧实现逐字相同**（不得回归）
//	④ 写失败（客户端断开）⇒ **留痕**（日志里有记录）
//
// # ⚠ 为什么 ③ 要钉「逐字相同」
//
//	§946 实测：`json.Marshal(v) + "\n"` 与 `json.Encoder.Encode(v)` **逐字相同**
//	⇒ ⇒⇒ 所以「先编码后写状态」这个改法**不该改变任何一个响应体的字节**
//	⇒ ⇒⇒ ⇒ 钉住它 ⇒ 万一以后有人把换行去掉、或改了转义行为，立刻会红。

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// s946Unmarshalable 返回一个**无法被 JSON 编码**的值（NaN）。
//
// ⚠ NaN 是 JSON 规范里**不存在**的 ⇒ `json.Marshal` 必然报错
// 　（实测错误类型：*json.UnsupportedValueError）。
func s946Unmarshalable() map[string]any {
	return map[string]any{"x": math.NaN()}
}

// s946FailWriter 是一个**写失败**的 ResponseWriter（模拟客户端断开）。
type s946FailWriter struct {
	header http.Header
	status int
	writes int
}

func (w *s946FailWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *s946FailWriter) WriteHeader(code int) { w.status = code }

func (w *s946FailWriter) Write(p []byte) (int, error) {
	w.writes++
	return 0, errors.New("s946: client gone")
}

// s946CaptureLog 把标准 logger 的输出截到 buffer，返回恢复函数。
func s946CaptureLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	old := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	return buf, func() {
		log.SetOutput(old)
		log.SetFlags(oldFlags)
	}
}

// TestS946EncodeFailureIsVisible 是断言 ①②：编码失败 ⇒ 500 + 可区分错误体。
func TestS946EncodeFailureIsVisible(t *testing.T) {
	logBuf, restore := s946CaptureLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, s946Unmarshalable())

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("编码失败时的状态码 = %d（期望 500）\n"+
			"⇒ 旧实现是 200 + **空体**（§946 实测）⇒ 客户端完全看不出出了事",
			rec.Code)
	}
	body := rec.Body.String()
	if body == "" {
		t.Fatalf("编码失败时的响应体是**空的** ⇒ 客户端拿到「200/500 + 空体」⇒ 必须给可区分的错误体")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q（期望 application/json）", ct)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("兜底响应体不是**合法 JSON**（%v）：%q", err, body)
	}
	// ⚠⚠ 稳定错误码必须**逐字**钉住，不能只比常量。
	//
	//	若这里只比 `payload["code"] != s946EncodeFailureCode`
	//	⇒ ⇒⇒ 那就等于「常量与它自己比」⇒ ⇒⇒⇒ **改掉常量的值判据照样绿**
	//	⇒ ⇒⇒ ⇒ 而前端是按这个码做映射的 ⇒ 改了它 = 改了契约。
	//	⇒ ⇒⇒ ⇒ 所以这里钉**字面量**。
	const wantCode = "response_encode_failed"
	if s946EncodeFailureCode != wantCode {
		t.Errorf("稳定错误码常量 = %q（期望 %q）⇒ 前端按这个码做映射 ⇒ 改它等于改契约",
			s946EncodeFailureCode, wantCode)
	}
	if got := payload["code"]; got != wantCode {
		t.Errorf("兜底响应体的 code = %v（期望 %q）⇒ 客户端/前端要靠它区分「服务端自己没写出响应」",
			got, wantCode)
	}
	if got, ok := payload["retryable"].(bool); !ok || got {
		t.Errorf("兜底响应体的 retryable = %v（期望 false）\n"+
			"⇒ 编码失败发生在**业务动作之后** ⇒ 主结果可能已落库 ⇒ 盲目重试可能造成重复副作用",
			payload["retryable"])
	}
	// ⚠ 前端 `extractErrorCode` 按 `:` 切错误串取前半段 ⇒ 文案必须带码前缀。
	msg, _ := payload["error"].(string)
	if !strings.HasPrefix(msg, s946EncodeFailureCode+":") {
		t.Errorf("兜底响应体的 error = %q（期望以 %q 开头）\n"+
			"⇒ 前端 extractErrorCode 靠「码: 说明」这个形态取码（frontend/src/api/error-message.ts）",
			msg, s946EncodeFailureCode)
	}
	// ⚠ 服务端必须留痕（否则又是「无声失败」）。
	if !strings.Contains(logBuf.String(), s946EncodeFailureCode) &&
		!strings.Contains(logBuf.String(), "响应体编码失败") {
		t.Errorf("编码失败时服务端**没有留痕**（日志=%q）⇒ 又是一次无声失败", logBuf.String())
	}
}

// TestS946SuccessBodyBytesUnchanged 是断言 ③：正常路径字节**逐字不变**。
//
// ⚠ 这是「改动不许偷偷改响应体」的那道闸：
// 　 期望值按 §946 实测写成**逐字**比较（不是 JSON 语义比较）
// 　⇒ 因为语义比较看不出「少了一个换行」「转义方式变了」这类回归。
func TestS946SuccessBodyBytesUnchanged(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"简单 map", map[string]any{"x": 1}},
		{"HTML 字符", map[string]any{"b": "中文<&>"}},
		{"嵌套", map[string]any{"a": []any{1, 2}, "c": map[string]any{"d": true}}},
		{"数组", []string{"a", "b"}},
		{"字符串", "plain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeJSON(rec, http.StatusOK, c.v)

			// 基线：**旧实现**的写法（json.NewEncoder(w).Encode）。
			var baseline bytes.Buffer
			if err := json.NewEncoder(&baseline).Encode(c.v); err != nil {
				t.Fatalf("基线编码失败：%v", err)
			}
			if got, want := rec.Body.String(), baseline.String(); got != want {
				t.Errorf("响应体字节变了：\n  实际 = %q\n  基线 = %q\n"+
					"⇒ §946 的改动**不许**改变任何一个正常响应的字节", got, want)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("状态码 = %d（期望 200）", rec.Code)
			}
		})
	}
}

// TestS946WriteFailureIsLogged 是断言 ④：写失败 ⇒ 留痕。
//
// ⚠ 写失败时状态行**早已提交** ⇒ 改不了状态码 ⇒ ⇒⇒ 唯一能做的是留痕。
// 　 旧实现是 `_, _ = w.Write(...)` ⇒ **一个字都没留**。
func TestS946WriteFailureIsLogged(t *testing.T) {
	logBuf, restore := s946CaptureLog(t)
	defer restore()

	w := &s946FailWriter{}
	writeJSON(w, http.StatusOK, map[string]any{"x": 1})

	if w.status != http.StatusOK {
		t.Errorf("状态码 = %d（期望 200）⇒ 写失败不该改变已提交的状态行", w.status)
	}
	if !strings.Contains(logBuf.String(), "响应体写出失败") {
		t.Errorf("写失败时服务端**没有留痕**（日志=%q）⇒ 客户端拿到截断响应而服务端零记录",
			logBuf.String())
	}
}

// TestS946HelperFamilyUsesSafePath 断言：同形 helper 也走「先编码后写状态」。
//
// ⚠ 这三个 helper 以前**各自**写了一遍「先 WriteHeader 再 Encode」
// 　⇒ ⇒⇒ 委托给 writeJSON 之后，它们应当有**同样**的行为。
func TestS946HelperFamilyUsesSafePath(t *testing.T) {
	t.Run("writeMigrationJSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeMigrationJSON(rec, http.StatusOK, s946Unmarshalable())
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("writeMigrationJSON 编码失败时状态码 = %d（期望 500）", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), s946EncodeFailureCode) {
			t.Errorf("writeMigrationJSON 编码失败时的响应体 = %q（期望含 %q）",
				rec.Body.String(), s946EncodeFailureCode)
		}
	})
	t.Run("writeSessionJSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeSessionJSON(rec, s946Unmarshalable())
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("writeSessionJSON 编码失败时状态码 = %d（期望 500）", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), s946EncodeFailureCode) {
			t.Errorf("writeSessionJSON 编码失败时的响应体 = %q（期望含 %q）",
				rec.Body.String(), s946EncodeFailureCode)
		}
	})
	t.Run("writeError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusBadRequest, "boom")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("writeError 状态码 = %d（期望 400）", rec.Code)
		}
		if got, want := rec.Body.String(), "{\"error\":\"boom\"}\n"; got != want {
			t.Errorf("writeError 响应体 = %q（期望 %q）⇒ 字节被改了", got, want)
		}
	})
}

// TestS946SSEWriteReturnsError 是断言 ⑤：SSE 写 helper 必须**把错误交回去**。
//
// ⚠ 为什么这条不能省：SSE 的失败语义是「写失败 = 这条流已经死了」
// 　 ⇒ ⇒⇒ 调用方必须能**中断**它 ⇒ ⇒⇒ ⇒ 而「能不能中断」完全取决于
// 　 ⇒ ⇒⇒ ⇒ helper **返不返回**那个错误。
// 　 ⇒ ⇒⇒ ⇒ 如果 helper 把错误吞掉（返回 nil）⇒ ⇒⇒⇒ 调用方的
// 　 ⇒ ⇒⇒ ⇒ `if err != nil { return }` 永远不成立 ⇒ ⇒⇒⇒ ⇒ 死流会一直写。
func TestS946SSEWriteReturnsError(t *testing.T) {
	logBuf, restore := s946CaptureLog(t)
	defer restore()

	w := &s946FailWriter{}
	err := s946SSEWritef(w, "data: %s\n\n", "x")
	if err == nil {
		t.Errorf("SSE 写失败时 s946SSEWritef 返回了 nil ⇒ 调用方**无法中断**这条流\n" +
			"⇒ 死流会继续写（每次都失败），客户端那边只是一直收不到事件")
	}
	if !strings.Contains(logBuf.String(), "SSE") {
		t.Errorf("SSE 写失败时没有留痕（日志=%q）", logBuf.String())
	}
}
