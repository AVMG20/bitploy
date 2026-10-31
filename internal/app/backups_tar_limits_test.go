package app

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeTarSizeCapDoesNotOverflow(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "a", Typeflag: tar.TypeReg, Mode: 0o644, Size: 100})
	_, _ = tw.Write(bytes.Repeat([]byte("x"), 100))
	// The header of a second entry that claims nearly 2^63 bytes; its body never comes.
	_ = tw.WriteHeader(&tar.Header{Name: "b", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1<<63 - 50})
	_, err := sanitizeTar(nil, tar.NewReader(&buf), tarLimits{MaxBytes: 100 << 20})
	if !errors.Is(err, errTarTooBig) {
		t.Errorf("err = %v, want errTarTooBig", err)
	}
}

// rawBlock makes one ustar header block by hand: Go's writer will not write
// the PAX records of a sparse file.
func rawBlock(name string, typ byte, size int) []byte {
	b := make([]byte, 512)
	copy(b[0:], name)
	copy(b[100:], "0000644")
	copy(b[108:], "0000000")
	copy(b[116:], "0000000")
	copy(b[124:], fmt.Sprintf("%011o", size))
	copy(b[136:], "00000000000")
	copy(b[148:], "        ")
	b[156] = typ
	copy(b[257:], "ustar")
	copy(b[263:], "00")
	sum := 0
	for _, c := range b {
		sum += int(c)
	}
	copy(b[148:], fmt.Sprintf("%06o", sum))
	b[154], b[155] = 0, ' '
	return b
}

// paxRecord is one "<length> key=value\n" record, the length counting itself.
func paxRecord(k, v string) string {
	rest := len(k) + len(v) + 3 // " ", "=" and "\n"
	n := rest + 1
	if len(fmt.Sprint(rest+len(fmt.Sprint(n)))) != len(fmt.Sprint(n)) {
		n++
	}
	return fmt.Sprintf("%d %s=%s\n", rest+len(fmt.Sprint(n)), k, v)
}

func TestSanitizeTarRefusesSparse(t *testing.T) {
	pax := paxRecord("GNU.sparse.major", "1") + paxRecord("GNU.sparse.minor", "0") +
		paxRecord("GNU.sparse.realsize", "4000000000000") + paxRecord("GNU.sparse.name", "sparse")
	var buf bytes.Buffer
	buf.Write(rawBlock("PaxHeaders/sparse", 'x', len(pax)))
	buf.WriteString(pax)
	buf.Write(make([]byte, (512-len(pax)%512)%512))
	buf.Write(rawBlock("sparse", '0', 512))
	buf.Write(make([]byte, 512))
	buf.Write(make([]byte, 1024))
	_, err := sanitizeTar(nil, tar.NewReader(&buf), tarLimits{MaxBytes: 100 << 20})
	if err == nil {
		t.Fatal("sparse archive accepted")
	}
	var tp *tarProblem
	if !errors.As(err, &tp) && !errors.Is(err, errTarTooBig) {
		t.Errorf("err = %v, want the archive refused", err)
	}
}

func TestSanitizeTarCountsSkippedEntries(t *testing.T) {
	// Left-out entries still use up the byte budget.
	_, _, err := sanitize(t, tarLimits{MaxBytes: 10, SkipSpecial: true},
		tent{name: "d/.wh.a", body: "123456"}, tent{name: "d/.wh.b", body: "123456"})
	if !errors.Is(err, errTarTooBig) {
		t.Errorf("err = %v", err)
	}
}

func TestSanitizeTarNameLength(t *testing.T) {
	long := strings.Repeat("a", 2000)
	if _, _, err := sanitize(t, tarLimits{}, tent{name: long, body: "x"}); err == nil {
		t.Error("a 2000 byte name was accepted from an upload")
	}
	if _, _, err := sanitize(t, tarLimits{Trusted: true}, tent{name: long, body: "x"}); err != nil {
		t.Errorf("trusted: %v", err)
	}
}

func TestSanitizeTarContextStopsTheCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := sanitizeTar(nil, tar.NewReader(bytes.NewReader(buildTar(t, tent{name: "a", body: "x"}))), tarLimits{Ctx: ctx})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestSanitizeTarTrusted(t *testing.T) {
	trusted := tarLimits{Trusted: true, SkipSpecial: true}
	// What a real app volume holds: an absolute link, a link to a file that was left out.
	_, hs, err := sanitize(t, trusted,
		tent{name: "releases", typ: tar.TypeDir},
		tent{name: "releases/1", typ: tar.TypeDir},
		tent{name: "current", typ: tar.TypeSymlink, link: "/data/releases/1"},
		tent{name: "outside", typ: tar.TypeSymlink, link: "/somewhere"},
		tent{name: "d/.wh.cache", body: "x"},
		tent{name: "d/alias", typ: tar.TypeLink, link: "d/.wh.cache"},
		tent{name: "f", body: "x"},
		tent{name: "f2", typ: tar.TypeLink, link: "f"},
		tent{name: "pipe", typ: tar.TypeFifo},
	)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range hs {
		names = append(names, h.Name)
	}
	if got, want := strings.Join(names, " "), "releases/ releases/1/ current outside f f2"; got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
	// The same archive from an upload is refused.
	if _, _, err := sanitize(t, tarLimits{}, tent{name: "current", typ: tar.TypeSymlink, link: "/data/releases/1"}); err == nil {
		t.Error("an absolute link was accepted from an upload")
	}
	// Writing through a link stays banned either way.
	_, _, err = sanitize(t, trusted,
		tent{name: "outside", typ: tar.TypeSymlink, link: "/somewhere"}, tent{name: "outside/file", body: "x"})
	var tp *tarProblem
	if !errors.As(err, &tp) {
		t.Errorf("trusted, file below a link: %v", err)
	}
	// And so does a path that leaves the folder.
	if _, _, err := sanitize(t, trusted, tent{name: "../x", body: "x"}); err == nil {
		t.Error("trusted: .. accepted")
	}
	if _, _, err := sanitize(t, trusted, tent{name: "h", typ: tar.TypeLink, link: "../x"}); err == nil {
		t.Error("trusted: hardlink to .. accepted")
	}
}
