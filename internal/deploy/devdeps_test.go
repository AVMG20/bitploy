package deploy

import (
	"strings"
	"testing"
)

func TestDevDepsInImage(t *testing.T) {
	const tail = "USER node\nEXPOSE 3000\nCMD [\"npm\", \"start\"]\n"
	const pkg = `{"scripts":{"build":"tsc","start":"node dist/index.js"},"dependencies":{"express":"5"},"devDependencies":{"typescript":"5"}}`
	tests := []struct {
		name string
		df   string
		pkg  string
		want int // line of the finding; 0: no finding
	}{
		{"one stage, full install", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm run build\n" + tail, pkg, 4},
		{"one stage, production install", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci --omit=dev\n" + tail, pkg, 0},
		{"NODE_ENV=production before the install", "FROM node:22-slim\nENV NODE_ENV=production\nWORKDIR /app\nCOPY . .\nRUN npm ci\n" + tail, pkg, 0},
		{"inline NODE_ENV=production", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN NODE_ENV=production npm ci\n" + tail, pkg, 0},
		{"pruned in the same RUN", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci && npm run build && npm prune --omit=dev\n" + tail, pkg, 0},
		{"rm -rf node_modules and a production install", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci && npm run build && rm -rf node_modules && npm ci --omit=dev\n" + tail, pkg, 0},
		{"plain npm prune keeps dev", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm prune\n" + tail, pkg, 4},
		{"pnpm and yarn", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN corepack enable && pnpm install --frozen-lockfile\n" + tail, pkg, 4},
		{"pnpm prune --prod", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN corepack enable && pnpm install --frozen-lockfile\nRUN pnpm build\nRUN pnpm prune --prod\n" + tail, pkg, 0},
		{"bare yarn", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN yarn\n" + tail, pkg, 4},
		{"bun production", "FROM oven/bun:1-slim\nWORKDIR /app\nCOPY . .\nRUN bun install --production\n" + tail, pkg, 0},
		{"global tool install only", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm install -g pm2\n" + tail, pkg, 0},
		{
			"whole app folder from a full build stage",
			"FROM node:22-slim AS build\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm run build\n\nFROM node:22-slim\nWORKDIR /app\nCOPY --from=build /app ./\n" + tail,
			pkg, 9,
		},
		{
			"whole app folder from a pruned build stage",
			"FROM node:22-slim AS build\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm run build\nRUN npm prune --omit=dev\n\nFROM node:22-slim\nWORKDIR /app\nCOPY --from=build /app ./\n" + tail,
			pkg, 0,
		},
		{
			"node_modules from a full build stage",
			"FROM node:22-slim AS build\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm run build\n\nFROM node:22-slim\nWORKDIR /app\nCOPY --from=build /app/dist ./dist\nCOPY --from=build /app/node_modules ./node_modules\n" + tail,
			pkg, 10,
		},
		{
			"only the build output",
			"FROM node:22-slim AS build\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm run build\n\nFROM node:22-slim\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --omit=dev\nCOPY --from=build /app/dist ./dist\n" + tail,
			pkg, 0,
		},
		{
			"a relative WORKDIR",
			"FROM node:22-slim AS build\nWORKDIR /srv\nWORKDIR app\nCOPY . .\nRUN npm ci\n\nFROM node:22-slim\nCOPY --from=build /srv/app/ /app\n" + tail,
			pkg, 8,
		},
		{
			"the last stage builds on a full stage",
			"FROM node:22-slim AS deps\nWORKDIR /app\nCOPY . .\nRUN npm ci\n\nFROM deps\nRUN npm run build\n" + tail,
			pkg, 4,
		},
		{"NODE_ENV from an ARG", "ARG NODE_ENV=production\nFROM node:22-slim\nARG NODE_ENV\nENV NODE_ENV=$NODE_ENV\nWORKDIR /app\nCOPY . .\nRUN npm ci\n" + tail, pkg, 0},
		{"--include=dev overrides NODE_ENV", "FROM node:22-slim\nENV NODE_ENV=production\nWORKDIR /app\nCOPY . .\nRUN npm ci --include=dev\n" + tail, pkg, 5},
		{"yarn --version is no install", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN yarn --version\n" + tail, pkg, 0},
		{"no devDependencies", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci\n" + tail, `{"scripts":{"start":"node index.js"},"dependencies":{"express":"5"}}`, 0},
		{"no package.json given", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci\n" + tail, "", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckDockerfile(CheckInput{Dockerfile: []byte(tc.df), DockerfilePath: "Dockerfile", AppType: "web", PackageJSON: []byte(tc.pkg)})
			f, has := codes(res)[CodeDevDepsInImage]
			if (tc.want != 0) != has {
				t.Fatalf("finding = %v, want line %d\n%s", has, tc.want, tc.df)
			}
			if has && (f.Line != tc.want || f.Severity != sevWarning) {
				t.Errorf("line %d severity %s, want line %d warning", f.Line, f.Severity, tc.want)
			}
		})
	}

	t.Run("the hint fits the framework", func(t *testing.T) {
		df := "FROM node:22-slim\nWORKDIR /app\nCOPY . .\nRUN npm ci\nRUN npm run build\n" + tail
		for pkg, want := range map[string]string{
			`{"devDependencies":{"nuxt":"4"}}`:                                ".output",
			`{"dependencies":{"next":"16"},"devDependencies":{"eslint":"9"}}`: "standalone",
			`{"devDependencies":{"@sveltejs/kit":"2"}}`:                       "node build",
			pkg: "npm prune --omit=dev",
		} {
			f := codes(CheckDockerfile(CheckInput{Dockerfile: []byte(df), DockerfilePath: "Dockerfile", AppType: "web", PackageJSON: []byte(pkg)}))[CodeDevDepsInImage]
			if !strings.Contains(f.Hint, want) {
				t.Errorf("%s: hint lacks %q: %s", pkg, want, f.Hint)
			}
		}
	})
}

func TestLargeImageFinding(t *testing.T) {
	if _, big := LargeImageFinding(400 << 20); big {
		t.Error("400 MB is not large")
	}
	f, big := LargeImageFinding(1900 << 20)
	if !big || f.Code != CodeLargeImage || f.Severity != sevWarning || !strings.Contains(f.Message, "1900 MB") {
		t.Errorf("%v %+v", big, f)
	}
}
