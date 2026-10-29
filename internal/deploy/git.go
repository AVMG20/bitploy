package deploy

// OWNER: MCP agent (git sources, M4). Signatures are a shared contract: the
// deploy pipeline calls Clone in the `fetching` state.

import (
	"context"

	"github.com/arno/bitploy/internal/apperr"
)

// GitSource describes one branch of a repository. Token is the user's GitHub
// token (decrypted), used only in memory as basic auth (x-access-token).
type GitSource struct {
	URL    string
	Branch string
	Subdir string
	Token  string
}

// CloneResult is what a shallow clone produced.
type CloneResult struct {
	CommitSHA     string
	CommitMessage string
	ContextDir    string // dest joined with Subdir
}

// LsRemote checks that the branch exists and is readable. Errors are
// *apperr.Error with codes like source_repo_not_found, github_token_missing,
// github_repo_forbidden, source_branch_not_found.
func LsRemote(ctx context.Context, src GitSource) (sha string, err error) {
	return "", apperr.NotImplemented("git sources")
}

// Clone shallow-clones (depth 1) one branch into dest, aborting past maxBytes.
// The token never reaches .git/config, a log line or the build context: Clone
// removes dest/.git before returning.
func Clone(ctx context.Context, src GitSource, dest string, maxBytes int64) (CloneResult, error) {
	return CloneResult{}, apperr.NotImplemented("git sources")
}
