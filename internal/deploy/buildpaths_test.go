package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// The exploit: every link points lexically inside the checkout, but the chain
// x -> ., y1 -> x/.., y2 -> y1/.. ... walks out of it on disk.
func TestDropEscapingSymlinksChain(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "checkout")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(base, "secret"), []byte("s"), 0o644)
	link(t, ".", filepath.Join(root, "x"))
	prev := "x"
	var chain []string
	for i := 1; i <= 6; i++ {
		name := "y" + string(rune('0'+i))
		link(t, prev+"/..", filepath.Join(root, name))
		chain = append(chain, name)
		prev = name
	}
	link(t, "a.txt", filepath.Join(root, "ok"))
	link(t, "../a.txt", filepath.Join(root, "sub", "up-inside"))
	link(t, "sub", filepath.Join(root, "dir"))
	link(t, "/etc/passwd", filepath.Join(root, "abs"))
	link(t, "missing", filepath.Join(root, "dangling"))
	link(t, "loop2", filepath.Join(root, "loop1"))
	link(t, "loop1", filepath.Join(root, "loop2"))
	link(t, "../secret", filepath.Join(root, "sub", "out"))

	if err := dropEscapingSymlinks(root); err != nil {
		t.Fatal(err)
	}
	for _, gone := range append([]string{"abs", "dangling", "loop1", "loop2", "sub/out"}, chain...) {
		if exists(filepath.Join(root, gone)) {
			t.Errorf("%s must be removed", gone)
		}
	}
	for _, kept := range []string{"x", "ok", "dir", "sub/up-inside"} {
		if !exists(filepath.Join(root, kept)) {
			t.Errorf("%s must stay", kept)
		}
	}
	// What is left resolves inside the checkout.
	realRoot, _ := filepath.EvalSymlinks(root)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type()&os.ModeSymlink != 0 {
			r, err := filepath.EvalSymlinks(p)
			if err != nil || (r != realRoot && !hasPrefix(r, realRoot)) {
				t.Errorf("%s resolves to %q (%v)", p, r, err)
			}
		}
		return nil
	})
}

func hasPrefix(p, root string) bool {
	return len(p) > len(root) && p[:len(root)] == root && p[len(root)] == filepath.Separator
}

func TestResolveBuildPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "src")
	os.MkdirAll(filepath.Join(root, "app"), 0o755)
	os.MkdirAll(filepath.Join(base, "outside"), 0o755)
	os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	os.WriteFile(filepath.Join(root, "app", "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	os.WriteFile(filepath.Join(base, "outside", "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	link(t, ".", filepath.Join(root, "x"))
	for i, prev := 1, "x"; i <= 5; i++ {
		name := "y" + string(rune('0'+i))
		link(t, prev+"/..", filepath.Join(root, name))
		prev = name
	}
	link(t, "../outside", filepath.Join(root, "out"))
	link(t, "../outside/Dockerfile", filepath.Join(root, "Dockerfile.link"))
	link(t, "app", filepath.Join(root, "applink"))

	tests := []struct {
		name, ctx, df string
		wantCode      string
	}{
		{"root", ".", "Dockerfile", ""},
		{"subdir", "app", "app/Dockerfile", ""},
		{"inner link", "applink", "Dockerfile", ""},
		{"self link", "x", "Dockerfile", ""},
		{"chain y5 as context", "y5", "Dockerfile", "source_path_escapes"},
		{"chain y1 as context", "y1", "Dockerfile", "source_path_escapes"},
		{"link to outside folder", "out", "Dockerfile", "source_path_escapes"},
		{"dockerfile link outside", ".", "Dockerfile.link", "source_path_escapes"},
		{"dockerfile via chain", ".", "y1/outside/Dockerfile", "source_path_escapes"},
		{"dockerfile outside via ..", ".", "../outside/Dockerfile", "source_path_escapes"},
		{"context missing", "nope", "Dockerfile", "source_context_not_found"},
		{"dockerfile missing", ".", "Nope", "source_dockerfile_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctxDir, df, err := ResolveBuildPaths(root, tc.ctx, tc.df)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				realRoot, _ := filepath.EvalSymlinks(root)
				if ctxDir != realRoot && !hasPrefix(ctxDir, realRoot) || !hasPrefix(df, realRoot) {
					t.Errorf("not inside the root: %s %s", ctxDir, df)
				}
				return
			}
			if err == nil || err.Code != tc.wantCode {
				t.Fatalf("got %v, want code %s", err, tc.wantCode)
			}
			if ctxDir != "" || df != "" {
				t.Errorf("paths returned with an error: %q %q", ctxDir, df)
			}
		})
	}
}
