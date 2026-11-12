package templates

import (
	"regexp"
	"strconv"
	"strings"
)

// The presets below are what the dashboard's build types need on top of
// ForFiles: a static build with a chosen output folder, a Next.js server that
// may lack output "standalone", and a start command for a Node project whose
// package.json has no start script. They only swap exact, known lines of the
// base templates, like ForFiles does, and report ok=false when a line is gone.

var outputSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidOutputDir reports whether dir is a safe output folder of a build: a
// relative path of plain names (letters, digits, . _ -), no empty part and no
// . or .. part. It goes into a Dockerfile and a .dockerignore, so nothing else
// is accepted.
func ValidOutputDir(dir string) bool {
	if dir == "" || len(dir) > 200 {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." || seg == ".." || !outputSegment.MatchString(seg) {
			return false
		}
	}
	return true
}

const (
	staticCopyLine = "COPY --from=build /app/dist /usr/share/nginx/html\n"
	stageTwo       = "# Stage 2:"
	nginxFrom      = "\nFROM nginxinc/"
)

// StaticBuild is the node-spa template (spa: unknown paths load index.html) or
// the node-static template (every page is a file, unknown paths are a 404),
// adapted with ForFiles and made to serve outputDir instead of dist. The
// folder is also added to the .dockerignore so a copy built on the person's
// computer is not sent. ok is false for an unsafe outputDir or when the
// template no longer has the line this swaps.
func StaticBuild(files []string, outputDir string, spa bool) (Template, bool) {
	stack := "node-static"
	if spa {
		stack = "node-spa"
	}
	if !ValidOutputDir(outputDir) {
		return Template{}, false
	}
	tpl, _ := ForFiles(stack, files)
	if strings.Count(tpl.Dockerfile, staticCopyLine) != 1 {
		return tpl, false
	}
	df := strings.Replace(tpl.Dockerfile, staticCopyLine, "COPY --from=build /app/"+outputDir+" /usr/share/nginx/html\n", 1)
	if i, j := strings.Index(df, stageTwo), strings.Index(df, nginxFrom); i >= 0 && j > i {
		df = df[:i] + stageTwo + ` serve the built files ("` + outputDir + `") with nginx.` + df[j:]
	}
	tpl.Dockerfile = df
	if !hasLine(tpl.Dockerignore, outputDir) {
		if !strings.HasSuffix(tpl.Dockerignore, "\n") {
			tpl.Dockerignore += "\n"
		}
		tpl.Dockerignore += outputDir + "\n"
	}
	tpl.Readme += "\n- The build output folder is `" + outputDir + "`.\n"
	return tpl, true
}

func hasLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// nextCacheNote: next start writes its cache into .next while it runs.
const nextCacheNote = "\n- The server runs with `next start`. Add `output: \"standalone\"` to the Next.js config for a smaller image that runs `node server.js`.\n"

// NextJS is the template for a Next.js app that runs its own server. With
// standalone (next.config has output: "standalone") it is the nextjs template.
// Without it, it is the node template with a build step running the project's
// start script (`next start`), with .next owned by the unprivileged user so
// Next.js can write its cache. A project without a start script runs
// `npx next start`. pruneDev is passed to NodeWithBuild; the build cache in
// .next/cache stays out of the image.
func NextJS(files []string, standalone, hasStart, pruneDev bool) (Template, bool) {
	if standalone {
		tpl, ok := ForFiles("nextjs", files)
		return tpl, ok
	}
	tpl, ok := nodeWithBuild(files, pruneDev, "RUN rm -rf .next/cache\n")
	if !ok {
		return tpl, false
	}
	const comment = "# Run as the unprivileged"
	if i := strings.Index(tpl.Dockerfile, comment); i >= 0 {
		user := "node"
		if strings.Contains(tpl.Dockerfile, "\nUSER bun\n") {
			user = "bun"
		}
		tpl.Dockerfile = tpl.Dockerfile[:i] + "RUN chown -R " + user + ":" + user + " /app/.next\n\n" + tpl.Dockerfile[i:]
	}
	tpl.Readme += nextCacheNote
	if !hasStart {
		return WithStart(tpl, []string{"npx", "next", "start"})
	}
	return tpl, true
}

var safeArg = regexp.MustCompile(`^[A-Za-z0-9_./@:=-]+$`)

const startComment = `# Change this if your start command is not "npm start" (for example ["node", "server.js"]).` + "\n"

// WithStart replaces the start command of an adapted node template (CMD
// ["npm", "start"]) with cmd, for a project whose package.json has no start
// script, and adds env as ENV lines ("NAME=value") before it. A nil cmd only
// adds the ENV lines. On a bun image node and npx become bun and bunx. ok is
// false when an argument is not plain or the template has no start line.
func WithStart(tpl Template, cmd []string, env ...string) (Template, bool) {
	bun := strings.Contains(tpl.Dockerfile, bunStartLine)
	old := nodeStartLine
	if bun {
		old = bunStartLine
	}
	if strings.Count(tpl.Dockerfile, old+"\n") != 1 {
		return tpl, false
	}
	var b strings.Builder
	for _, e := range env {
		if !safeArg.MatchString(e) || !strings.Contains(e, "=") {
			return tpl, false
		}
		b.WriteString("ENV " + e + "\n")
	}
	if cmd == nil {
		b.WriteString(old + "\n")
	} else {
		args := make([]string, len(cmd))
		for i, a := range cmd {
			if !safeArg.MatchString(a) {
				return tpl, false
			}
			if bun && i == 0 {
				switch a {
				case "node":
					a = "bun"
				case "npx":
					a = "bunx"
				}
			}
			args[i] = strconv.Quote(a)
		}
		b.WriteString("CMD [" + strings.Join(args, ", ") + "]\n")
	}
	df := strings.Replace(tpl.Dockerfile, startComment, "", 1)
	tpl.Dockerfile = strings.Replace(df, old+"\n", b.String(), 1)
	return tpl, true
}

const bunStartLine = `CMD ["bun", "run", "start"]`
