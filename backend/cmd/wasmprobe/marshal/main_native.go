//go:build !js

package main

// native 入口（`//go:build !js`）：没有边界，用来当基线。
//
// 这里**不测跨界**，测的是「计算本身要多久」。wasm 两条路径的耗时减去这个数，
// 才是边界成本。少了它，wasm 的绝对耗时没有意义 ——
// 因为 A 路线真正关心的是「多付了多少」，不是「一共花多少」。
import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"
)

// emitCorpus 让 native 侧把 corpus 原样吐成 JSON，供 wasm 侧复用同一份数据。
// **两份语料必须由同一段代码生成** —— 手工维护两份会在任何一次修改后悄悄漂移，
// 而漂移的表现是「耗时对不上」这种极难归因的现象。
var emitCorpus = flag.Bool("emit-corpus", false, "print the corpus as JSON and exit")

func mustStdout() io.Writer { return os.Stdout }

func main() {
	flag.Parse()
	if *emitCorpus {
		b, err := json.Marshal(corpus(corpusSize))
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(b)
		return
	}

	// 预热一次：首次调用会触发包初始化与正则编译等一次性开销。
	// 不预热的话第一次跑的耗时会被这些摊进去。
	_ = classifyAll(corpus(corpusSize))

	in := corpus(corpusSize)
	start := time.Now()
	out := classifyAll(in)
	elapsed := time.Since(start)

	report("native", corpusSize, elapsed, in, out)
}
