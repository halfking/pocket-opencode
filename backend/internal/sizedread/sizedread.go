// Package sizedread —— §952：**读一个不知道多大的东西，必须自带上界**。
//
// # 这个包存在的理由（§952 的实测依据，不是推理）
//
//	§950 给每个出站 HTTP 请求加了**时间上界**（`http.Client.Timeout`）。
//	⇒⇒⇒ 但**时间上界不是大小上界**：30 秒内一台本机可以送出 30 GiB。
//	⇒⇒⇒ ⇒ 「上游返回一个超大响应体」这条路径，在 §950 之后**仍然能
//	　　　把 pocketd 的内存打穿**。
//
//	★ §950 治的是「等太久」，§952 治的是「读太多」——**两件事**，
//	  需要**两个独立的杠杆**。
//
//	★ 这也直接命中属主的核心诉求：「会议时长要能无限长，只取决于存储大小」
//	  ⇒⇒⇒ ⇒ 一个**没有大小上界**的读 = 会议越长、或上游越坏，
//	 　　 pocketd 的内存占用就越不受控 ⇒⇒⇒ ⇒ 「时长无限长」会变成
//	 　　 「内存也无限长」⇒⇒⇒ ⇒ 必须是两个独立的杠杆，不是一个。
//
// # 为什么是**一个包**而不是「每个调用点自己写 io.LimitReader」
//
//	仓里本来就有 `io.ReadAll(io.LimitReader(resp.Body, N))` 这个正确形态
//	（§952 普查：38 处）⇒⇒⇒ **纪律是有的**，缺的是**统一**：
//
//	  - N 的档位散落在 12 个包里（512 B / 4 KiB / 1 MiB / 8 MiB / 20 MiB…）
//	    ⇒ 新人不知道该抄哪一个；
//	  - 「超限了」在多数调用点是**静默截断**（`body, _ := io.ReadAll(...)`）
//	    ⇒ 「太大」与「格式错」**完全同形**（§941 的老教训）。
//
//	⇒⇒⇒ 所以本包提供**三个**入口，把「有界」和「超限可辨认」绑在一起：
//
//	All(r, max)      —— 严格：整个读进内存；**超限 ⇒ ErrTooLarge**（不返回半个载荷）
//	Snippet(r, max)  —— 尽力：至多读 max 字节转字符串（**错误文案**用；超限静默截断）
//	Limit(r, max)    —— reader：至多读 max 字节；**超限 ⇒ ErrTooLarge**（不是 EOF）
//
// # 为什么 `Limit` 超限时**报错**而不是「假装 EOF」
//
//	`json.NewDecoder(io.LimitReader(body, N))` 这种写法，body 超限时
//	解码器看到的是**半个 JSON** ⇒ 报「unexpected EOF」
//	⇒⇒⇒ 「太大」被伪装成「格式错」⇒ 用户和运维都无从判断。
//	⇒⇒⇒ `Limit` 在超限时返回 `ErrTooLarge` ⇒ 解码器把它**原样带出来**
//	⇒⇒⇒ ⇒ 调用方可以 `errors.Is(err, sizedread.ErrTooLarge)`
//	⇒⇒⇒ ⇒ **「太大」与「格式错」从此可分辨**。
//
// # 档位
//
//	三档是**按载荷种类**分的，不是随手取的数：
//
//	- MaxErrorBodyBytes：只为了拼一句错误文案 ⇒ 8 KiB 足够；
//	- MaxJSONBodyBytes：JSON 成功载荷 ⇒ 8 MiB（仓里已有的 JSON 档是 1–8 MiB）；
//	- MaxBinaryBodyBytes：音频/二进制 ⇒ 32 MiB（仓里入站音频是 25 MiB）。
package sizedread

import (
	"errors"
	"fmt"
	"io"
)

