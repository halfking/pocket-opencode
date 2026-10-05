package email

// invoice_human_mark_test.go — 钉住「人工标注不被采集流程覆盖」。
//
// ## 要防的具体事故（2026-10-04 round44 实测）
//
// 授权处置是「保留并标注」：不改 status，只把 last_error 写成
// 「非发票凭证（营销横幅）」/「非发票（信用卡对账单）」。
//
// 但 harvest 每轮处理 `status IN ('new','pending')`，而 markRetry 原本
// `inv.LastError = msg` **整段覆盖**。工行那行恰好是 pending、attempts=2，
// 于是它下一轮采集失败时，人工标注会被一句「发票链接未能取到 PDF 文件」
// 原样抹掉。⇒ **人工判断被例行失败静默覆盖**，比不标还糟。
//
// 横幅那两行是 status=downloaded，不在 harvest 的选择集里，覆盖不到；
// 真正需要这条保护的是**任何 pending 行上的人工标注**。
//
// ## 判据分两层
//
// ① TestComposeHarvestRetryMessage_PreservesHumanMark 打**生产纯函数**本身；
// ② TestMarkRetry_CallsComposeBeforeAssign 打**接线**（markRetry 真的调它、
//    且在赋值之前调）。缺 ① 会漏掉函数本身写错，缺 ② 会漏掉「函数对了但没接线」——
//    这正是本仓反复栽过的「测函数 ≠ 测接线」。
//
// ## 判据的方向
//
// ① 人工标注 + 采集失败 ⇒ 标注**仍在**，且本轮原因也**在**（不是二选一）。
// ② 无人工标注的普通失败 ⇒ 行为与改动前**逐字相同**（不放松任何既有保证）。
// ③ 标注已存在时连续两次失败 ⇒ 不出现两个前缀（否则每轮都叠一个标记）。

import (
	"os"
	"strings"
	"testing"
)

// 这组判据直接调用**生产纯函数** composeHarvestRetryMessage。
//
// 为什么不复刻：第一版判据是复刻一份拼接逻辑，而复刻与生产会漂移——
// 实测把生产的条件改成 `false && strings.HasPrefix(...)`（短路成恒假 = 保护失效）时，
// 复刻版的三条断言全绿，因为它们根本没碰生产代码；
// 文本匹配那条也绿，因为字面文本还在。于是「保护已失效」被读成了通过。
// ⇒ 判据必须打生产函数本身，不能打它的复制品。
func TestComposeHarvestRetryMessage_PreservesHumanMark(t *testing.T) {
	const mark = invoiceHumanMarkPrefix + "非发票（信用卡对账单）"

	t.Run("人工标注在采集失败后仍然保留，且本轮原因也留下", func(t *testing.T) {
		got := composeHarvestRetryMessage(mark, "发票链接未能取到 PDF 文件：http://x")
		if !strings.Contains(got, "非发票（信用卡对账单）") {
			t.Errorf("人工标注被采集失败覆盖了：%q", got)
		}
		if !strings.Contains(got, "发票链接未能取到 PDF 文件") {
			t.Errorf("本轮失败原因被标注挤掉了（两边都该留）：%q", got)
		}
	})

	t.Run("无人工标注时行为与改动前逐字相同", func(t *testing.T) {
		const msg = "no usable pdf/xml found in message"
		if got := composeHarvestRetryMessage("", msg); got != msg {
			t.Errorf("空 last_error 时结果应原样返回 %q，实际 %q", msg, got)
		}
		if got := composeHarvestRetryMessage("发票链接未能取到 PDF 文件：x", msg); got != msg {
			t.Errorf("非标注的旧 last_error 应被原样覆盖为 %q，实际 %q", msg, got)
		}
	})

	t.Run("连续失败不会叠出第二个标记前缀", func(t *testing.T) {
		got := composeHarvestRetryMessage(mark, "第一次失败")
		got = composeHarvestRetryMessage(got, "第二次失败")
		if n := strings.Count(got, invoiceHumanMarkPrefix); n != 1 {
			t.Errorf("两轮失败后标记前缀出现 %d 次（应为 1）：%q", n, got)
		}
	})
}

// TestMarkRetry_CallsComposeBeforeAssign 是接线层判据：
// 纯函数对了不代表 markRetry 真的调它。
//
// 读的是 invoice_harvest.go 而**不是** pipeline.go：markRetry 在 harvest 里。
// 复用 readPipelineSource 会读到另一个文件，判据就会以「源码里找不到」转红——
// 那是个看起来像护栏生效、实际是指错容器的假信号。
func TestMarkRetry_CallsComposeBeforeAssign(t *testing.T) {
	b, err := os.ReadFile("invoice_harvest.go")
	if err != nil {
		t.Fatalf("read invoice_harvest.go: %v", err)
	}
	body := extractFuncBody(t, string(b), "func (h *InvoiceHarvester) markRetry(")
	const call = "composeHarvestRetryMessage("
	idxCall := strings.Index(body, call)
	idxAssign := strings.Index(body, "inv.LastError = msg")
	if idxCall < 0 {
		t.Fatalf("markRetry 没有调用 composeHarvestRetryMessage ⇒ 人工标注保护是死代码。\n函数体：\n%s", body)
	}
	if !strings.Contains(body, "composeHarvestRetryMessage(inv.LastError, msg)") {
		t.Errorf("markRetry 传给纯函数的不是 inv.LastError ⇒ 接错了参数。\n函数体：\n%s", body)
	}
	if idxCall > idxAssign {
		t.Errorf("调用出现在 `inv.LastError = msg` **之后** ⇒ 等于被覆盖。\n函数体：\n%s", body)
	}
}
