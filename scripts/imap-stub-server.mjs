// 极简 IMAP4rev1 服务器（明文 1143），供 pocketd 的 email fetcher 端到端验证。
//
// 为什么需要它：真实邮箱账户拿不到，但用户点名的两个问题
//   - 「邮件详情展示不正常，缺失图片或内容」
//   - 「没有自动归纳整理的能力」
// 都必须在真机上用真实 MIME 结构复现。本服务按 fetcher.go / mime.go
// 实际发出的命令实现，邮件内容专门覆盖易出错的 MIME 形态。
//
// 端口 1143 是 fetcher.go:isPlainIMAPPort 显式放行的明文端口之一，
// 因此不需要自签证书，绕开了 insecureSkipVerify 的生产红线。
import net from 'node:net'
import { buildMails } from './imap-fixture-mails.mjs'

const PORT = Number(process.env.IMAP_PORT || 1143)
const MAILS = buildMails()

const log = (s) => console.log(`[imap] ${s}`)

// IMAP quoted-string：内部 " 与 \ 需转义
const q = (s) => `"${String(s ?? '').replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
const nstr = (s) => (s == null || s === '' ? 'NIL' : q(s))

// go-imap v2 imapclient/fetch.go:readAddress 要求地址是四个字段
//   "(" addr-name SP addr-adl SP addr-mailbox SP addr-host ")"
// 第四个 host 是 RFC 9051(IMAP4rev2) 加的，按 RFC 3501 只写三个字段会在
// 读 mailbox 后卡在 ExpectSP 上，报 "in address: imapwire: expected SP,
// got \")\"" —— 报文看起来完全合法，只有对照解析器源码才能定位。
// 另外 readAddressList 的 ExpectNList 会先吃掉列表的 "("，readAddress 自己
// 再吃一个 "("，所以 address-list 必须是双层括号 ((...))。
const addr = (name, email) => `(${nstr(name)} NIL ${nstr(email)} NIL)`
const addrList = (v) => (v == null ? 'NIL' : `(${addr(v.name, v.email)})`)

// ENVELOPE 字段顺序是 RFC 3501 强制的，顺序错位不会报错、只会让
// 发件人/主题串到别的字段上：
//   date subject from sender reply-to to cc bcc in-reply-to message-id
function envelope(m) {
  const from = { name: m.fromName, email: m.from }
  return [
    `(${nstr(m.date)}`,
    nstr(m.subject),
    addrList(from),
    addrList(from),
    addrList(from),
    addrList({ name: '', email: m.to }),
    'NIL', // cc
    'NIL', // bcc
    'NIL', // in-reply-to
    `${nstr(m.messageId)})`,
  ].join(' ')
}

// quoted-printable 解码。真实 IMAP 服务器返回的 BODY[TEXT] 是**已解码**的
// 文本（RFC 3501 §6.4.8），不会把 "=E5=90=84" 这种转义序列原样丢给客户端。
// 桩不解的话，列表摘要就是一串 =XX，看不出任何内容。
//
// 注意：=XX 序列是**字节**，中文一个字要三个字节。必须先还原成字节缓冲再按
// UTF-8 解码；直接 String.fromCharCode 会把每个字节当成一个码位，中文全变
// "åä½ä½" 这种乱码。
function decodeQP(s) {
  const bytes = []
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (ch === '=') {
      if (s[i + 1] === '\r' && s[i + 2] === '\n') { i += 2; continue } // 软换行
      if (s[i + 1] === '\n') { i += 1; continue }
      const h = s.substr(i + 1, 2)
      if (/^[0-9A-Fa-f]{2}$/.test(h)) { bytes.push(parseInt(h, 16)); i += 2; continue }
    }
    // 非转义字符按 latin1 还原成字节
    for (const b of Buffer.from(ch, 'latin1')) bytes.push(b)
  }
  return new TextDecoder('utf-8').decode(Uint8Array.from(bytes))
}

// 剥掉 HTML 标签
function htmlToText(html) {
  return html
    .replace(/<(script|style)[\s\S]*?<\/\1>/gi, '')
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/(p|div|h[1-6]|li|tr)>/gi, '\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/g, ' ')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

// splitParts 按 boundary 拆 MIME 各部件，返回 [{headers, body}]。
function splitParts(raw) {
  const b = /boundary="?([^";\r\n]+)"?/i.exec(raw.split(/\r?\n\r?\n/)[0] || '')
  if (!b) return null
  const marker = `--${b[1]}`
  const chunks = raw.split(marker)
  const out = []
  for (const c of chunks.slice(1)) {
    if (/^--/.test(c.trim())) break // 结束边界
    const i = c.indexOf('\r\n\r\n')
    const j = c.indexOf('\n\n')
    const cut = i >= 0 ? i : j
    if (cut < 0) continue
    out.push({ headers: c.slice(0, cut), body: c.slice(cut + (i >= 0 ? 4 : 2)) })
  }
  return out
}

// 收集正文候选：递归下钻嵌套 multipart。
// 只处理一层的话，fixture ③（mixed > related > alternative）和任何真实
// 嵌套结构都会整段回落，摘要直接变成 "--OUTER Content-Type: …"。
function collectText(parts, out) {
  for (const p of parts) {
    const nested = /content-type:\s*multipart\//i.test(p.headers)
      ? splitParts(p.body)
      : null
    if (nested && nested.length) { collectText(nested, out); continue }
    if (/content-type:\s*text\/plain/i.test(p.headers)) {
      out.push({ kind: 'plain', part: p })
    } else if (/content-type:\s*text\/html/i.test(p.headers)) {
      out.push({ kind: 'html', part: p })
    }
  }
}

// BODY[TEXT] 的服务端语义：整封邮件的纯文本部分（已解码）。
//
// 之前的实现只处理一层 multipart、单段邮件漏掉结束边界、quoted-printable
// 不解码，会让列表摘要出现邮件头、MIME 边界或一串 =XX。
function textSnippet(raw) {
  const cand = []
  const parts = splitParts(raw)
  if (parts && parts.length) {
    collectText(parts, cand)
  } else {
    const head = raw.split(/\r?\n\r?\n/)[0]
    const i = raw.indexOf('\r\n\r\n')
    const j = raw.indexOf('\n\n')
    const cut = i >= 0 ? i : j
    if (cut >= 0) cand.push({ kind: 'plain', part: { headers: head, body: raw.slice(cut + (i >= 0 ? 4 : 2)) } })
  }
  // 优先级：任意 text/plain > 任意 text/html（真服务器也是这个优先级）
  const pick = cand.find((c) => c.kind === 'plain') || cand.find((c) => c.kind === 'html')
  if (!pick) return ''
  const isQP = /quoted-printable/i.test(pick.part.headers)
  const body = pick.part.body
  let text
  const m = /<body[^>]*>([\s\S]*?)<\/body>/i.exec(body)
  text = m ? htmlToText(m[1]) : body.trim()
  return isQP ? decodeQP(text).trim() : text
}

const server = net.createServer((sock) => {
  let buf = Buffer.alloc(0)
  let selected = false
  const tag = () => `t${Math.floor(performance.now() * 1000) % 100000}`

  const write = (s) => sock.write(s + '\r\n')
  const writeRaw = (b) => sock.write(b)

  write('* OK [CAPABILITY IMAP4rev1 UIDPLUS] pocket-audit IMAP ready')

  sock.on('data', (chunk) => {
    buf = Buffer.concat([buf, chunk])
    let idx
    while ((idx = buf.indexOf('\r\n')) >= 0) {
      const line = buf.subarray(0, idx).toString('utf8')
      buf = buf.subarray(idx + 2)
      if (!line.trim()) continue
      // 单条命令的异常不能带走整个服务器：否则一个格式 bug 会让 stub
      // 进程退出，pocketd 侧只看到连接断开，排查时完全找不到根因。
      const t = line.slice(0, line.indexOf(' '))
      try {
        handle(line.trim())
      } catch (e) {
        log(`!! handler crashed on: ${line.slice(0, 80)} -> ${e && e.message}`)
        write(`${t} NO internal stub error`)
      }
    }
  })
  sock.on('error', () => {})

  function handle(line) {
    const sp = line.indexOf(' ')
    if (sp < 0) return
    const t = line.slice(0, sp)
    const rest = line.slice(sp + 1).trim()
    const [cmdRaw, ...args] = rest.split(' ')
    // go-imap 发的是双词命令（UID SEARCH / UID FETCH），只取首词会落进
    // default 分支回 "OK UID ignored"——客户端收到成功但无 untagged 数据，
    // 于是判定 0 封新邮件且不报错，症状与「没邮件」完全一样。
    let cmd = cmdRaw.toUpperCase()
    if (cmd === 'UID' && args[0]) {
      const sub = args[0].toUpperCase()
      if (sub === 'SEARCH' || sub === 'FETCH' || sub === 'STORE' || sub === 'COPY') {
        cmd = `UID ${sub}`
        args.shift()
      }
    }
    // 全命令日志：排查「同步返回 new:0」这类静默失败时，唯一能还原
    // pocketd 实际发了什么、服务器回了什么的地方。
    log(`< ${t} ${cmd} ${rest.slice(cmd.length).trim().slice(0, 90)}`)

    switch (cmd) {
      case 'CAPABILITY':
        write('* CAPABILITY IMAP4rev1 UIDPLUS LITERAL+')
        write(`${t} OK CAPABILITY completed`)
        break

      case 'LOGIN':
        // 不校验口令：本服务只用于本地审计，且口令经 AES-GCM 加密后落库
        log(`LOGIN ${args[0]}`)
        write(`${t} OK LOGIN completed`)
        break

      case 'ID':
        write('* ID NIL')
        write(`${t} OK ID completed`)
        break

      case 'LIST':
      case 'LSUB':
        write('* LIST (\\HasNoChildren) "/" "INBOX"')
        write(`${t} OK LIST completed`)
        break

      case 'SELECT': {
        selected = true
        write(`* ${MAILS.length} EXISTS`)
        write('* 0 RECENT')
        write('* OK [UIDVALIDITY 1] UIDs valid')
        write(`* OK [UIDNEXT ${MAILS.length + 1}] Predicted next UID`)
        write(`${t} OK [READ-WRITE] SELECT completed`)
        break
      }

      case 'UID SEARCH': {
        if (!selected) { write(`${t} BAD not selected`); break }
        const uids = MAILS.map((_, i) => i + 1)
        write(`* SEARCH ${uids.join(' ')}`)
        write(`${t} OK UID SEARCH completed`)
        break
      }

      case 'UID FETCH': {
        if (!selected) { write(`${t} BAD not selected`); break }
        // 两种实际会被发出的请求：
        //   <set> (UID ENVELOPE INTERNALDATE)            —— Sync 批量取信封
        //   <uid>  (UID BODY.PEEK[TEXT])                 —— Sync 补拉 snippet
        //   <uid>  (UID BODY.PEEK[]<0.N>)                —— harvester 取整封原文
        const set = args[0] || ''
        // BODY.PEEK[TEXT] 里没有 "BODY["，只按 /BODY\[/ 判断会把它误判成
        // 纯信封请求，snippet 永远补不上（列表摘要全空）。
        const bodyItem = (rest.match(/BODY(?:\.PEEK)?\[([^\]]*)\]/i) || [])[1]
        const partial = (rest.match(/<0\.(\d+)>/) || [])[1]
        const seqs = []
        for (const part of set.split(',')) {
          if (part.includes(':')) {
            const [a, b] = part.split(':')
            for (let i = Number(a); i <= Number(b); i++) seqs.push(i)
          } else if (part) seqs.push(Number(part))
        }
        for (const s of seqs) {
          const m = MAILS[s - 1]
          if (!m) continue
          const parts = [`UID ${s}`, `ENVELOPE ${envelope(m)}`, `INTERNALDATE ${q(m.internaldate)}`]
          let payload = null
          let label = ''
          if (bodyItem != null) {
            const upper = bodyItem.toUpperCase()
            if (upper === '' || upper === 'TEXT') {
              label = upper === 'TEXT' ? 'BODY[TEXT]' : 'BODY[]'
              payload = Buffer.from(
                upper === 'TEXT' ? textSnippet(m.raw) : m.raw,
                'utf8',
              )
              if (partial) payload = payload.subarray(0, Number(partial))
            } else {
              // 结构化 section（如 BODY[HEADER]）本审计用不到，
              // 明确回 NO 而不是伪造空字面量：空字面量会被当成功解析，
              // 又变成一条查不出根因的静默路径。
              write(`${t} NO unsupported section ${bodyItem}`)
              return
            }
          }
          if (payload) {
            parts.push(`${label} {${payload.length}}`)
            writeRaw(`* ${s} FETCH (${parts.join(' ')}\r\n`)
            writeRaw(payload)
            writeRaw(`)\r\n`)
          } else {
            write(`* ${s} FETCH (${parts.join(' ')})`)
          }
        }
        write(`${t} OK UID FETCH completed`)
        break
      }

      case 'NOOP':
        write(`${t} OK NOOP completed`)
        break

      case 'LOGOUT':
        write('* BYE logging out')
        write(`${t} OK LOGOUT completed`)
        sock.end()
        break

      default:
        write(`${t} OK ${cmd} ignored`)
    }
  }
})

server.listen(PORT, '127.0.0.1', () => {
  log(`listening on 127.0.0.1:${PORT}，共 ${MAILS.length} 封邮件`)
  MAILS.forEach((m, i) => log(`  UID ${i + 1}  ${m.subject}  [${m.label}]`))
})
