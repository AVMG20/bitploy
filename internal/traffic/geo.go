package traffic

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Geo finds the country of an IP address with the free "IP to Country Lite"
// database of DB-IP (https://db-ip.com, CC BY 4.0). It downloads the file into
// Dir in the background and refreshes it monthly. Without a database every
// lookup says "unknown".
type Geo struct {
	Dir    string
	Log    *slog.Logger
	Client *http.Client
	// URL gives the download address for a month; tests replace it.
	URL func(month time.Time) string
	Now func() time.Time

	mu sync.RWMutex
	r  *maxminddb.Reader
}

const (
	geoFile     = "dbip-country-lite.mmdb"
	geoMaxAge   = 30 * 24 * time.Hour
	geoMaxBytes = 100 << 20
)

// DBIPURL is the download address of the Lite country database for a month.
func DBIPURL(month time.Time) string {
	return "https://download.db-ip.com/free/dbip-country-lite-" + month.UTC().Format("2006-01") + ".mmdb.gz"
}

func (g *Geo) path() string { return filepath.Join(g.Dir, geoFile) }

func (g *Geo) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Country returns an ISO 3166-1 alpha-2 code, or "" when unknown.
func (g *Geo) Country(ip string) string {
	if g == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.r == nil {
		return ""
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := g.r.Lookup(addr.Unmap()).Decode(&rec); err != nil {
		return ""
	}
	return cleanCountry(rec.Country.ISOCode)
}

// Load opens the database file if there is one.
func (g *Geo) Load() error {
	r, err := maxminddb.Open(g.path())
	if err != nil {
		return err
	}
	g.mu.Lock()
	old := g.r
	g.r = r
	g.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (g *Geo) stale() bool {
	st, err := os.Stat(g.path())
	return err != nil || g.now().Sub(st.ModTime()) > geoMaxAge
}

// Run loads the database, downloads it when it is missing or a month old, and
// checks again every day (every six hours after a failed download).
func (g *Geo) Run(ctx context.Context) {
	if err := g.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		g.Log.Warn("geoip database unreadable, downloading a new one", "err", err)
		_ = os.Remove(g.path())
	}
	for {
		wait := 24 * time.Hour
		if g.stale() {
			if err := g.download(ctx); err != nil {
				g.Log.Warn("geoip download failed, countries stay as they are", "err", err)
				wait = 6 * time.Hour
			} else {
				g.Log.Info("geoip database updated")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// download fetches this month's file, or last month's when it is not published yet.
func (g *Geo) download(ctx context.Context) error {
	if err := os.MkdirAll(g.Dir, 0o750); err != nil {
		return err
	}
	now := g.now().UTC()
	err := g.fetch(ctx, g.URL(now))
	if errors.Is(err, errNotPublished) {
		err = g.fetch(ctx, g.URL(now.AddDate(0, -1, 0)))
	}
	return err
}

var errNotPublished = errors.New("not published yet")

func (g *Geo) fetch(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotPublished
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer zr.Close()

	tmp, err := os.CreateTemp(g.Dir, ".geo-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(zr, geoMaxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > geoMaxBytes {
		return errors.New("downloaded database is too big")
	}
	check, err := maxminddb.Open(tmp.Name())
	if err != nil {
		return fmt.Errorf("downloaded database is not valid: %w", err)
	}
	_ = check.Close()
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), g.path()); err != nil {
		return err
	}
	return g.Load()
}
