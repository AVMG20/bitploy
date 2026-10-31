//go:build integration

package app

import (
	"context"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// Needs network access to github.com.
func TestSetGitSourcePublicRepo(t *testing.T) {
	s := catalogService(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "git@example.com", "Git", "correct horse battery", false)
	if err != nil {
		t.Fatal(err)
	}
	a := Actor{UserID: u.ID, Role: "user", Channel: ChannelMCP, Scope: ScopeDeploy}
	ap, err := s.CreateApp(ctx, a, CreateAppInput{Name: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SetGitSource(ctx, a, ap.Slug, GitSource{RepoURL: "https://github.com/octocat/Hello-World/", Branch: "master", Subdir: "./"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GitSource == nil || got.GitSource.RepoURL != "https://github.com/octocat/Hello-World" || got.GitSource.Branch != "master" {
		t.Errorf("git source = %+v", got.GitSource)
	}
	// Default branch is resolved and stored.
	got, err = s.SetGitSource(ctx, a, ap.Slug, GitSource{RepoURL: "https://github.com/octocat/Hello-World"})
	if err != nil || got.GitSource.Branch != "master" {
		t.Errorf("default branch: %+v, %v", got.GitSource, err)
	}
	// A private or missing repo without a stored token.
	_, err = s.SetGitSource(ctx, a, ap.Slug, GitSource{RepoURL: "https://github.com/octocat/no-such-repo-zzz", Branch: "main"})
	ae := apperr.As(err)
	if ae.Code != "github_access_missing" || ae.Next != "set_git_source" {
		t.Errorf("err = %+v", ae)
	}
	// The saved source survives a failed attempt.
	again, _ := s.GetApp(ctx, a, ap.Slug)
	if again.GitSource == nil || again.GitSource.RepoURL != "https://github.com/octocat/Hello-World" {
		t.Errorf("failed attempt changed the source: %+v", again.GitSource)
	}
}
