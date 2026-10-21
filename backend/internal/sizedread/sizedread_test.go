package sizedread

// sizedread_test.go — §952：唯一收口的**自证**。
//
// ⚠ 断言必须**双边**（§951 的教训）：
//
//	「至多读 N 字节」这件事有两个坏法：
//	  - 读**太多** ⇒ 漏掉了超限 ⇒ 内存又被撑爆；
//	  - 读**太少** ⇒ 把「刚好等于 N」误判成「超限」⇒ 正常载荷被拒。
//	⇒⇒⇒ ⇒ 所以每条用例都要同时钉住「N 以内成功」与「N+1 失败」。

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// s952Repeat 造一个重复 n 次的 reader。
func s952Repeat(n int) io.Reader {
	return strings.NewReader(strings.Repeat("a", n))
}

// TestAllAcceptsExactlyAtLimit 钉住「刚好等于上限」**不是**超限。
func TestAllAcceptsExactlyAtLimit(t *testing.T) {
	for _, n := range []int{0, 1, 10, 4096} {
		got, err := All(s952Repeat(n), int64(n))
		if err != nil {
			t.Errorf("All(重复 %d 字节, limit=%d) 返回错误 %v\n"+
				"⇒ 载荷**恰好等于**上限时必须成功 ⇒ 否则正常载荷被误拒",
				n, n, err)
			continue
		}
		if len(got) != n {
			t.Errorf("All(重复 %d 字节, limit=%d) 读到 %d 字节（期望 %d）", n, n, len(got), n)
		}
	}
}

// TestAllRejectsOverLimit 钉住「超出一个字节」必须**可辨认地**失败。
func TestAllRejectsOverLimit(t *testing.T) {
	for _, n := range []int{1, 10, 4096} {
		_, err := All(s952Repeat(n+1), int64(n))
		if err == nil {
			t.Errorf("All(重复 %d 字节, limit=%d) 成功了\n"+
				"⇒ 超限必须失败 ⇒ 否则「整个读进内存」这件事根本没被上界管住", n+1, n)
			continue
		}
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("All(重复 %d 字节, limit=%d) 的错误 = %v（不是 ErrTooLarge）\n"+
				"⇒ 「太大」必须与「读失败」可分辨 ⇒ 否则运维无从判断", n+1, n, err)
		}
	}
}

// TestAllDoesNotReturnPartialPayload 钉住超限时**不返回半个载荷**。
//
// ⚠ 这是 §941 的老教训：「太大」退化成「半个 JSON」⇒ 解码器报「unexpected EOF」
// ⇒ 「太大」与「格式错」同形。
func TestAllDoesNotReturnPartialPayload(t *testing.T) {
	got, err := All(s952Repeat(100), 10)
	if err == nil {
		t.Fatalf("All(100 字节, limit=10) 成功了（期望失败）")
	}
	if got != nil {
		t.Errorf("All(100 字节, limit=10) 返回了 %d 字节的载荷\n"+
			"⇒ 超限必须返回 nil ⇒ 否则调用方会拿半个载荷去解析 ⇒ 「太大」被伪装成「格式错」",
			len(got))
	}
}

// TestLimitReportsTooLargeNotEOF 钉住 `Limit` 的**核心区别**。
func TestLimitReportsTooLargeNotEOF(t *testing.T) {
	// 恰好等于上限 ⇒ EOF（不是超限）。
	//
	// ⚠ 用 io.ReadAll 而不是单次 Read：io.Reader 的契约允许「读出数据但
	//   不同时给 EOF」⇒⇒⇒ 单次 Read 拿不到 EOF 是**正常**的，
	//   真正的判据是「一直读到出错为止」。
	//
	// ⚠ io.ReadAll 把 io.EOF 当成「正常结束」⇒ 返回 nil error
	//   ⇒⇒⇒ 所以 EOF 语义要另外用「一直读到出错」来钉。
	total, err := io.ReadAll(Limit(s952Repeat(10), 10))
	if err != nil {
		t.Errorf("io.ReadAll(Limit(10 字节, limit=10)) 的错误 = %v（期望 nil）\n"+
			"⇒ 载荷**恰好等于**上限时必须成功 ⇒ 否则正常载荷被误判成超限", err)
	}
	if len(total) != 10 {
		t.Errorf("io.ReadAll(Limit(10 字节, limit=10)) 读到 %d 字节（期望 10）", len(total))
	}
	if !s952ReadToEOF(Limit(s952Repeat(10), 10)) {
		t.Errorf("Limit(10 字节, limit=10) 没有以 io.EOF 收尾\n" +
			"⇒ 恰好等于上限必须是 EOF（不是 ErrTooLarge）")
	}

	// 多一个字节 ⇒ ErrTooLarge（**不是** EOF）。
	_, err2 := io.ReadAll(Limit(s952Repeat(11), 10))
	if err2 == nil {
		t.Fatalf("io.ReadAll(Limit(11 字节, limit=10)) 没有报错\n" +
			"⇒ 超限必须报错 ⇒ 这正是本包与 io.LimitReader 的唯一区别")
	}
	if !errors.Is(err2, ErrTooLarge) {
		t.Errorf("io.ReadAll(Limit(11 字节, limit=10)) 的错误 = %v（不是 ErrTooLarge）", err2)
	}
	if errors.Is(err2, io.EOF) {
		t.Errorf("Limit 超限时返回了 io.EOF\n" +
			"⇒ 那就退化成 io.LimitReader 的行为 ⇒「太大」会被伪装成「半个 JSON」")
	}
}

