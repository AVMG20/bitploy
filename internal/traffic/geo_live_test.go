//go:build integration

package traffic

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// Downloads the real DB-IP Lite country database (needs the internet).
func TestGeoDBIPLive(t *testing.T) {
	g := &Geo{Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), URL: DBIPURL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := g.download(ctx); err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]string{"8.8.8.8": "US", "1.1.1.1": "AU", "::ffff:8.8.8.8": "US", "2a00:1450:4001::1": "", "192.168.1.1": ""} {
		got := g.Country(ip)
		t.Logf("%s -> %q", ip, got)
		if want != "" && got != want {
			t.Errorf("%s = %q, want %q", ip, got, want)
		}
	}
}
