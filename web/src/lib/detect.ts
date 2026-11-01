// A preview of what the server will decide for a source (internal/deploy/buildmode.go
// PlanBuild). The server decides for real when it builds; this only tells people
// early. Only erasable TypeScript, so Node can run the file in a test.

import type { BuildMode } from './types'

export type Detected =
  | { kind: 'dockerfile'; label: string }
  | { kind: 'static'; label: string }
  | { kind: 'none'; label: string; message: string; hint: string }

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

/**
 * What a source is built as. `paths` are the files relative to the project root
 * (a wrapping folder already removed). `mode` is "auto" (a Dockerfile, else an
 * index.html), or an override: "static" or "dockerfile".
 */
export function detectSource(paths: string[], mode: BuildMode = 'auto', dockerfilePath = 'Dockerfile'): Detected {
  const files = new Set(paths.map(cleanRel))
  const top = new Set<string>()
  for (const f of files) if (!f.includes('/')) top.add(f.toLowerCase())
  const hasDockerfile = files.has(dockerfilePath)
  const hasIndex = files.has('index.html')

  if (mode === 'static') {
    if (hasIndex) return { kind: 'static', label: 'Static site' }
    const other = otherCase(files, 'index.html')
    if (other) return renamed(other, 'index.html')
    return {
      kind: 'none',
      label: 'No website found',
      message: 'There is no index.html at the top of the folder, so there is no website to show.',
      hint: 'Put index.html in the top folder, next to your other website files.',
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
  const otherIndex = otherCase(files, 'index.html')
  if (otherIndex && lang === '' && top.has('index.html')) return renamed(otherIndex, 'index.html')
  let message = 'No Dockerfile or index.html found.'
  if (hasIndex) message = lang ? `No Dockerfile found. This is a ${lang} project, so it cannot be shown as a plain website.` : 'No Dockerfile found.'
  return { kind: 'none', label: 'Nothing to build', message, hint: NOTHING_HINT }
}
