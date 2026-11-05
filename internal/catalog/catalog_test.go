package catalog

import (
	"regexp"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// defaultQuotaMB is the memory quota of a new account (users.quota_memory_mb).
const defaultQuotaMB = 2048

func TestEntriesCount(t *testing.T) {
	if n := len(All()); n < 6 || n > 40 {
		t.Fatalf("want 6 to 40 entries, got %d", n)
	}
}

func TestEntries(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All() {
		t.Run(e.ID, func(t *testing.T) {
			if !slugRe.MatchString(e.ID) {
				t.Errorf("id %q is not a slug", e.ID)
			}
			if seen[e.ID] {
				t.Errorf("duplicate id %q", e.ID)
			}
			seen[e.ID] = true
			for name, v := range map[string]string{"name": e.Name, "tagline": e.Tagline, "description": e.Description, "category": e.Category, "version": e.Version, "website": e.Website, "health path": e.HealthPath} {
				if strings.TrimSpace(v) == "" {
					t.Errorf("%s is empty", name)
				}
			}
			if strings.ContainsAny(e.Tagline, "\n") || len(e.Tagline) > 80 {
				t.Errorf("tagline must be one short line: %q", e.Tagline)
			}
			if !strings.HasPrefix(e.HealthPath, "/") {
				t.Errorf("health path %q must start with /", e.HealthPath)
			}
			if e.Port < 1 || e.Port > 65535 || e.Port == deploy.ReservedPort {
				t.Errorf("bad port %d", e.Port)
			}
			// Each entry must fit a new account on its own; together they need not.
			if e.MemoryMB < 64 || e.MemoryMB > defaultQuotaMB {
				t.Errorf("memory %d MB is not between 64 MB and the default quota of %d MB", e.MemoryMB, defaultQuotaMB)
			}
			if e.Volume != (e.VolumeMB > 0) {
				t.Errorf("volume=%v but volume_mb=%d", e.Volume, e.VolumeMB)
			}
			if len(e.FirstSteps) == 0 {
				t.Error("no first steps")
			}

			// Pinned version.
			if strings.EqualFold(e.Version, "latest") || !regexp.MustCompile(`^\d+(\.\d+)*$`).MatchString(e.Version) {
				t.Errorf("version %q is not an exact version", e.Version)
			}
			if !strings.Contains(e.Dockerfile, e.Version) {
				t.Errorf("the Dockerfile does not mention version %s", e.Version)
			}
			for _, line := range strings.Split(e.Dockerfile, "\n") {
				f := strings.Fields(line)
				// scratch is the empty image: nothing to pin.
				if len(f) >= 2 && strings.EqualFold(f[0], "FROM") && f[1] != "scratch" && (strings.HasSuffix(f[1], ":latest") || !strings.Contains(f[1], ":")) && !strings.EqualFold(f[len(f)-2], "AS") {
					t.Errorf("unpinned base image: %s", line)
				}
			}

			// Placeholders.
			for _, s := range e.FirstSteps {
				checkPlaceholders(t, s)
			}
			names := map[string]bool{}
			for _, ev := range e.Env {
				if !envNameRe.MatchString(ev.Name) || strings.EqualFold(ev.Name, "PORT") || strings.HasPrefix(strings.ToUpper(ev.Name), "BITPLOY_") {
					t.Errorf("env name %q is not allowed", ev.Name)
				}
				if names[ev.Name] {
					t.Errorf("duplicate env %q", ev.Name)
				}
				names[ev.Name] = true
				if ev.Generate != "" && ev.Generate != "secret" {
					t.Errorf("env %s: generate %q", ev.Name, ev.Generate)
				}
				if ev.Generate == "secret" && ev.Value != "" {
					t.Errorf("env %s: a generated value has no fixed value", ev.Name)
				}
				if ev.Generate == "" && ev.Value == "" {
					t.Errorf("env %s has neither a value nor generate", ev.Name)
				}
				if ev.Description == "" {
					t.Errorf("env %s has no description", ev.Name)
				}
				checkPlaceholders(t, refsOut(t, e, ev.Value))
			}
			for _, sv := range e.Services {
				if !slugRe.MatchString(sv.Name) && !regexp.MustCompile(`^[a-z0-9]{1,3}$`).MatchString(sv.Name) {
					t.Errorf("service name %q", sv.Name)
				}
			}

			// The Dockerfile passes the same checks as a deploy: no error, no warning.
			res := deploy.CheckDockerfile(deploy.CheckInput{
				Dockerfile: []byte(e.Dockerfile), DockerfilePath: "Dockerfile", AppType: "web", AppPort: e.Port,
				Files: []string{"Dockerfile"},
			})
			for _, f := range res.Findings {
				if f.Severity == "error" || f.Severity == "warning" {
					t.Errorf("finding %s (%s): %s", f.Code, f.Severity, f.Message)
				}
			}
			if !res.OK {
				t.Error("the Dockerfile check failed")
			}
			// Without the port setting the Dockerfile must still say the same port.
			res = deploy.CheckDockerfile(deploy.CheckInput{Dockerfile: []byte(e.Dockerfile), DockerfilePath: "Dockerfile", AppType: "web", Files: []string{"Dockerfile"}})
			if res.Port != e.Port {
				t.Errorf("EXPOSE says port %d, the entry says %d", res.Port, e.Port)
			}
			// Data lives under /data and nowhere else.
			if e.Volume && !strings.Contains(e.Dockerfile, "/data") {
				t.Error("a volume app must keep its data in /data")
			}
		})
	}
}

// refsOut checks that each ${{name.VAR}} names a service of the entry and
// removes them.
func refsOut(t *testing.T, e Entry, v string) string {
	t.Helper()
	names := map[string]bool{}
	for _, sv := range e.Services {
		names[sv.Name] = true
	}
	for _, m := range RefPattern.FindAllStringSubmatch(v, -1) {
		if !names[m[1]] {
			t.Errorf("reference %s names no service of the entry", m[0])
		}
	}
	return RefPattern.ReplaceAllString(v, "")
}

func checkPlaceholders(t *testing.T, s string) {
	t.Helper()
	rest := strings.ReplaceAll(s, URLPlaceholder, "")
	if strings.ContainsAny(rest, "{}") {
		t.Errorf("unknown placeholder in %q (only {url} exists)", s)
	}
}

func TestGetAndFill(t *testing.T) {
	if _, ok := Get("nope"); ok {
		t.Error("unknown id found")
	}
	e, ok := Get(" Memos ")
	if !ok || e.Dockerfile == "" {
		t.Fatalf("memos: %v %q", ok, e.Dockerfile)
	}
	if got := Fill("open {url}/x", "https://a.test"); got != "open https://a.test/x" {
		t.Errorf("Fill: %q", got)
	}
	if len(IDs()) != len(All()) {
		t.Error("IDs and All differ")
	}
}
