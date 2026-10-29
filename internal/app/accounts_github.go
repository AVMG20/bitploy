package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/store/db"
)

// acctGithubAPI is the GitHub REST base URL; tests point it at a local server.
var acctGithubAPI = "https://api.github.com"

var acctGithubClient = &http.Client{Timeout: 10 * time.Second}

const acctGithubSettings = "Create a fine-grained token at https://github.com/settings/personal-access-tokens/new with read-only Contents and Metadata on the repos you want to deploy, then paste it in bitploy under Settings."

// SetGitHubToken validates with GitHub GET /user, stores it encrypted.
func (s *Service) SetGitHubToken(ctx context.Context, a Actor, token string) (GitHubStatus, error) {
	if err := RequireSession(a); err != nil {
		return GitHubStatus{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return GitHubStatus{}, apperr.BadRequest("github_token_invalid", "That does not look like a GitHub token.", acctGithubSettings)
	}
	login, expires, err := s.checkGitHubToken(ctx, token)
	if err != nil {
		return GitHubStatus{}, err
	}
	sealed := s.Crypto.Seal([]byte(token), "github:"+a.UserID)
	if err := s.Q.SetUserGitHub(ctx, db.SetUserGitHubParams{
		GithubTokenEnc: sealed, GithubLogin: nullStr(login), GithubTokenExpiresAt: expires,
		UpdatedAt: s.now(), ID: a.UserID,
	}); err != nil {
		return GitHubStatus{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "github_token.set", "user", a.UserID, map[string]any{"login": login})
	return GitHubStatus{Connected: true, Login: login, ExpiresAt: tsPtr(expires)}, nil
}

func (s *Service) checkGitHubToken(ctx context.Context, token string) (login string, expires sql.NullInt64, err error) {
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, acctGithubAPI+"/user", nil)
	if rerr != nil {
		return "", expires, apperr.Internal(rerr)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "bitploy")
	resp, rerr := acctGithubClient.Do(req)
	if rerr != nil {
		return "", expires, apperr.Unavailable("github_unreachable",
			"Bitploy could not reach GitHub to check the token.", "Try again in a minute.")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", expires, apperr.Unprocessable("github_token_invalid",
			"GitHub rejected this token. It may be wrong, expired or revoked.", acctGithubSettings)
	case resp.StatusCode != http.StatusOK:
		return "", expires, apperr.Unavailable("github_unreachable",
			"GitHub answered with an unexpected status ("+acctItoa(int64(resp.StatusCode))+") when checking the token.", "Try again in a minute.")
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &u); err != nil || u.Login == "" {
		return "", expires, apperr.Unavailable("github_unreachable", "GitHub returned an answer bitploy could not read.", "Try again in a minute.")
	}
	if h := strings.TrimSpace(resp.Header.Get("github-authentication-token-expiration")); h != "" {
		for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700", time.RFC3339} {
			if t, perr := time.Parse(layout, h); perr == nil {
				expires = nullInt(t.Unix(), true)
				break
			}
		}
	}
	return u.Login, expires, nil
}

func (s *Service) DeleteGitHubToken(ctx context.Context, a Actor) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	if err := s.Q.ClearUserGitHub(ctx, db.ClearUserGitHubParams{UpdatedAt: s.now(), ID: a.UserID}); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "github_token.delete", "user", a.UserID, nil)
	return nil
}

func (s *Service) GetGitHubStatus(ctx context.Context, a Actor) (GitHubStatus, error) {
	u, err := s.Q.GetUser(ctx, a.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubStatus{}, acctNotLoggedIn()
	}
	if err != nil {
		return GitHubStatus{}, apperr.Internal(err)
	}
	if len(u.GithubTokenEnc) == 0 {
		return GitHubStatus{}, nil
	}
	return GitHubStatus{Connected: true, Login: u.GithubLogin.String, ExpiresAt: tsPtr(u.GithubTokenExpiresAt)}, nil
}

// GitHubTokenFor returns the decrypted token for server-side cloning only.
// Never log it or return it to a client. Empty string when none is set.
func (s *Service) GitHubTokenFor(ctx context.Context, userID string) (string, error) {
	u, err := s.Q.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", apperr.Internal(err)
	}
	if len(u.GithubTokenEnc) == 0 {
		return "", nil
	}
	plain, err := s.Crypto.Open(u.GithubTokenEnc, "github:"+userID)
	if err != nil {
		return "", apperr.Internal(err)
	}
	return string(plain), nil
}
