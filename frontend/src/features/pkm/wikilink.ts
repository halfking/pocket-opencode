/**
 * wikilink.ts — TipTap 自定义 inline node：[[双向链接]]。
 *
 * 渲染为 <a data-wikilink data-target="标题">标题</a>，atom=true 作为整体选中。
 * 输入规则：打完 `[[标题]]` 自动转为 wikilink 节点（Roam/Logseq 核心交互）。
 *
 * 存储侧：pkm-store.extractLinks() 用正则扫 data-target 提取链接目标，
 * getBacklinks() 反查 meta_json.links。因此 node 的 HTML 输出格式是契约，
 * 改 data-target 属性名需同步改 extractLinks。
 */
import { InputRule, Node } from '@tiptap/core'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    wikilink: {
      /** 在当前光标插入一个 wikilink。 */
      insertWikilink: (target: string) => ReturnType
    }
  }
}

export interface WikilinkOptions {
  HTMLAttributes: Record<string, string>
}

export const Wikilink = Node.create<WikilinkOptions>({
  name: 'wikilink',

  inline: true,
  group: 'inline',
  atom: true, // 整体选中，光标不进入节点内部

  addAttributes() {
    return {
      target: {
        default: '',
        // 从 HTML 解析时读 data-target
        parseHTML: (el) => (el as HTMLElement).getAttribute('data-target') || '',
        renderHTML: (attrs) => ({ 'data-target': attrs.target as string }),
      },
    }
  },

  parseHTML() {
    return [{ tag: 'a[data-wikilink]' }]
  },

  renderHTML({ node, HTMLAttributes }) {
    const target = (node.attrs.target as string) || ''
    return [
      'a',
      {
        ...this.options.HTMLAttributes,
        'data-wikilink': '',
        'data-target': target,
        class: 'wikilink',
        // href 留空，由前端 router 接管跳转（避免默认 a 行为）
        href: 'javascript:void(0)',
        ...HTMLAttributes,
      },
      target,
    ]
  },

  addCommands() {
    return {
      insertWikilink:
        (target: string) =>
        ({ commands }) => {
          return commands.insertContent({
            type: 'wikilink',
            attrs: { target },
          })
        },
    }
  },

  addInputRules() {
    // 匹配 `[[标题]]`（标题非空，不含换行），转为 wikilink 节点。
    return [
      new InputRule({
        // \[\[ + 非空非]]内容 + \]\] 在行尾/光标处
        find: /\[\[([^\]\n]+)\]\]$/,
        handler: ({ state, range, match }) => {
          const target = match[1]
          const tr = state.tr
          // 删掉刚输入的 [[...]] 文本
          tr.delete(range.from, range.to)
          // 插入 wikilink 节点
          tr.insert(range.from, state.schema.nodes.wikilink.create({ target }))
        },
      }),
    ]
  },
})
