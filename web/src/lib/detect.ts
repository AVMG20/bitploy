// A preview of what the server will decide for a source (internal/deploy/buildmode.go
// PlanBuild). The server decides for real when it builds; this only tells people
// early. Only erasable TypeScript, so Node can run the file in a test.

import type { BuildMode } from './types'

export type Detected =
  | { kind: 'dockerfile'; label: string }
  /**
   * home: the page served at "/" when it is not index.html. choices: the pages to pick it from, when there are several.
   * unsure: website files next to a package.json that may be a server app after all.
   */
  | { kind: 'static'; label: string; home?: string; choices?: string[]; unsure?: boolean }
  /**
   * A Go, Python, Django, PHP or Laravel project that bitploy builds from the template of its stack.
   * description: one plain sentence on what happens. unsure: a guess (Python; the server reads the code when you deploy). warn: something to set first.
   */
  | { kind: 'app'; stack: AppStack; label: string; description: string; unsure?: boolean; warn?: string }
  /**
   * A Node project without a Dockerfile that bitploy builds from its own template.
   * type: a Node.js app, or a static build served with nginx; unknown when package.json was not read here (the server looks at it when you deploy).
   * output and spa: for a static build, the folder served and whether unknown paths load index.html.
   * unsure: a guess, check the type. warn: something to fix in the project first.
   */
  | {
      kind: 'template'
      type: 'node' | 'static_build' | 'unknown'
      label: string
      known: boolean
      framework?: string
      output?: string
      spa?: boolean
      build?: boolean
      unsure?: boolean
      warn?: string
    }
  /** assistant: the dashboard adds a line that points to the Connect page. */
  | { kind: 'none'; label: string; message: string; hint: string; choices?: string[]; assistant?: boolean }

const NOTHING_HINT = 'Add a Dockerfile, or an index.html for a plain website, at the top of the folder.'

function cleanRel(p: string): string {
  return p.replace(/\\/g, '/').replace(/^(\.\/)+/, '').replace(/^\/+/, '')
}

export type AppStack = 'go' | 'python' | 'django' | 'php' | 'laravel'
type Stack = AppStack | 'node' | ''

// The order of the server's DetectStack (internal/deploy/dockerfile.go): a
// Laravel or Django project is not a Node project, even with a package.json.
function stackOf(top: Set<string>): Stack {
  if (top.has('go.mod')) return 'go'
  if (top.has('artisan')) return 'laravel'
  if (top.has('manage.py')) return 'django'
  if (top.has('package.json')) return 'node'
  if (top.has('requirements.txt') || top.has('pyproject.toml')) return 'python'
  if (top.has('composer.json') || top.has('index.php')) return 'php'
  return ''
}

function cannot(label: string, message: string, hint: string): Detected {
  return { kind: 'none', label, message, hint, assistant: true }
}

/**
 * The server's stackPlan (internal/deploy/stacks.go) as far as file names tell:
 * the server also reads main.py, manage.py and the .go files when you deploy,
 * so a Python or Django project is shown as a guess.
 */
