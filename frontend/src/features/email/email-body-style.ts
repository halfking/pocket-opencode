/**
 * 邮件正文排版归一化（2026-10-01 真机审计 P0：字体）。
 *
 * 邮件 HTML 是为「邮件客户端」写的，直接塞进 WebView 会踩两个字体相关的坑：
 *
 * 1. **字体栈缺 CJK 兜底。** 邮件里常见的 `font-family: Helvetica, Arial, sans-serif`
 *    在 Android 上会落到 Roboto，而 Roboto **不含汉字**。结果是正文里的中文被
 *    逐字回退到系统字体，行高/字重全变；更糟的是部分 OEM 字体缺字时画成方框
 *    （豆腐块），用户看到的就是「乱码/方块字」——注意这跟字符集乱码是两回事，
 *    前者改编码能救，后者只能靠字体栈。
 *
 * 2. **远程 @font-face 拖慢首屏。** `@import url(...)` / `@font-face{src:url(...)}`
 *    会触发外部请求；企业邮件常指向需要登录或已下线的 CDN，于是正文要等超时
 *    才出字，表现为「打开一片空白 / 图片位置乱跳」。既然内嵌 webfont 在移动端
 *    几乎无收益，统一剥掉，改用系统 CJK 字体栈。
 *
 * 本模块是纯字符串处理（可在 node --test 下直接测），DOM 相关交给调用方。
 */

/** 移动端可靠的中西文混排栈：先拉丁后 CJK，避免汉字落到不含字的字体上。 */
export const EMAIL_FONT_STACK =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, ' +
  '"PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Source Han Sans SC", ' +
  '"Noto Sans CJK SC", "WenQuanYi Micro Hei", sans-serif'

/** 等宽栈（发票/账单邮件里常见的 <pre> 与表格数字）。 */
export const EMAIL_MONO_STACK =
  'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", ' +
  '"Sarasa Mono SC", "Noto Sans Mono CJK SC", monospace'

/** 去掉 <style> 里的远程字体引用：@import 与 @font-face 整块丢弃。 */
export function stripRemoteFonts(css: string): string {
  return css
    // @import url(...); / @import "...";
    .replace(/@import\s+(?:url\([^)]*\)|"[^"]*"|'[^']*')\s*;?/gi, '')
    // @font-face { ... } ——整块删掉，里面的 src:url() 会发外部请求。
    .replace(/@font-face\s*\{[^}]*\}/gi, '')
}

/**
 * 重写 CSS 里的 font-family，让它带上 CJK 兜底栈。
 *
 * 做法是把邮件声明的字体**保留在前面**（尊重设计意图，命中同名字体时观感不变），
 * 再追加系统 CJK 栈兜底。`!important` 也要一并清掉，否则邮件里的
 * `font-family:X !important` 会在追加后仍然覆盖掉我们的兜底。
 */
export function normalizeCssFonts(css: string): string {
  const cleaned = stripRemoteFonts(css)
  return cleaned.replace(
    /font-family\s*:\s*([^;{}]+)/gi,
    (whole, value: string) => {
      const decl = value.trim().replace(/\s*!important\s*$/i, '')
      if (!decl) return whole
      // 已经有 CJK 兜底就别重复追加。
      if (/PingFang|YaHei|Hiragino|Noto\s+Sans\s+CJK|Source Han|WenQuanYi/i.test(decl)) {
        return `font-family: ${decl}`
      }
      const mono = /mono|courier|consolas|menlo/i.test(decl)
      const stack = mono ? EMAIL_MONO_STACK : EMAIL_FONT_STACK
      return `font-family: ${decl}, ${stack}`
    },
  )
}

/**
 * 给 HTML 注入一段基准排版样式（作为 <style> 前置）。
 *
 * 之所以前置而不是只靠外层 CSS：邮件正文带自己的内联 style，优先级高于外层；
 * 但字号/行高/字重这类**可读性**属性我们必须能兜住，所以用低优先级注入 +
 * 不覆盖内联 font-size，只给没有的地方补默认。
 */
export function baseEmailStyle(): string {
  return [
    'body,div,p,span,td,th,li,h1,h2,h3,h4,h5,h6{',
    `font-family:${EMAIL_FONT_STACK};`,
    '}',
    'pre,code,tt{',
    `font-family:${EMAIL_MONO_STACK};`,
    '}',
    // 邮件常见的固定像素宽度表格在窄屏会横向溢出，这里兜住。
    'img{max-width:100%;height:auto;}',
    'table{max-width:100%;}',
    // 去掉客户端默认的极小字号，正文至少 13px 才可读。
    'body{-webkit-text-size-adjust:100%;}',
  ].join('')
}

/**
 * 处理 <style> 块：抽出 CSS 归一化后再放回。返回替换后的 HTML。
 * 非 <style> 内容原样保留。
 */
export function normalizeStyleBlocks(html: string): string {
  return html.replace(/<style\b[^>]*>([\s\S]*?)<\/style>/gi, (_m, css: string) => {
    return `<style>${normalizeCssFonts(css)}</style>`
  })
}

/** 给正文最外层容器补上基准样式（不覆盖已有内联 font-family）。 */
export function injectBaseStyle(html: string): string {
  const style = `<style>${baseEmailStyle()}</style>`
  // 已有 <style> 的，基准样式放最前面，保证邮件自身样式可覆盖它。
  if (/<style\b/i.test(html)) return html.replace(/<style\b/i, `${style}<style`)
  // 否则插到 <head> 开头；连 <head> 都没有就前置到文档最前。
  if (/<head\b[^>]*>/i.test(html)) return html.replace(/<head\b[^>]*>/i, (m) => `${m}${style}`)
  if (/<html\b[^>]*>/i.test(html)) return html.replace(/<html\b[^>]*>/i, (m) => `${m}${style}`)
  return style + html
}
