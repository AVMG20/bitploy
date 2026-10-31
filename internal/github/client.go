// Package github talks to GitHub for bitploy's GitHub App: the manifest
// conversion, app JWTs, installation access tokens, the OAuth code exchange
// that proves who an installation belongs to, repository and branch lists, and
// webhook signatures. It holds no state and no bitploy business rules; the
// service layer (internal/app) decides who may do what.
//
// The API and web base URLs are fields of Client, so tests point them at an
// httptest server and nothing here touches the network on its own.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAPIURL = "https://api.github.com"
	DefaultWebURL = "https://github.com"

	maxResponseBytes = 8 << 20
	perPage          = 100
	maxRepoPages     = 20 // 2,000 repositories per installation
	maxBranchPages   = 3  // 300 branches
	maxInstallPages  = 5
)

var (
	sharedHTTP = &http.Client{Timeout: 15 * time.Second}

	codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,99}$`)
)

// Client is a thin GitHub REST client. The zero value is not usable: call NewClient.
type Client struct {
	APIURL string // https://api.github.com
	WebURL string // https://github.com
	HTTP   *http.Client
}

// NewClient returns a client; empty URLs mean github.com.
func NewClient(apiURL, webURL string) *Client {
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	if webURL == "" {
		webURL = DefaultWebURL
	}
	return &Client{APIURL: strings.TrimRight(apiURL, "/"), WebURL: strings.TrimRight(webURL, "/"), HTTP: sharedHTTP}
}

// APIError is an answer from GitHub that was not a success. Message is what
// GitHub said; it never contains credentials.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return "github answered " + strconv.Itoa(e.Status) + ": " + e.Message
}

// Status returns the HTTP status of err when it is an *APIError, else 0.
func Status(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// IsUnreachable reports a failure to talk to GitHub at all (network, timeout,
// unreadable answer), as opposed to GitHub refusing the request.
func IsUnreachable(err error) bool {
	var ae *APIError
	return err != nil && !errors.As(err, &ae)
}

func (c *Client) do(ctx context.Context, method, rawURL, bearer string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "bitploy")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github unreachable: %w", scrubURLError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("github unreachable: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("github unreachable: unreadable answer: %w", err)
	}
	return nil
}

// scrubURLError drops the URL (which can carry a code in its query) from a
// net/http error.
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// ---- manifest flow ----

// AppCredentials is what GitHub returns when the manifest is converted.
type AppCredentials struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// ConvertManifest exchanges the one-time code GitHub sent back after the
// admin created the app (POST /app-manifests/{code}/conversions).
func (c *Client) ConvertManifest(ctx context.Context, code string) (*AppCredentials, error) {
	if !codeRe.MatchString(code) {
		return nil, &APIError{Status: http.StatusUnprocessableEntity, Message: "malformed code"}
	}
	var out AppCredentials
	if err := c.do(ctx, http.MethodPost, c.APIURL+"/app-manifests/"+code+"/conversions", "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- installation tokens ----

// InstallationToken mints an installation access token (valid one hour).
// appJWT authenticates as the app. With repos set the token only works for
// those repositories (names without the owner), and with readContents it only
// carries read access to contents.
func (c *Client) InstallationToken(ctx context.Context, appJWT string, installationID int64, repos []string, contentsOnly bool) (string, time.Time, error) {
	body := map[string]any{}
	if len(repos) > 0 {
		body["repositories"] = repos
	}
	if contentsOnly {
		body["permissions"] = map[string]string{"contents": "read"}
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	u := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.APIURL, installationID)
	if err := c.do(ctx, http.MethodPost, u, appJWT, body, &out); err != nil {
		return "", time.Time{}, err
	}
	if out.Token == "" {
		return "", time.Time{}, &APIError{Status: http.StatusBadGateway, Message: "no token in the answer"}
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = time.Now().Add(time.Hour)
	}
	return out.Token, out.ExpiresAt, nil
}

// ---- OAuth: who is this GitHub user, and which installations can they see ----

// ExchangeOAuthCode turns the code GitHub appended to the callback URL into a
// user access token (the app's client id and secret prove it is us).
func (c *Client) ExchangeOAuthCode(ctx context.Context, clientID, clientSecret, code string) (string, error) {
	if !codeRe.MatchString(code) {
		return "", &APIError{Status: http.StatusUnprocessableEntity, Message: "malformed code"}
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WebURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "bitploy")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("github unreachable: %w", scrubURLError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("github unreachable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &APIError{Status: resp.StatusCode, Message: "token exchange refused"}
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("github unreachable: unreadable answer: %w", err)
	}
	if out.AccessToken == "" {
		// GitHub answers 200 with {"error": "bad_verification_code"} for a used or wrong code.
		msg := out.Error
		if out.Description != "" {
			msg = out.Description
		}
		return "", &APIError{Status: http.StatusUnauthorized, Message: msg}
	}
	return out.AccessToken, nil
}

// Installation is one installation of the app as GitHub reports it.
type Installation struct {
	ID                  int64      `json:"id"`
	RepositorySelection string     `json:"repository_selection"`
	SuspendedAt         *time.Time `json:"suspended_at"`
	Account             struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

// UserInstallations lists the installations of this app that the GitHub user
// behind userToken can access (GET /user/installations).
func (c *Client) UserInstallations(ctx context.Context, userToken string) ([]Installation, error) {
	var all []Installation
	for page := 1; page <= maxInstallPages; page++ {
		var out struct {
			Installations []Installation `json:"installations"`
		}
		u := fmt.Sprintf("%s/user/installations?per_page=%d&page=%d", c.APIURL, perPage, page)
		if err := c.do(ctx, http.MethodGet, u, userToken, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Installations...)
		if len(out.Installations) < perPage {
			break
		}
	}
	return all, nil
}

// ---- repositories and branches ----

// Repo is one repository an installation covers.
type Repo struct {
	FullName      string    `json:"full_name"`
	Name          string    `json:"name"`
	Private       bool      `json:"private"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// InstallationRepos lists every repository the installation can access
