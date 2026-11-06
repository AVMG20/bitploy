package deploy

import (
	"strings"
	"testing"
)

func TestBuildArgsComposeArgsAndTarget(t *testing.T) {
	b := BuildSpec{ContextDir: "/src", Dockerfile: "/src/Dockerfile", Tag: "bitploy/x_web:3",
		Args: map[string]string{"B": "2", "A": "1"}, Target: "runner"}
	got := strings.Join(b.BuildArgs(), " ")
	if !strings.HasSuffix(got, "--build-arg A=1 --build-arg B=2 --target runner /src") {
		t.Fatalf("%s", got)
	}
}

func TestBuildOutOfMemory(t *testing.T) {
	oom := []string{
		`ERROR: failed to build: failed to solve: ResourceExhausted: process "/bin/sh -c bun run build" did not complete successfully: cannot allocate memory`,
		`ERROR: process "/bin/sh -c npm run build" did not complete successfully: exit code: 137`,
	}
	for _, l := range oom {
		if !BuildOutOfMemory([]string{"#10 building", l}) {
			t.Errorf("not seen as out of memory: %s", l)
		}
	}
	if BuildOutOfMemory([]string{`ERROR: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1`}) {
		t.Error("exit code 1 seen as out of memory")
	}
}

func TestBuildFailureHint(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string // all must be in the hint; nil means no hint
	}{
		{"go toolchain", []string{"#12 [build 4/6] RUN go mod tidy", "#12 1.2 go: modernc.org/sqlite@v1.61.1 requires go >= 1.25.0 (running go 1.24.5; GOTOOLCHAIN=local)"},
			[]string{"Go 1.25 or newer", "golang:1.25-alpine", "GOTOOLCHAIN=auto", "go.sum"}},
		{"go.mod itself", []string{"go: go.mod requires go >= 1.27 (running go 1.26.1; GOTOOLCHAIN=local)"}, []string{"Go 1.27 or newer"}},
		{"native module", []string{"#9 12.3 npm error gyp ERR! find Python", "#9 12.4 npm error gyp ERR! stack Error: Could not find any Python installation to use"},
			[]string{"node:22-bookworm-slim", "-alpine", "apk add"}},
		{"prebuild", []string{"#9 3.1 prebuild-install warn install No prebuilt binaries found (target=24.0.0 runtime=node arch=x64 libc=musl platform=linux)"}, []string{"better-sqlite3"}},
		{"node user on bun", []string{"#8 0.2 chown: invalid user: 'node:node'"}, []string{"USER bun", "chown bun:bun"}},
		{"anything else", []string{"#7 ERROR: failed to compute cache key: \"/app/dist\": not found"}, nil},
	}
	for _, tc := range tests {
		got := BuildFailureHint(tc.lines)
		if tc.want == nil {
			if got != "" {
				t.Errorf("%s: unexpected hint %q", tc.name, got)
			}
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: hint lacks %q: %s", tc.name, w, got)
			}
		}
		// get_deployment shows a hint as the fix only when it is one short line.
		if len(got) > 300 || strings.Contains(got, "\n") {
			t.Errorf("%s: hint is %d characters", tc.name, len(got))
		}
		if strings.Contains(got, "apk add --no-cache python3") {
			t.Errorf("%s: hint steers to compilers on Alpine: %s", tc.name, got)
		}
	}
}

func TestBuildFailureSummary(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name: "the compiler message, not the docker wrapper",
			lines: []string{
				"#7 [build 5/6] RUN apt-get install -y curl",
				"#7 1.200 debconf: unable to initialize frontend: Dialog",
				"#7 DONE 2.1s",
				"#9 [build 6/6] RUN npm run build",
				"#9 0.499 > vite build",
				"#9 3.950 error during build:",
				`#9 3.950 src/pages/Checkout.tsx (14:9): "formatPrice" is not exported by "src/lib/money.ts", imported by "src/pages/Checkout.tsx".`,
				"#9 3.951     at getRollupError (file:///app/node_modules/rollup/dist/es/shared/parseAst.js:395:41)",
				`#9 ERROR: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1`,
				"------",
				" > [build 6/6] RUN npm run build:",
				"------",
				"Dockerfile:11",
				`ERROR: failed to solve: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1`,
			},
			want: `src/pages/Checkout.tsx (14:9): "formatPrice" is not exported by "src/lib/money.ts", imported by "src/pages/Checkout.tsx".`,
		},
		{
			name: "a typescript error",
			lines: []string{
				"#8 [4/5] RUN npx tsc",
				"#8 2.1 src/app.ts(3,5): error TS2304: Cannot find name 'foo'.",
				"#8 2.2 npm error code 2",
				`#8 ERROR: process "/bin/sh -c npx tsc" did not complete successfully: exit code: 2`,
			},
			want: "src/app.ts(3,5): error TS2304: Cannot find name 'foo'.",
		},
		{
			name: "the last output of the step when no line says error",
			lines: []string{
				"#6 [3/4] RUN go build -o /app .",
				"#6 0.9 # example.com/app",
				"#6 0.9 ./main.go:5:2: x declared and not used",
				`#6 ERROR: process "/bin/sh -c go build -o /app ." did not complete successfully: exit code: 1`,
			},
			want: "./main.go:5:2: x declared and not used",
		},
		{
			name:  "a dockerfile mistake is the error itself",
			lines: []string{"#1 [internal] load build definition from Dockerfile", "ERROR: failed to solve: dockerfile parse error on line 3: unknown instruction: RUNN"},
			want:  "ERROR: failed to solve: dockerfile parse error on line 3: unknown instruction: RUNN",
		},
		{name: "only the wrapper", lines: []string{"ERROR: failed to solve"}, want: "ERROR: failed to solve"},
		{name: "nothing", lines: nil, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuildFailureSummary(c.lines); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
	long := BuildFailureSummary([]string{"error: " + strings.Repeat("x", 400)})
	if n := len([]rune(long)); n != 300 {
		t.Errorf("long line has %d runes", n)
	}
}
