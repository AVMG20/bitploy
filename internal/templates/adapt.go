package templates

import (
	"path"
	"strings"
)

// ForFiles returns the template for stack adapted to the project's files
// (package manager from the lock file, ...). ok is false for unknown stacks.
//
// Only top-level file names are looked at. The base templates use npm and pip;
// adapting swaps exact, known lines (see the marker constants) so the result
// stays a valid Dockerfile. Unknown or missing lock files leave the template
// as it is, except that Node stacks without any lock file use `npm install`
// because `npm ci` would fail.
func ForFiles(stack string, files []string) (Template, bool) {
	tpl, ok := Get(stack)
	if !ok {
		return tpl, false
	}
	top := topLevel(files)
	if len(top) == 0 {
		return tpl, true // no file list: nothing to adapt to
	}
	switch tpl.Stack {
	case "node", "node-spa", "node-static", "nextjs":
		adaptNode(&tpl, top)
	case "python", "django":
		adaptPython(&tpl, top)
	case "go":
		adaptGo(&tpl, top)
	case "laravel":
		if !top["package.json"] {
			dropLaravelAssets(&tpl)
		} else {
			adaptNode(&tpl, top)
		}
	}
	return tpl, true
}

// topLevel returns the set of top-level file names. Paths may start with ./
// or use backslashes; anything in a sub-folder is ignored.
func topLevel(files []string) map[string]bool {
	set := map[string]bool{}
	for _, f := range files {
		f = strings.ReplaceAll(strings.TrimSpace(f), "\\", "/")
		f = strings.TrimPrefix(path.Clean(f), "./")
		if f == "" || f == "." || strings.HasPrefix(f, "/") || strings.Contains(f, "/") {
			continue
		}
		set[f] = true
	}
	return set
}

// replaceLine swaps one exact Dockerfile line (without newline) for new text.
// An empty replacement removes the line.
func replaceLine(df, old, replacement string) string {
	if replacement == "" {
		return strings.ReplaceAll(df, old+"\n", "")
	}
	return strings.ReplaceAll(df, old+"\n", replacement+"\n")
}

// Lines of the base Node templates that adaptNode swaps. The bun image is the
// Debian one: its binary is copied into the Debian node build stages, and an
// Alpine (musl) binary does not run there.
const (
	nodeCopyLine        = "COPY package*.json ./"
	nodeInstallProd     = "RUN npm ci --omit=dev"
	nodeInstallDev      = "RUN npm ci"
	nodeBuildLine       = "RUN npm run build"
	nodePrune           = "RUN npm prune --omit=dev"
	nodeFromRuntime     = "FROM node:22-bookworm-slim"
	nodeFromBuild       = "FROM node:22-bookworm-slim AS build"
	nodeFromAssets      = "FROM node:22-bookworm-slim AS assets"
	nodeUserLine        = "USER node"
	nodeUserComment     = `# Run as the unprivileged "node" user that ships with the image.`
	nodeStartLine       = `CMD ["npm", "start"]`
	bunImage            = "oven/bun:1-slim"
	bunCopyFromOfficial = "COPY --from=" + bunImage + " /usr/local/bin/bun /usr/local/bin/bun"
)

// BuildScriptLines are the lines of the Node templates that run the project's
// build script, one per package manager: the step that bakes build-time
// variables (VITE_*, NEXT_PUBLIC_*) into the site.
func BuildScriptLines() []string {
	out := []string{nodeBuildLine}
	for _, lock := range []string{"pnpm-lock.yaml", "bun.lock", "yarn.lock"} {
		out = append(out, detectNode(map[string]bool{lock: true}).build)
	}
	return out
}

type pkgManager struct {
	name       string
	copyLine   string // replaces nodeCopyLine
	installDev string // all dependencies, for stages that build
	installRun string // runtime dependencies only, for the node stack
	build      string
	prune      string // removes the dev dependencies after the build; empty: keep them
	bun        bool
}

