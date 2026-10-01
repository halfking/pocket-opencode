//go:build js

package main

// wasm 入口（`//go:build js`）：两条边界路径。
//
// 数据来源是 JS 侧（设备端就是 sql.js 持有 SQLite），所以这里通过
// syscall/js 拿值，模拟真实的「SQLite 查询结果 → wasm」流向。
//
// 两条路径共用 classifyAll，因此
//
//	wasm_bulk  - wasm_struct = 两种传递形态的差
//	wasm_bulk  - native      = bulk 形态的总边界成本
//
// 驱动方是 scripts/wasmprobe-run.mjs，它负责在 Node 里造数据并计时。
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"syscall/js"
	"time"
)

func mustStdout() io.Writer { return os.Stdout }

// emitWasm 用 fmt.Println 一次性把 JSON 打到宿主 stdout。
//
// 为什么不在 `go.run()` 返回后再由 driver 读 globalThis：
// Go/wasm 的 main 返回后 runtime 会结束运行，Node 侧的
// `process.stdout.write(...)` 根本执行不到（实测 stdout 为空）。
// 而 `fmt.Println` 在 wasm 下被 wasm_exec.js 接到 `console.log`，
// 是在运行时尚未结束前就发出去了。**每个形态只发一次** ——
// `json.Encoder` 会分多次 Write，在 wasm 下就变成多行 JSON。
func emitWasm(payload map[string]any) {
	// 刻意**不**往 globalThis 挂这个 map：js.ValueOf 不接受 int64，
	// 设进去会 panic（"ValueOf: invalid value"），而 driver 现在是
	// 劫持 console.log 收集结果，根本不需要它。
	b, err := json.Marshal(payload)
	if err != nil {
		fmt.Println(`{"error":"encode: ` + err.Error() + `"}`)
		return
	}
	fmt.Println(string(b))
}

// warmup 让驱动方先调一次，摊掉 wasm 的一次性初始化开销。
func warmup() {
	if f := js.Global().Get("marshalWarmup"); f.Type() == js.TypeFunction {
		f.Invoke()
	}
}

