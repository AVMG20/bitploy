// Colours for log lines: the level (error, warning, info, debug), timestamps,
// Docker build steps (#5 [build 2/6] RUN ...), HTTP methods, status codes and
// durations, URLs and stack trace frames. One regular expression pass per
// line, no parser: logs run to thousands of lines and arrive live.
// Like sqlHighlight.ts it returns tokens, never HTML, and the tokens always add
// up to the line. Pure on purpose (no Vue), so Node can run its test:
// tests/logHighlight.test.ts.

export type LogLevel = 'error' | 'warn' | 'info' | 'debug'

export type LogTokenType =
  | 'text'
  | 'time'
  | LogLevel // the level word itself
  | 'stepnum' // #5 at the start of a BuildKit line
  | 'step' // [build 2/6], Step 2/6 :, [1/3]
  | 'keyword' // the Dockerfile instruction of a build step
  | 'done' // DONE / CACHED of a build step
  | 'method' // GET, POST, ...
  | 'path' // the path after the method
  | 'url'
  | 'status2'
  | 'status3'
  | 'status4'
  | 'status5'
  | 'number' // a duration: 12ms, 0.3s
  | 'key' // key= in key=value logs
  | 'location' // file:line in a stack trace frame

export interface LogToken {
  type: LogTokenType
  text: string
}

export interface LogLineInfo {
  /** The line's level, from the first level word in it; null when it names none. */
  level: LogLevel | null
  /** A frame of a stack trace (at fn (file.js:1:2), File "x.py", line 3, ...). */
  trace: boolean
  tokens: LogToken[]
}

const LEVELS: Record<string, LogLevel> = {
  error: 'error',
  err: 'error',
  'err!': 'error',
  fatal: 'error',
  panic: 'error',
  critical: 'error',
  crit: 'error',
  severe: 'error',
  emerg: 'error',
  emergency: 'error',
  alert: 'error',
  traceback: 'error',
  warn: 'warn',
  warning: 'warn',
  info: 'info',
  notice: 'info',
  log: 'info',
  debug: 'debug',
  dbg: 'debug',
  trace: 'debug',
  verbose: 'debug',
}

const INSTRUCTIONS = /^(?:ADD|ARG|CMD|COPY|ENTRYPOINT|ENV|EXPOSE|FROM|HEALTHCHECK|LABEL|MAINTAINER|ONBUILD|RUN|SHELL|STOPSIGNAL|USER|VOLUME|WORKDIR)$/

// One pass over the line; the first alternative that matches at a spot wins.
const GENERAL = new RegExp(
  [
    String.raw`(?<url>\bhttps?:\/\/[^\s"'<>\x60]*[^\s"'<>\x60.,;:!?)\]}])`,
    String.raw`(?<time>\b\d{4}[-/]\d{2}[-/]\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2}|\sUTC)?)?(?![\w:])|\b\d{2}\/[A-Z][a-z]{2}\/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?|\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b)`,
    String.raw`(?<kv>\b(?:level|lvl|severity|loglevel)"?\s*[=:]\s*"?[A-Za-z]+"?)`,
    String.raw`(?<bracket>\[[A-Za-z]{3,9}\])`,
    String.raw`(?<lead>(?<=^\s*)(?:[Ee]rror|[Ww]arn(?:ing)?|[Ff]atal|[Pp]anic)(?=:)|(?<=\bnpm )(?:ERR!|WARN|error|warn|notice)|^Traceback(?= \(most recent call last\)))`,
    String.raw`(?<word>\b(?:ERROR|ERR|FATAL|PANIC|CRITICAL|CRIT|SEVERE|EMERG|ALERT|WARN|WARNING|INFO|NOTICE|DEBUG|TRACE|VERBOSE|DBG|LOG)\b)`,
    String.raw`(?<exc>\b(?:[A-Za-z_$][\w$]*\.)*(?:[A-Z][\w$]*)?(?:Error|Exception)\b(?=:|$))`,
    String.raw`(?<method>\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b)`,
    String.raw`(?<status>(?<![\w.:/-])[1-5]\d\d(?![\w.:/-]))`,
    String.raw`(?<dur>\b\d+(?:\.\d+)?(?:ms|µs|us|ns|s)\b)`,
    String.raw`(?<key>\b[A-Za-z_][\w.-]*(?==[^=\s]|="))`,
  ].join('|'),
  'g',
)