// detectNode picks the package manager from the lock files: pnpm, bun, yarn,
// npm, in that order (the least common lock file wins when several exist).
func detectNode(top map[string]bool) pkgManager {
	switch {
	case top["pnpm-lock.yaml"]:
		files, prune := "package.json pnpm-lock.yaml", "RUN pnpm prune --prod"
		if top["pnpm-workspace.yaml"] {
			files += " pnpm-workspace.yaml"
			prune = "" // pnpm prune does not handle a workspace
		}
		return pkgManager{
			name:       "pnpm",
			copyLine:   "COPY " + files + " ./",
			installDev: "RUN corepack enable && pnpm install --frozen-lockfile",
			installRun: "RUN corepack enable && pnpm install --frozen-lockfile --prod",
			build:      "RUN pnpm build",
			prune:      prune,
		}
	case top["bun.lock"] || top["bun.lockb"]:
		lock := "bun.lock"
		if !top[lock] {
			lock = "bun.lockb"
		}
		return pkgManager{
			name:       "bun",
			copyLine:   "COPY package.json " + lock + " ./",
			installDev: "RUN bun install --frozen-lockfile",
			installRun: "RUN bun install --frozen-lockfile --production",
			build:      "RUN bun run build",
			bun:        true,
		}
	case top["yarn.lock"] && top[".yarnrc.yml"]: // Yarn Berry (2+)
		return pkgManager{
			name:       "yarn",
			copyLine:   "COPY package.json yarn.lock .yarnrc.yml ./",
			installDev: "RUN corepack enable && yarn install --immutable",
			installRun: "RUN corepack enable && yarn install --immutable",
			build:      "RUN yarn build",
		}
	case top["yarn.lock"]:
		return pkgManager{
			name:       "yarn",
			copyLine:   "COPY package.json yarn.lock ./",
			installDev: "RUN corepack enable && yarn install --frozen-lockfile",
			installRun: "RUN corepack enable && yarn install --frozen-lockfile --production",
			build:      "RUN yarn build",
			prune:      "RUN yarn install --frozen-lockfile --production --ignore-scripts --prefer-offline",
		}
	case top["package-lock.json"], top["npm-shrinkwrap.json"]:
		return pkgManager{name: "npm"}
	default:
		return pkgManager{
			name:       "npm-nolock",
			installDev: "RUN npm install",
			installRun: "RUN npm install --omit=dev",
			prune:      nodePrune,
		}
	}
}

func adaptNode(t *Template, top map[string]bool) {
	pm := detectNode(top)
	if pm.name == "npm" {
		return
	}
	df := t.Dockerfile
	if pm.copyLine != "" {
		df = replaceLine(df, nodeCopyLine, pm.copyLine)
		df = replaceLine(df, nodeBuildLine, pm.build)
	}
	df = replaceLine(df, nodeInstallProd, pm.installRun)
	df = replaceLine(df, nodeInstallDev, pm.installDev)
	if pm.bun {
		// Build stages keep Node (Vite, Astro and Next.js need it) and get the
		// bun binary from the official image. The node stack has no build
		// stage, so it runs on the bun image itself.
		df = replaceLine(df, nodeFromBuild, nodeFromBuild+"\n"+bunCopyFromOfficial)
		df = replaceLine(df, nodeFromAssets, nodeFromAssets+"\n"+bunCopyFromOfficial)
		if t.Stack == "node" {
			df = replaceLine(df, nodeFromRuntime, "FROM "+bunImage)
			df = replaceLine(df, nodeUserComment, `# Run as the unprivileged "bun" user that ships with the image.`)
			df = replaceLine(df, nodeUserLine, "USER bun")
			df = replaceLine(df, nodeStartLine, `CMD ["bun", "run", "start"]`)
		}
	}
	t.Dockerfile = df
	t.Readme += nodeNote(t.Stack, pm)
}

func nodeNote(stack string, pm pkgManager) string {
	var n string
	switch pm.name {
	case "npm-nolock":
		n = "No lock file was found, so the Dockerfile runs `npm install` and every build installs the newest versions that `package.json` allows: a build can break although the code did not change. Ship the lock file (`package-lock.json`, or `bun.lock`, `pnpm-lock.yaml`, `yarn.lock`) and switch back to `npm ci`, or pin exact versions in `package.json` (`\"3.13.0\"`, not `\"^3.13.0\"`)."
	case "pnpm":
		n = "Adapted for pnpm: the Dockerfile runs `pnpm install --frozen-lockfile` (needs `pnpm-lock.yaml` to match `package.json`). Set `\"packageManager\": \"pnpm@x.y.z\"` in `package.json` to pin the version; corepack uses it. If the install stops with `ERR_PNPM_IGNORED_BUILDS`, approve the build scripts locally (`pnpm approve-builds`) and commit the result."
	case "yarn":
		n = "Adapted for Yarn: the Dockerfile enables corepack and installs with the lock file. Set `\"packageManager\"` in `package.json` (required for Yarn 2+) so corepack uses your version. Yarn 2+ with a committed `.yarn/releases` folder: also copy `.yarn`."
	case "bun":
		n = "Adapted for bun: the Dockerfile runs `bun install --frozen-lockfile`."
		if stack == "node" {
			n += " The image is `oven/bun`, so the start command is `bun run start`. Its unprivileged user is `bun`, not `node`: for saved files use `RUN mkdir /data && chown bun:bun /data` before `USER bun`."
		} else {
			n += " Node stays in the build stage; bun is copied from the official image."
		}
	}
	return "\n- " + n + "\n"
}

