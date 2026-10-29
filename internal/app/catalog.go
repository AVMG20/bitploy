package app

// OWNER: MCP agent. Templates, deploy rules, Dockerfile validation and git
// sources. Signatures are a shared contract; implement the bodies.

import (
	"context"

	"github.com/arno/bitploy/internal/apperr"
)

func (s *Service) ListTemplates(ctx context.Context, a Actor) ([]Template, error) {
	return nil, apperr.NotImplemented("templates")
}

// GetTemplate returns one stack; unknown stacks → template_unknown listing the stacks.
func (s *Service) GetTemplate(ctx context.Context, a Actor, stack string) (Template, error) {
	return Template{}, apperr.NotImplemented("templates")
}

// DeployRules returns the server instructions plus the runtime contract (markdown).
func (s *Service) DeployRules(ctx context.Context, a Actor) (string, error) {
	return "", apperr.NotImplemented("rules")
}

// ValidateDockerfile runs the static checks without building.
func (s *Service) ValidateDockerfile(ctx context.Context, a Actor, dockerfile string, fileList []string, appPort int) (ValidateResult, error) {
	return ValidateResult{}, apperr.NotImplemented("validate")
}

// SetGitSource saves repo/branch/subdir and checks access with ls-remote
// (using the user's GitHub token for github.com repos when set).
func (s *Service) SetGitSource(ctx context.Context, a Actor, ref string, src GitSource) (App, error) {
	return App{}, apperr.NotImplemented("git source")
}
