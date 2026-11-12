package deploy

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/templates"
)

func codes(res CheckResult) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range res.Findings {
		if _, ok := m[f.Code]; !ok {
			m[f.Code] = f
		}
	}
	return m
}

func TestCheckDockerfile(t *testing.T) {
	const good = "FROM node:24-alpine\nUSER node\nEXPOSE 3000\nHEALTHCHECK CMD wget -q -O /dev/null http://127.0.0.1:3000/\nCMD [\"node\",\"x.js\"]\n"
	tests := []struct {
		name     string
		in       CheckInput
		want     []string // codes that must be present
		notWant  []string // codes that must be absent
		wantOK   bool
		wantPort int
		wantHC   bool
		line     map[string]int
	}{
		{
			name:    "clean",
			in:      CheckInput{Dockerfile: []byte(good), Files: []string{"package.json", "server.js"}},
			wantOK:  true,
			notWant: []string{"port_unknown", "runs_as_root", "no_healthcheck", "unpinned_base"}, wantPort: 3000, wantHC: true,
		},
		{
			name:   "missing file",
			in:     CheckInput{Files: []string{"index.html"}},
			want:   []string{"dockerfile_missing"},
			wantOK: false,
		},
		{
			name:   "parse error unknown instruction with line",
			in:     CheckInput{Dockerfile: []byte("FROM alpine:3.22\nFORM x\nEXPOSE 80\n")},
			want:   []string{"dockerfile_parse_error"},
			wantOK: false,
			line:   map[string]int{"dockerfile_parse_error": 2},
		},
		{
			name:   "parse error bad arguments",
			in:     CheckInput{Dockerfile: []byte("FROM alpine:3.22\n\nEXPOSE\n")},
			want:   []string{"dockerfile_parse_error"},
			line:   map[string]int{"dockerfile_parse_error": 3},
			wantOK: false,
		},
		{
			name:   "parse error unterminated heredoc",
			in:     CheckInput{Dockerfile: []byte("FROM alpine:3.22\nRUN <<EOT\necho hi\n")},
			want:   []string{"dockerfile_parse_error"},
			wantOK: false,
		},
		{
			name:   "empty file is no_from",
			in:     CheckInput{Dockerfile: []byte("# nothing here\n")},
			want:   []string{"no_from"},
			wantOK: false,
		},
		{
			name:   "no FROM but instructions",
			in:     CheckInput{Dockerfile: []byte("RUN echo hi\nEXPOSE 80\n")},
			want:   []string{"no_from"}, // instructions before any FROM
			wantOK: false,
		},
		{
			name:    "no EXPOSE: port comes from the image",
			in:      CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\n")},
			want:    []string{"port_from_image"},
			notWant: []string{"port_unknown"},
			wantOK:  true,
		},
		{
			name:    "one-line FROM image",
			in:      CheckInput{Dockerfile: []byte("FROM ghcr.io/foo/bar:1.2\n")},
			want:    []string{"port_from_image"},
			notWant: []string{"port_unknown"},
			wantOK:  true,
		},
		{
			name:     "port unknown but app port set",
			in:       CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\n"), AppPort: 9000},
			notWant:  []string{"port_unknown"},
			wantOK:   true,
			wantPort: 9000,
		},
		{
			name:   "port ambiguous",
			in:     CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\nEXPOSE 80 443\n")},
			want:   []string{"port_ambiguous"},
			wantOK: false,
		},
		{
			name:     "ambiguous resolved by app port",
			in:       CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\nEXPOSE 80 443\n"), AppPort: 443},
			notWant:  []string{"port_ambiguous"},
			wantOK:   true,
			wantPort: 443,
		},
		{
			name:     "udp and duplicate tcp ports are not ambiguous",
			in:       CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\nEXPOSE 8080/tcp 8080 53/udp\n")},
			notWant:  []string{"port_ambiguous", "port_unknown"},
			wantOK:   true,
			wantPort: 8080,
		},
		{
			name:     "expose from ARG default",
			in:       CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\nARG P=4000\nEXPOSE $P\n")},
			notWant:  []string{"port_unknown"},
			wantOK:   true,
			wantPort: 4000,
		},
		{
			name:   "expose unresolved variable",
			in:     CheckInput{Dockerfile: []byte("FROM node:24-alpine\nUSER node\nEXPOSE $WHAT\n")},
			want:   []string{"port_unknown"},
			wantOK: false,
		},
		{
			name:   "expose in builder stage only",
			in:     CheckInput{Dockerfile: []byte("FROM golang:1.26 AS build\nEXPOSE 8080\nFROM alpine:3.22\nUSER 1000\n")},
			want:   []string{"port_from_image"},
			wantOK: true,
		},
		{
			name:     "expose inherited from local base stage",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22 AS base\nEXPOSE 8080\nUSER app\nFROM base\nCMD [\"x\"]\n")},
			notWant:  []string{"port_unknown", "runs_as_root"},
			wantOK:   true,
			wantPort: 8080,
		},
		{
			name:     "final stage is the last FROM, not the first",
			in:       CheckInput{Dockerfile: []byte("FROM golang:1.26 AS build\nEXPOSE 1\nEXPOSE 2\nFROM alpine:3.22\nUSER 1000\nEXPOSE 8080\n")},
			notWant:  []string{"port_ambiguous"},
			wantOK:   true,
			wantPort: 8080,
		},
		{
			name:    "worker skips port checks",
			in:      CheckInput{Dockerfile: []byte("FROM python:3.14-slim\nUSER app\nCMD [\"python\",\"bot.py\"]\n"), AppType: "worker"},
			notWant: []string{"port_unknown", "port_ambiguous", "no_healthcheck"},
			wantOK:  true,
		},
		{
			name:     "unpinned: no tag",
			in:       CheckInput{Dockerfile: []byte("FROM node\nUSER node\nEXPOSE 80\n")},
			want:     []string{"unpinned_base"},
			wantOK:   true,
			line:     map[string]int{"unpinned_base": 1},
			wantPort: 80,
		},
		{
			name:     "unpinned: latest",
			in:       CheckInput{Dockerfile: []byte("FROM ghcr.io/acme/app:latest\nUSER node\nEXPOSE 80\n")},
			want:     []string{"unpinned_base"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "pinned: registry with port, digest, scratch, stage alias",
			in:       CheckInput{Dockerfile: []byte("FROM localhost:5000/base:1.2 AS a\nFROM alpine@sha256:abc AS b\nFROM scratch AS c\nFROM a AS d\nFROM b\nUSER x\nEXPOSE 80\n")},
			notWant:  []string{"unpinned_base"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "registry with port and no tag is unpinned",
			in:       CheckInput{Dockerfile: []byte("FROM localhost:5000/base\nUSER x\nEXPOSE 80\n")},
			want:     []string{"unpinned_base"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "base from ARG with default",
			in:       CheckInput{Dockerfile: []byte("ARG V=24\nFROM node:${V}-alpine\nUSER node\nEXPOSE 80\n")},
			notWant:  []string{"unpinned_base"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "base from ARG without default is skipped",
			in:       CheckInput{Dockerfile: []byte("ARG BASE\nFROM ${BASE}\nUSER node\nEXPOSE 80\n")},
			notWant:  []string{"unpinned_base"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "runs as root: no USER",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nEXPOSE 80\n")},
			want:     []string{"runs_as_root"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "runs as root: explicit root",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER root\nEXPOSE 80\n")},
			want:     []string{"runs_as_root"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "runs as root: uid 0 with group",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER 0:0\nEXPOSE 80\n")},
			want:     []string{"runs_as_root"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "non-root: last USER wins",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER root\nRUN apk add curl\nUSER app\nEXPOSE 80\n")},
			notWant:  []string{"runs_as_root"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "USER in builder stage does not count for the final stage",
			in:       CheckInput{Dockerfile: []byte("FROM golang:1.26 AS build\nUSER app\nFROM alpine:3.22\nEXPOSE 80\n")},
			want:     []string{"runs_as_root"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "unprivileged base image runs non-root",
			in:       CheckInput{Dockerfile: []byte("FROM nginxinc/nginx-unprivileged:alpine\nCOPY . /usr/share/nginx/html\nEXPOSE 8080\n")},
			notWant:  []string{"runs_as_root", "unpinned_base"},
			wantOK:   true,
			wantPort: 8080,
		},
		{
			name:     "distroless nonroot",
			in:       CheckInput{Dockerfile: []byte("FROM gcr.io/distroless/static-debian12:nonroot\nEXPOSE 8080\n")},
			notWant:  []string{"runs_as_root"},
			wantOK:   true,
			wantPort: 8080,
		},
		{
			name:     "healthcheck NONE counts as none",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER a\nEXPOSE 80\nHEALTHCHECK NONE\n")},
			want:     []string{"no_healthcheck"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "healthcheck present",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER a\nEXPOSE 80\nHEALTHCHECK --interval=5s CMD wget -q http://localhost/ || exit 1\n")},
			notWant:  []string{"no_healthcheck"},
			wantOK:   true,
			wantPort: 80,
			wantHC:   true,
		},
		{
			name:     "no healthcheck is info",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER a\nEXPOSE 80\n")},
			want:     []string{"no_healthcheck"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env file copied",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine\nUSER node\nCOPY . .\nEXPOSE 80\n"),
				Files:      []string{"package.json", ".env", "src/app.js"},
			},
			want:     []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env file ignored by dockerignore",
			in: CheckInput{
				Dockerfile:   []byte("FROM node:24-alpine\nUSER node\nCOPY . .\nEXPOSE 80\n"),
				Files:        []string{"package.json", ".env", ".dockerignore"},
				Dockerignore: []byte("node_modules\n.env\n"),
			},
			notWant:  []string{"env_file_in_context", "no_dockerignore"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env wildcard in dockerignore",
			in: CheckInput{
				Dockerfile:   []byte("FROM node:24-alpine\nUSER node\nCOPY . .\nEXPOSE 80\n"),
				Files:        []string{".env.production", "config/.env"},
				Dockerignore: []byte(".env*\n**/.env\n"),
			},
			notWant:  []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "dockerignore negation re-includes env",
			in: CheckInput{
				Dockerfile:   []byte("FROM node:24-alpine\nUSER node\nCOPY . .\nEXPOSE 80\n"),
				Files:        []string{".env"},
				Dockerignore: []byte(".env*\n!.env\n"),
			},
			want:     []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env example is fine",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine\nUSER node\nCOPY . .\nEXPOSE 80\n"),
				Files:      []string{".env.example", ".env.sample"},
			},
			notWant:  []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env file not copied when only specific files are copied",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine\nUSER node\nCOPY package.json ./\nCOPY src ./src\nEXPOSE 80\n"),
				Files:      []string{"package.json", ".env", "src/a.js"},
			},
			notWant:  []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env file in copied sub folder",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine\nUSER node\nCOPY src ./src\nEXPOSE 80\n"),
				Files:      []string{"src/.env"},
			},
			want:     []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "env file only copied from another stage is not flagged",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine AS b\nFROM node:24-alpine\nUSER node\nCOPY --from=b /app /app\nEXPOSE 80\n"),
				Files:      []string{".env"},
			},
			notWant:  []string{"env_file_in_context"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "no dockerignore with node_modules",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine\nUSER node\nEXPOSE 80\n"),
				Files:      []string{"package.json", "node_modules/a/index.js"},
			},
			want:     []string{"no_dockerignore"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "no dockerignore with .git or venv",
			in: CheckInput{
				Dockerfile: []byte("FROM python:3.14-slim\nUSER app\nEXPOSE 80\n"),
				Files:      []string{"app.py", "venv/bin/python"},
			},
			want:     []string{"no_dockerignore"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "dockerignore present but unreadable is trusted",
			in: CheckInput{
				Dockerfile: []byte("FROM node:24-alpine\nUSER node\nCOPY . .\nEXPOSE 80\n"),
				Files:      []string{".env", ".dockerignore", "node_modules/x"},
			},
			notWant:  []string{"env_file_in_context", "no_dockerignore"},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name: "no dockerignore needed for clean folder",
			in: CheckInput{
				Dockerfile: []byte("FROM nginxinc/nginx-unprivileged:alpine\nCOPY . /usr/share/nginx/html\nEXPOSE 8080\n"),
				Files:      []string{"index.html", "style.css"},
			},
			notWant:  []string{"no_dockerignore", "env_file_in_context"},
			wantOK:   true,
			wantPort: 8080,
		},
		{
			name:     "heredoc RUN parses",
			in:       CheckInput{Dockerfile: []byte("FROM alpine:3.22\nRUN <<EOT\necho hi\nEOT\nUSER a\nEXPOSE 80\n")},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "BOM and CRLF",
			in:       CheckInput{Dockerfile: []byte("\xef\xbb\xbfFROM alpine:3.22\r\nUSER a\r\nEXPOSE 80\r\n")},
			wantOK:   true,
			wantPort: 80,
		},
		{
			name:     "port from a variable default",
			in:       CheckInput{Dockerfile: []byte("FROM nginx:1.27-alpine\nEXPOSE ${PORT:-8080}\n")},
			wantOK:   true,
			notWant:  []string{"port_unknown"},
			wantPort: 8080,
		},
		{
			name:     "ENV with a default, then EXPOSE $PORT",
			in:       CheckInput{Dockerfile: []byte("FROM nginx:1.27-alpine\nENV PORT=${P:-3000}\nEXPOSE $PORT\n")},
			wantOK:   true,
			notWant:  []string{"port_unknown"},
			wantPort: 3000,
		},
		{
			name:   "unknown variable without default stays unknown",
			in:     CheckInput{Dockerfile: []byte("FROM nginx:1.27-alpine\nEXPOSE $NOPE\n")},
			want:   []string{"port_unknown"},
			wantOK: false,
		},
		{
			name:   "forbidden base hidden in a variable default",
			in:     CheckInput{Dockerfile: []byte("FROM ${B:-bitploy/other:1}\nEXPOSE 80\n")},
			want:   []string{"base_image_forbidden"},
			wantOK: false,
		},
		{
			name:   "cache mount is refused",
			in:     CheckInput{Dockerfile: []byte("FROM node:24-alpine\nRUN --mount=type=cache,target=/root/.npm npm ci\nUSER node\nEXPOSE 3000\n")},
			want:   []string{"cache_mount_forbidden"},
			line:   map[string]int{"cache_mount_forbidden": 2},
			wantOK: false,
		},
		{
			name:   "cache mount with an id and capitals is refused",
			in:     CheckInput{Dockerfile: []byte("FROM node:24-alpine\nRUN --mount=target=/x,TYPE=Cache,id=shared true\nUSER node\nEXPOSE 3000\n")},
			want:   []string{"cache_mount_forbidden"},
			wantOK: false,
		},
		{
			name:   "cache mount type from a variable is refused",
			in:     CheckInput{Dockerfile: []byte("FROM node:24-alpine\nARG T=cache\nRUN --mount=type=$T,target=/x true\nUSER node\nEXPOSE 3000\n")},
			want:   []string{"cache_mount_forbidden"},
			wantOK: false,
		},
		{
			name:     "bind, tmpfs and secret mounts are fine",
			in:       CheckInput{Dockerfile: []byte("FROM node:24-alpine AS b\nRUN true\nFROM node:24-alpine\nRUN --mount=type=bind,from=b,source=/etc,target=/b --mount=type=tmpfs,target=/t --mount=type=secret,id=s true\nUSER node\nEXPOSE 3000\n")},
			notWant:  []string{"cache_mount_forbidden"},
			wantOK:   true,
			wantPort: 3000,
		},
		{
			name:   "forbidden COPY --from hidden in a variable default",
			in:     CheckInput{Dockerfile: []byte("FROM nginx:1.27-alpine\nCOPY --from=${S:-bitploy/other:1} /x /y\nEXPOSE 80\n")},
			want:   []string{"base_image_forbidden"},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckDockerfile(tc.in)
			got := codes(res)
			for _, c := range tc.want {
				if _, ok := got[c]; !ok {
					t.Errorf("missing finding %s; got %+v", c, res.Findings)
				}
			}
			for _, c := range tc.notWant {
				if f, ok := got[c]; ok {
					t.Errorf("unexpected finding %s: %+v", c, f)
				}
			}
			if res.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v; findings %+v", res.OK, tc.wantOK, res.Findings)
			}
			if tc.wantOK && tc.wantPort != 0 && res.Port != tc.wantPort {
				t.Errorf("Port = %d, want %d", res.Port, tc.wantPort)
			}
			if res.HasHealthcheck != tc.wantHC {
				t.Errorf("HasHealthcheck = %v, want %v", res.HasHealthcheck, tc.wantHC)
			}
			for c, l := range tc.line {
				if got[c].Line != l {
					t.Errorf("%s line = %d, want %d (%s)", c, got[c].Line, l, got[c].Message)
				}
			}
			for _, f := range res.Findings {
				if f.Message == "" || (f.Severity != "info" && f.Hint == "") {
					t.Errorf("finding %s needs a message and a hint: %+v", f.Code, f)
				}
			}
		})
	}
}

func TestFindingsSortedBySeverity(t *testing.T) {
	res := CheckDockerfile(CheckInput{Dockerfile: []byte("FROM node\nEXPOSE 80 81\n")})
	rank := map[string]int{"error": 0, "warning": 1, "info": 2}
	for i := 1; i < len(res.Findings); i++ {
		if rank[res.Findings[i-1].Severity] > rank[res.Findings[i].Severity] {
			t.Fatalf("not sorted: %+v", res.Findings)
		}
	}
	if len(res.Findings) < 3 {
		t.Fatalf("expected several findings, got %+v", res.Findings)
	}
}

func TestEveryTemplatePassesTheChecks(t *testing.T) {
	for _, s := range templates.Stacks {
		tpl, _ := templates.Get(s)
		res := CheckDockerfile(CheckInput{Dockerfile: []byte(tpl.Dockerfile), Dockerignore: []byte(tpl.Dockerignore),
			Files: []string{"Dockerfile", ".dockerignore", ".env", "x"}})
		if !res.OK {
			t.Errorf("%s: template has errors: %+v", s, res.Findings)
		}
		for _, f := range res.Findings {
			if f.Severity == "warning" {
				t.Errorf("%s: template has a warning: %+v", s, f)
			}
		}
	}
}

func TestDetectStack(t *testing.T) {
	tests := []struct {
		files []string
		want  string
	}{
		{nil, ""},
		{[]string{"index.html"}, "static"},
		{[]string{"index.html", "css/site.css", "img/a.png"}, "static"},
		{[]string{"index.html", "package.json"}, "node"},
		{[]string{"package.json", "server.js"}, "node"},
		{[]string{"requirements.txt", "app.py"}, "python"},
		{[]string{"pyproject.toml"}, "python"},
		{[]string{"go.mod", "main.go", "package.json"}, "go"},
		{[]string{"composer.json"}, "php"},
		{[]string{"index.php", "index.html"}, "php"},
		{[]string{"./index.html"}, "static"},
		{[]string{"sub/index.html"}, ""},
		{[]string{"README.md"}, ""},
		{[]string{"Index.HTML"}, "static"},
		{[]string{"Planning tool v3.html"}, "static"},
		{[]string{"report.htm", "a.png"}, "static"},
		{[]string{"tool.html", "package.json"}, "node"},
		{[]string{"artisan", "composer.json", "package.json"}, "laravel"},
		{[]string{"manage.py", "requirements.txt"}, "django"},
		{[]string{"next.config.mjs", "package.json"}, "nextjs"},
		{[]string{"next.config.ts", "package.json"}, "nextjs"},
		{[]string{"astro.config.mjs", "package.json"}, "node-static"},
		{[]string{"src/next.config.js", "package.json"}, "node"},
	}
	for _, tc := range tests {
		if got := DetectStack(tc.files); got != tc.want {
			t.Errorf("DetectStack(%v) = %q, want %q", tc.files, got, tc.want)
		}
	}
}

func TestFirstErrorMissingIncludesTemplate(t *testing.T) {
	files := []string{"index.html", "about.html"}
	res := CheckDockerfile(CheckInput{Files: files, DockerfilePath: "Dockerfile"})
	e := FirstError(res, files)
	if e == nil {
		t.Fatal("expected error")
	}
	tpl, _ := templates.Get("static")
	if e.Code != "dockerfile_missing" || e.Next != "get_template" || e.HTTPStatus != 422 {
		t.Errorf("unexpected error %+v", e)
	}
	if !strings.Contains(e.Hint, tpl.Dockerfile) || !strings.Contains(e.Hint, tpl.Dockerignore) {
		t.Errorf("hint lacks template text:\n%s", e.Hint)
	}
	if e.Details["dockerfile"] != tpl.Dockerfile || e.Details["dockerignore"] != tpl.Dockerignore || e.Details["stack"] != "static" {
		t.Errorf("details = %+v", e.Details)
	}
	if !strings.Contains(e.Message, "static") && !strings.Contains(e.Message, "website") {
		t.Errorf("message should name the stack: %s", e.Message)
	}
}

func TestFirstErrorUnknownStackFallsBackToGeneric(t *testing.T) {
	res := CheckDockerfile(CheckInput{Files: []string{"main.rb"}})
	e := FirstError(res, []string{"main.rb"})
	if e == nil || e.Details["template"] != "generic" {
		t.Fatalf("got %+v", e)
	}
}

func TestFirstErrorOtherCodes(t *testing.T) {
	res := CheckDockerfile(CheckInput{Dockerfile: []byte("FROM alpine:3.22\nBOGUS x\n")})
	e := FirstError(res, nil)
	if e == nil || e.Code != "dockerfile_parse_error" || e.Next != "validate_dockerfile" || e.Details["line"] != 2 {
		t.Fatalf("got %+v", e)
	}
	if FirstError(CheckDockerfile(CheckInput{Dockerfile: []byte("FROM alpine:3.22\nUSER a\nEXPOSE 80\n")}), nil) != nil {
		t.Error("clean Dockerfile must give nil")
	}
}

func TestImageTooLargeFinding(t *testing.T) {
	f := ImageTooLargeFinding(3 << 30)
	if f.Code != "image_too_large" || f.Severity != "error" || f.Hint == "" {
		t.Errorf("%+v", f)
	}
}

func TestNewFindings(t *testing.T) {
	const tail = "\nUSER node\nEXPOSE 3000\nCMD [\"node\",\"x.js\"]\n"
	tests := []struct {
		name   string
		df     string
		files  []string
		want   string // code that must be present; "" = none of the three
		line   int
		inHint string
	}{
		{"compose yml", "FROM node:24-alpine" + tail, []string{"docker-compose.yml", "server.js"}, "compose_ignored", 0, "SQLite"},
		{"compose yaml", "FROM node:24-alpine" + tail, []string{"compose.yaml"}, "compose_ignored", 0, ""},
		{"compose in subfolder", "FROM node:24-alpine" + tail, []string{"deploy/docker-compose.yml"}, "", 0, ""},
		{"apt postgres", "FROM node:24\nRUN apt-get update && apt-get install -y --no-install-recommends postgresql redis-server" + tail, nil, "database_server_in_image", 2, "SQLite"},
		{"apk mariadb", "FROM alpine:3.22\nRUN apk add --no-cache mariadb mariadb-client" + tail, nil, "database_server_in_image", 2, ""},
		{"dnf mysql in later stage", "FROM node:24 AS b\nFROM debian:12\nRUN dnf install -y mysql-server" + tail, nil, "database_server_in_image", 3, ""},
		{"heredoc", "FROM debian:12\nRUN <<EOT\napt-get update\napt-get install -y postgresql-16\nEOT" + tail, nil, "database_server_in_image", 2, ""},
		{"clients are fine", "FROM node:24\nRUN apt-get update && apt-get install -y libpq-dev postgresql-client default-libmysqlclient-dev mysql-client libhiredis-dev redis-tools" + tail, nil, "", 0, ""},
		{"php extension is fine", "FROM php:8.4-cli\nRUN docker-php-ext-install pdo_pgsql pdo_mysql" + tail, nil, "", 0, ""},
		{"alpine postgres with a version", "FROM alpine:3.22\nRUN apk add --no-cache postgresql17" + tail, nil, "database_server_in_image", 2, ""},
		{"debian default mysql", "FROM debian:12\nRUN apt-get install -y default-mysql-server" + tail, nil, "database_server_in_image", 2, ""},
		{"pecl redis extension is fine", "FROM php:8.4-apache\nRUN pecl install redis && docker-php-ext-enable redis" + tail, nil, "", 0, ""},
		{"build deps are fine", "FROM node:24-alpine\nRUN apk add --no-cache --virtual .build-deps python3 make g++ libmariadb-dev sqlite" + tail, nil, "", 0, ""},
		{"alpine client is fine", "FROM alpine:3.22\nRUN apk add --no-cache postgresql-libs postgresql-dev" + tail, nil, "", 0, ""},
		{"pnpm lock", "FROM node:24-alpine\nCOPY package.json ./\nRUN npm ci" + tail, []string{"package.json", "pnpm-lock.yaml"}, "lockfile_mismatch", 3, "pnpm install --frozen-lockfile"},
		{"yarn lock", "FROM node:24-alpine\nRUN npm install && npm ci" + tail, []string{"package.json", "yarn.lock"}, "lockfile_mismatch", 2, "yarn install --frozen-lockfile"},
		{"bun lock", "FROM node:24-alpine\nRUN npm ci" + tail, []string{"package.json", "bun.lockb"}, "lockfile_mismatch", 2, "bun"},
		{"npm lock present", "FROM node:24-alpine\nRUN npm ci" + tail, []string{"package.json", "package-lock.json", "pnpm-lock.yaml"}, "", 0, ""},
		{"no lock at all", "FROM node:24-alpine\nRUN npm ci" + tail, []string{"package.json"}, "no_lockfile", 2, "pin exact versions"},
		{"no lock, npm install", "FROM node:22-bookworm-slim\nCOPY . .\nRUN npm install --omit=dev" + tail, []string{"package.json", "server.js"}, "no_lockfile", 3, "lock file"},
		{"no lock, bun", "FROM oven/bun:1-slim\nRUN bun install" + tail, []string{"package.json", "index.ts"}, "no_lockfile", 2, "USER bun"},
		{"no lock, global tool only", "FROM node:22-bookworm-slim\nRUN npm install -g serve" + tail, []string{"package.json"}, "", 0, ""},
		{"no lock, nothing installed", "FROM nginx:1.29-alpine\nCOPY dist /usr/share/nginx/html" + tail, []string{"package.json", "dist/index.html"}, "", 0, ""},
		{"lock in a sub-folder only", "FROM node:22-bookworm-slim\nRUN yarn" + tail, []string{"package.json", "web/yarn.lock"}, "no_lockfile", 2, ""},
		{"bun lock", "FROM oven/bun:1-slim\nRUN bun install --frozen-lockfile" + tail, []string{"package.json", "bun.lock"}, "", 0, ""},
		{"pnpm used properly", "FROM node:24-alpine\nRUN corepack enable && pnpm install --frozen-lockfile" + tail, []string{"package.json", "pnpm-lock.yaml"}, "", 0, ""},
	}
	all := []string{"compose_ignored", "database_server_in_image", "lockfile_mismatch", "no_lockfile"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckDockerfile(CheckInput{Dockerfile: []byte(tc.df), Files: tc.files})
			got := codes(res)
			for _, c := range all {
				f, has := got[c]
				if has != (c == tc.want) {
					t.Fatalf("%s present=%v, want %v: %+v", c, has, c == tc.want, res.Findings)
				}
				if has {
					if f.Severity != "warning" || f.Hint == "" {
						t.Errorf("bad finding %+v", f)
					}
					if tc.line > 0 && f.Line != tc.line {
						t.Errorf("line = %d, want %d", f.Line, tc.line)
					}
					if !strings.Contains(f.Hint, tc.inHint) {
						t.Errorf("hint lacks %q: %s", tc.inHint, f.Hint)
					}
				}
			}
			if !res.OK {
				t.Errorf("warnings must not block: %+v", res.Findings)
			}
		})
	}
}

func TestFirstErrorNewStacks(t *testing.T) {
	for stack, name := range map[string]string{"laravel": "Laravel", "django": "Django", "nextjs": "Next.js", "node-static": "Node"} {
		if got := describeStack(stack); !strings.Contains(got, name) {
			t.Errorf("describeStack(%q) = %q", stack, got)
		}
	}
}

func TestNativeModuleOnAlpine(t *testing.T) {
	const tail = "\nUSER node\nEXPOSE 3000\nCMD [\"node\",\"x.js\"]\n"
	sqlite := `{"dependencies": {"express": "^4.21.0", "better-sqlite3": "^11.3.0", "sharp": "0.33.5"}, "devDependencies": {"bcrypt": "5.1.1"}}`
	tests := []struct {
		name string
		df   string
		pkg  string
		want bool
		line int
		in   string // in the message
	}{
		{"alpine", "FROM node:24-alpine\nCOPY package*.json ./\nRUN npm ci" + tail, sqlite, true, 1, "better-sqlite3, bcrypt"},
		{"alpine build stage", "FROM node:24-alpine AS build\nRUN npm ci\nFROM node:22-bookworm-slim\nCOPY --from=build /app /app" + tail, sqlite, true, 1, "node:24-alpine"},
		{"stage built on an alpine stage", "ARG V=24\nFROM node:${V}-alpine AS base\nFROM base AS deps\nRUN yarn install --frozen-lockfile" + tail, sqlite, true, 3, "node:24-alpine"},
		{"bun on alpine", "FROM oven/bun:1-alpine\nRUN bun install" + tail, `{"dependencies": {"bcrypt": "5.1.1"}}`, true, 1, "bcrypt"},
		{"debian", "FROM node:22-bookworm-slim\nRUN npm ci" + tail, sqlite, false, 0, ""},
		{"sharp has alpine builds", "FROM node:24-alpine\nRUN npm ci" + tail, `{"dependencies": {"sharp": "0.33.5"}}`, false, 0, ""},
		{"alpine without native modules", "FROM node:24-alpine\nRUN npm ci" + tail, `{"dependencies": {"express": "^4.21.0"}}`, false, 0, ""},
		{"alpine final stage installs nothing", "FROM node:22-bookworm-slim AS build\nRUN npm ci\nFROM node:24-alpine\nCOPY --from=build /app /app" + tail, sqlite, false, 0, ""},
		{"package.json unknown", "FROM node:24-alpine\nRUN npm ci" + tail, "", false, 0, ""},
		{"package.json unreadable", "FROM node:24-alpine\nRUN npm ci" + tail, "{not json", false, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := CheckInput{Dockerfile: []byte(tc.df)}
			if tc.pkg != "" {
				in.PackageJSON = []byte(tc.pkg)
			}
			res := CheckDockerfile(in)
			f, has := codes(res)[CodeNativeOnAlpine]
			if has != tc.want {
				t.Fatalf("present=%v, want %v: %+v", has, tc.want, res.Findings)
			}
			if !has {
				return
			}
			if f.Severity != "warning" || f.Line != tc.line || !strings.Contains(f.Message, tc.in) ||
				!strings.Contains(f.Hint, "node:22-bookworm-slim") || strings.Contains(f.Hint, "apk add --no-cache") {
				t.Errorf("finding %+v", f)
			}
			if !res.OK {
				t.Errorf("a warning must not block: %+v", res.Findings)
			}
		})
	}
}
