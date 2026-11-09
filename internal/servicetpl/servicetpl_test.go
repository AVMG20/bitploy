package servicetpl

import (
	"regexp"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
)

var idRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func TestTemplates(t *testing.T) {
	seen := map[string]bool{}
	for _, tp := range All() {
		t.Run(tp.ID, func(t *testing.T) {
			if !idRe.MatchString(tp.ID) || seen[tp.ID] {
				t.Errorf("id %q is not a unique DNS label", tp.ID)
			}
			seen[tp.ID] = true
			for name, v := range map[string]string{"name": tp.Name, "category": tp.Category, "description": tp.Description, "image": tp.Image, "version": tp.Version, "main_var": tp.MainVar, "user": tp.User, "healthcheck": tp.Healthcheck} {
				if strings.TrimSpace(v) == "" {
					t.Errorf("%s is empty", name)
				}
			}
			if strings.EqualFold(tp.Version, "latest") || !regexp.MustCompile(`\d+\.\d+`).MatchString(tp.Version) {
				t.Errorf("version %q is not pinned", tp.Version)
			}
			if tp.User == "0" || strings.HasPrefix(tp.User, "0:") || strings.HasPrefix(tp.User, "root") {
				t.Errorf("runs as root: %q", tp.User)
			}
			if tp.MemoryMB < 64 || tp.MemoryMB > 1024 || tp.CPUMillis < 100 || tp.VolumeMB < 256 {
				t.Errorf("sizes: memory %d, cpu %d, volume %d", tp.MemoryMB, tp.CPUMillis, tp.VolumeMB)
			}

			own := map[string]string{}
			for _, e := range tp.Env {
				if !envNameRe.MatchString(e.Name) || strings.HasPrefix(e.Name, "BITPLOY_") || e.Name == "PORT" {
					t.Errorf("env name %q", e.Name)
				}
				if e.Description == "" || (e.Generate == "" && e.Value == "") || (e.Generate != "" && e.Generate != "secret" && (e.Generate != "hex" || e.HexLength < 16)) {
					t.Errorf("env %+v", e)
				}
				own[e.Name] = "v-" + e.Name
			}
			names := map[string]bool{}
			for _, c := range tp.Connection {
				names[c.Name] = true
				v, ok := tp.Resolve(c.Name, "db", own)
				if !ok || v == "" || strings.Contains(v, "${") || strings.Contains(v, "{host}") || strings.Contains(v, "{port}") {
					t.Errorf("connection %s resolves to %q", c.Name, v)
				}
			}
			if !names[tp.MainVar] {
				t.Errorf("main_var %s is not a connection variable", tp.MainVar)
			}
			if _, ok := tp.Resolve("NOPE", "db", own); ok {
				t.Error("an unknown variable resolved")
			}

			df := tp.Dockerfile()
			for _, want := range []string{"FROM " + tp.ImageRef() + "\n", "USER " + tp.User + "\n", "HEALTHCHECK ", "EXPOSE "} {
				if !strings.Contains(df, want) {
					t.Errorf("Dockerfile lacks %q:\n%s", want, df)
				}
			}
			if !strings.Contains(df, "/data") {
				t.Errorf("the data does not go to /data:\n%s", df)
			}
			// Secrets never go into the Dockerfile.
			for _, e := range tp.Env {
				if e.Generate != "" && strings.Contains(df, e.Name+"=") {
					t.Errorf("Dockerfile sets the secret %s", e.Name)
				}
			}
			res := deploy.CheckDockerfile(deploy.CheckInput{
				Dockerfile: []byte(df), DockerfilePath: "Dockerfile", AppType: "worker", Files: []string{"Dockerfile"},
			})
			if !res.OK {
				t.Errorf("the Dockerfile check failed: %+v\n%s", res.Findings, df)
			}
			for _, f := range res.Findings {
				if f.Severity == "error" || f.Severity == "warning" {
					t.Errorf("finding %s (%s): %s", f.Code, f.Severity, f.Message)
				}
			}
		})
	}
}

func TestPostgresConnection(t *testing.T) {
	tp, ok := Get(" Postgres ")
	if !ok {
		t.Fatal("postgres missing")
	}
	v, _ := tp.Resolve("DATABASE_URL", "postgres", map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "s3cr3t", "POSTGRES_DB": "app"})
	if v != "postgresql://app:s3cr3t@postgres:5432/app" {
		t.Errorf("DATABASE_URL = %q", v)
	}
	if _, ok := Get("nope"); ok {
		t.Error("unknown template found")
	}
	for _, tp := range Listed() {
		if tp.Hidden {
			t.Errorf("%s is hidden but listed", tp.ID)
		}
	}
	if len(Listed()) >= len(All()) {
		t.Error("redis should be hidden behind valkey")
	}
}