function appStack(stack: AppStack, files: Set<string>): Detected {
  switch (stack) {
    case 'go': {
      const cmds = [...new Set([...files].map((f) => /^cmd\/([^/]+)\/main\.go$/.exec(f)?.[1]).filter((x): x is string => !!x))].sort()
      const topGo = [...files].some((f) => !f.includes('/') && f.endsWith('.go') && !f.endsWith('_test.go'))
      if (files.has('main.go') || (cmds.length !== 1 && topGo)) {
        return { kind: 'app', stack, label: 'Go app', description: 'Builds it as a Go app from the top folder.' }
      }
      if (cmds.length === 1) return { kind: 'app', stack, label: 'Go app', description: `Builds it as a Go app from cmd/${cmds[0]}.` }
      if (cmds.length > 1) {
        return cannot(
          'Several Go programs',
          `Found a Go project with several programs in cmd (${cmds.join(', ')}), so it is not clear which one is the web app.`,
          'Add a Dockerfile that builds the right one (your AI assistant can write it), or keep only that program in cmd.',
        )
      }
      return cannot(
        'No Go program',
        'Found a Go project, but no program to build (like main.go at the top, or cmd/server/main.go).',
        'Put the main package in main.go at the top or in cmd/<name>/main.go, or add a Dockerfile (your AI assistant can write one).',
      )
    }
    case 'python':
      return {
        kind: 'app',
        stack,
        label: 'Python app',
        description: 'Runs it as a FastAPI or Flask app. bitploy finds the file that starts it when you deploy.',
        unsure: true,
      }
    case 'django':
      if (!files.has('requirements.txt') && !files.has('pyproject.toml')) {
        return cannot(
          'No requirements.txt',
          'Found a Django project, but no requirements.txt that lists its packages.',
          'Add requirements.txt with django and the other packages it needs (pip freeze > requirements.txt), or add a Dockerfile: your AI assistant can write one.',
        )
      }
      return { kind: 'app', stack, label: 'Django app', description: 'Runs it as a Django app with gunicorn. Migrations run on every start.', unsure: true }
    case 'php':
      if (!files.has('index.php')) {
        return cannot('No index.php', 'Found a PHP project, but no index.php at the top to open.', 'Put index.php in the top folder, or add a Dockerfile: your AI assistant can write one.')
      }
      return { kind: 'app', stack, label: 'PHP site', description: 'Serves it with PHP and Apache.' }
    case 'laravel':
      if (!files.has('composer.json')) {
        return cannot(
          'No composer.json',
          'Found a Laravel project, but no composer.json that lists its packages.',
          'Add composer.json and composer.lock from your project, or add a Dockerfile: your AI assistant can write one.',
        )
      }
      return {
        kind: 'app',
        stack,
        label: 'Laravel app',
        description: 'Builds it as a Laravel app and serves it with Apache. Migrations run on every start.',
        warn: "Set APP_KEY in the app's variables before it opens (php artisan key:generate --show makes one).",
      }
  }
}

// A file whose name differs only in upper and lower case ("Index.html"): the
// server matches names exactly, so it says to rename it.
function otherCase(files: Set<string>, want: string): string {
  for (const f of files) if (f !== want && f.toLowerCase() === want.toLowerCase()) return f
  return ''
}

// ---- Node projects: the server's internal/deploy/nodeproject.go ----

const NEXT_CONFIG_NAMES = ['next.config.js', 'next.config.mjs', 'next.config.cjs', 'next.config.ts', 'next.config.mts']
export { NEXT_CONFIG_NAMES }

interface Project {
  scripts: Set<string>
  start: string
  deps: Set<string>
  /** dependencies is not empty: packages the site may run with, not only tools. */
  runtime: boolean
  main: string
  hasIndex: boolean
  nextExport: boolean
}

type Fields = Record<string, unknown>

