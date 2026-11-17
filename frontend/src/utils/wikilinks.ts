/**
 * wikilinks.ts - WikiLinks 解析和渲染工具
 * 
 * 功能:
 * 1. 解析 [[keyword]] 语法
 * 2. 转换为可点击的链接
 * 3. 支持笔记标题到 ID 的映射
 * 4. 处理不存在的链接
 */

export interface WikiLink {
  raw: string          // 原始文本，如 "[[Q3 预算]]"
  keyword: string      // 关键词，如 "Q3 预算"
  noteId?: string      // 对应的笔记 ID
  exists: boolean      // 是否存在对应笔记
  startIndex: number   // 在原文中的起始位置
  endIndex: number     // 在原文中的结束位置
}

export interface NoteMap {
  [title: string]: string  // title -> noteId
}

/**
 * 解析文本中的所有 WikiLinks
 */
export function parseWikiLinks(content: string): WikiLink[] {
  const wikiLinkRegex = /\[\[([^\]]+)\]\]/g
  const links: WikiLink[] = []
  let match: RegExpExecArray | null

  while ((match = wikiLinkRegex.exec(content)) !== null) {
    links.push({
      raw: match[0],
      keyword: match[1].trim(),
      startIndex: match.index,
      endIndex: match.index + match[0].length,
      exists: false
    })
  }

  return links
}

/**
 * 检查链接是否存在对应笔记
 */
export function resolveWikiLinks(links: WikiLink[], noteMap: NoteMap): WikiLink[] {
  return links.map(link => {
    const noteId = noteMap[link.keyword.toLowerCase()]
    return {
      ...link,
      noteId,
      exists: !!noteId
    }
  })
}

/**
 * 将 WikiLinks 渲染为 HTML
 */
export function renderWikiLinksToHTML(content: string, noteMap: NoteMap = {}): string {
  const links = parseWikiLinks(content)
  const resolvedLinks = resolveWikiLinks(links, noteMap)

  if (resolvedLinks.length === 0) {
    return content
  }

  let result = content
  for (let i = resolvedLinks.length - 1; i >= 0; i--) {
    const link = resolvedLinks[i]
    const before = result.substring(0, link.startIndex)
    const after = result.substring(link.endIndex)
    
    let replacement: string
    if (link.exists && link.noteId) {
      replacement = `<a href="#/knowledge/notes/${link.noteId}" class="wiki-link wiki-link-exists" data-note-id="${link.noteId}">${escapeHTML(link.keyword)}</a>`
    } else {
      replacement = `<span class="wiki-link wiki-link-missing">${escapeHTML(link.keyword)}<button class="wiki-link-create" title="创建笔记">+</button></span>`
    }
    
    result = before + replacement + after
  }

  return result
}

function escapeHTML(text: string): string {
  const div = document.createElement('div')
  div.textContent = text
  return div.innerHTML
}

export function extractWikiLinkKeywords(content: string): string[] {
  const links = parseWikiLinks(content)
  return links.map(link => link.keyword)
}

export function hasWikiLinks(content: string): boolean {
  return /\[\[([^\]]+)\]\]/.test(content)
}

export function buildNoteMap(notes: Array<{ id: string; title: string }>): NoteMap {
  const map: NoteMap = {}
  for (const note of notes) {
    if (note.title) {
      map[note.title.toLowerCase()] = note.id
    }
  }
  return map
}
