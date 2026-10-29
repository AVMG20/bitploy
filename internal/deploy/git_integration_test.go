//go:build integration

package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/arno/bitploy/internal/apperr"
)

// Clones a small public repository from github.com.
func TestGitHubPublicClone(t *testing.T) {
	ctx := context.Background()
	src := GitSource{URL: "https://github.com/octocat/Hello-World", Branch: "master"}
	if _, err := LsRemote(ctx, src); err != nil {
		t.Fatalf("ls-remote: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "d")
	res, err := Clone(ctx, src, dest, 50<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CommitSHA) != 40 {
		t.Errorf("sha %q", res.CommitSHA)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		t.Error(".git left behind")
	}
}

func TestGitHubErrors(t *testing.T) {
	ctx := context.Background()
	_, err := LsRemote(ctx, GitSource{URL: "https://github.com/octocat/definitely-not-a-repo-zzz", Branch: "main"})
	if ae := apperr.As(err); ae.Code != "github_token_missing" {
		t.Errorf("missing repo without token: %v", err)
	}
	_, err = LsRemote(ctx, GitSource{URL: "https://github.com/octocat/Hello-World", Branch: "no-such-branch"})
	if ae := apperr.As(err); ae.Code != "source_branch_not_found" {
		t.Errorf("missing branch: %v", err)
	}
}
