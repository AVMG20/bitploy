package traffic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
)

// Tailer reads new lines from a file that another process (Traefik) keeps
// appending to. Traefik cannot rotate its access log, so the Tailer keeps the
// file small itself: once it has read everything and the file is past
// TruncateAt, it truncates it. Traefik opens the file with O_APPEND, so its
// next write simply lands at the new start.
type Tailer struct {
	Path       string
	TruncateAt int64 // truncate after reading when the file is at least this big
	SkipOver   int64 // at the first poll, a file bigger than this is mostly skipped
	SkipKeep   int64 // ...keeping only this many bytes from its end
	MaxRead    int64 // most bytes read per poll; the rest waits for the next one

	resume  *Position // where to continue at the first poll
	started bool
	offset  int64
	pending []byte // an unfinished last line
	skip    bool   // drop up to the first newline (we started in the middle of a line)
	info    fs.FileInfo
}

// NewTailer returns a Tailer with the sizes bitploy uses.
func NewTailer(path string) *Tailer {
	return &Tailer{Path: path, TruncateAt: 4 << 20, SkipOver: 16 << 20, SkipKeep: 2 << 20, MaxRead: 8 << 20}
}

// maxLine is the longest line kept while waiting for its newline.
const maxLine = 64 << 10

// Poll calls fn for every complete line added since the last call. It returns
// a non-nil error only for real failures; a missing file is not one. A failed
// truncation is returned after the lines were handled.
func (t *Tailer) Poll(fn func(line []byte)) error {
	f, err := os.Open(t.Path)
	if errors.Is(err, fs.ErrNotExist) {
		t.reset()
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	switch {
	case !t.started:
		t.started = true
		if p := t.resume; p != nil && p.Offset > 0 && p.Offset <= st.Size() && p.matches(f) {
			t.offset = p.Offset
		}
		t.resume = nil
		if st.Size()-t.offset > t.SkipOver {
			t.offset, t.skip = st.Size()-t.SkipKeep, true
		}
	case !os.SameFile(t.info, st) || st.Size() < t.offset:
		t.reset() // replaced or truncated by someone else: start over
		t.started = true
	}
	t.info = st

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, t.MaxRead))
	if err != nil {
		return err
	}
	t.offset += int64(len(data))
	t.consume(data, fn)

	if t.offset >= t.TruncateAt {
		// Wait for a moment when nothing was added since we read (those lines would be lost),
		// unless the file keeps growing and gets far too big.
		now, err := f.Stat()
		quiet := err == nil && now.Size() == t.offset && len(t.pending) == 0
		if quiet || t.offset >= 16*t.TruncateAt {
			if err := os.Truncate(t.Path, 0); err != nil {
				return err
			}
			t.offset, t.pending = 0, nil
		}
	}
	return nil
}

// headBytes is how much of the start of the file identifies it.
const headBytes = 256

// Position is how far the Tailer has handled the file, as bytes up to the end of
// the last complete line, plus a hash of the start of the file to tell it from a
// new file. Save it together with the counts it produced.
type Position struct {
	Offset  int64
	HeadLen int
	Head    string
}

func (p Position) matches(f *os.File) bool {
	h, ok := headHash(f, p.HeadLen)
	return ok && h == p.Head
}

func headHash(f *os.File, n int) (string, bool) {
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return "", false
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), true
}

// Position reports how far the file has been handled (false when it cannot tell).
func (t *Tailer) Position() (Position, bool) {
	off := t.offset - int64(len(t.pending))
	if off <= 0 {
		return Position{}, true
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return Position{}, false
	}
	defer f.Close()
	n := int(min(off, headBytes))
	h, ok := headHash(f, n)
	return Position{Offset: off, HeadLen: n, Head: h}, ok
}

// Resume makes the first Poll continue at p instead of the start, if the file
// is still the one p was taken from.
func (t *Tailer) Resume(p Position) { t.resume = &p }

func (t *Tailer) reset() {
	t.started, t.offset, t.pending, t.skip, t.info = false, 0, nil, false, nil
}

func (t *Tailer) consume(data []byte, fn func([]byte)) {
	if t.skip {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return
		}
		data, t.skip = data[i+1:], false
	}
	if len(t.pending) > 0 {
		data = append(t.pending, data...)
		t.pending = nil
	}
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if line := bytes.TrimSpace(data[:i]); len(line) > 0 {
			fn(line)
		}
		data = data[i+1:]
	}
	if len(data) > 0 && len(data) <= maxLine {
		t.pending = append([]byte(nil), data...)
	}
}
