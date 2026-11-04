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
