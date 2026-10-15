// Package livetest 给真网关探针提供「本轮做完了没有」的记账能力。
//
// ── 它防的是什么（§476.1 ① 的原始形态）───────────────────────────
//
// 精校探针第一版把 ctx 预算写死 300s，14 条样本只跑完 5 条，
// 而末尾照样打印「小结：修好 3 / 改错 0 / 原样 1」——
//
//	**截断的跑被当成完整结果上报。**
//
// §476 的修法是「给那条探针的用法注释补 `-timeout 2400s`」，那治的是**症状**：
// 仓里 15 个真网关探针只有那一条写了 flag，其余 14 条仍在同一形态下 ——
// 被砍掉时读数长得和完整结果**一模一样**。
//
// ⚠⚠ 为什么不能顺手给 14 条都补 `-timeout`：每条的耗时**本轮没有实测过**。
// 填一个没测过的数字进 14 个文件，就是把 §476 明令拒绝的事换个形式做一遍。
//
// ── 本包给的替代物：不需要任何耗时假设 ──────────────────────────
//
// **完整性对账**：登记「本轮要做 N 项」，每完成一项记一次，结束时**对不上就出声**。
// 它不预测要多久，只检查**做没做完**。
// ⇒ 无论 `-timeout` 是多少、网关多慢、被砍在第几项，
// 「跑了一半」的读数**再也不能**以完整结果的身份出现。
//
// ★ 这是本仓反复出现的同一形态的第四处：
//
//	「表照旧打印、退出码照旧 0，与『没跑』完全同形」⇒ 必须自己出声。
//	（§474.4 漏类别 · §481.3 空档 · §483 目标组 0 条 · 这里）
//
// ── 为什么不依赖 testing.T ───────────────────────────────────────
//
// ① **自检需要**：用 `testing.T` 造一个假实例去验证「它会不会报错」很别扭
// 　 （`Fatalf` 内部 `runtime.Goexit()` 会终止 goroutine，在子测试里无法安全触发）。
// ② **跨包复用**：`internal/stt` 与 `internal/server` 两侧都有真网关探针。
// 　 同一套记账逻辑**必须只有一份** —— `incremental.go` 里那份 Go/TS 手抄实现
// 　 的教训（改一边忘另一边）说明两份副本迟早会漂。
// 　 ⇒ 核心不碰 testing.T，两个包共用这一份。
package livetest

import (
	"fmt"
	"strings"
)

// Ledger 记账「本轮要做几项、实际做了几项」。
type Ledger struct {
	stage      string
	total      int
	done       int
	doneLabels []string
	skipped    []string
}

// New 声明本轮要做 total 项。
//
// ⚠ total <= 0 是**非法**的（`Valid()` 返回 false）：那意味着
// 「一项都不做却打算报读数」—— 与 §483「目标组 0 条照样打印小结」同族。
// 构造函数**不 Fatal**（本包不依赖 testing.T），由调用方决定怎么报。
func New(stage string, total int) *Ledger {
	return &Ledger{stage: stage, total: total}
}

// Valid 报告声明本身是否合法（total > 0）。
func (l *Ledger) Valid() bool { return l.total > 0 }

// Complete 记一项完成。label 用于对不上时的定位。
func (l *Ledger) Complete(label string) {
	l.done++
	l.doneLabels = append(l.doneLabels, label)
}

// Skip 记一项**预期跳过**：本轮就不该做它，而不是做失败了。
//
// ⚠⚠⚠ 判别式 —— 这个方法存在是为了把两种「没做」分开：
//
//	Complete 缺席 = 「这件事存在，但本轮没做成」   ⇒ 失败，要报
//	Skip     缺席 = 「这件事按设计就不存在」       ⇒ 不算失败，但要报
//
// ⛔ 把「转写失败」记成 Skip，读数就从「少跑了两档」变成「按设计跳过了两档」
// ⇒ **比不记更糟**：它把失败洗成了设计。
//
// ⇒ 所以每次 Skip 都必须带一个**先验**的 label（输入不足 / 档位不适用这类
// 「还没跑就知道不成立」的理由），而不能是「试了一下没成」。
// ⚠ 本包**无法**判断 label 是不是先验的 —— 判别式只有调用点知道。
// ⇒ 哪几处出口允许 Skip，由该探针所在包的门**逐处钉住**，
// 　而不是靠记账器自觉（§494 重写那张表的教训：登记的理由必须逐条对源码核）。
func (l *Ledger) Skip(label string) {
	l.skipped = append(l.skipped, label)
}

// Skipped 返回本轮按设计跳过的项数。
//
// ⚠ 与 Done() 分开是有意的：Done 是「跑成了几项」（成功数），
// 　跳过项**不是成功**。两者混进同一个数，读者就分不出
// 「这一项没跑」和「这一项跑成了」——而后者根本不存在。
func (l *Ledger) Skipped() int { return len(l.skipped) }

// Incomplete 报告是否未跑完。
//
// ⚠ skip 计入对账，但**不计入 Done**：不跳过的那些必须真的做完。
func (l *Ledger) Incomplete() bool {
	return l.done+len(l.skipped) != l.total || l.total <= 0
}

// Done / Total 供调用方在读数里如实标注分母。
func (l *Ledger) Done() int  { return l.done }
func (l *Ledger) Total() int { return l.total }

// Report 返回「本轮不完整」或「本轮有按设计跳过」的说明；
// 跑满且零跳过时返回空串。
//
// ⚠⚠ 调用方**必须在打印汇总之前**调用它 ——
// 否则就已经把不完整的分母印出去了，而那正是本包要防的那一形态。
//
// ⚠⚠ 有跳过时**即使跑满也要说话**，且必须**点名**：
// 「3 档只跑了 1 档」与「3 档按设计跳过 2 档」在只印 Done() 时完全同形，
// 而后者是更需要人知道的事实 —— 它意味着那两档**本轮没有读数**。
func (l *Ledger) Report() string {
	if !l.Valid() {
		return fmt.Sprintf("【%s】声明要做 %d 项 ⇒ 一项都不做却要报读数。"+
			"这是 §483「目标组 0 条照样打印小结」的同一种形态。", l.stage, l.total)
	}
	if l.done+len(l.skipped) != l.total {
		// ⚠⚠ 有跳过时必须**点破它**，否则「只完成 2/3」会被读成「完成了 2 项」，
		// 而真实情况是「完成 1 + 跳过 1 + 缺 1」——
		// 「跳过」被算进「完成」这个动作，正是 Skip 最容易被误读的方向。
		skippedNote := ""
		if len(l.skipped) > 0 {
			skippedNote = fmt.Sprintf("（其中按设计跳过 %d 项：%s）",
				len(l.skipped), strings.Join(l.skipped, "、"))
		}
		return fmt.Sprintf("【%s】本轮只完成 %d/%d 项%s —— **下面的读数不是完整结果**：%s",
			l.stage, l.done+len(l.skipped), l.total, skippedNote, l.missing())
	}
	if len(l.skipped) == 0 {
		return ""
	}
	return fmt.Sprintf("【%s】%d/%d 项按设计跳过（这几项**本轮没有读数**）：%s",
		l.stage, len(l.skipped), l.total, strings.Join(l.skipped, "、"))
}

func (l *Ledger) missing() string {
	n := l.total - l.done - len(l.skipped)
	if n <= 0 {
		return "（无缺失）"
	}
	const show = 8
	if n <= show {
		return fmt.Sprintf("共缺 %d 项（探针按序跑，缺的是最后 %d 项）", n, n)
	}
	return fmt.Sprintf("共缺 %d 项（最后 %d 项未跑）", n, n)
}