// Lines of the base Python templates that adaptPython swaps.
const (
	pyDefaultBlock = "COPY requirements.txt ./\nRUN pip install -r requirements.txt\n\nCOPY . .\n"
	uvImage        = "ghcr.io/astral-sh/uv:0.9.5"
)

func adaptPython(t *Template, top map[string]bool) {
	var block, note string
	switch {
	case top["uv.lock"] && top["pyproject.toml"]:
		block = "COPY --from=" + uvImage + " /uv /bin/uv\n" +
			"ENV PATH=\"/app/.venv/bin:$PATH\" \\\n" +
			"    UV_LINK_MODE=copy \\\n" +
			"    UV_PYTHON_DOWNLOADS=never\n" +
			"COPY pyproject.toml uv.lock ./\n" +
			"RUN uv sync --frozen --no-dev --no-install-project\n\n" +
			"COPY . .\n" +
			"RUN uv sync --frozen --no-dev\n"
		note = "Adapted for uv: the Dockerfile installs with `uv sync --frozen --no-dev` into `/app/.venv`, which is on `PATH`, so `gunicorn` and `uvicorn` must be dependencies in `pyproject.toml`. `requires-python` must match the `FROM python:` version."
	case top["poetry.lock"] && top["pyproject.toml"]:
		block = "ENV POETRY_VIRTUALENVS_CREATE=false \\\n" +
			"    POETRY_NO_INTERACTION=1\n" +
			"COPY pyproject.toml poetry.lock ./\n" +
			"RUN pip install poetry && poetry install --only main --no-root\n\n" +
			"COPY . .\n"
		note = "Adapted for Poetry: the Dockerfile runs `poetry install --only main --no-root` without a virtualenv, so `gunicorn` and `uvicorn` must be main dependencies in `pyproject.toml`."
	case top["requirements.txt"]:
		return
	case top["pyproject.toml"]:
		block = "COPY . .\nRUN pip install .\n"
		note = "Adapted for `pyproject.toml` without `requirements.txt`: the Dockerfile runs `pip install .`, so `gunicorn` and `uvicorn` must be dependencies in `pyproject.toml`."
	default:
		return
	}
	t.Dockerfile = strings.Replace(t.Dockerfile, pyDefaultBlock, block, 1)
	t.Readme += "\n- " + note + "\n"
}

// goBuildLine is the line of the go template that compiles the program.
const goBuildLine = `RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .`

// adaptGo handles a Go project without go.sum: go build refuses to run
// ("missing go.sum entry"), so the Dockerfile runs go mod tidy first. tidy
// takes the newest version of a module that go.mod does not list, which is
// why the note asks for go.sum; GOTOOLCHAIN=auto in the template covers a
// module that needs a newer Go.
func adaptGo(t *Template, top map[string]bool) {
	if !top["go.mod"] || top["go.sum"] {
		return
	}
	t.Dockerfile = replaceLine(t.Dockerfile, goBuildLine, "RUN go mod tidy\n"+goBuildLine)
	t.Readme += "\n- No `go.sum` was found, so the Dockerfile runs `go mod tidy` before the build. It picks the newest version of every module `go.mod` does not pin, so builds can change by themselves. Run `go mod tidy` on your computer, ship `go.mod` and `go.sum`, and remove the `go mod tidy` line.\n"
}

// dropLaravelAssets removes the Vite stage for projects without package.json.
func dropLaravelAssets(t *Template) {
	const start = "# Stage 2:"
	const end = "# Stage 3:"
	i, j := strings.Index(t.Dockerfile, start), strings.Index(t.Dockerfile, end)
	if i < 0 || j < i {
		return
	}
	df := t.Dockerfile[:i] + strings.Replace(t.Dockerfile[j:], "# Stage 3:", "# Stage 2:", 1)
	t.Dockerfile = strings.Replace(df, "COPY --from=assets /app/public/build ./public/build\n", "", 1)
	t.Readme += "\n- No `package.json` was found, so the Vite stage was left out.\n"
}

