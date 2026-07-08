// Package migration — 4 类会话迁移辅助提示词模板（Go 版）。
//
// 与 opencode-plugin/src/prompts.ts 对齐，保证 Pocket 端预拼和 plugin 端 fallback 一致。
// 4 类：
//   - env_sync:      检查 git/依赖/工作目录
//   - task_resume:   从上次 nextAction 续接
//   - result_verify: 验证上次产物
//   - acc_report:    阶段完成时向 ACC 上报
package migration

import (
	"fmt"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// BuildPrompts 按选中模板拼接最终注入提示词。
// 顺序固定 env → resume → verify → report，保证环境检查永远在续接之前。
func BuildPrompts(brief *model.SessionResumeBrief, templates []string) string {
	if brief == nil {
		brief = &model.SessionResumeBrief{}
	}
	order := []string{"env_sync", "task_resume", "result_verify", "acc_report"}
	selected := make(map[string]bool)
	for _, t := range templates {
		selected[t] = true
	}

	title := brief.Title
	if title == "" {
		title = brief.SessionID
	}
	parts := []string{
		fmt.Sprintf("# 任务迁移续接\n来源会话：%s\n来源实例：%s", title, brief.InstanceID),
	}
	for _, t := range order {
		if !selected[t] {
			continue
		}
		switch t {
		case "env_sync":
			parts = append(parts, buildEnvSync(brief))
		case "task_resume":
			parts = append(parts, buildTaskResume(brief))
		case "result_verify":
			parts = append(parts, buildResultVerify(brief))
		case "acc_report":
			parts = append(parts, buildAccReport(brief))
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func buildEnvSync(b *model.SessionResumeBrief) string {
	// 工作目录由迁移命令的 workingDirectory 字段单独传递给目标实例，此处给通用指引。
	return strings.Join([]string{
		"## 环境同步（请在继续任务前完成）",
		"1. 进入迁移命令指定的工作目录",
		"2. 运行 `git status` 与 `git log --oneline -5`，确认当前分支与上次提交",
		"3. 如来源实例有未推送的提交，先 `git pull --rebase` 同步远端",
		"4. 检查依赖：根据语言（go.mod / package.json / requirements.txt）执行安装",
		"5. 如工作目录与本机路径不一致，做相应重映射后再继续",
	}, "\n")
}

func buildTaskResume(b *model.SessionResumeBrief) string {
	lines := []string{"## 任务续接（从上次进度继续，不要重头开始）"}
	if b.LastObjective != "" {
		lines = append(lines, fmt.Sprintf("- 原始目标：%s", b.LastObjective))
	}
	if b.CurrentState != "" {
		lines = append(lines, fmt.Sprintf("- 当前状态：%s", b.CurrentState))
	}
	if len(b.Decisions) > 0 {
		lines = append(lines, "- 已确定的决策：")
		for _, d := range b.Decisions {
			lines = append(lines, fmt.Sprintf("  • %s", d))
		}
	}
	if len(b.Blockers) > 0 {
		lines = append(lines, "- 上次阻塞：")
		for _, blk := range b.Blockers {
			lines = append(lines, fmt.Sprintf("  • %s", blk))
		}
	}
	if b.NextAction != "" {
		lines = append(lines, fmt.Sprintf("- 下一步（请直接从这里接续）：%s", b.NextAction))
	} else if b.CurrentState != "" {
		lines = append(lines, "- 请根据当前状态判断下一步并继续。")
	}
	return strings.Join(lines, "\n")
}

func buildResultVerify(b *model.SessionResumeBrief) string {
	if len(b.ChangedFiles) == 0 && len(b.Attachments) == 0 {
		return "## 成果验证\n（迁移包未提供已改文件清单，跳过验证步骤。）"
	}
	lines := []string{"## 成果验证（继续前先确认上次产物）"}
	if len(b.ChangedFiles) > 0 {
		lines = append(lines, "上次已修改/新增的文件，请逐一确认存在且内容完整：")
		for _, f := range b.ChangedFiles {
			lines = append(lines, fmt.Sprintf("  • `%s`", f))
		}
	}
	fileAtts := filterFileAtts(b.Attachments)
	if len(fileAtts) > 0 {
		lines = append(lines, "产物文件（如缺失请从链接重新拉取）：")
		for _, a := range fileAtts {
			lines = append(lines, fmt.Sprintf("  • %s — %s", a.Name, fallback(a.CloudReveURL, "(无URL)")))
		}
	}
	lines = append(lines, "确认后运行测试套件，全绿再继续；若缺失，先补齐再继续。")
	return strings.Join(lines, "\n")
}

func buildAccReport(b *model.SessionResumeBrief) string {
	return strings.Join([]string{
		"## ACC 汇报（阶段完成时执行）",
		"当本阶段任务完成或到达自然检查点时：",
		"1. 调用 `acc_task_complete` 标记任务完成（附简短成果说明）",
		"2. 若属于 Mission，调用 `acc_mission_report` 上报阶段进度",
		"3. 如遇阻塞需人类介入，调用 `acc_request_human_input`",
		fmt.Sprintf("来源任务标识（如适用）：%s", fallback(b.SessionID, "(无)")),
	}, "\n")
}

func filterFileAtts(atts []model.AttachmentRef) []model.AttachmentRef {
	var out []model.AttachmentRef
	for _, a := range atts {
		if a.Type == "file" || a.Type == "diff" {
			out = append(out, a)
		}
	}
	return out
}

func fallback(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
