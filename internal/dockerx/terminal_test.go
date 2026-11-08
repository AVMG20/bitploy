package dockerx

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestReadPIDMarker(t *testing.T) {
	for _, tc := range []struct {
		in   string
		pid  int
		rest string
	}{
		{"\x1b]bitploy-pid;42\a$ ", 42, "$ "},
		{"\x1b]bitploy-pid;1234567\aroot@x:/# ", 1234567, "root@x:/# "},
		// No marker: the output stays as it is (a container without /bin/sh).
		{"OCI runtime exec failed", 0, "OCI runtime exec failed"},
		{"\x1b]bitploy-pid;12x\a$ ", 0, "\x1b]bitploy-pid;12x\a$ "},
		{"\x1b]bitploy-pid;\a$ ", 0, "\x1b]bitploy-pid;\a$ "},
		{"\x1b]bitploy-pid;123456789012345\a", 0, "\x1b]bitploy-pid;123456789012345\a"},
		{"\x1b]bitploy-pid;12", 0, "\x1b]bitploy-pid;12"},
		{"", 0, ""},
	} {
		r := bufio.NewReader(strings.NewReader(tc.in))
		pid := readPIDMarker(r)
		rest, _ := io.ReadAll(r)
		if pid != tc.pid || string(rest) != tc.rest {
			t.Errorf("readPIDMarker(%q) = %d, rest %q; want %d, %q", tc.in, pid, rest, tc.pid, tc.rest)
		}
	}
}
