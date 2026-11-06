// Small tokenizers for syntax colours in Dockerfiles and compose (YAML) files.
// Like sqlHighlight.ts they return tokens, not HTML: the view renders each one
// as a text node in a span, so nothing in the file can ever become markup. The
// tokens always add up to the input, character for character, which is what
// lets the editor lay them under its textarea.
// Pure on purpose (no Vue), so Node can run its test: tests/codeHighlight.test.ts.

export type CodeLang = 'dockerfile' | 'yaml'

export type CodeTokenType =
  | 'keyword' // a Dockerfile instruction (FROM, RUN), AS; YAML true/false/null and tags
  | 'key' // a YAML key, a --flag, the name in ENV NAME=value
  | 'string'
  | 'number'
  | 'comment'
  | 'variable' // $VAR, ${VAR:-default}
  | 'anchor' // YAML &anchor and *alias
  | 'operator' // && | ; \ and block scalar marks
  | 'punct' // - : , [ ] { } =
  | 'text'

export interface CodeToken {
  type: CodeTokenType
  text: string
}

class Tokens {
  out: CodeToken[] = []
  push(type: CodeTokenType, text: string) {
    if (!text) return
    const last = this.out[this.out.length - 1]
    if (last && last.type === type && (type === 'text' || type === 'string' || type === 'comment')) last.text += text
    else this.out.push({ type, text })
  }
  /** Text of `type` with the $VAR / ${VAR} inside it as variables. `$$` is a literal dollar. */
  withVars(type: CodeTokenType, text: string) {
    VARS.lastIndex = 0
    let at = 0
    let m: RegExpExecArray | null
    while ((m = VARS.exec(text))) {
      if (m[0] === '$$') continue
      this.push(type, text.slice(at, m.index))
      this.push('variable', m[0])
      at = m.index + m[0].length
    }
    this.push(type, text.slice(at))
  }
}

const VARS = /\$\$|\$\{[^}\n]*\}?|\$[A-Za-z_][A-Za-z0-9_]*/g

/** The lines of a text, each with its line break (the last may have none). */
function lines(text: string): string[] {
  const out = text.match(/[^\n]*\n|[^\n]+$/g)
  return out ?? []
}

/** The end of a quoted run starting at `from` (the quote). A backslash escapes the next character; an unterminated run ends at the line's end. */
function endOfQuoted(s: string, from: number, backslash: boolean): number {
  const q = s[from]
  let i = from + 1
  while (i < s.length && s[i] !== '\n') {
    if (backslash && s[i] === '\\') i += 2
    else if (s[i] === q) {
      // YAML: '' inside a single-quoted string is one quote.
      if (!backslash && s[i + 1] === q) i += 2
      else return i + 1
    } else i++
  }
  return Math.min(i, s.length)
}

// ---------------------------------------------------------------- Dockerfile

