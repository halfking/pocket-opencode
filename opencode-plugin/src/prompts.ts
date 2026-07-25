/**
 * 会话迁移辅助提示词模板（Phase 3）
 *
 * 4 类模板，按迁移包（SessionResumeBrief + messages + summary）拼接成一段
 * 注入到新会话首条 prompt 的文本，引导新实例：
 *   - env_sync:      检查 git/依赖/工作目录，确保环境一致
 *   - task_resume:   从上次 nextAction 续接，不重头开始
 *   - result_verify: 先验证上次产物（文件存在 + 测试通过），再继续
 *   - acc_report:    阶段完成时向 ACC 上报，驱动后续编排
 *
 * 同一份逻辑在 Pocket 的 internal/migration/prompts 包里有 Go 镜像实现，
 * 用于 Pocket 端预览/编辑提示词后再下发命令。
 */

/** 迁移包的最小结构（与 model.SessionResumeBrief + 附件对齐）。 */
export interface MigrationPack {
  session_meta?: {
    id?: string
    title?: string
    directory?: string
    instance?: string
  }
  resume_brief?: {
    current_state?: string
    last_objective?: string
    decisions?: string[]
    changed_files?: string[]
    blockers?: string[]
    next_action?: string
  }
  summary?: string
  attachments?: Array<{ name?: string; cloudreve_url?: string; type?: string }>
  messages?: Array<{ role?: string; content?: string }>
}

export type PromptTemplate = 'env_sync' | 'task_resume' | 'result_verify' | 'acc_report'

/**
 * 按选中的模板拼接最终的注入提示词。
 * 选中的模板按固定顺序（env → resume → verify → report）拼合，
 * 保证环境检查永远在续接之前。
 */
export function buildMigrationPrompts(
  pack: MigrationPack,
  templates: PromptTemplate[] = ['env_sync', 'task_resume', 'result_verify'],
): string {
  const order: PromptTemplate[] = ['env_sync', 'task_resume', 'result_verify', 'acc_report']
  const selected = order.filter((t) => templates.includes(t))

  const parts: string[] = []
  const title = pack.session_meta?.title || pack.session_meta?.id || '(未知会话)'
  parts.push(`# 任务迁移续接\n来源会话：${title}\n来源实例：${pack.session_meta?.instance || '(未知)'}`)

  for (const t of selected) {
    const block = TEMPLATE_BUILDERS[t](pack)
    if (block) parts.push(block)
  }

  return parts.join('\n\n---\n\n')
}

const TEMPLATE_BUILDERS: Record<PromptTemplate, (p: MigrationPack) => string> = {
  env_sync: (p) => {
    const dir = p.session_meta?.directory || '当前工作目录'
    return [
      '## 环境同步（请在继续任务前完成）',
      `1. 进入工作目录：\`${dir}\``,
      '2. 运行 `git status` 与 `git log --oneline -5`，确认当前分支与上次提交',
      '3. 如来源实例有未推送的提交，先 `git pull --rebase` 同步远端',
      '4. 检查依赖：根据语言（go.mod / package.json / requirements.txt）执行安装',
      '5. 如工作目录与本机路径不一致，做相应重映射后再继续',
    ].join('\n')
  },

  task_resume: (p) => {
    const b = p.resume_brief || {}
    const lines: string[] = ['## 任务续接（从上次进度继续，不要重头开始）']
    if (b.last_objective) lines.push(`- 原始目标：${b.last_objective}`)
    if (b.current_state) lines.push(`- 当前状态：${b.current_state}`)
    if (b.decisions?.length) {
      lines.push('- 已确定的决策：')
      for (const d of b.decisions) lines.push(`  • ${d}`)
    }
    if (b.blockers?.length) {
      lines.push('- 上次阻塞：')
      for (const blk of b.blockers) lines.push(`  • ${blk}`)
    }
    if (b.next_action) {
      lines.push(`- 下一步（请直接从这里接续）：${b.next_action}`)
    } else if (p.summary) {
      lines.push(`- 上次摘要：${p.summary}`)
      lines.push('- 请根据摘要判断下一步并继续。')
    }
    return lines.join('\n')
  },

  result_verify: (p) => {
    const files = p.resume_brief?.changed_files || []
    const atts = (p.attachments || []).filter((a) => a.type === 'file' || a.type === 'diff')
    if (files.length === 0 && atts.length === 0) {
      return '## 成果验证\n（迁移包未提供已改文件清单，跳过验证步骤。）'
    }
    const lines: string[] = ['## 成果验证（继续前先确认上次产物）']
    if (files.length) {
      lines.push('上次已修改/新增的文件，请逐一确认存在且内容完整：')
      for (const f of files) lines.push(`  • \`${f}\``)
    }
    if (atts.length) {
      lines.push('产物文件（如缺失请从链接重新拉取）：')
      for (const a of atts) lines.push(`  • ${a.name || '(文件)'} — ${a.cloudreve_url || '(无URL)'}`)
    }
    lines.push('确认后运行测试套件，全绿再继续；若缺失，先补齐再继续。')
    return lines.join('\n')
  },

  acc_report: (p) => {
    return [
      '## ACC 汇报（阶段完成时执行）',
      '当本阶段任务完成或到达自然检查点时：',
      '1. 调用 `acc_task_complete` 标记任务完成（附简短成果说明）',
      '2. 若属于 Mission，调用 `acc_mission_report` 上报阶段进度',
      '3. 如遇阻塞需人类介入，调用 `acc_request_human_input`',
      `来源任务标识（如适用）：${p.session_meta?.id || '(无)'}`,
    ].join('\n')
  },
}
