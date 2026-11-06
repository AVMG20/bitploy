package dockerx

import (
	"strings"
	"testing"
)

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &TailBuffer{Max: 8}
	for _, s := range []string{"abc", "defgh", "ijklmnop", "q"} {
		if n, err := tb.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	if got := tb.String(); got != "jklmnopq" {
		t.Errorf("tail = %q", got)
	}
	big := &TailBuffer{Max: 4}
	_, _ = big.Write([]byte(strings.Repeat("x", 100) + "end!"))
	if got := big.String(); got != "end!" {
		t.Errorf("tail = %q", got)
	}
}

func TestExecErrorText(t *testing.T) {
	if got := (&ExecError{Code: 2}).Error(); got != "the command exited with code 2" {
		t.Error(got)
	}
	if got := (&ExecError{Code: 1, Tail: "boom"}).Error(); !strings.HasSuffix(got, ": boom") {
		t.Error(got)
	}
}
