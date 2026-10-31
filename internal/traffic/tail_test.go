package traffic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tailEnv struct {
	t    *testing.T
	path string
	tl   *Tailer
	got  []string
}

func newTailEnv(t *testing.T) *tailEnv {
	t.Helper()
	p := filepath.Join(t.TempDir(), "access.log")
	return &tailEnv{t: t, path: p, tl: &Tailer{Path: p, TruncateAt: 100, SkipOver: 1000, SkipKeep: 200, MaxRead: 1 << 20}}
}

// append writes like Traefik: O_APPEND.
func (e *tailEnv) append(s string) {
	e.t.Helper()
	f, err := os.OpenFile(e.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		e.t.Fatal(err)
	}
}

func (e *tailEnv) poll() []string {
	e.t.Helper()
	e.got = nil
	if err := e.tl.Poll(func(b []byte) { e.got = append(e.got, string(b)) }); err != nil {
		e.t.Fatal(err)
	}
	return e.got
}

func (e *tailEnv) size() int64 {
	st, err := os.Stat(e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	return st.Size()
}

func eq(got []string, want ...string) bool { return strings.Join(got, "|") == strings.Join(want, "|") }

func TestTailMissingFile(t *testing.T) {
	e := newTailEnv(t)
	if got := e.poll(); len(got) != 0 {
		t.Errorf("got %v", got)
	}
	e.append("a\n")
	if got := e.poll(); !eq(got, "a") {
		t.Errorf("after the file appeared: %v", got)
	}
}

func TestTailPartialLine(t *testing.T) {
	e := newTailEnv(t)
	e.tl.TruncateAt = 1 << 20
	e.append("one\ntw")
	if got := e.poll(); !eq(got, "one") {
		t.Errorf("first: %v", got)
	}
	if got := e.poll(); len(got) != 0 {
		t.Errorf("nothing new: %v", got)
	}
	e.append("o\nthree\nfo")
	if got := e.poll(); !eq(got, "two", "three") {
		t.Errorf("second: %v", got)
	}
	e.append("ur\n")
	if got := e.poll(); !eq(got, "four") {
		t.Errorf("third: %v", got)
	}
}

func TestTailTruncatesWhenBig(t *testing.T) {
	e := newTailEnv(t)
	line := strings.Repeat("x", 59) + "\n" // 60 bytes
	e.append(line)
	if got := e.poll(); len(got) != 1 || e.size() != 60 {
		t.Fatalf("below the limit: %d lines, size %d", len(got), e.size())
	}
	e.append(line)
	if got := e.poll(); len(got) != 1 {
		t.Fatalf("second poll: %d lines", len(got))
	}
	if e.size() != 0 {
		t.Fatalf("file not truncated: %d bytes", e.size())
	}
	// Traefik keeps appending to the same file; its next line lands at the start.
	e.append("after\n")
	if got := e.poll(); !eq(got, "after") {
		t.Errorf("after truncation: %v", got)
	}
}

func TestTailKeepsPartialLineInsteadOfTruncating(t *testing.T) {
	e := newTailEnv(t)
	e.append(strings.Repeat("x", 150) + "\nhal")
	e.poll()
	if e.size() == 0 {
		t.Fatal("truncated in the middle of a line")
	}
	e.append("f\n")
	if got := e.poll(); !eq(got, "half") {
		t.Errorf("got %v", got)
	}
	if e.size() != 0 {
		t.Errorf("not truncated once the line was complete: %d", e.size())
	}
}

func TestTailFileShrunkByOthers(t *testing.T) {
	e := newTailEnv(t)
	e.tl.TruncateAt = 1 << 20
	e.append("aaaa\nbbbb\n")
	e.poll()
	if err := os.WriteFile(e.path, []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := e.poll(); !eq(got, "c") {
		t.Errorf("got %v", got)
	}
}

func TestTailSkipsAheadInHugeFile(t *testing.T) {
	e := newTailEnv(t)
	var sb strings.Builder
	for i := 0; i < 100; i++ { // 100 lines of 20 bytes = 2000 bytes > SkipOver
		sb.WriteString(strings.Repeat("l", 19) + "\n")
	}
	e.append(sb.String())
	got := e.poll()
	// Only about the last 200 bytes are read, and no half line comes through.
	if len(got) == 0 || len(got) > 11 {
		t.Fatalf("%d lines read", len(got))
	}
	for _, l := range got {
		if len(l) != 19 {
			t.Errorf("half line %q", l)
		}
	}
}

func TestTailOverlongLineIsDropped(t *testing.T) {
	e := newTailEnv(t)
	e.tl.TruncateAt = 1 << 30
	e.append(strings.Repeat("y", maxLine+10))
	if got := e.poll(); len(got) != 0 {
		t.Fatalf("got %d lines", len(got))
	}
	if len(e.tl.pending) != 0 {
		t.Errorf("pending grew to %d", len(e.tl.pending))
	}
}
