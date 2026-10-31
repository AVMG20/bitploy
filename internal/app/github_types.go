package app

import "time"

// GitHubAppInfo is the GitHub App of this bitploy. Everyone may see whether it
// exists and its name; the rest is for admins.
type GitHubAppInfo struct {
	Configured bool   `json:"configured"`
	Name       string `json:"name,omitempty"`
	Slug       string `json:"slug,omitempty"`
	// Admin only:
	HTMLURL    string `json:"html_url,omitempty" doc:"The app's page on GitHub"`
	ManageURL  string `json:"manage_url,omitempty" doc:"Where the owner changes or deletes the app on GitHub"`
	Owner      string `json:"owner,omitempty" doc:"GitHub account that owns the app"`
	WebhookURL string `json:"webhook_url,omitempty" doc:"Where GitHub sends push events"`
	// DefaultName is the app name offered when creating the app.
	DefaultName string     `json:"default_name,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	// PublicURLProblem says why GitHub cannot reach bitploy; empty when it can.
	PublicURLProblem string `json:"public_url_problem,omitempty"`
}

// GitHubInstallation is one GitHub account (a person or an organization) on
// which the user installed the bitploy GitHub App.
type GitHubInstallation struct {
	ID                  int64     `json:"id"`
	AccountLogin        string    `json:"account_login"`
	AccountType         string    `json:"account_type" enum:"User,Organization"`
	RepositorySelection string    `json:"repository_selection" enum:"all,selected" doc:"all: every repository of the account. selected: only the ones chosen on GitHub."`
	Status              string    `json:"status" enum:"active,suspended,deleted" doc:"deleted means the app was uninstalled on GitHub"`
	ManageURL           string    `json:"manage_url" doc:"Where to choose repositories or uninstall, on GitHub"`
	RepoCount           *int      `json:"repo_count,omitempty" doc:"Number of repositories, when only selected ones are shared"`
	ConnectedAt         time.Time `json:"connected_at"`
}

// GitHubOverview is what Settings > GitHub shows.
type GitHubOverview struct {
	App           GitHubAppInfo        `json:"app"`
	Installations []GitHubInstallation `json:"installations"`
}

// GitHubRedirect is a GitHub address the dashboard sends the browser to.
type GitHubRedirect struct {
	URL string `json:"url"`
}

// GitHubLinkInput finishes connecting: GitHub sent the browser back with
// these values.
type GitHubLinkInput struct {
	InstallationID int64  `json:"installation_id,omitempty" doc:"From the redirect after installing. Leave out when GitHub sent only a code."`
	Code           string `json:"code" doc:"The OAuth code from the redirect"`
	State          string `json:"state" doc:"The state from the redirect"`
}

// GitHubManifestInput starts creating the GitHub App.
type GitHubManifestInput struct {
	Name         string `json:"name,omitempty" doc:"Name of the GitHub App, unique on GitHub. Default: bitploy <host>."`
	Organization string `json:"organization,omitempty" doc:"Create the app under this GitHub organization instead of your own account."`
}

// GitHubManifest is what the browser posts to GitHub to create the app.
type GitHubManifest struct {
	PostURL  string `json:"post_url" doc:"Post the manifest field of a form to this address"`
	Manifest string `json:"manifest" doc:"JSON text for the form field named manifest"`
	State    string `json:"state"`
	Name     string `json:"name"`
}

// GitHubManifestResult finishes creating the app.
type GitHubManifestResult struct {
	Code  string `json:"code" doc:"The code from the redirect"`
	State string `json:"state" doc:"The state from the redirect"`
}

// GitHubRepo is a repository of one of the user's GitHub accounts.
type GitHubRepo struct {
	FullName       string     `json:"full_name"`
	Private        bool       `json:"private"`
	HTMLURL        string     `json:"html_url"`
	DefaultBranch  string     `json:"default_branch"`
	InstallationID int64      `json:"installation_id"`
	PushedAt       *time.Time `json:"pushed_at,omitempty"`
}

// GitHubBranches lists the branches of a repository.
type GitHubBranches struct {
	DefaultBranch string   `json:"default_branch"`
	Branches      []string `json:"branches"`
}
