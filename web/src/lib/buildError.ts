import type { DeployError } from './types'

// The line of a failed deploy's log that says what went wrong, shown on top of
// the failure. The server already puts it in the message of a build failure
// ("The build failed: <line>", see BuildFailureSummary in internal/deploy);
// this finds it in the log tail for deployments that only say "The build
// failed." or name Docker's wrapper ("... did not complete successfully: exit
// code: 1"), and for apps that crashed on start.

const STEP_PREFIX = /^#\d+ (?:\d+\.\d+ )?/
const STEP = /^#(\d+) /
const STEP_HEADER = /^\[[^\]]*\d+\/\d+\] /
const ERROR_WORD =
  /(error\b|err!|\bfailed\b|\bfatal\b|\bpanic\b|\bcannot\b|\bcan't\b|\bcould not\b|\bunable to\b|\bnot found\b|\bnot exported\b|\bno such file\b|\bundefined\b|permission denied)/i
const NOISE =
  /^(at |-{3,}$|> \[|dockerfile\S*:\d+$|(>>>\s*)?\d+\s*\||\s*\^|(npm )?warn(ing)?\b|debconf:|npm (error|err!)( (code|path|errno|syscall|command|workspace|location|signal|cwd|a complete log|lifecycle script|exit status|failed at)\b|$)|done \d|cached$)/i
const EXIT_CODE = /exit code: \d+$/
// A timestamp and level in front of a runtime log line: "2026-11-06T10:00:05.288Z ERROR ".
const RUNTIME_PREFIX = /^\S*\d{2}:\d{2}:\d{2}\S*\s+/

/** Docker's line about a failed step, which only repeats the command and its exit code. */
export function isBuildWrapper(line: string): boolean {
  return line.includes('did not complete successfully') || line.includes('executor failed running') || EXIT_CODE.test(line.trim())
}

function isNoise(line: string): boolean {
  return isBuildWrapper(line) || NOISE.test(line)
}

function isErrorLine(line: string): boolean {
  return line !== '' && !isNoise(line) && ERROR_WORD.test(line)
}

function short(line: string): string {
  const chars = [...line]
  return chars.length > 300 ? chars.slice(0, 299).join('') + '…' : line
}

/**
 * The first line that names the error, from the step that failed: a compiler
 * message rather than Docker's wrapper; a header like "error during build:"
 * gives way to the line after it. Without one, the last output of the failing
 * step. "" when the log shows neither.
 */
export function firstErrorLine(lines: string[] | undefined): string {
  if (!lines?.length) return ''
  const clean = lines.map((l) => l.trim().replace(STEP_PREFIX, ''))
  let end = lines.length
  let step = ''
  for (let i = 0; i < lines.length; i++) {
    if (isBuildWrapper(lines[i]!)) {
      end = i
      step = STEP.exec(lines[i]!.trim())?.[1] ?? ''
      break
    }
  }
  const inStep = (i: number) => !step || lines[i]!.trim().startsWith(`#${step} `)
  const first = (stepOnly: boolean): string => {
    for (let i = 0; i < end; i++) {
      if ((stepOnly && !inStep(i)) || !isErrorLine(clean[i]!)) continue
      if (clean[i]!.endsWith(':')) {
        for (let j = i + 1; j < end; j++) {
          if ((stepOnly && !inStep(j)) || clean[j] === '') continue
          if (!isNoise(clean[j]!)) return short(clean[j]!)
          break
        }
      }
      return short(clean[i]!)
    }
    return ''
  }
  if (step) {
    const l = first(true)
    if (l) return l
    for (let i = end - 1; i >= 0; i--) {
      if (inStep(i) && clean[i] && !isNoise(clean[i]!) && !STEP_HEADER.test(clean[i]!)) return short(clean[i]!)
    }
  }
  return first(false)
}

// "TypeError: ...", "ModuleNotFoundError: ...", "Error: Cannot find module ...".
const EXCEPTION = /^[A-Z]\w*(Error|Exception)\b/

/**
 * The line of an app's own output that names the error, without its
 * timestamp: the first exception ("TypeError: ..."), else the first line that
 * says error. "" when there is none.
 */
export function firstRuntimeError(lines: string[] | undefined): string {
  const errors = (lines ?? [])
    .map((raw) => raw.trim())
    .filter(isErrorLine)
    .map((l) => l.replace(RUNTIME_PREFIX, '').replace(/^(ERROR|FATAL|PANIC)\s+/, '') || l)
  return short(errors.find((l) => EXCEPTION.test(l)) ?? errors[0] ?? '')
}

/**
 * The line of a deploy command's output (marked [before-deploy] or
 * [after-deploy] in the log) that names the error, else its last line.
 */
export function hookErrorLine(lines: string[] | undefined, which: 'before-deploy' | 'after-deploy'): string {
  const mark = `[${which}] `
  const own = (lines ?? []).map((l) => l.trim()).filter((l) => l.startsWith(mark)).map((l) => l.slice(mark.length).trim()).filter(Boolean)
  return firstRuntimeError(own) || short(own[own.length - 1] ?? '')
}

const BUILD_FAILED = /^([\s\S]*?The build failed)(?:\.|: ([\s\S]+))$/

/**
 * A deploy error split in what happened ("The build failed.") and the line of
 * the log that says why, so the page can show that line on top.
 */
export function deployErrorSummary(error: Pick<DeployError, 'code' | 'message'>, logTail?: string[]): { message: string; line: string } {
  if (error.code === 'build_failed') {
    const m = BUILD_FAILED.exec(error.message)
    if (m) {
      const told = m[2]?.trim() ?? ''
      const line = told && !isBuildWrapper(told) ? told : firstErrorLine(logTail) || told
      return { message: `${m[1]}.`, line }
    }
    return { message: error.message, line: '' }
  }
  if (error.code === 'app_crashed') return { message: error.message, line: firstRuntimeError(logTail) }
  if (error.code === 'before_deploy_failed' || error.code === 'before_deploy_timeout') return { message: error.message, line: hookErrorLine(logTail, 'before-deploy') }
  return { message: error.message, line: '' }
}