// (GET /installation/repositories, with an installation token), up to 2,000.
func (c *Client) InstallationRepos(ctx context.Context, instToken string) ([]Repo, error) {
	var all []Repo
	for page := 1; page <= maxRepoPages; page++ {
		var out struct {
			Repositories []Repo `json:"repositories"`
		}
		u := fmt.Sprintf("%s/installation/repositories?per_page=%d&page=%d", c.APIURL, perPage, page)
		if err := c.do(ctx, http.MethodGet, u, instToken, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Repositories...)
		if len(out.Repositories) < perPage {
			break
		}
	}
	return all, nil
}

// RepoInfo reads one repository (default branch, visibility).
func (c *Client) RepoInfo(ctx context.Context, token, owner, name string) (*Repo, error) {
	if !nameRe.MatchString(owner) || !nameRe.MatchString(name) {
		return nil, &APIError{Status: http.StatusUnprocessableEntity, Message: "malformed repository name"}
	}
	var out Repo
	if err := c.do(ctx, http.MethodGet, c.APIURL+"/repos/"+owner+"/"+name, token, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Branches lists branch names of a repository, up to 300.
func (c *Client) Branches(ctx context.Context, token, owner, name string) ([]string, error) {
	if !nameRe.MatchString(owner) || !nameRe.MatchString(name) {
		return nil, &APIError{Status: http.StatusUnprocessableEntity, Message: "malformed repository name"}
	}
	var names []string
	for page := 1; page <= maxBranchPages; page++ {
		var out []struct {
			Name string `json:"name"`
		}
		u := fmt.Sprintf("%s/repos/%s/%s/branches?per_page=%d&page=%d", c.APIURL, owner, name, perPage, page)
		if err := c.do(ctx, http.MethodGet, u, token, nil, &out); err != nil {
			return nil, err
		}
		for _, b := range out {
			names = append(names, b.Name)
		}
		if len(out) < perPage {
			break
		}
	}
	return names, nil
}

// ValidSlug reports whether s can be used in a github.com/apps/<slug> link.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// ValidName reports whether s is a plausible owner or repository name.
func ValidName(s string) bool { return nameRe.MatchString(s) }
