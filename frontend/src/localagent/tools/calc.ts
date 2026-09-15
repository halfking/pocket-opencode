/**
 * localagent/tools/calc.ts — 安全算术表达式求值(无 eval)。
 *
 * 递归下降:支持 + - * / % ^、一元负号、括号、常量 pi/e、常用函数
 * (sqrt/abs/floor/ceil/round/min/max)。字符集白名单,杜绝注入面。
 */

const FUNCS: Record<string, (...args: number[]) => number> = {
  sqrt: Math.sqrt,
  abs: Math.abs,
  floor: Math.floor,
  ceil: Math.ceil,
  round: Math.round,
  min: Math.min,
  max: Math.max,
  pow: Math.pow,
}

export function evaluateExpression(input: string): number {
  const src = input.replace(/\s+/g, '').replace(/×/g, '*').replace(/÷/g, '/').replace(/π/g, 'pi')
  const parser = new Parser(src)
  const val = parser.parseExpression()
  parser.expectEnd()
  return val
}

class Parser {
  private pos = 0
  private readonly src: string

  constructor(src: string) {
    this.src = src
  }

  parseExpression(): number {
    return this.parseAdditive()
  }

  private parseAdditive(): number {
    let left = this.parseMultiplicative()
    for (;;) {
      const op = this.peek()
      if (op === '+' || op === '-') {
        this.pos++
        const right = this.parseMultiplicative()
        left = op === '+' ? left + right : left - right
      } else {
        return left
      }
    }
  }

  private parseMultiplicative(): number {
    let left = this.parsePower()
    for (;;) {
      const op = this.peek()
      if (op === '*' || op === '/' || op === '%') {
        this.pos++
        const right = this.parsePower()
        if ((op === '/' || op === '%') && right === 0) throw new Error('除数为 0')
        left = op === '*' ? left * right : op === '/' ? left / right : left % right
      } else {
        return left
      }
    }
  }

  private parsePower(): number {
    const base = this.parseUnary()
    if (this.peek() === '^') {
      this.pos++
      return Math.pow(base, this.parsePower()) // 右结合
    }
    return base
  }

  private parseUnary(): number {
    if (this.peek() === '-') {
      this.pos++
      return -this.parseUnary()
    }
    if (this.peek() === '+') {
      this.pos++
      return this.parseUnary()
    }
    return this.parsePrimary()
  }

  private parsePrimary(): number {
    const c = this.peek()
    if (c === '(') {
      this.pos++
      const v = this.parseAdditive()
      this.expect(')')
      return v
    }
    if (this.startsWith('pi')) {
      this.pos += 2
      return Math.PI
    }
    if (this.startsWith('e')) {
      // e 只有在不是数字/函数名开头时才是常量;此处单字母直接按常量。
      this.pos += 1
      return Math.E
    }
    const num = this.parseNumber()
    if (num !== null) {
      // 数字后跟标识符 → 函数调用,如 2*sqrt(9) 已被乘法拆开;sqrt(9) 直接走这里。
      return num
    }
    const ident = this.parseIdentifier()
    if (ident) {
      const fn = FUNCS[ident]
      if (!fn) throw new Error(`未知函数:${ident}`)
      this.expect('(')
      const args: number[] = [this.parseAdditive()]
      while (this.peek() === ',') {
        this.pos++
        args.push(this.parseAdditive())
      }
      this.expect(')')
      return fn(...args)
    }
    throw new Error(`无法解析的字符:${c || '(空)'}`)
  }

  private parseNumber(): number | null {
    const m = /^\d+(\.\d+)?([eE][+-]?\d+)?/.exec(this.src.slice(this.pos))
    if (!m) return null
    this.pos += m[0].length
    return parseFloat(m[0])
  }

  private parseIdentifier(): string | null {
    const m = /^[a-zA-Z_][a-zA-Z_]*/.exec(this.src.slice(this.pos))
    if (!m) return null
    this.pos += m[0].length
    return m[0]
  }

  private startsWith(s: string): boolean {
    return this.src.startsWith(s, this.pos)
  }

  private peek(): string {
    return this.src[this.pos] ?? ''
  }

  private expect(ch: string): void {
    if (this.src[this.pos] !== ch) throw new Error(`期望 "${ch}"`)
    this.pos++
  }

  expectEnd(): void {
    if (this.pos < this.src.length) throw new Error(`表达式末尾有多余内容:"${this.src.slice(this.pos, this.pos + 8)}"`)
  }
}
