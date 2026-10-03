package email

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// pipeline_a4.go — 每日流水线的 A4 网格导出阶段。
//
// 为什么需要它（2026-10-04 查实）：需求原文要求「每天定时或手工进行邮件接收，
// 然后进行处理…发票类的邮件中发票导出格式：单个 PDF 文件，包含多张发票，
// 按照 A4 纸张规范排版，每页可容纳多张发票（类似 2x2 或 3x3 网格），打印后
// 可直接剪裁，作为凭证附件」。而在这之前 ExportInvoiceGrid 的**唯一生产
// 调用方**是 HTTP 端点 /api/emails/invoices/export
// （server_email_pipeline.go:465）——只有人点按钮时才产出 A4。定时流水线
// 只生成 CSV/MD 汇总，于是「无人值守跑完一天」这个场景下，财务手上**没有**
// 可打印的凭证。
//
// 口径有意选「已下载且 exported_at=0」而不是「本轮采集到的」：
//   - HarvestResult 只有计数没有清单，拿不到「本轮采到哪几张」；
//   - 「未导出」是**持久状态**，跨天可续：今天导过的明天不会重导，没导过的
//     明天一定补上。若按「本轮采集」，采集阶段漏一轮 ⇒ 那几张票永远没有 A4。
//
// 有意**不**做的事：不改发票状态机；不把 A4 推去飞书（A4 是本地打印件，
// 推送是 pusher 的职责）；失败只记 rep.AddError，不让整个流水线失败。
func (p *Pipeline) exportPendingA4(ctx context.Context, rep *PipelineReport, userID, workspaceID string, downloaded []Invoice) {
	if p.A4Grid != 2 && p.A4Grid != 3 {
		rep.A4ExportSkip = fmt.Sprintf("a4 grid export disabled (POCKET_EMAIL_A4_GRID=%d, need 2 or 3)", p.A4Grid)
		return
	}
	files, ids := p.pendingA4Files(downloaded)
	if len(files) == 0 {
		rep.A4ExportSkip = "no unexported invoice file in this scope"
		return
	}
	outDir := filepath.Join(p.DataDir, "email-invoices", "exports", workspaceID)
	res, err := ExportInvoiceGridDetailed(outDir, files, p.A4Grid)
	if err != nil {
		rep.AddError("a4 export user=%s ws=%s: %v", userID, workspaceID, err)
		return
	}
	rep.A4ExportPath = res.Path
	rep.A4ExportCount = res.Count
	rep.A4ExportSkipped = res.Skipped
	// 只给**真正进入网格**的票打时间戳，与 HTTP 端点同一口径
	// （server_email_pipeline.go:477-495）。若连坏文件一起打时间戳，发票页会
	// 显示一张根本没进 A4 的票已导出；而它下轮又会被当成「未导出」重新选中
	// —— 同一张票每天重导一次，且网格里其实没有它。
	skipped := make(map[string]bool, len(res.Skipped))
	for _, n := range res.Skipped {
		skipped[n] = true
	}
	now := time.Now().Unix()
	if p.Store == nil {
		// 没有 Store 就记不了 exported_at ⇒ 下一轮这些票又会「未导出」，
		// 于是同一批票每天被重新拼一次 A4、exports 目录每天多一个文件。
		// 静默跳过这个标记是最糟的形态（报告上看起来一切正常），所以记成错误。
		//
		// 生产路径不会到这里：ensurePipeline 在 emailStore==nil 时直接不构造
		// Pipeline。留这条分支是为了让该阶段能被无库判据完整覆盖。
		rep.AddError("a4 export produced %s but no store: exported_at not recorded, next round would re-export the same %d invoice(s)",
			res.Path, len(files))
		return
	}
	for i, f := range files {
		if i >= len(ids) {
			break
		}
		if skipped[filepath.Base(f)] {
			continue
		}
		if err := p.Store.MarkInvoiceExported(ctx, ids[i], userID, workspaceID, now); err != nil {
			rep.AddError("a4 mark exported invoice=%s: %v", ids[i], err)
			continue
		}
		rep.A4ExportMarked++
	}
}

// pendingA4Files 挑出该 scope 里「有本地文件且从未进入过 A4 网格」的发票。
//
// 存在的文件必须 stat 过：FilePath 是库里的相对路径，而落盘文件可能被外部
// 清理掉；直接 join 后交给 ExportInvoiceGrid 会让它**整批**失败
// （export_pdf.go:83-87 见到缺文件就 return error）。
func (p *Pipeline) pendingA4Files(downloaded []Invoice) (files, ids []string) {
	for _, inv := range downloaded {
		if inv.ExportedAt != 0 || inv.FilePath == "" {
			continue
		}
		abs := filepath.Join(p.DataDir, inv.FilePath)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			continue
		}
		files = append(files, abs)
		ids = append(ids, inv.ID)
	}
	return files, ids
}
