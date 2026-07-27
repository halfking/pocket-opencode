/** Convert the common ENML/XHTML subset used by Evernote exports to Markdown. */
export function enmlToMarkdown(enml: string): string {
  if (!enml) return ''
  const body = enml.replace(/<\/?en-note[^>]*>/gi, '')
  if (typeof DOMParser === 'undefined') {
    return body.replace(/<br\s*\/?>/gi, '\n').replace(/<[^>]+>/g, '').trim()
  }

  const doc = new DOMParser().parseFromString(`<root>${body}</root>`, 'text/html')
  return walk(doc.body.querySelector('root') || doc.body).replace(/\n{3,}/g, '\n\n').trim()
}

function walk(node: Node): string {
  let result = ''
  for (const child of Array.from(node.childNodes)) {
    if (child.nodeType === Node.TEXT_NODE) {
      result += child.textContent || ''
      continue
    }
    if (child.nodeType !== Node.ELEMENT_NODE) continue
    const el = child as Element
    const inner = walk(el)
    switch (el.tagName.toLowerCase()) {
      case 'h1': result += `\n# ${inner.trim()}\n\n`; break
      case 'h2': result += `\n## ${inner.trim()}\n\n`; break
      case 'h3': result += `\n### ${inner.trim()}\n\n`; break
      case 'h4': result += `\n#### ${inner.trim()}\n\n`; break
      case 'h5': result += `\n##### ${inner.trim()}\n\n`; break
      case 'h6': result += `\n###### ${inner.trim()}\n\n`; break
      case 'p': result += `\n${inner.trim()}\n\n`; break
      case 'div': result += `\n${inner}`; break
      case 'br': result += '\n'; break
      case 'hr': result += '\n---\n'; break
      case 'b': case 'strong': result += `**${inner}**`; break
      case 'i': case 'em': result += `*${inner}*`; break
      case 'strike': case 's': case 'del': result += `~~${inner}~~`; break
      case 'code': result += `\`${inner}\``; break
      case 'pre': result += `\n\`\`\`\n${inner.trim()}\n\`\`\`\n`; break
      case 'blockquote': result += `\n> ${inner.trim().replace(/\n/g, '\n> ')}\n`; break
      case 'a': result += `[${inner}](${el.getAttribute('href') || ''})`; break
      case 'img': result += `![${el.getAttribute('alt') || ''}](${el.getAttribute('src') || ''})`; break
      case 'ul': case 'ol': result += `\n${inner}\n`; break
      case 'li': result += `- ${inner.trim()}\n`; break
      case 'en-todo': result += `- [${el.getAttribute('checked') === 'true' ? 'x' : ' '}] ${inner.trim()}\n`; break
      case 'en-media': result += `\n[附件: ${el.getAttribute('hash') || 'resource'}]\n`; break
      default: result += inner
    }
  }
  return result
}