/** package.json as the server reads it. null: it cannot be read (not JSON, or a field of the wrong type). */
function readProject(packageJson: string, nextConfig: string | null, hasIndex: boolean): Project | null {
  let raw: unknown
  try {
    raw = JSON.parse(packageJson)
  } catch {
    return null
  }
  if (raw === null) raw = {}
  if (typeof raw !== 'object' || Array.isArray(raw)) return null
  const obj = raw as Fields
  const group = (k: string): Fields | null => {
    const v = obj[k]
    if (v === undefined || v === null) return {}
    return typeof v === 'object' && !Array.isArray(v) ? (v as Fields) : null
  }
  const scripts = group('scripts')
  const deps = group('dependencies')
  const devDeps = group('devDependencies')
  if (!scripts || !deps || !devDeps) return null
  if (obj.main !== undefined && obj.main !== null && typeof obj.main !== 'string') return null
  const names = new Set<string>()
  let start = ''
  for (const [k, v] of Object.entries(scripts)) {
    if (typeof v === 'string' && v.trim() !== '') {
      names.add(k)
      if (k === 'start') start = v
    }
  }
  return {
    scripts: names,
    start,
    deps: new Set([...Object.keys(deps), ...Object.keys(devDeps)]),
    runtime: Object.keys(deps).length > 0,
    main: typeof obj.main === 'string' ? obj.main : '',
    hasIndex,
    nextExport: nextConfig !== null && /output\s*:\s*['"`]export['"`]/.test(nextConfig.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '')),
  }
}

type Framework = 'sveltekit' | 'next' | 'remix' | 'react-router' | 'nuxt' | 'server' | 'astro' | 'cra' | 'vite' | ''

const FRAMEWORK_NAME: Record<Framework, string> = {
  sveltekit: 'SvelteKit',
  next: 'Next.js',
  remix: 'Remix',
  'react-router': 'React Router',
  nuxt: 'Nuxt',
  server: 'Node.js server',
  astro: 'Astro',
  cra: 'Create React App',
  vite: 'Vite',
  '': '',
}

function framework(p: Project): Framework {
  const d = p.deps
  if (d.has('@sveltejs/kit')) return 'sveltekit'
  if (d.has('next')) return 'next'
  // @remix-run/router is a dependency of react-router-dom 6, not Remix.
  if ([...d].some((x) => x.startsWith('@remix-run/') && x !== '@remix-run/router')) return 'remix'
  if (d.has('@react-router/dev')) return 'react-router'
  if (d.has('nuxt')) return 'nuxt'
  if (['@nestjs/core', 'express', 'fastify', 'hono', 'koa', '@hapi/hapi'].some((x) => d.has(x))) return 'server'
  if (d.has('astro')) return 'astro'
  if (d.has('react-scripts')) return 'cra'
  if (d.has('vite')) return 'vite'
  return ''
}

/**
 * A start script that only runs Vite's own dev or preview server (vite, vite dev, vite preview --port 3000,
 * npx vite, pnpm vite): a website, not a server. vite-node, "vite build" and chained commands do not match.
 */
const VITE_DEV_CMD =
  /^(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*(?:(?:npx|bunx)\s+(?:(?:--yes|-y)\s+)?|pnpm\s+(?:exec\s+)?|yarn\s+(?:exec\s+)?)?vite(?:@\S+)?(?:\s+-.*|\s+(?:dev|preview|serve)(?:\s.*)?)?$/
function viteDevStart(start: string): boolean {
  const s = start.trim()
  return VITE_DEV_CMD.test(s) && !/[&|;<>$`()]/.test(s)
}

/**
 * A start script that only runs a static file server on the top folder (serve, serve ., http-server -p 8080,
 * npx live-server): its arguments may only be flags, port numbers and . or ./. "serve dist" does not match.
 */
const FILE_SERVER_CMD =
  /^(?:(?:npx|bunx|pnpx)\s+(?:(?:--yes|-y)\s+)?|pnpm\s+(?:exec\s+|dlx\s+)?|yarn\s+(?:exec\s+|dlx\s+)?)?(?:serve|http-server|live-server)(?:@\S+)?((?:\s+\S+)*)$/
function fileServerStart(start: string): boolean {
  const s = start.trim()
  const m = FILE_SERVER_CMD.exec(s)
  if (!m || /[&|;<>$`()]/.test(s)) return false
  return m[1].split(/\s+/).every((arg) => arg === '' || /^(?:-\S*|\.|\.\/|[0-9]+)$/.test(arg))
}

/** Where a framework writes its site, and whether it is a single-page app. */
function staticDefaults(fw: Framework): { output: string; spa: boolean } {
  switch (fw) {
    case 'cra':
      return { output: 'build', spa: true }
    case 'sveltekit':
      return { output: 'build', spa: false }
    case 'next':
      return { output: 'out', spa: false }
    case 'astro':
      return { output: 'dist', spa: false }
  }
  return { output: 'dist', spa: true }
}

/** path.Clean for the relative "main" of package.json; '' when it leaves the project. */
function cleanMain(main: string): string {
  const out: string[] = []
  for (const part of main.replace(/^\.\//, '').split('/')) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      if (!out.length) return ''
      out.pop()
    } else out.push(part)
  }
  return out.join('/')
}

/** Whether the server knows a way to start the project: a start script, a command for the framework, or a safe "main". */
function canStart(p: Project, fw: Framework): boolean {
  if (p.scripts.has('start')) return true
  if (['sveltekit', 'nuxt', 'remix', 'react-router', 'astro', 'next'].includes(fw)) return true
  const m = cleanMain(p.main)
  return p.main !== '' && m !== '' && m.length <= 200 && !m.startsWith('.') && m.split('/').every((x) => /^[A-Za-z0-9_.-]+$/.test(x) && x !== '..')
}

const SVELTE_ADAPTER = 'Add @sveltejs/adapter-node to package.json and use it in svelte.config.js, or the app will not start.'

type Template = Extract<Detected, { kind: 'template' }>
type Static = Extract<Detected, { kind: 'static' }>

function nodeApp(fw: Framework, build: boolean, extra: Partial<Template> = {}): Template {
  return { kind: 'template', type: 'node', label: 'Node.js app', known: true, framework: FRAMEWORK_NAME[fw], build, ...extra }
}

function staticBuild(fw: Framework, output?: string): Template {
  const d = staticDefaults(fw)
  return { kind: 'template', type: 'static_build', label: 'Static build', known: true, framework: FRAMEWORK_NAME[fw], output: output ?? d.output, spa: d.spa }
}

/**
 * The server's Detection.detect (nodeproject.go): what auto mode makes of a
 * Node project without a Dockerfile. null: nothing it can build.
 */
function autoNode(p: Project): Template | Static | null {
  const fw = framework(p)
  const build = p.scripts.has('build')
  // A plain website with a package.json for tools (prettier, eslint) or a dev
  // server: no framework, nothing to build and no server of its own.
  if (fw === '' && !build && p.hasIndex) {
    if (!p.scripts.has('start')) return { kind: 'static', label: 'Static site', ...(p.runtime ? { unsure: true } : {}) }
    if (fileServerStart(p.start)) return { kind: 'static', label: 'Static site', unsure: true }
  }
  switch (fw) {
    case 'sveltekit':
      if (p.deps.has('@sveltejs/adapter-static') && build) return staticBuild(fw)
      if (canStart(p, fw)) return nodeApp(fw, build, p.deps.has('@sveltejs/adapter-node') ? {} : { warn: SVELTE_ADAPTER })
      break
    case 'next':
      if (p.nextExport && build) return staticBuild(fw)
      if (build) return nodeApp(fw, build)
      break
    case 'remix':
    case 'react-router':
    case 'nuxt':
    case 'server':
      if (canStart(p, fw)) return nodeApp(fw, build)
      break
    case 'astro':
      if (p.deps.has('@astrojs/node')) {
        if (canStart(p, fw)) return nodeApp(fw, build)
      } else if (build) return staticBuild(fw)
      break
    case 'cra':
      if (build) return staticBuild(fw)
      break
    case 'vite':
      if (build && p.hasIndex) return p.scripts.has('start') && !viteDevStart(p.start) ? nodeApp(fw, build, { unsure: true }) : staticBuild(fw)
      break
  }
  if (p.scripts.has('start')) return nodeApp(fw, build)
  return null
}

const MISSING_PACKAGE = (what: string): Detected => ({
  kind: 'none',
  label: 'No package.json',
  message: `There is no package.json at the top of your code, so it cannot be built as ${what}.`,
  hint: 'Put package.json in the top folder, or choose another type.',
})

/** The server's explicit modes node and static_build. */
function explicitNode(mode: 'node' | 'static_build', packageJson: string | null, nextConfig: string | null, files: Set<string>): Detected {
  const what = mode === 'node' ? 'a Node.js app' : 'a static site'
  if (!files.has('package.json')) return MISSING_PACKAGE(what)
  if (packageJson === null) return { kind: 'template', type: mode, label: mode === 'node' ? 'Node.js app' : 'Static build', known: false }
  const p = readProject(packageJson, nextConfig, files.has('index.html'))
  if (!p) return { kind: 'none', label: 'package.json is not valid', message: 'package.json cannot be read.', hint: 'Check that package.json is valid JSON.' }
  const fw = framework(p)
  if (mode === 'node') {
    if (!canStart(p, fw)) {
      return {
        kind: 'none',
        label: 'No start script',
        message: 'package.json has no start script, so bitploy does not know how to start the app.',
        hint: 'Add a start script to package.json, like "start": "node server.js".',
      }
    }
    return nodeApp(fw, p.scripts.has('build'))
  }
  if (!p.scripts.has('build')) {
    return {
      kind: 'none',
      label: 'No build script',
      message: 'package.json has no build script, so there is nothing to build.',
      hint: 'Add a build script to package.json, like "build": "vite build", or set the type to Plain website if the files are ready to serve.',
    }
  }
  return staticBuild(fw)
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
 * (a wrapping folder already removed). `mode` is "auto" (a Dockerfile, else
 * what the project is: see autoNode, an index.html, else the only page or the
 * chosen `homePage`), or the person's choice: "node", "static_build", "static"
 * or "dockerfile". `packageJson` is the text of package.json at the top of the
 * project; null when it could not be read here (a zip this page cannot open),
 * which gives a neutral preview for a Node project. `nextConfig` is the text
 * of the Next.js config at the top, if there is one.
 */
export function detectSource(
  paths: string[],
  mode: BuildMode = 'auto',
  homePage = '',
  dockerfilePath = 'Dockerfile',
  packageJson: string | null = null,
  nextConfig: string | null = null,
): Detected {
  const files = new Set(paths.map(cleanRel))
  const top = new Set<string>()
  for (const f of files) if (!f.includes('/')) top.add(f.toLowerCase())
  const hasDockerfile = files.has(dockerfilePath)
  const hasIndex = files.has('index.html')

  if (mode === 'node' || mode === 'static_build') return explicitNode(mode, packageJson, nextConfig, files)
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
      hint: 'Add a Dockerfile at the top of the folder. For a site of HTML files, set the type to Plain website.',
    }
  }
  // auto: the same rule as the server. A project with a package.json and so on
  // is not shown as a plain website by the page rule: it is built as a project.
  const stack = stackOf(top)
  if (hasIndex && stack === '') return { kind: 'static', label: 'Static site' }
  const otherDockerfile = otherCase(files, dockerfilePath)
  if (otherDockerfile) return renamed(otherDockerfile, dockerfilePath)
  if (stack === 'node' && files.has('package.json')) {
    if (packageJson === null) return { kind: 'template', type: 'unknown', label: 'Node.js app', known: false }
    const project = readProject(packageJson, nextConfig, hasIndex)
    const node = project && autoNode(project)
    if (node) return node
  }
  if (stack === '') {
    const home = homePageOf(files, homePage)
    if (home) return home
  }
  if (stack !== '' && stack !== 'node') return appStack(stack, files)
  if (stack === 'node' && rootPages(files).length) {
    return {
      kind: 'none',
      label: 'Needs a build first',
      message: 'No Dockerfile found. This is a Node.js app, so it has to be built before it can be shown.',
      hint: 'Add a Dockerfile (your AI assistant can write one), or upload the built website instead, usually the dist or build folder.',
      assistant: true,
    }
  }
  return { kind: 'none', label: 'Nothing to build', message: 'No Dockerfile or index.html found.', hint: NOTHING_HINT, assistant: true }
}

