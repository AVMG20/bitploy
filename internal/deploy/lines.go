package deploy

import (
	"bufio"
	"io"
	"strings"
)

// LineRing keeps the last N lines written to it.
type LineRing struct {
	max   int
	lines []string
	start int
}

func NewLineRing(n int) *LineRing { return &LineRing{max: n} }

func (r *LineRing) Add(line string) {
	if len(r.lines) < r.max {
		r.lines = append(r.lines, line)
		return
	}
	r.lines[r.start] = line
	r.start = (r.start + 1) % r.max
}

// Lines returns the kept lines, oldest first.
func (r *LineRing) Lines() []string {
	out := make([]string, 0, len(r.lines))
	out = append(out, r.lines[r.start:]...)
	out = append(out, r.lines[:r.start]...)
	return out
}

// TailLines returns the last n lines of s.
func TailLines(s string, n int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// ReadLines calls fn for every line of r. Lines longer than max bytes are
// split, so a binary blob without newlines cannot exhaust memory.
func ReadLines(r io.Reader, max int, fn func(string)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var cur []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			if len(cur) > 0 {
				fn(string(cur))
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
		cur = append(cur, chunk...)
		if !isPrefix || len(cur) >= max {
			fn(string(cur))
			cur = cur[:0]
		}
	}
}
