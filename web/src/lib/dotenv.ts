/** What a pasted .env file holds: the variables, and the lines that are not one. */
export interface DotenvResult {
  vars: { name: string; value: string }[]
  skipped: { line: number; text: string }[]
}

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

/**
 * Reads the text of a .env file: NAME=value lines, with `export ` in front or
 * not. Blank lines and # comments are left out. A value in double quotes may
 * span lines and understands \n; single quotes keep the text as it is; an
 * unquoted value ends at " #". A name given twice keeps the last value.
 */
export function parseDotenv(text: string): DotenvResult {
  const lines = text.replace(/\r\n?/g, '\n').split('\n')
  const out = new Map<string, string>()
  const skipped: DotenvResult['skipped'] = []
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i]
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const m = /^(?:export\s+)?([^=\s]+)\s*=\s*(.*)$/.exec(line)
    if (!m || !NAME.test(m[1])) {
      skipped.push({ line: i + 1, text: raw })
      continue
    }
    const name = m[1]
    let value = m[2]
    const q = value[0]
    if (q === '"' || q === "'") {
      let body = value.slice(1)
      let end = body.indexOf(q)
      // A double-quoted value can go on over the next lines until its closing quote.
      while (end === -1 && q === '"' && i + 1 < lines.length) {
        i++
        body += '\n' + lines[i]
        end = body.indexOf(q)
      }
      value = end === -1 ? body : body.slice(0, end)
      if (q === '"') value = value.replace(/\\n/g, '\n').replace(/\\"/g, '"')
    } else {
      value = value.replace(/\s+#.*$/, '').trim()
    }
    out.set(name, value)
  }
  return { vars: [...out].map(([name, value]) => ({ name, value })), skipped }
}