// A stack trace frame: JavaScript/Java, Python, Go, PHP.
const TRACE = /^\s+at\s|^\s+File ".*", line \d+|^\s*\S+\.go:\d+(?:\s+\+0x[0-9a-f]+)?\s*$|^goroutine \d+ \[|^\s+\.\.\. \d+ more|^\s*#\d+ (?:\/|\{main\})|^\s*Caused by:/
const TRACE_TOKENS = /(?<loc>[^\s()"']+:\d+(?::\d+)?|"[^"]+", line \d+|[^\s()"']+\(\d+\))|(?<exc>\b(?:[A-Za-z_$][\w$]*\.)*(?:[A-Z][\w$]*)?(?:Error|Exception)\b)/g

// Docker build output. BuildKit: "#5 [build 2/6] RUN npm ci", "#5 0.512 output",
// "#5 DONE 1.2s"; the classic builder: "Step 2/6 : RUN npm ci"; bitploy's own
// steps: "[1/3] Fetching the source".
const BUILDKIT = /^(#\d+|\s*=>) /
const STEP_BRACKET = /\[[^\]\n]{1,60}\]/y
const CLASSIC_STEP = /^Step \d+\/\d+ :/
const OWN_STEP = /^\[\d+\/\d+\]/

/** Lines longer than this are only checked for their level: colouring a 100 kB JSON blob helps nobody. */
const LONG = 4000

export function highlightLog(line: string): LogLineInfo {
  const tokens: LogToken[] = []
  const info: LogLineInfo = { level: null, trace: false, tokens }
  const push = (type: LogTokenType, text: string) => {
    if (!text) return
    const last = tokens[tokens.length - 1]
    if (last && last.type === type && type === 'text') last.text += text
    else tokens.push({ type, text })
  }
  const setLevel = (l: LogLevel) => {
    if (!info.level) info.level = l
  }

  if (line.length > LONG) {
    general(line.slice(0, 300), () => {}, setLevel)
    push('text', line)
    return info
  }

  if (TRACE.test(line)) {
    trace(line, push)
    info.trace = true
    return info
  }

  // The start of a build line: the step, then the instruction.
  let i = 0
  let step = false
  const bk = BUILDKIT.exec(line)
  if (bk) {
    push('stepnum', bk[1]!)
    push('text', ' ')
    i = bk[0].length
    STEP_BRACKET.lastIndex = i
    const br = STEP_BRACKET.exec(line)
    if (br) {
      push('step', br[0])
      i += br[0].length
      step = true
    } else {
      // "#5 DONE 1.2s", or the seconds since the step started: "#5 0.512 added 12 packages".
      const state = /(?:DONE|CACHED)\b|\d+\.\d+(?= )/y
      state.lastIndex = i
      const s = state.exec(line)
      if (s) {
        push(/\d/.test(s[0][0]!) ? 'time' : 'done', s[0])
        i += s[0].length
      }
    }
  } else {
    const cs = CLASSIC_STEP.exec(line) ?? OWN_STEP.exec(line)
    if (cs) {
      push('step', cs[0])
      i = cs[0].length
      step = true
    }
  }
  if (step) {
    const ins = /(\s+)([A-Z]+)(?=\s|$)/y
    ins.lastIndex = i
    const m = ins.exec(line)
    if (m && INSTRUCTIONS.test(m[2]!)) {
      push('text', m[1]!)
      push('keyword', m[2]!)
      i += m[0].length
    }
  }
  const rest = line.slice(i)
  if (bk && TRACE.test(rest)) {
    // A stack trace printed by a build step: "#9 3.951     at fn (file.js:1:2)".
    trace(rest, push)
    info.trace = true
    return info
  }
  // A build step's command (RUN --mount=type=cache ...) is not key=value logging.
  general(rest, push, setLevel, !step)
  return info
}

function trace(s: string, push: (type: LogTokenType, text: string) => void) {
  let at = 0
  TRACE_TOKENS.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = TRACE_TOKENS.exec(s))) {
    push('text', s.slice(at, m.index))
    push(m.groups!.loc ? 'location' : 'error', m[0])
    at = m.index + m[0].length
  }
  push('text', s.slice(at))
}

function general(s: string, push: (type: LogTokenType, text: string) => void, setLevel: (l: LogLevel) => void, keys = true) {
  let at = 0
  let sawMethod = false
  let sawStatus = false
  GENERAL.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = GENERAL.exec(s))) {
    const g = m.groups!
    const text = m[0]
    let type: LogTokenType = 'text'
    if (g.url) type = 'url'
    else if (g.time) type = 'time'
    else if (g.kv) {
      // level=warn, "level":"warn": the name like any key, the value as the level.
      const value = /[A-Za-z]+(?="?$)/.exec(text)!
      const level = LEVELS[value[0].toLowerCase()]
      if (level) {
        setLevel(level)
        push('text', s.slice(at, m.index))
        push('key', text.slice(0, value.index))
        push(level, value[0])
        push('text', text.slice(value.index + value[0].length))
        at = m.index + text.length
        continue
      }
    } else if (g.bracket || g.lead || g.word) {
      const level = LEVELS[(g.bracket ? text.slice(1, -1) : text).toLowerCase()]
      if (level) {
        type = level
        setLevel(level)
      }
    } else if (g.exc) {
      type = 'error'
      setLevel('error')
    } else if (g.method) {
      type = 'method'
      sawMethod = true
      // The path right after the method.
      const p = /^ (\/[^\s"]*)/.exec(s.slice(m.index + text.length))
      if (p) {
        push('text', s.slice(at, m.index))
        push('method', text)
        push('text', ' ')
        push('path', p[1]!)
        at = m.index + text.length + p[0].length
        GENERAL.lastIndex = at
        continue
      }
    } else if (g.status) {
      const keyed = /(?:status|code)"?\s*[=:]\s*"?$/i.test(s.slice(Math.max(0, m.index - 16), m.index))
      if (!sawStatus && (sawMethod || keyed)) {
        type = `status${text[0]}` as LogTokenType
        sawStatus = true
      }
    } else if (g.dur) type = 'number'
    else if (g.key && keys) type = 'key'
    push('text', s.slice(at, m.index))
    push(type, text)
    at = m.index + text.length
  }
  push('text', s.slice(at))
}

const cache = new WeakMap<object, LogLineInfo>()

/** highlightLog for a log entry, worked out once per entry (logs re-render on every new batch of lines). */
export function highlightEntry(entry: { text: string }): LogLineInfo {
  let info = cache.get(entry)
  if (!info) {
    info = highlightLog(entry.text)
    cache.set(entry, info)
  }
  return info
}

// Tailwind classes, kept here so the full log, the build progress and the
// panels all colour lines alike. The colours are tokens in style.css.
export const LOG_TOKEN_CLASS: Record<LogTokenType, string> = {
  text: '',
  time: 'text-muted-foreground',
  error: 'font-semibold text-destructive',
  warn: 'font-semibold text-log-warn',
  info: 'text-code-key',
  debug: 'text-muted-foreground',
  stepnum: 'text-muted-foreground',
  step: 'font-medium text-code-key',
  keyword: 'font-semibold text-sql-keyword',
  done: 'text-success',
  method: 'font-medium text-sql-keyword',
  path: 'text-code-key',
  url: 'text-code-key underline decoration-code-key/40 underline-offset-2',
  status2: 'font-medium text-success',
  status3: 'font-medium text-code-key',
  status4: 'font-medium text-log-warn',
  status5: 'font-medium text-destructive',
  number: 'text-sql-number',
  key: 'text-muted-foreground',
  location: 'text-code-key',
}

/** The whole line: errors and warnings get a tinted row, debug lines and stack frames step back. */
export function logRowClass(info: LogLineInfo): string {
  if (info.trace) return 'text-muted-foreground'
  switch (info.level) {
    case 'error':
      return 'bg-destructive/10'
    case 'warn':
      return 'bg-warning/10'
    case 'debug':
      return 'text-muted-foreground'
    default:
      return ''
  }
}
