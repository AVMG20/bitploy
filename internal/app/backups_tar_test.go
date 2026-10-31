package app

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type tent struct {
	name, link string
	typ        byte
	body       string
	mode       int64
	size       int64 // header size when it differs from body
}

func buildTar(t *testing.T, ents ...tent) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: typ, Mode: e.mode, ModTime: time.Unix(1700000000, 0), Uid: 1000, Gid: 1000}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
			if e.size != 0 {
				h.Size = e.size
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sanitize(t *testing.T, lim tarLimits, ents ...tent) (tarStats, []*tar.Header, error) {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	st, err := sanitizeTar(tw, tar.NewReader(bytes.NewReader(buildTar(t, ents...))), lim)
	if err != nil {
		return st, nil, err
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var hs []*tar.Header
	tr := tar.NewReader(&out)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		hs = append(hs, h)
	}
	return st, hs, nil
}

func TestSanitizeTarAccepts(t *testing.T) {
	st, hs, err := sanitize(t, tarLimits{},
		tent{name: "./", typ: tar.TypeDir},
		tent{name: "./notes", typ: tar.TypeDir, mode: 0o755},
		tent{name: "./notes/a.txt", body: "hello"},
		tent{name: "b.txt", body: "world", mode: 0o4755}, // setuid must go
		tent{name: "notes/link", typ: tar.TypeSymlink, link: "a.txt"},
		tent{name: "notes/up", typ: tar.TypeSymlink, link: "../b.txt"},
		tent{name: "hard", typ: tar.TypeLink, link: "./b.txt"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.Entries != 6 || st.Bytes != 10 {
		t.Errorf("stats = %+v", st)
	}
	var names []string
	for _, h := range hs {
		names = append(names, h.Name)
		if h.Mode&0o6000 != 0 {
			t.Errorf("%s keeps setuid/setgid: %o", h.Name, h.Mode)
		}
		if h.Uid != 1000 || h.Gid != 1000 {
			t.Errorf("%s owner changed to %d:%d", h.Name, h.Uid, h.Gid)
		}
	}
	want := "notes/ notes/a.txt b.txt notes/link notes/up hard"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
	if hs[5].Linkname != "b.txt" {
		t.Errorf("hard link target = %q", hs[5].Linkname)
	}
}

func TestSanitizeTarRefuses(t *testing.T) {
	cases := map[string][]tent{
		"absolute path":        {{name: "/etc/passwd", body: "x"}},
		"dotdot":               {{name: "a/../../x", body: "x"}},
		"leading dotdot":       {{name: "../x", body: "x"}},
		"absolute symlink":     {{name: "l", typ: tar.TypeSymlink, link: "/etc/passwd"}},
		"escaping symlink":     {{name: "l", typ: tar.TypeSymlink, link: "../x"}},
		"deep escape":          {{name: "a/b/l", typ: tar.TypeSymlink, link: "../../../x"}},
		"dotdot after name":    {{name: "a/l", typ: tar.TypeSymlink, link: "b/../../x"}},
		"empty symlink":        {{name: "l", typ: tar.TypeSymlink}},
		"hardlink to nothing":  {{name: "h", typ: tar.TypeLink, link: "nope"}},
		"hardlink absolute":    {{name: "f", body: "x"}, {name: "h", typ: tar.TypeLink, link: "/f"}},
		"hardlink escape":      {{name: "h", typ: tar.TypeLink, link: "../f"}},
		"hardlink to a dir":    {{name: "d", typ: tar.TypeDir}, {name: "h", typ: tar.TypeLink, link: "d"}},
		"device":               {{name: "dev", typ: tar.TypeChar}},
		"block device":         {{name: "dev", typ: tar.TypeBlock}},
		"fifo":                 {{name: "p", typ: tar.TypeFifo}},
		"file below symlink":   {{name: "l", typ: tar.TypeSymlink, link: "."}, {name: "l/x", body: "x"}},
		"file below file":      {{name: "f", body: "x"}, {name: "f/x", body: "x"}},
		"duplicate":            {{name: "f", body: "x"}, {name: "f", body: "y"}},
		"root as a file":       {{name: ".", body: "x"}},
		"unsupported typeflag": {{name: "x", typ: tar.TypeGNUSparse}},
	}
	for name, ents := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := sanitize(t, tarLimits{}, ents...)
			var tp *tarProblem
			if !errors.As(err, &tp) {
				t.Fatalf("err = %v, want a tarProblem", err)
			}
			if strings.HasSuffix(tp.msg, "\n") || tp.msg == "" {
				t.Errorf("message = %q", tp.msg)
			}
		})
	}
}

