const TRANSACTION_CONTROL = new Set(['BEGIN', 'COMMIT', 'END', 'ROLLBACK', 'SAVEPOINT', 'RELEASE'])

/**
 * Public calls must finish their own transaction before releasing the connection queue.
 * CREATE TRIGGER bodies are one statement: their terminating sequence is ; END ;,
 * so CASE ... END and semicolons in quoted strings do not end the trigger.
 * Private initialization/migration SQL remains inside the same queued init operation.
 */
export function assertNoTransactionControl(sql: string): void {
  const tokens = sql.match(/--[^\r\n]*|\/\*[\s\S]*?(?:\*\/|$)|'(?:''|[^'])*(?:'|$)|"(?:""|[^"])*(?:"|$)|`(?:``|[^`])*(?:`|$)|\[[^\]]*(?:\]|$)|[a-zA-Z_][\w$]*|[^\s]/g) ?? []
  let start = true
  let prefix: string[] = []
  let trigger = false
  let afterSemicolon = false
  let triggerEnd = false
  for (const token of tokens) {
    if (token.startsWith('--') || token.startsWith('/*')) continue
    const word = token.toUpperCase()
    if (start) {
      if (token === ';') continue
      if (TRANSACTION_CONTROL.has(word)) {
        throw new Error('LocalDB 公共 SQL 不支持事务控制语句，请使用 runInTransaction')
      }
      start = false
      prefix = []
      afterSemicolon = false
      triggerEnd = false
    }
    if (!trigger) {
      if (prefix.length < 4) prefix.push(word)
      const head = prefix[0] === 'EXPLAIN' ? prefix.slice(1) : prefix
      trigger = head[0] === 'CREATE' && (head[1] === 'TRIGGER' ||
        ((head[1] === 'TEMP' || head[1] === 'TEMPORARY') && head[2] === 'TRIGGER'))
      if (token === ';') start = true
    } else if (token === ';') {
      if (triggerEnd) { start = true; trigger = false }
      afterSemicolon = true
      triggerEnd = false
    } else {
      triggerEnd = afterSemicolon && word === 'END'
      afterSemicolon = false
    }
  }
}
