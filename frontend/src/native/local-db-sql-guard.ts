const TRANSACTION_CONTROL = new Set(['BEGIN', 'COMMIT', 'END', 'ROLLBACK', 'SAVEPOINT', 'RELEASE'])
const SQL_TOKENS = /--[^\r\n]*|\/\*[\s\S]*?(?:\*\/|$)|'(?:''|[^'])*(?:'|$)|"(?:""|[^"])*(?:"|$)|`(?:``|[^`])*(?:`|$)|\[[^\]]*(?:\]|$)|[a-zA-Z_][\w$]*|[^\s]/g

/**
 * Public calls must finish their own transaction before releasing the connection queue.
 * CREATE TRIGGER bodies are one statement: their terminating sequence is ; END ;,
 * so CASE ... END and semicolons in quoted strings do not end the trigger.
 * Private initialization/migration SQL remains inside the same queued init operation.
 */
export function assertNoTransactionControl(sql: string): void {
  const tokens = sql.match(SQL_TOKENS) ?? []
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

const WRITE_WORDS = new Set(['INSERT', 'UPDATE', 'DELETE', 'REPLACE', 'CREATE', 'DROP', 'ALTER', 'VACUUM', 'ATTACH', 'DETACH', 'REINDEX', 'ANALYZE', 'PRAGMA'])
const INSPECTION_PRAGMAS = new Set(['table_info', 'table_xinfo', 'index_list', 'index_info', 'index_xinfo', 'foreign_key_list', 'foreign_key_check', 'quick_check', 'integrity_check'])
const VALUE_PRAGMAS = new Set(['user_version', 'schema_version', 'data_version', 'foreign_keys', 'database_list', 'compile_options', 'table_list', 'function_list', 'module_list', 'pragma_list', 'collation_list'])

/** query() is not a driver-enforced read boundary; reject writable SQL before dispatch. */
export function assertReadOnlyQuery(sql: string): void {
  const tokens = (sql.match(SQL_TOKENS) ?? []).filter(token => !token.startsWith('--') && !token.startsWith('/*'))
  let statement: string[] = []
  const check = () => {
    if (!statement.length) return
    const words = statement.map(token => token.toUpperCase())
    // Some PRAGMAs act during preparation; EXPLAIN must not exempt them.
    const explained = words[1] === 'QUERY' && words[2] === 'PLAN' ? words[3] : words[1]
    if (words[0] === 'EXPLAIN' && ['SELECT', 'WITH', 'INSERT', 'UPDATE', 'DELETE', 'REPLACE'].includes(explained ?? '')) return
    if ((words[0] === 'SELECT' || words[0] === 'WITH') && !words.some((word, at) =>
      WRITE_WORDS.has(word) && !(word === 'REPLACE' && words[at + 1] === '('))) return
    if (words[0] === 'PRAGMA') {
      const at = statement[2] === '.' ? 3 : 1
      const name = statement[at]?.toLowerCase()
      if (name && ((INSPECTION_PRAGMAS.has(name) && !statement.includes('=')) ||
        (VALUE_PRAGMAS.has(name) && statement.length === at + 1))) return
    }
    throw new Error('LocalDB query 仅接受只读查询，请使用 run/execute/runInTransaction 写入')
  }
  for (const token of tokens) {
    if (token === ';') { check(); statement = [] }
    else statement.push(token)
  }
  check()
}
