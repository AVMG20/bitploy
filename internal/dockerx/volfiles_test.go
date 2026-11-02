package dockerx

import (
	"strings"
	"testing"
)

func TestParseListing(t *testing.T) {
	out := strings.Join([]string{
		"81a4 120 1700000000 /data/sub/app.db",
		"41ed 4096 1700000100 /data/sub/Zeta",
		"41ed 4096 1700000200 /data/sub/alpha",
		"a1ff 9 1700000300 /data/sub/link",
		"1180 0 1700000400 /data/sub/pipe",
		"81a4 5 1700000500 /data/sub/with space.txt",
		"81a4 7 1700000600 /data/sub/broken", // the next line is the rest of a name with a line break
		"name.txt",
		"81a4 x 1700000600 /data/sub/badsize",
		"81a4 1 1700000600 /data/other/outside",
		"",
	}, "\n")
	l := parseListing(out, "sub", 100)
	var got []string
	for _, e := range l.Entries {
		got = append(got, e.Kind+":"+e.Name)
	}
	// Folders first, then everything else by name without regard to case.
	want := "dir:alpha dir:Zeta file:app.db file:broken symlink:link other:pipe file:with space.txt"
	if strings.Join(got, " ") != want {
		t.Fatalf("entries = %q", got)
	}
	if l.Truncated {
		t.Error("truncated")
	}
	if e := l.Entries[2]; e.Size != 120 || e.Mtime.Unix() != 1700000000 {
		t.Errorf("app.db = %+v", e)
	}
	if e := l.Entries[0]; e.Size != 0 {
		t.Errorf("a folder has no size: %+v", e)
	}
}

func TestParseListingRootAndCap(t *testing.T) {
	out := "81a4 1 1 /data/a\n81a4 1 1 /data/b\n81a4 1 1 /data/c\n"
	l := parseListing(out, "", 2)
	if len(l.Entries) != 2 || !l.Truncated || l.Entries[0].Name != "a" {
		t.Errorf("listing = %+v", l)
	}
	if l := parseListing(out, "", 3); len(l.Entries) != 3 || l.Truncated {
		t.Errorf("listing = %+v", l)
	}
}
