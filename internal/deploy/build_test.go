package deploy

import "testing"

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
