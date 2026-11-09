package servicetpl

import (
	"regexp"
	"strings"
	"testing"
)

func TestGeneratedValues(t *testing.T) {
	hexRe := regexp.MustCompile(`^[0-9a-f]+$`)
	g, ok := Get("garage")
	if !ok {
		t.Fatal("garage missing")
	}
	want := map[string]struct {
		prefix string
		n      int
	}{"GARAGE_RPC_SECRET": {"", 64}, "GARAGE_DEFAULT_ACCESS_KEY": {"GK", 32}, "GARAGE_DEFAULT_SECRET_KEY": {"", 64}}
	for _, e := range g.Env {
		w, isGen := want[e.Name]
		if !isGen {
			continue
		}
		v := e.NewValue()
		if !strings.HasPrefix(v, w.prefix) || len(v) != len(w.prefix)+w.n || !hexRe.MatchString(strings.TrimPrefix(v, w.prefix)) {
			t.Errorf("%s = %q", e.Name, v)
		}
		if v == e.NewValue() {
			t.Errorf("%s is the same twice", e.Name)
		}
		delete(want, e.Name)
	}
	if len(want) != 0 {
		t.Errorf("garage lacks generated variables %v", want)
	}
	if v := (EnvSpec{Name: "X", Generate: "secret"}).NewValue(); len(v) != 32 {
		t.Errorf("secret = %q", v)
	}
	if v := (EnvSpec{Name: "X", Value: "app"}).NewValue(); v != "app" {
		t.Errorf("fixed = %q", v)
	}
}

func TestNewTemplates(t *testing.T) {
	cases := []struct {
		id, category string
		backups      bool
		main         string
	}{
		{"typesense", "search", false, "TYPESENSE_URL"},
		{"clickhouse", "database", true, "CLICKHOUSE_URL"},
		{"garage", "storage", true, "S3_ENDPOINT"},
		{"mailpit", "email", false, "SMTP_URL"},
	}
	for _, c := range cases {
		tp, ok := Get(c.id)
		if !ok {
			t.Fatalf("%s missing", c.id)
		}
		if tp.Category != c.category || tp.Backups() != c.backups || tp.BackupEngine() != "" || tp.MainVar != c.main {
			t.Errorf("%s: category %q, backups %v, engine %q, main %q", c.id, tp.Category, tp.Backups(), tp.BackupEngine(), tp.MainVar)
		}
		if !tp.Backups() && !strings.Contains(tp.NoBackupsReason(), "makes no backups") {
			t.Errorf("%s: reason %q", c.id, tp.NoBackupsReason())
		}
	}
	m, _ := Get("mailpit")
	if v, _ := m.Resolve("SMTP_URL", "mail", nil); v != "smtp://mail:1025" {
		t.Errorf("SMTP_URL = %q", v)
	}
	g, _ := Get("garage")
	if df := g.Dockerfile(); !strings.Contains(df, "COPY <<EOF /etc/garage.toml\n") || !strings.Contains(df, `CMD ["/garage", "server", "--single-node", "--default-bucket"]`) {
		t.Errorf("garage Dockerfile:\n%s", df)
	}
}
