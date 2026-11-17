/**
 * useWikiLinks - Vue Composable for WikiLinks
 * 
 * 提供 WikiLinks 功能的 Vue 3 组合式 API
 */

import { ref, computed, watch } from 'vue'
import {
  parseWikiLinks,
  resolveWikiLinks,
  renderWikiLinksToHTML,
  extractWikiLinkKeywords,
  hasWikiLinks,
  buildNoteMap,
  type WikiLink,
  type NoteMap
} from '@/utils/wikilinks'

export interface UseWikiLinksOptions {
  content: string
  notes?: Array<{ id: string; title: string }>
  autoResolve?: boolean
}

export function useWikiLinks(options: UseWikiLinksOptions) {
  const { content, notes = [], autoResolve = true } = options

  // 状态
  const noteMap = ref<NoteMap>({})
  const wikiLinks = ref<WikiLink[]>([])
  const resolvedLinks = ref<WikiLink[]>([])

  // 构建笔记映射表
  const updateNoteMap = () => {
    noteMap.value = buildNoteMap(notes)
  }

  // 解析 WikiLinks
  const parse = (text: string = content) => {
    wikiLinks.value = parseWikiLinks(text)
    
    if (autoResolve) {
      resolve()
    }
  }

  // 解析并验证链接
  const resolve = () => {
    resolvedLinks.value = resolveWikiLinks(wikiLinks.value, noteMap.value)
  }

  // 渲染为 HTML
  const renderHTML = computed(() => {
    return renderWikiLinksToHTML(content, noteMap.value)
  })

  // 提取关键词
  const keywords = computed(() => {
    return extractWikiLinkKeywords(content)
  })

  // 是否包含 WikiLinks
  const hasLinks = computed(() => {
    return hasWikiLinks(content)
  })

  // 存在的链接数
  const existingLinksCount = computed(() => {
    return resolvedLinks.value.filter(l => l.exists).length
  })

  // 缺失的链接数
  const missingLinksCount = computed(() => {
    return resolvedLinks.value.filter(l => !l.exists).length
  })

  // 获取所有缺失的关键词
  const missingKeywords = computed(() => {
    return resolvedLinks.value
      .filter(l => !l.exists)
      .map(l => l.keyword)
  })

  // 初始化
  updateNoteMap()
  parse()

  // 监听 notes 变化
  watch(() => notes, () => {
    updateNoteMap()
    resolve()
  }, { deep: true })

  // 监听 content 变化
  watch(() => content, (newContent) => {
    parse(newContent)
  })

  return {
    // 状态
    noteMap,
    wikiLinks,
    resolvedLinks,
    
    // 计算属性
    renderHTML,
    keywords,
    hasLinks,
    existingLinksCount,
    missingLinksCount,
    missingKeywords,
    
    // 方法
    parse,
    resolve,
    updateNoteMap
  }
}