func main() {
	// 驱动方会设置 globalThis.marshalInput（bulk：JSON 字符串）
	// 与 globalThis.marshalRows（struct：JS 数组）。
	const n = 120

	// --- bulk：一次跨界 ---
	if s := js.Global().Get("marshalInput"); s.Type() == js.TypeString {
		raw := s.String()

		// 边界成本**在 wasm 内部直接测**，不用「wasm 总耗时 − native」倒推。
		// 原因：classifyAll 只做字符串归一化，120 封装不满 1ms，
		// 而 time 计时器在 wasm 下的分辨率下会读到 0 —— 一旦计算耗时落在
		// 分辨率以下，减法就变成「0 − 0」，边界成本被彻底抹掉。
		// 直接测 read/write 是唯一在计算很轻时仍然有效的口径。
		readStart := time.Now()
		var in []mailIn
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			js.Global().Set("marshalError", err.Error())
			return
		}
		readElapsed := time.Since(readStart)

		calcStart := time.Now()
		out := classifyAll(in)
		calcElapsed := time.Since(calcStart)

		writeStart := time.Now()
		b, _ := json.Marshal(out)
		encoded := string(b)
		writeElapsed := time.Since(writeStart)

		js.Global().Set("marshalOutput", encoded)
		emitWasm(map[string]any{
			"label":         "wasm-bulk",
			"elapsed_ns":    readElapsed + calcElapsed + writeElapsed,
			"read_ns":       readElapsed.Nanoseconds(),
			"calc_ns":       calcElapsed.Nanoseconds(),
			"write_ns":      writeElapsed.Nanoseconds(),
			"count":         len(in),
			"input_bytes":   len(raw),
			"result_digest": digestJSON(out),
		})
		return
	}

	// --- permail：每封一次往返（这才是 A 路线的真实模型）---
	//
	// 为什么必须单独测这一形态：AI 分类的 HTTP 请求**不能**在 wasm 里发
	// （js/wasm 没有 fetch，net 也不可用），所以它必须由 JS 侧发起。
	// 于是真实架构变成：
	//
	//   JS(sql.js 取行) → wasm(判是否需要分类) → JS(fetch LLM) → wasm(解析结果)
	//
	// 也就是**每封邮件至少两次跨界**。bulk 形态（一次 JSON 串过边界）
	// 在这个架构里根本不会出现 —— 它假设数据一次性进 wasm、算完一次性出，
	// 可中间要调外部服务时这个假设就破了。
	// 拿 bulk 的数字去论证 A 路线的可行性会**系统性低估**边界成本。
	if arr := js.Global().Get("marshalPerMail"); arr.Type() == js.TypeObject {
		list := js.Global().Get("Array").New(arr)
		l := list.Length()

		// 预热：第一次 json.Marshal/Unmarshal 会触发编码器初始化。
		_ = classifyAll([]mailIn{corpus(1)[0]})

		var readNs, calcNs, writeNs float64
		digests := make([]mailOut, 0, l)
		for i := 0; i < l; i++ {
			one := list.Index(i).String() // JS → wasm 的字符串取值

			t0 := time.Now()
			var m mailIn
			if err := json.Unmarshal([]byte(one), &m); err != nil {
				js.Global().Set("marshalError", err.Error())
				return
			}
			t1 := time.Now()
			res := classifyAll([]mailIn{m})[0]
			t2 := time.Now()
			b, _ := json.Marshal(res)
			js.Global().Set("marshalScratch", string(b)) // wasm → JS 的写回
			t3 := time.Now()

			readNs += float64(t1.Sub(t0).Nanoseconds())
			calcNs += float64(t2.Sub(t1).Nanoseconds())
			writeNs += float64(t3.Sub(t2).Nanoseconds())
			digests = append(digests, res)
		}

		emitWasm(map[string]any{
			"label":         "wasm-permail",
			"elapsed_ns":    int64(readNs + calcNs + writeNs),
			"read_ns":       int64(readNs),
			"calc_ns":       int64(calcNs),
			"write_ns":      int64(writeNs),
			"count":         l,
			"result_digest": digestJSON(digests),
		})
		return
	}

	// --- struct：逐字段跨界 ---
	rows := js.Global().Get("marshalRows")
	if rows.Type() != js.TypeObject {
		js.Global().Set("marshalError", "neither marshalInput (string) nor marshalRows (array) set by host")
		return
	}
	arr := js.Global().Get("Array").New(rows)
	l := arr.Length()
	in := make([]mailIn, 0, l)

	readStart := time.Now()
	for i := 0; i < l; i++ {
		row := arr.Index(i)
		in = append(in, mailIn{
			From:       row.Get("from").String(),
			Subject:    row.Get("subject").String(),
			Snippet:    row.Get("snippet").String(),
			Importance: row.Get("importance").String(),
			Category:   row.Get("category").String(),
		})
	}
	readElapsed := time.Since(readStart)

	calcStart := time.Now()
	out := classifyAll(in)
	calcElapsed := time.Since(calcStart)

	// 写回也是跨界：逐条 push 成 JS 对象。
	writeStart := time.Now()
	res := js.Global().Get("Array").New(len(out))
	for i, o := range out {
		obj := js.Global().Get("Object").New()
		obj.Set("from", o.From)
		obj.Set("category", o.Category)
		obj.Set("importance", o.Importance)
		res.SetIndex(i, obj)
	}
	js.Global().Set("marshalStructOutput", res)
	writeElapsed := time.Since(writeStart)

	emitWasm(map[string]any{
		"label":         "wasm-struct",
		"elapsed_ns":    readElapsed.Nanoseconds() + calcElapsed.Nanoseconds() + writeElapsed.Nanoseconds(),
		"read_ns":       readElapsed.Nanoseconds(),
		"calc_ns":       calcElapsed.Nanoseconds(),
		"write_ns":      writeElapsed.Nanoseconds(),
		"count":         len(in),
		"result_digest": digestJSON(out),
	})
}

func digestJSON(out []mailOut) string {
	b, _ := json.Marshal(digest(out))
	return string(b)
}
