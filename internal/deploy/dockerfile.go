package deploy

// OWNER: MCP agent (dockerfile checks + stack detection). Signatures are a
// shared contract: the deploy pipeline calls CheckDockerfile in the
// `validating` state and validate_dockerfile calls it directly.

import "github.com/arno/bitploy/internal/apperr"

// Finding is one Dockerfile check result.
type Finding struct {
	Code     string `json:"code"`     // e.g. port_unknown
	Severity string `json:"severity"` // error | warning | info
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// CheckInput is everything the static checks look at.
type CheckInput struct {
	Dockerfile     []byte   // nil when the file does not exist
	DockerfilePath string   // as configured on the app, for messages
	Files          []string // relative slash paths in the build context (may be empty if unknown)
	Dockerignore   []byte   // nil when there is no .dockerignore
	AppPort        int      // 0 when the app has no port setting
	AppType        string   // web | worker
}

// CheckResult summarises the checks.
type CheckResult struct {
	OK             bool      // no error-severity findings
	Findings       []Finding // sorted: errors, then warnings, then info
	Port           int       // resolved port: app port, else the single EXPOSE of the final stage
	HasHealthcheck bool      // final stage has a HEALTHCHECK (not NONE)
	Stack          string    // detected stack from Files (static, node, python, go, php) or ""
}

// CheckDockerfile runs all static checks. It never builds.
func CheckDockerfile(in CheckInput) CheckResult {
	return CheckResult{OK: false, Findings: []Finding{{Code: "not_implemented", Severity: "error", Message: "Dockerfile checks are not implemented yet."}}}
}

// DetectStack guesses the stack from file names, only to make errors helpful.
func DetectStack(files []string) string { return "" }

// FirstError converts the first error finding of a result into an *apperr.Error
// (dockerfile_missing includes the matching template text in Details). Returns
// nil when the result is OK.
func FirstError(res CheckResult, files []string) *apperr.Error { return nil }
