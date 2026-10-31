package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// MaxWebhookBody is the largest webhook body bitploy reads. GitHub's own cap
// is 25 MB, but a push event with a few thousand commits is far smaller.
const MaxWebhookBody = 5 << 20

// VerifySignature checks the X-Hub-Signature-256 header ("sha256=<hex>")
// against the HMAC-SHA256 of the raw body, in constant time. An empty secret
// or a missing or malformed header never verifies.
func VerifySignature(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	hexSum, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign returns the header value for body. It exists for tests.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// PushEvent is the part of a push webhook bitploy reads.
type PushEvent struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	HeadCommit *struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"head_commit"`
}

// Branch returns the branch name of a push to a branch, and false for tags,
// other refs and branch deletions.
func (p PushEvent) Branch() (string, bool) {
	name, ok := strings.CutPrefix(p.Ref, "refs/heads/")
	if !ok || name == "" || p.Deleted || isZeroSHA(p.After) {
		return "", false
	}
	return name, true
}

func isZeroSHA(s string) bool {
	return s != "" && strings.Trim(s, "0") == ""
}

// InstallationEvent covers the installation and installation_repositories
// webhooks.
type InstallationEvent struct {
	Action       string `json:"action"`
	Installation struct {
		ID                  int64  `json:"id"`
		RepositorySelection string `json:"repository_selection"`
		Account             struct {
			Login string `json:"login"`
		} `json:"account"`
	} `json:"installation"`
	RepositorySelection string      `json:"repository_selection"`
	RepositoriesAdded   []EventRepo `json:"repositories_added"`
	RepositoriesRemoved []EventRepo `json:"repositories_removed"`
}

// EventRepo is the short repository form inside installation events.
type EventRepo struct {
	FullName string `json:"full_name"`
}

// RepoKey turns "Owner/Repo" or a github.com URL (with or without .git, a
// trailing slash or www.) into the lower-case key "owner/repo". ok is false
// for anything that is not a plain two-part GitHub repository address.
func RepoKey(s string) (key string, ok bool) {
	s = strings.TrimSpace(s)
	path := s
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		h := strings.ToLower(u.Hostname())
		if h != "github.com" && h != "www.github.com" {
			return "", false
		}
		path = u.Path
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	owner, name, found := strings.Cut(path, "/")
	if !found || strings.Contains(name, "/") || !nameRe.MatchString(owner) || !nameRe.MatchString(name) {
		return "", false
	}
	return strings.ToLower(owner + "/" + name), true
}

// SplitRepoKey splits "owner/name".
func SplitRepoKey(key string) (owner, name string) {
	owner, name, _ = strings.Cut(key, "/")
	return owner, name
}