const INSTRUCTIONS = new Set(
  'ADD ARG CMD COPY ENTRYPOINT ENV EXPOSE FROM HEALTHCHECK LABEL MAINTAINER ONBUILD RUN SHELL STOPSIGNAL USER VOLUME WORKDIR'.split(' '),
)
const DOCKER_WORD = /[^\s"'$\\&|;<>[\],=]+/y
const HEREDOC = /<<(-?)(["']?)([A-Za-z_][A-Za-z0-9_]*)\2/g

export function tokenizeDockerfile(text: string): CodeToken[] {
  const t = new Tokens()
  // A `# escape=`` ` parser directive makes the backtick the line continuation.
  const escape = /^\s*#\s*escape\s*=\s*`/.test(text) ? '`' : '\\'
  let continued = false
  const heredocs: { name: string; strip: boolean }[] = []

  for (const line of lines(text)) {
    const body = line.replace(/\r?\n$/, '')
    const eol = line.slice(body.length)
    if (heredocs.length && !continued) {
      const h = heredocs[0]!
      if ((h.strip ? body.trim() : body) === h.name) {
        heredocs.shift()
        t.push('operator', body)
      } else t.push('string', body)
      t.push('text', eol)
      continue
    }
    if (!body.trim() || /^\s*#/.test(body)) {
      // Also inside a continued RUN: Docker drops comment and empty lines there, and the continuation goes on.
      t.push(body.trim() ? 'comment' : 'text', body)
      t.push('text', eol)
      continue
    }
    dockerLine(t, body, !continued, escape)
    t.push('text', eol)
    continued = body.trimEnd().endsWith(escape)
    HEREDOC.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = HEREDOC.exec(body))) heredocs.push({ name: m[3]!, strip: m[1] === '-' })
  }
  return t.out
}

function dockerLine(t: Tokens, s: string, startsInstruction: boolean, escape: string) {
  let instr = ''
  let word: string | null
  const lead = /^\s*/.exec(s)![0]
  t.push('text', lead)
  let i = lead.length
  if (startsInstruction) {
    // ONBUILD takes another instruction after it.
    for (let n = 0; n < 2; n++) {
      const m = /[A-Za-z]+/y
      m.lastIndex = i
      const w = m.exec(s)?.[0]
      if (!w || !INSTRUCTIONS.has(w.toUpperCase()) || !(i + w.length === s.length || /\s/.test(s[i + w.length]!))) break
      instr = w.toUpperCase()
      t.push('keyword', w)
      i += w.length
      const sp = /\s*/y
      sp.lastIndex = i
      const space = sp.exec(s)![0]
      t.push('text', space)
      i += space.length
      if (instr !== 'ONBUILD') break
    }
  }
  // Flags (--from=build) only right after the instruction, not those of the command it runs.
  let flags = startsInstruction
  const lastEscape = s.trimEnd().endsWith(escape) ? s.trimEnd().length - 1 : -1
  while (i < s.length) {
    const c = s[i]!
    if (i === lastEscape) {
      t.push('operator', c)
      i++
    } else if (/\s/.test(c)) {
      const m = /\s+/y
      m.lastIndex = i
      const w = m.exec(s)![0]
      t.push('text', w)
      i += w.length
    } else if (c === '"' || c === "'") {
      const end = endOfQuoted(s, i, true)
      flags = false
      if (c === '"') t.withVars('string', s.slice(i, end))
      else t.push('string', s.slice(i, end))
      i = end
    } else if (c === '$') {
      VARS.lastIndex = i
      const m = VARS.exec(s)
      if (m && m.index === i && m[0] !== '$$') {
        t.push('variable', m[0])
        i += m[0].length
      } else {
        t.push('text', c)
        i++
      }
    } else if (c === '&' || c === '|' || c === ';' || c === '>' || c === '<') {
      const m = /<<-?["']?[A-Za-z_][A-Za-z0-9_]*["']?|&&|\|\||[&|;<>]/y
      m.lastIndex = i
      const w = m.exec(s)![0]
      t.push('operator', w)
      i += w.length
    } else if (c === '[' || c === ']' || c === ',' || c === '=') {
      t.push('punct', c)
      i++
    } else if (c === '\\') {
      t.push('text', s.slice(i, i + 2))
      i += 2
    } else {
      DOCKER_WORD.lastIndex = i
      word = DOCKER_WORD.exec(s)?.[0] ?? c
      const atWordStart = i === 0 || /[\s[,]/.test(s[i - 1]!)
      if (flags && atWordStart && /^--[A-Za-z]/.test(word)) {
        // The instruction's own flag, like --mount=type=cache,target=/root/.npm: its value is one piece.
        t.push('key', word)
        i += word.length
        if (s[i] === '=') {
          const value = /=\S*/y
          value.lastIndex = i
          const v = value.exec(s)![0]
          t.push('punct', '=')
          t.withVars('text', v.slice(1))
          i += v.length
        }
        continue
      }
      flags = false
      if (atWordStart && s[i + word.length] === '=' && (instr === 'ENV' || instr === 'ARG' || instr === 'LABEL')) t.push('key', word)
      else if (atWordStart && instr === 'FROM' && /^as$/i.test(word)) t.push('keyword', word)
      else if (atWordStart && instr === 'HEALTHCHECK' && /^(CMD|NONE)$/.test(word)) t.push('keyword', word)
      else if (atWordStart && /^\d+(?:\.\d+)?(?:\/(?:tcp|udp))?$/.test(word) && wordEnds(s, i + word.length)) t.push('number', word)
      else t.push('text', word)
      i += word.length
    }
  }
}

function wordEnds(s: string, at: number): boolean {
  return at >= s.length || /[\s\],]/.test(s[at]!)
}

// ---------------------------------------------------------------- YAML (compose files)

const YAML_CONST = /^(?:true|false|yes|no|on|off|null|~)$/i
const YAML_NUMBER = /^[-+]?(?:\d[\d_]*(?:\.\d*)?(?:[eE][-+]?\d+)?|\.\d+|0x[0-9a-fA-F]+|0o[0-7]+|\.inf|\.nan)$/i
// A key: plain or quoted, then a colon followed by a space or the line's end.
const YAML_KEY = /(?:"(?:[^"\\\n]|\\.)*"|'(?:[^'\n]|'')*'|<<|(?:[^\s#'"[\]{}&*!|>%@`,-]|-(?=\S))[^\n]*?)(?=[ \t]*:(?:[ \t]|$))/y

export function tokenizeYaml(text: string): CodeToken[] {
  const t = new Tokens()
  // Inside a block scalar (key: | or key: >): lines indented deeper than this are its text.
  let blockIndent = -1
  for (const line of lines(text)) {
    const body = line.replace(/\r?\n$/, '')
    const eol = line.slice(body.length)
    const indent = /^[ \t]*/.exec(body)![0].length
    if (blockIndent >= 0) {
      if (!body.trim() || indent > blockIndent) {
        t.withVars('string', body)
        t.push('text', eol)
        continue
      }
      blockIndent = -1
    }
    if (yamlLine(t, body, indent)) blockIndent = indent
    t.push('text', eol)
  }
  return t.out
}

/** Colours one line; true when it opens a block scalar. */
function yamlLine(t: Tokens, s: string, indent: number): boolean {
  let i = indent
  t.push('text', s.slice(0, i))
  const doc = /^(?:---|\.\.\.)(?=\s|$)/.exec(s)
  if (doc) {
    t.push('punct', doc[0])
    i = doc[0].length
  }
  // Sequence dashes, also "- - a".
  for (;;) {
    const m = /-(?=[ \t]|$)[ \t]*/y
    m.lastIndex = i
    const d = m.exec(s)
    if (!d) break
    t.push('punct', '-')
    t.push('text', d[0].slice(1))
    i += d[0].length
  }
  if (s[i] === '#') {
    t.push('comment', s.slice(i))
    return false
  }
  YAML_KEY.lastIndex = i
  const key = YAML_KEY.exec(s)
  if (key && key[0]) {
    t.push('key', key[0])
    i += key[0].length
    const m = /[ \t]*:/y
    m.lastIndex = i
    const colon = m.exec(s)![0]
    t.push('text', colon.slice(0, -1))
    t.push('punct', ':')
    i += colon.length
  }
  return yamlValue(t, s, i)
}

function yamlValue(t: Tokens, s: string, i: number): boolean {
  let depth = 0 // inside [ ] or { }
  let atStart = true
  let opensBlock = false
  while (i < s.length) {
    const c = s[i]!
    if (c === ' ' || c === '\t') {
      const m = /[ \t]+/y
      m.lastIndex = i
      const w = m.exec(s)![0]
      t.push('text', w)
      i += w.length
      continue
    }
    if (c === '#' && (i === 0 || /\s/.test(s[i - 1]!))) {
      t.push('comment', s.slice(i))
      break
    }
    if ((c === '&' || c === '*') && /[^\s,[\]{}]/.test(s[i + 1] ?? '')) {
      const m = /[&*][^\s,[\]{}]+/y
      m.lastIndex = i
      const w = m.exec(s)![0]
      t.push('anchor', w)
      i += w.length
      continue
    }
    if (c === '!') {
      const m = /![^\s,[\]{}]*/y
      m.lastIndex = i
      const w = m.exec(s)![0]
      t.push('keyword', w)
      i += w.length
      continue
    }
    if (atStart && depth === 0 && (c === '|' || c === '>')) {
      const m = /[|>][-+0-9]*(?=\s|$)/y
      m.lastIndex = i
      const w = m.exec(s)
      if (w) {
        t.push('operator', w[0])
        i += w[0].length
        opensBlock = true
        continue
      }
    }
    if (c === '"' || c === "'") {
      const end = endOfQuoted(s, i, c === '"')
      t.withVars('string', s.slice(i, end))
      i = end
      atStart = false
      continue
    }
    if (c === '[' || c === '{') depth++
    if ((c === ']' || c === '}') && depth > 0) depth--
    if (c === '[' || c === '{' || c === ']' || c === '}' || (c === ',' && depth > 0)) {
      t.push('punct', c)
      i++
      atStart = false
      continue
    }
    if (depth > 0 && c === ':' && /[\s,\]}]/.test(s[i + 1] ?? ' ')) {
      t.push('punct', c)
      i++
      continue
    }
    // A plain scalar: to a comment, or in [ ] / { } to the next , ] } or ": ".
    const m = depth > 0 ? /(?:[^\s,[\]{}#:]|:(?![\s,\]}])|#(?<!\s#)|[ \t]+(?=[^\s,[\]{}#]))+/y : /(?:[^\s#]|#(?<!\s#)|[ \t]+(?=[^\s#]))+/y
    m.lastIndex = i
    const w = m.exec(s)?.[0] || c
    const isKey = depth > 0 && /^[ \t]*:(?:[\s,\]}]|$)/.test(s.slice(i + w.length))
    if (isKey) t.push('key', w)
    else if (YAML_CONST.test(w)) t.push('keyword', w)
    else if (YAML_NUMBER.test(w)) t.push('number', w)
    else t.withVars('string', w)
    i += w.length
    atStart = false
  }
  return opensBlock
}

export function tokenizeCode(text: string, lang: CodeLang): CodeToken[] {
  return lang === 'yaml' ? tokenizeYaml(text) : tokenizeDockerfile(text)
}

/** A compose file has a top-level services: key; anything else is taken for a Dockerfile. */
export function looksLikeCompose(text: string): boolean {
  return /^services\s*:/m.test(text)
}
