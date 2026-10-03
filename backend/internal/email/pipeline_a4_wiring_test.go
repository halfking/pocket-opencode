package email

// pipeline_a4_wiring_test.go — A4 阶段的**接线**判据。
//
// ## 为什么要有它（本会话第三次同类）
//
// 1. 第三十七节：A4 的唯一生产调用方是 HTTP 端点，**定时流水线压根没调用它**
//    ——功能「实现了」但没接上，且报告上看不出任何异常。
// 2. 第三十九节：PG 隔离护栏在 `./internal/server`，我一直只跑
//    `./internal/email`，**护栏红了三轮都没发现**。
// 3. 第五节（本轮）：我给流水线加了 `exportPendingA4`，`pipeline_a4_test.go`
//    四条用例**全部直接调那个函数**——于是「函数本身对」被证明，
//    但「它被 Run() 调用了吗」**无人验证**。
//
// 也就是说：前两轮的教训都没落到这一轮的判据上。
// 照仓库既有惯例（`extractFuncBody` 读源码做接线断言）补上。
//
// ## 判的是什么
//
// 1. `Run()` 的函数体里**必须**出现 `exportPendingA4`——否则这个阶段是死代码，
//    开关打开也不会有任何产物，而报告不会报错；
// 2. 调用必须**在 scope 循环内**（与 `sc` 有关）——放到循环外就只会导出
//    最后一个 scope 的票；
// 3. 关闭时不得**产生文件**（这一条已由 pipeline_a4_test.go 的
//    `DisabledByDefaultProducesNothing` 用磁盘事实覆盖，这里不重复）。
//
// 判据读的是**源码**而不是行为。行为层面的接线需要真实 Store 与数据，
// 属于 diag 的职责；这里只保证「调用点存在且位置正确」，
// 两者分工不重叠。
import (
	"strings"
	"testing"
)

func TestPipelineRun_WiresA4ExportStage(t *testing.T) {
	src := readPipelineSource(t)
	body := extractFuncBody(t, src, "func (p *Pipeline) Run(")

	if !strings.Contains(body, "p.exportPendingA4(") {
		t.Fatalf("Run() 的函数体里没有 p.exportPendingA4( —— A4 阶段是死代码。\n"+
			"后果：POCKET_EMAIL_A4_GRID 打开也不会有任何产物，且**报告不会报错**。\n"+
			"这正是第三十七节那个问题的翻版：功能写好了但没接线。\n\nRun() 函数体：\n%s", body)
	}

	// 调用点必须**在 scope 循环内**：用 `sc` 下标传 scope 是标志。
	// 放到循环外就只剩最后一个 scope 的票，其它工作区永远拿不到凭证。
	if !strings.Contains(body, "p.exportPendingA4(ctx, rep, sc[0], sc[1], invoices)") {
		t.Errorf("exportPendingA4 的调用点不是 (ctx, rep, sc[0], sc[1], invoices) —— "+
			"scope 参数若不是 sc 循环的下标，说明调用被移出了循环，"+
			"只有最后一个 scope 的票会被导出。\n\nRun() 函数体：\n%s", body)
	}
}
