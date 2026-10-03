#!/usr/bin/env node
/**
 * commit-staged-only.mjs — 只提交索引里**指定路径**的内容，不碰工作区。
 *
 * ## 为什么不能用 `git commit -- <pathspec>`
 *
 * 我踩过一次，写在这里防止再犯：
 * `git commit -F msg -- path/a path/b` 是**从工作区取这些路径的内容**提交，
 * 绕过索引。直觉上"我已经用 update-index 精确构造好 blob 了"，但那条命令
 * 完全无视索引里的 blob，实际提交的是工作区版本 —— 在本例里就是混着并发
 * 会话 234 行 i18n 改动的整文件。
 *
 * `git commit`（不带 pathspec）才读索引，但它会提交索引里**所有**内容 ——
 * 本仓库索引里常年躺着并发会话的 54 个 staged 改动，一并提交就越权了。
 *
 * ## 做法：临时索引 + commit-tree
 *
 *   1. GIT_INDEX_FILE 指向一个临时索引，`read-tree HEAD` 从 HEAD 初始化
 *      （等于"什么都没有"的干净起点）
 *   2. `update-index --cacheinfo` 把**精确的 blob** 放进去 —— 对已 add 的普通
 *      文件，直接从真实索引里把它的 blob 抄过来
 *   3. `write-tree` + `commit-tree` 直接造 commit 对象
 *   4. `update-ref` 把 main 指过去
 *
 * 全程不写工作区、不写真实索引、不影响并发会话的 staged 状态。
 *
 * 用法：node scripts/commit-staged-only.mjs <commit-msg-file> <path> [<path>...]
 */
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const [msgFile, ...paths] = process.argv.slice(2)
if (!msgFile || paths.length === 0) {
  console.error('usage: node scripts/commit-staged-only.mjs <msg-file> <path>...')
  process.exit(2)
}

const git = (args, opts = {}) =>
  execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, ...opts })

const realIdx = join(mkdtempSync(join(tmpdir(), 'gidx-')), 'index')
const env = { ...process.env, GIT_INDEX_FILE: realIdx }

try {
  // 1. 干净起点
  git(['read-tree', 'HEAD'], { env })

  // 2. 把真实索引里这些路径的 blob 抄进临时索引
  const listed = git(['ls-files', '-s', '--', ...paths])
  const entries = listed.trim().split('\n').filter(Boolean)
  if (entries.length === 0) {
    console.error('这些路径在索引里没有任何条目，确认已 git add 了吗？')
    process.exit(3)
  }
  for (const line of entries) {
    // `ls-files -s` 输出：<mode> SP <hash> SP <stage> TAB <path>
    // 先按第一个 tab 切开：左边是三个空格分隔字段，右边整段都是路径
    // （路径本身可能含空格，不能按空白再切）。
    const tab = line.indexOf('\t')
    if (tab < 0) { console.error(`无法解析 ls-files 行：${line}`); process.exit(5) }
    const [mode, hash, stage] = line.slice(0, tab).trim().split(/\s+/)
    const p = line.slice(tab + 1)
    if (stage !== '0') {
      console.error(`路径 ${p} 处于 stage ${stage}（合并冲突未解决），中止`)
      process.exit(4)
    }
    git(['update-index', '--add', '--cacheinfo', `${mode},${hash},${p}`], { env })
    console.log(`  + ${p}  ${hash.slice(0, 10)}`)
  }

  // 3. 造 commit
  const tree = git(['write-tree'], { env }).trim()
  const parent = git(['rev-parse', 'HEAD'], { env }).trim()
  const commit = git(['commit-tree', tree, '-p', parent, '-F', msgFile], { env }).trim()
  const short = commit.slice(0, 7)

  // 4. 移动分支指针
  git(['update-ref', 'refs/heads/main', commit, parent])
  console.log(`\ncommit ${short}  tree ${tree.slice(0, 10)}  parent ${parent.slice(0, 7)}`)

  // 5. 自检：这次提交里各文件的实际变化行数
  console.log('\n--- 提交内容自检（每文件 +/- 行数）---')
  for (const line of git(['show', '--numstat', '--format=', short]).trim().split('\n').filter(Boolean)) {
    const [add, del, p] = line.split('\t')
    console.log(`  +${add} -${del}  ${p}`)
  }
  console.log('\n提示：提交后真实索引里这些路径仍是旧 blob，git status 会显示它们')
  console.log('为 modified。要恢复「已暂存」状态，执行：git add <同样的路径>')
} finally {
  rmSync(realIdx, { force: true })
}
