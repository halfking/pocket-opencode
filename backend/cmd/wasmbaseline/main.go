// wasmbaseline — 量出「什么都不做」的 wasm 体积下限（2026-10-02）。
//
// ## 为什么需要它
//
// §7cc 报的是 4.85MB（只 import rules），§7cd 报的是 8.78MB
// （同时 import rules + internal/email）。两个数都真实，但**单独看没有意义** ——
// 用户要判断的是「为需求 6 的包体代价」，而这 4~9MB 里有几 MB 是 Go runtime
// 与 stdlib（regexp / encoding/json / time / reflect / crypto…），
// 跟邮件代码本身无关。
//
// 这个包只 `fmt.Println` 一行、不 import 任何邮件代码，编出来的体积就是
// **runtime 底线**。三者相减才能说清「邮件纯逻辑真实值多少字节」。
//
// 它同时给出一条可操作的结论：如果 runtime 底线已经 4MB+，
// 那么「多引几个包」几乎不要钱，成本是一次性的；
// 反之如果底线很小，才说明体积随代码线性增长、必须精打细算。
//
// 用法：
//
//	GOOS=js GOARCH=wasm go build -o baseline.wasm ./cmd/wasmbaseline/
//	node -e "console.log(require('fs').statSync('baseline.wasm').size)"
package main

import "fmt"

func main() {
	fmt.Println("baseline")
}
