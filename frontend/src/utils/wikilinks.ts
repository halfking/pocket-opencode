// WikiLinks Utility Functions
// 解析和渲染 Wiki 风格的双向链接

export interface WikiLink {
  raw: string;
  target: string;
  displayText?: string;
  start: number;
  end: number;
}

const WIKI_LINK_PATTERN = /\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g;

/**
 * 从文本中解析所有 wiki links
 * @param text Markdown 文本
 * @returns 解析出的 wiki links 数组
 */
export function parseWikiLinks(text: string): WikiLink[] {
  const links: WikiLink[] = [];
  let match: RegExpExecArray | null;

  while ((match = WIKI_LINK_PATTERN.exec(text)) !== null) {
    links.push({
      raw: match[0],
      target: match[1].trim(),
      displayText: match[2]?.trim(),
      start: match.index,
      end: match.index + match[0].length,
    });
  }

  return links;
}

/**
 * 渲染 wiki links 为 HTML 链接
 * @param text Markdown 文本
 * @param onLinkClick 点击回调函数
 * @returns 渲染后的 HTML
 */
export function renderWikiLinks(
  text: string,
  onLinkClick?: (target: string) => void
): string {
  return text.replace(WIKI_LINK_PATTERN, (match, target, displayText) => {
    const linkText = displayText || target;
    const clickHandler = onLinkClick
      ? ` onclick="handleWikiLinkClick('${target.replace(/'/g, "\\'")}')"`
      : '';
    return `<a href="#/knowledge/search?q=${encodeURIComponent(target)}" class="wiki-link"${clickHandler}>${linkText}</a>`;
  });
}

/**
 * 提取所有 wiki link 目标
 * @param text Markdown 文本
 * @returns 目标标题数组
 */
export function extractWikiLinkTargets(text: string): string[] {
  const links = parseWikiLinks(text);
  return links.map(link => link.target);
}

/**
 * 检查文本是否包含 wiki links
 * @param text Markdown 文本
 * @returns 是否包含 wiki links
 */
export function hasWikiLinks(text: string): boolean {
  return WIKI_LINK_PATTERN.test(text);
}
