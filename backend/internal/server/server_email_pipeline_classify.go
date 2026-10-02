package server

// server_email_pipeline_classify.go — 需求 4 的「定时…然后进行处理」在
// **每日流水线**上的分类环节。
//
// ## 为什么需要这个文件
//
// importance（邮件重要度）是重要提醒的**唯一入口**：生产侧的
// splitReminderCandidates 只看 importance='high'。而 importance 只有两条
// 写入路径——账户规则与 AI 分类。规则路径在真实库里 5 个账户全为 NULL，
// AI 路径在没有 kxmemory 时整步放弃（ClassifySkipReason）。
//
// 于是每日流水线跑完：新邮件的 importance 永远是空 → 需求 4 对新邮件恒
// 0 条提醒。而手动端点 /api/emails/classify 有 LLM 网关兜底，看起来
// 「分类功能是有的」——差异只存在于那条没人走的路径上。
//
// ## 为什么不把 email 包改成直接依赖网关
//
// 分类上游有两个且形状不同（kxmemory.Client 与这里的网关调用）。把网关
// 硬塞进 kxmemory.Client 会造一个谎报形状的接口；而复制一遍
// 「列未分类 → 逐条分类 → 归一化 → 写库 → 汇总失败」的编排，就是两份会
// 各自漂移的代码（两份相同的代码就是两份可以各自漂移的代码）。所以编排
// 抽在 email.ClassifyUnclassifiedWith 里，这里只提供「单封怎么分类」。
//
// ## 已知限制（不是 bug，是上游形状决定的）
//
// 网关的返回体 classifyResultJSON 只有 category/importance/summary，
// **没有 action / action_reason**。所以走这条路分类的邮件，这两个列会是
// 空。kxmemory 路径不受影响（契约里有 action_reason）。不要在报表里把
// 「action_reason 为空」当成这条链路坏了——分不清是网关形状如此，还是
// 分类压根没跑，后者看报告的 ClassifySkip 字段。

import (
	"context"
	"fmt"
	"log"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// classifyViaGatewayBatch 把 LLM 网关兜底包装成 email.EmailClassifier，
// 供每日流水线的第 1.6 步调用。
func (s *Server) classifyViaGatewayBatch(ctx context.Context, userID, workspaceID string, limit int) (int, error) {
	return email.ClassifyUnclassifiedWith(ctx, s.emailStore, userID, workspaceID, limit,
		func(ctx context.Context, it email.ClassifyItem) (email.RawClassifyResult, error) {
			out, err := s.classifyViaGateway(ctx, it, userID, workspaceID)
			if err != nil {
				return email.RawClassifyResult{}, err
			}
			// classifyViaGateway 的失败**不一定**体现为 err：解析失败那条分支
			// 是把原因写进 out.Error 后正常返回的。只判 err 会把一次失败当
			// 成功写库，而「失败却记成已分类」是邮件永远不会被再分类的那种错。
			if out.Error != "" {
				return email.RawClassifyResult{}, fmt.Errorf("llm gateway: %s", out.Error)
			}
			if out.Category == "" {
				// 交给上层 BuildClassifyWrites 去归一化会更啰嗦；这里直接判掉，
				// 换来一条能直接指向「网关返回了不可用内容」的日志。
				return email.RawClassifyResult{}, fmt.Errorf("llm gateway 返回空 category（importance=%q）", out.Importance)
			}
			return email.RawClassifyResult{
				EmailID:    it.ID,
				Category:   out.Category,
				Importance: out.Importance,
				Summary:    out.Summary,
				// Action / Reason 留空：网关返回体没有这两个字段，见文件头说明。
			}, nil
		})
}

// logClassifyWiring 把「定时分类到底开没开」打进启动日志。
//
// 没有这一行的话，「需求 4 对新邮件没有提醒」在定时路径上是**完全无声**的
// ——ClassifySkip 只在报告里，而报告要跑完一整轮才看得到。
func logClassifyWiring(enabled bool) {
	if enabled {
		log.Printf("[email/pipeline] 定时分类已开启（POCKET_EMAIL_CLASSIFY_VIA_GATEWAY=true）：" +
			"第 1.6 步会用 LLM 网关给未归类邮件分类，**会产生真实 LLM 调用与费用**")
		return
	}
	log.Printf("[email/pipeline] 定时分类未开启（默认）：第 1.6 步跳过，" +
		"新邮件 importance 不会被写入 ⇒ 需求 4「对其它重要邮件进行提醒」对新邮件不会有提醒。" +
		"设 POCKET_EMAIL_CLASSIFY_VIA_GATEWAY=true 开启")
}
