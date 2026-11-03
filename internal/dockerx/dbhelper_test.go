package dockerx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDBHelperBinChecks(t *testing.T) {
	dir := t.TempDir()
	notElf := filepath.Join(dir, "mac")
	if err := os.WriteFile(notElf, []byte("\xcf\xfa\xed\xfe not linux"), 0o755); err != nil {
		t.Fatal(err)
	}
	elf := filepath.Join(dir, "linux")
	if err := os.WriteFile(elf, []byte("\x7fELF rest of the file"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{DBHelperBin: filepath.Join(dir, "missing")}
	if _, err := c.dbHelperBin(); !errors.Is(err, ErrNoDBHelper) {
		t.Errorf("missing file: %v", err)
	}
	c.DBHelperBin = notElf
	if _, err := c.dbHelperBin(); !errors.Is(err, ErrNoDBHelper) {
		t.Errorf("not a Linux program: %v", err)
	}
	c.DBHelperBin = elf
	if p, err := c.dbHelperBin(); err != nil || p != elf {
		t.Errorf("elf: %q %v", p, err)
	}
	if runtime.GOOS != "linux" {
		c.DBHelperBin = ""
		if _, err := c.dbHelperBin(); !errors.Is(err, ErrNoDBHelper) {
			t.Errorf("no setting on %s: %v", runtime.GOOS, err)
		}
	}
	// The tag follows the content of the binary, not its name.
	a, _ := fileHash(elf)
	b, _ := fileHash(notElf)
	if a == "" || a == b || len(a) != 64 {
		t.Errorf("hashes %q %q", a, b)
	}
}

func TestLimitWriter(t *testing.T) {
	var b bytes.Buffer
	w := &limitWriter{w: &b, left: 5}
	for _, s := range []string{"ab", "cdef", "gh"} {
		if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	if b.String() != "abcde" {
		t.Errorf("kept %q", b.String())
	}
}