func TestSanitizeTarSkips(t *testing.T) {
	// Reading a live volume: pipes and devices are left out instead of failing.
	st, hs, err := sanitize(t, tarLimits{SkipSpecial: true},
		tent{name: "p", typ: tar.TypeFifo},
		tent{name: "dev", typ: tar.TypeChar},
		tent{name: "ok", body: "x"},
		tent{name: "d/.wh.gone", body: "x"}, // Docker would read this as "delete gone"
		tent{name: "d/.wh..wh..opq", body: "x"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.Entries != 1 || len(hs) != 1 || hs[0].Name != "ok" {
		t.Errorf("stats %+v, headers %v", st, hs)
	}
}

func TestSanitizeTarDropsExtras(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{
		Name: "bin", Typeflag: tar.TypeReg, Size: 1, Mode: 0o755, Uname: "root", Gname: "root", Format: tar.FormatPAX,
		PAXRecords: map[string]string{"SCHILY.xattr.security.capability": "\x01\x00\x00\x02"},
	})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	var out bytes.Buffer
	ow := tar.NewWriter(&out)
	if _, err := sanitizeTar(ow, tar.NewReader(&buf), tarLimits{}); err != nil {
		t.Fatal(err)
	}
	_ = ow.Close()
	h, err := tar.NewReader(&out).Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(h.PAXRecords) != 0 || len(h.Xattrs) != 0 || h.Uname != "" || h.Gname != "" { //nolint:staticcheck // Xattrs is checked on purpose
		t.Errorf("extended attributes survived: %+v", h)
	}
}

func TestSanitizeTarLimits(t *testing.T) {
	_, _, err := sanitize(t, tarLimits{MaxBytes: 10}, tent{name: "a", body: "123456"}, tent{name: "b", body: "123456"})
	if !errors.Is(err, errTarTooBig) {
		t.Errorf("err = %v, want errTarTooBig", err)
	}
	if _, _, err := sanitize(t, tarLimits{MaxBytes: 12}, tent{name: "a", body: "123456"}, tent{name: "b", body: "123456"}); err != nil {
		t.Errorf("exactly at the limit: %v", err)
	}
	// A header that promises more than the body holds is a damaged archive.
	data := buildTar(t, tent{name: "a", body: "123456"})
	_, err = sanitizeTar(nil, tar.NewReader(bytes.NewReader(data[:515])), tarLimits{})
	var tp *tarProblem
	if !errors.As(err, &tp) {
		t.Errorf("truncated archive: %v", err)
	}
	// Not a tar at all.
	_, err = sanitizeTar(nil, tar.NewReader(strings.NewReader(strings.Repeat("hello world ", 100))), tarLimits{})
	if !errors.As(err, &tp) {
		t.Errorf("garbage: %v", err)
	}
}

// A reader that fails is not a damaged archive: the error passes through.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestSanitizeTarPassesReaderErrors(t *testing.T) {
	_, err := sanitizeTar(nil, tar.NewReader(failReader{}), tarLimits{})
	var tp *tarProblem
	if errors.As(err, &tp) || err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("err = %v", err)
	}
}

func TestCleanTarName(t *testing.T) {
	for in, want := range map[string]string{"a/b": "a/b", "./a//b/": "a/b", "./": "", ".": "", "a/./b": "a/b"} {
		if got, err := cleanTarName(in); err != nil || got != want {
			t.Errorf("cleanTarName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/a", "a/../b", "..", "a\x00b", strings.Repeat("a/", 3000)} {
		if _, err := cleanTarName(in); err == nil {
			t.Errorf("cleanTarName(%q) accepted", in)
		}
	}
}

func TestCheckSymlink(t *testing.T) {
	ok := [][2]string{{"l", "a/b"}, {"a/l", "../x"}, {"a/b/l", "../../x"}, {"a/l", "./x/./y"}, {"a/l", ".."}}
	for _, c := range ok {
		if err := checkSymlink(c[0], c[1]); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	bad := [][2]string{{"l", ".."}, {"a/l", "../.."}, {"l", "/x"}, {"a/l", "x/../.."}, {"l", ""}}
	for _, c := range bad {
		if err := checkSymlink(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}