/** The kind of a detected source as a name for the Type select: "Node.js app", "Static build", "Plain website", "Own Dockerfile"; '' when nothing was found. */
export function detectedType(d: Detected | null): string {
  if (!d || d.kind === 'none') return ''
  if (d.kind === 'dockerfile') return 'Own Dockerfile'
  if (d.kind === 'static') return 'Plain website'
  if (d.kind === 'app') return d.label
  return d.type === 'static_build' ? 'Static build' : d.type === 'node' ? 'Node.js app' : ''
}

/**
 * Why an output folder is not allowed, or '' when it is fine (empty means the
 * default of the framework). The server's deploy.CleanOutputDir: a relative
 * folder of letters, numbers and . _ - /, nothing that leaves the project.
 */
export function outputDirError(dir: string): string {
  let d = dir.trim().replace(/\\/g, '/')
  if (d === '') return ''
  while (d.startsWith('./')) d = d.slice(2)
  d = d.replace(/\/+$/, '')
  if (d.startsWith('/')) return 'Use a folder inside your code, not an absolute path.'
  if (d.split('/').includes('..')) return 'The folder cannot contain ..'
  const ok = d.length <= 200 && d.split('/').every((seg) => seg !== '' && seg !== '.' && /^[A-Za-z0-9_.-]+$/.test(seg))
  return ok ? '' : 'Use only letters, numbers and . _ - /, like dist or build/client.'
}