// TestLimitPropagatesUnderlyingError 钉住「源自己的错误」不被吞掉。
func TestLimitPropagatesUnderlyingError(t *testing.T) {
	sentinel := errors.New("boom")
	r := Limit(&s952ErrReader{err: sentinel}, 100)
	_, err := r.Read(make([]byte, 16))
	if !errors.Is(err, sentinel) {
		t.Errorf("Limit(errReader).Read 的错误 = %v（期望 %v）\n"+
			"⇒ 源的真实错误必须透出去 ⇒ 否则「读失败」被伪装成「空」", err, sentinel)
	}
}

// TestSnippetTruncatesSilently 钉住 `Snippet` 的**有意**行为。
func TestSnippetTruncatesSilently(t *testing.T) {
	got := Snippet(s952Repeat(100), 10)
	if len(got) != 10 {
		t.Errorf("Snippet(100 字节, limit=10) 长度 = %d（期望 10）\n"+
			"⇒ 错误文案本来就是截断的 ⇒ 超限静默截断是有意的", len(got))
	}
	// 超限时**不许**把整段读进来（那是本包要治的病）。
	if got := Snippet(s952Repeat(1<<20), 10); len(got) != 10 {
		t.Errorf("Snippet(1 MiB, limit=10) 长度 = %d（期望 10）", len(got))
	}
}

// TestSnippetReturnsPartialOnError 钉住「读失败时保留已读到的部分」。
//
// ⚠ 这与修复前 `body, _ := io.ReadAll(resp.Body)` 的行为**逐字一致**
// ⇒⇒⇒ ⇒ 换用它不改既有语义（§945 的纪律：不要顺手改别的语义）。
func TestSnippetReturnsPartialOnError(t *testing.T) {
	got := Snippet(&s952ErrReader{prefix: "abc", err: errors.New("boom")}, 100)
	if got != "abc" {
		t.Errorf("Snippet(出错 reader) = %q（期望 %q）", got, "abc")
	}
}

// TestNilAndZeroLimitsAreSafe 钉住「零值/空值不许 panic」。
func TestNilAndZeroLimitsAreSafe(t *testing.T) {
	if got := Snippet(nil, 10); got != "" {
		t.Errorf("Snippet(nil, 10) = %q（期望空串）", got)
	}
	if got := Snippet(s952Repeat(10), 0); got != "" {
		t.Errorf("Snippet(10 字节, limit=0) = %q（期望空串）", got)
	}
	if _, err := All(s952Repeat(10), 0); !errors.Is(err, ErrTooLarge) {
		t.Errorf("All(10 字节, limit=0) 的错误 = %v（期望 ErrTooLarge）\n"+
			"⇒ 上限为 0 必须**拒绝**而不是「读 0 字节成功」", err)
	}
}

// s952ReadToEOF 一直读到出错为止，返回是否以 io.EOF 收尾。
func s952ReadToEOF(r io.Reader) bool {
	buf := make([]byte, 64)
	for {
		n, err := r.Read(buf)
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if n == 0 {
			return false
		}
	}
}

// s952ErrReader 先吐出 prefix，再返回 err。
type s952ErrReader struct {
	prefix string
	err    error
}

// ⚠ 必须用**指针**接收者：值接收者改的是副本 ⇒ prefix 永远不被消费
// ⇒ 读起来像无限重复（这个坑在写测试时当场踩到过一次）。
func (r *s952ErrReader) Read(p []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	return 0, r.err
}