// NodeWithBuild is the node template, adapted with ForFiles, for a project
// that must be built before it starts (a build script next to the start
// script: TypeScript, NestJS, Remix, SvelteKit, Astro). It has two stages:
// the first installs every dependency and runs the build script, the second
// gets only the result, so compilers and bundlers stay out of the image. With
// pruneDev the dev dependencies are removed after the build; the caller turns
// it off when the start command needs one of them (tsx, vite preview). Bun and
// Yarn 2+ have no safe way to remove them and keep them. ok is false when the
// template no longer has the lines this swaps.
func NodeWithBuild(files []string, pruneDev bool) (Template, bool) {
	return nodeWithBuild(files, pruneDev, "")
}

// nodeWithBuild is NodeWithBuild; extra are lines that run in the build stage
// after the build script (before the dev dependencies are removed).
func nodeWithBuild(files []string, pruneDev bool, extra string) (Template, bool) {
	tpl, _ := ForFiles("node", files)
	pm := pkgManager{name: "npm"}
	if top := topLevel(files); len(top) > 0 {
		pm = detectNode(top)
	}
	copyLine, installRun, installDev, build, prune := nodeCopyLine, nodeInstallProd, nodeInstallDev, nodeBuildLine, nodePrune
	if pm.copyLine != "" {
		copyLine = pm.copyLine
	}
	if pm.installRun != "" {
		installRun, installDev, prune = pm.installRun, pm.installDev, pm.prune
	}
	if pm.build != "" {
		build = pm.build
	}
	depsBlock := "# Install dependencies first so Docker can cache this layer.\n" + copyLine + "\n" + installRun + "\n\nCOPY . .\n"
	df := tpl.Dockerfile
	from, _, _ := strings.Cut(df, "\n")
	if !strings.HasPrefix(from, "FROM ") || strings.Contains(from, " AS ") || strings.Count(df, depsBlock) != 1 {
		return tpl, false
	}
	var b strings.Builder
	b.WriteString("# Stage 1: install all dependencies and run the build.\n")
	b.WriteString(from + " AS build\nWORKDIR /app\n")
	b.WriteString(copyLine + "\n" + installDev + "\nCOPY . .\n" + build + "\n")
	b.WriteString(extra)
	pruned := pruneDev && prune != ""
	if pruned {
		b.WriteString("# Remove the dev dependencies (compilers, bundlers) and build caches: the app does not need them to run.\n")
		b.WriteString(prune + " && rm -rf node_modules/.cache\n")
	}
	b.WriteString("\n# Stage 2: run the app with only what the build left in /app.\n")
	b.WriteString(strings.Replace(df, depsBlock, "COPY --from=build /app ./\n", 1))
	tpl.Dockerfile = b.String()
	tpl.Readme += "\n- package.json has a build script, so the Dockerfile has two stages: the first installs all dependencies and runs the build, the second copies the result and starts it."
	if pruned {
		tpl.Readme += " The dev dependencies are removed after the build, so a package the app needs while it runs must be in `dependencies`, not `devDependencies`."
	}
	tpl.Readme += "\n"
	return tpl, true
}

// NuxtServer is the template for a Nuxt 3+ app that runs its own server.
// nuxt build writes a server that runs by itself into .output (Nitro copies
// the packages it needs into .output/server/node_modules), so the image gets
// only that folder and no node_modules or source.
func NuxtServer(files []string) (Template, bool) {
	tpl, ok := NodeWithBuild(files, false)
	if !ok {
		return tpl, false
	}
	const copyApp = "COPY --from=build /app ./\n"
	if strings.Count(tpl.Dockerfile, copyApp) != 1 {
		return tpl, false
	}
	tpl.Dockerfile = strings.Replace(tpl.Dockerfile, copyApp,
		"# nuxt build makes a server that runs by itself: only .output goes into the image.\nCOPY --from=build /app/.output ./.output\n", 1)
	tpl.Readme += "\n- Nuxt: the image holds only the `.output` folder of `nuxt build` and starts `.output/server/index.mjs`.\n"
	return WithStart(tpl, []string{"node", ".output/server/index.mjs"})
}
