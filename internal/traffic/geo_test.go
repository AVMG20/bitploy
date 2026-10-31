package traffic

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

func testGeo(t *testing.T, srv *httptest.Server) *Geo {
	t.Helper()
	return &Geo{
		Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Client: srv.Client(),
		Now: func() time.Time { return time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC) },
		URL: func(m time.Time) string { return srv.URL + "/dbip-country-lite-" + m.Format("2006-01") + ".mmdb.gz" },
	}
}

func TestGeoWithoutDatabaseIsUnknown(t *testing.T) {
	g := &Geo{Dir: t.TempDir()}
	if got := g.Country("8.8.8.8"); got != "" {
		t.Errorf("country = %q", got)
	}
	var none *Geo
	if got := none.Country("8.8.8.8"); got != "" {
		t.Errorf("nil Geo: country = %q", got)
	}
	if !g.stale() {
		t.Error("a missing file is not stale")
	}
}

func TestGeoFallsBackToLastMonthAndRejectsGarbage(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if strings.Contains(r.URL.Path, "2026-11") {
			http.NotFound(w, r) // this month is not published yet
			return
		}
		_, _ = w.Write(gz("this is not a database"))
	}))
	defer srv.Close()
	g := testGeo(t, srv)
	err := g.download(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("garbage accepted: %v", err)
	}
	if len(asked) != 2 || !strings.Contains(asked[1], "2026-10") {
		t.Errorf("asked %v", asked)
	}
	if _, err := os.Stat(filepath.Join(g.Dir, geoFile)); err == nil {
		t.Error("an invalid file was installed")
	}
	if left, _ := filepath.Glob(filepath.Join(g.Dir, ".geo-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestGeoServerErrorKeepsOldFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	g := testGeo(t, srv)
	if err := g.download(context.Background()); err == nil {
		t.Fatal("no error")
	}
}
