// A preview of what the server will decide for a source (internal/deploy/buildmode.go
// PlanBuild). The server decides for real when it builds; this only tells people
// early. Only erasable TypeScript, so Node can run the file in a test.

import type { BuildMode } from './types'

export type Detected =
  | { kind: 'dockerfile'; label: string }
  /** home: the page served at "/" when it is not index.html. choices: the pages to pick it from, when there are several. */
  | { kind: 'static'; label: string; home?: string; choices?: string[] }
  | { kind: 'none'; label: string; message: string; hint: string; choices?: string[] }

const NOTHING_HINT = 'Add a Dockerfile, or an index.html for a plain website, at the top of the folder.'

function cleanRel(p: string): string {
  return p.replace(/\\/g, '/').replace(/^(\.\/)+/, '').replace(/^\/+/, '')
}

function language(top: Set<string>): string {
  if (top.has('go.mod')) return 'Go'
  if (top.has('package.json')) return 'Node.js'
  if (top.has('requirements.txt') || top.has('pyproject.toml')) return 'Python'
  if (top.has('composer.json') || top.has('index.php')) return 'PHP'
  return ''
}

// A file whose name differs only in upper and lower case ("Index.html"): the
// server matches names exactly, so it says to rename it.
function otherCase(files: Set<string>, want: string): string {
  for (const f of files) if (f !== want && f.toLowerCase() === want.toLowerCase()) return f
  return ''
}

function renamed(got: string, want: string): Detected {
  return {
    kind: 'none',
    label: `Rename ${got}`,
    message: `Found ${got}, but the name must be exactly ${want}: upper and lower case matter.`,
    hint: `Rename ${got} to ${want}.`,
  }
}

function isPage(name: string): boolean {
  return /\.html?$/i.test(name)
}

/** The .html and .htm files at the top of the project, sorted like the server does. */
export function rootPages(files: Iterable<string>): string[] {
  const out: string[] = []
  for (const f of files) if (!f.includes('/') && isPage(f)) out.push(f)
  return out.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
}

/** A first guess for the home page among several: index.html in any case, then a name that starts with home, start, main or index, else the first by name. */
export function guessHomePage(pages: string[]): string {
  const named = (re: RegExp) => pages.find((p) => re.test(p))
  return (
    named(/^index\.html?$/i) ??
    named(/^(home|start|main|index)\b/i) ??
    [...pages].sort((a, b) => a.localeCompare(b, 'en', { numeric: true, sensitivity: 'base' }))[0] ??
    ''
  )
}

// The server's homePage rule (buildmode.go): the chosen page when the files
// have it, else the only page at the top. null: no page at all.
function homePageOf(files: Set<string>, chosen: string): Detected | null {
  const pages = rootPages(files)
  const choices = pages.length > 1 ? pages : undefined
  if (chosen && !chosen.includes('/') && isPage(chosen) && files.has(chosen)) return { kind: 'static', label: 'Static site', home: chosen, choices }
  if (pages.length === 0) return null
  if (pages.length === 1) return { kind: 'static', label: 'Static site', home: pages[0] }
  const other = otherCase(files, 'index.html')
  if (other && !chosen) return { ...(renamed(other, 'index.html') as Extract<Detected, { kind: 'none' }>), choices }
  return {
    kind: 'none',
    label: 'Choose the home page',
    message: 'There are several pages and no index.html.',
    hint: 'Choose the page people should see first.',
    choices,
  }
}

/**
 * What a source is built as. `paths` are the files relative to the project root
 * (a wrapping folder already removed). `mode` is "auto" (a Dockerfile, else an
 * index.html, else the only page or the chosen `homePage`), or an override:
 * "static" or "dockerfile".
 */
export function detectSource(paths: string[], mode: BuildMode = 'auto', homePage = '', dockerfilePath = 'Dockerfile'): Detected {
  const files = new Set(paths.map(cleanRel))
  const top = new Set<string>()
  for (const f of files) if (!f.includes('/')) top.add(f.toLowerCase())
  const hasDockerfile = files.has(dockerfilePath)
  const hasIndex = files.has('index.html')

  if (mode === 'static') {
    if (hasIndex) return { kind: 'static', label: 'Static site' }
    const home = homePageOf(files, homePage)
    if (home) return home
    return {
      kind: 'none',
      label: 'No website found',
      message: 'There is no .html page at the top of the folder, so there is no website to show.',
      hint: 'Put index.html, or your page, in the top folder next to your other website files.',
    }
  }
  if (mode === 'dockerfile' || hasDockerfile) {
    if (hasDockerfile) return { kind: 'dockerfile', label: 'Dockerfile' }
    return {
      kind: 'none',
      label: 'No Dockerfile found',
      message: 'No Dockerfile found.',
      hint: 'Add a Dockerfile at the top of the folder, or choose Static site if this is a plain website.',
    }
  }
  // auto: the same rule as the server. A project with a package.json and so on
  // is not shown as a plain website, because its index.html is usually unbuilt.
  const lang = language(top)
  if (hasIndex && lang === '') return { kind: 'static', label: 'Static site' }
  const otherDockerfile = otherCase(files, dockerfilePath)
  if (otherDockerfile) return renamed(otherDockerfile, dockerfilePath)
  if (lang === '') {
    const home = homePageOf(files, homePage)
    if (home) return home
  }
  if (lang && rootPages(files).length) {
    return {
      kind: 'none',
      label: 'Needs a build first',
      message: `No Dockerfile found. This is a ${lang} project, so it has to be built before it can be shown.`,
      hint: 'Add a Dockerfile, or upload the built website instead, usually the dist or build folder.',
    }
  }
  return { kind: 'none', label: 'Nothing to build', message: 'No Dockerfile or index.html found.', hint: NOTHING_HINT }
}