const (
	// MaxErrorBodyBytes 是**错误消息体**的默认档（8 KiB）。
	//
	// ⚠ 为什么这么小：错误文案最终会被塞进 `fmt.Errorf(...)` 再写进日志/回给用户
	// ⇒⇒⇒ 读它不需要「全量」⇒⇒⇒ 读全量只是**白烧内存**。
	MaxErrorBodyBytes int64 = 8 << 10

	// MaxJSONBodyBytes 是 **JSON 成功载荷**的默认档（8 MiB）。
	//
	// ⚠ 与仓内既有档位对齐：`internal/stt` 8 MiB、`internal/accruntime` 8 MiB、
	// `internal/server/companion_client` 8 MiB、`internal/facade` 1 MiB、
	// `internal/llmgateway` 1 MiB ⇒ 取最宽的一档当默认，
	// ⇒⇒⇒ **不制造「本来能过、现在过不了」的回归**。
	MaxJSONBodyBytes int64 = 8 << 20

	// MaxBinaryBodyBytes 是**音频/二进制载荷**的默认档（32 MiB）。
	//
	// ⚠ 与仓内入站音频上限（`maxAudioBodyBytes = 25 MiB`）同量级但更宽一档
	// ⇒⇒⇒ （出站的转换结果可能比入站原件略大）。
	MaxBinaryBodyBytes int64 = 32 << 20
)

// ErrTooLarge 表示「载荷超过了允许的大小上界」。
//
// ⚠ 它必须是**可辨认**的（errors.Is）：「太大」与「格式错」同形是 §941
//
//	的老教训（「同一件事给出两种完全一样的现象」⇒ 运维无从下手）。
var ErrTooLarge = errors.New("payload exceeds size limit")

// limitReader 是一个「至多读 n 字节」的 reader；超出时返回 ErrTooLarge。
type limitReader struct {
	r   io.Reader
	max int64
	n   int64
}

// Limit 返回一个至多读 max 字节的 reader。
//
// ⚠ **超限时返回 ErrTooLarge，而不是伪装成 EOF** —— 这是本包与
//
//	`io.LimitReader` 的**唯一**区别，也正是它存在的理由
//	（`io.LimitReader` 会让「太大」退化成「半个 JSON」⇒ 形态与「格式错」一样）。
//
// ⚠ max <= 0 ⇒ 什么都读不到（第一个字节就会得到 ErrTooLarge）。
func Limit(r io.Reader, max int64) io.Reader {
	if r == nil {
		return &limitReader{r: emptyReader{}, max: max, n: 0}
	}
	if max < 0 {
		max = 0
	}
	return &limitReader{r: r, max: max, n: max}
}

// Read 实现 io.Reader。
func (l *limitReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		// 上限已用尽：**探一个字节**，确认源里是不是真的还有内容。
		//
		// ⚠ 这一步不能省：源恰好等于 max 时必须返回 EOF（**没超限**），
		//   源比 max 多一个字节时必须返回 ErrTooLarge（**超了**）。
		//   ⇒ 只靠「读满了 max 就报错」会把「刚好等于上限」误判成超限。
		var one [1]byte
		n, err := io.ReadFull(l.r, one[:])
		if n > 0 || err == nil {
			return 0, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, l.max)
		}
		return 0, err
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

// All 读整个 reader，最多 max 字节。
//
// ⚠ **超限 ⇒ 返回 ErrTooLarge，且不返回半个载荷**
//
//	（半个载荷会让调用方拿它去 json.Unmarshal ⇒ 报「unexpected EOF」
//	 ⇒ 「太大」又被伪装成「格式错」⇒ §941 的老教训）。
func All(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(Limit(r, max))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Snippet 尽力读至多 max 字节并转成字符串 —— 给**错误文案**用。
//
// ⚠ 超限**静默截断**（这是有意的：错误文案本来就要截断，
//
//	而「错误文案超长」不是需要区分的两种现象）。
//
// ⚠ 读失败时返回**已经读到的部分**（与修复前 `body, _ := io.ReadAll(...)`
//
//	的行为逐字一致 ⇒ 不改既有语义）。
func Snippet(r io.Reader, max int64) string {
	if r == nil || max <= 0 {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(r, max))
	if err != nil && len(b) == 0 {
		return ""
	}
	return string(b)
}

// emptyReader 是一个永远返回 EOF 的 reader。
type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }
