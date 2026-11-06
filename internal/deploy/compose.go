package deploy

// Compose files (docs/compose-plan.md, "Phase 2"): parse a compose file,
// check it against bitploy's rules and translate it into the services and
// volumes of an app.
//
// The file is loaded from bytes with compose-go. Nothing is read from the
// filesystem except through ComposeInput.Read (the source of the
// deployment), and ${VAR} is interpolated only from the app's shared
// variables, never from the environment of the bitploy process.
//
// Values that come from a shared variable are not copied into the result in
// plain text where they are kept (environment, command, entrypoint,
// healthcheck): they become EnvRef markers that are filled in when the
// container starts (ExpandEnvRefs), so the variable stays encrypted at rest.

import (
	"context"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/dotenv"
	interp "github.com/compose-spec/compose-go/v2/interpolation"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/compose-spec/compose-go/v2/types"
	"gopkg.in/yaml.v3"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// Codes of compose problems. Errors stop the deploy; warnings do not.
const (
	CodeComposeInvalid            = "compose_invalid"
	CodeComposeForbidden          = "compose_forbidden"
	CodeComposeUnsupported        = "compose_unsupported"
	CodeComposePublicAmbiguous    = "compose_public_ambiguous"
	CodeComposeTooManyServices    = "compose_too_many_services"
	CodeComposeNoServices         = "compose_no_services"
	CodeComposeVolumeShared       = "compose_volume_shared"
	CodeComposeEnvFileMissing     = "compose_env_file_missing"
	CodeComposeServiceNameInvalid = "compose_service_name_invalid"
	CodeComposeFileMissing        = "compose_file_missing"
	CodeComposeSourceMissing      = "compose_source_missing"
	CodeComposeReference          = "compose_reference_unsupported"
	CodeComposeImageVariable      = "compose_image_variable"
	// Warnings.
	CodeComposeKeyIgnored     = "compose_key_ignored"
	CodeComposeVariableUnset  = "compose_variable_unset"
	CodeComposeServiceSkipped = "compose_service_skipped"
	CodeComposePortsIgnored   = "compose_ports_not_published"
)

// Limits and defaults of compose apps.
const (
	MaxComposeServices         = 6
	MaxComposeBytes            = 256 << 10
	maxEnvFileBytes            = 256 << 10
	ComposeDefaultMemoryMB     = 256
	ComposePublicBuildMemoryMB = 512
	ComposeDefaultCPUMillis    = 250
	ComposeDefaultVolumeMB     = 1024
	composeMinMemoryMB         = 64
	composeMinCPUMillis        = 50
	composeMinVolumeMB         = 100
	composeMaxVolumeMB         = 1 << 20
	composeProjectName         = "bitploy"
)

// MaxMemoryMB and MaxCPUMillis bound one app, or one service of a compose
// app: sanity limits that also hold for users who overallocate.
const (
	MaxMemoryMB  = 64 << 10
	MaxCPUMillis = 64 * 1000
)

// ComposeFileNames are the names bitploy looks for, in this order.
var ComposeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// IsComposeFileName reports whether the base name of p is one of ComposeFileNames.
func IsComposeFileName(p string) bool {
	return composeNames[path.Base(p)]
}

// FindComposeFile returns the compose file of a source: composePath when it
// is set (and the source has it), else the first of ComposeFileNames in dir
// (the app's context folder, "" or "." for the root) and then at the root.
// "" means there is none.
func FindComposeFile(files []string, dir, composePath string) string {
	has := make(map[string]bool, len(files))
	for _, f := range files {
		has[cleanRel(f)] = true
	}
	if composePath != "" {
		p := cleanRel(composePath)
		if has[p] {
			return p
		}
		return ""
	}
	dirs := []string{"."}
	if d := cleanRel(dir); d != "." && d != "" {
		dirs = []string{d, "."}
	}
	for _, d := range dirs {
		for _, n := range ComposeFileNames {
			if p := path.Join(d, n); has[p] {
				return p
			}
		}
	}
	return ""
}

// ComposeInput is what ParseCompose looks at.
type ComposeInput struct {
	Content []byte
	// Path is where the compose file is in the source, like compose.yaml or
	// deploy/compose.yaml. Build contexts and env_file paths are relative to
	// its folder. Empty means compose.yaml at the root.
	Path string
	// Env holds the app's shared variables: the only values ${VAR} can take.
	Env map[string]string
	// Read returns a file of the source by its path from the source root, nil
	// when it does not exist. Nil when there is no source (a pasted file):
	// env_file entries and Dockerfiles are then not checked.
	Read func(rel string) []byte
}

// ComposeIssue is one problem in a compose file.
type ComposeIssue struct {
	Code    string `json:"code"`
	Service string `json:"service,omitempty" doc:"The service at fault, if any"`
	Key     string `json:"key,omitempty" doc:"The compose key at fault, like ports or volumes[0]"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Err is the issue as an API error, pointing at validate_compose.
func (i ComposeIssue) Err() *apperr.Error {
	e := apperr.Unprocessable(i.Code, i.Message, i.Hint).WithNext("validate_compose")
	if i.Service != "" {
		e.WithDetail("service", i.Service)
	}
	if i.Key != "" {
		e.WithDetail("key", i.Key)
	}
	return e
}

// ComposeService is one service of a compose file as bitploy runs it.
type ComposeService struct {
	Name string
	Kind string // build | image
	// Image is the reference to pull, for an image service.
	Image string
	// Context and Dockerfile are paths from the source root (slash form), for a build service.
	Context    string
	Dockerfile string
	Target     string
	Args       map[string]string
	Public     bool
	// Port is the port the public service listens on; 0 when it is only known
	// from the image (EXPOSE), after the build.
	Port       int
	HealthPath string
	MemoryMB   int64
	CPUMillis  int64
	// Command and Entrypoint override the image's; nil keeps them.
	Command     []string
	Entrypoint  []string
	Healthcheck *ComposeHealthcheck
	DependsOn   []ComposeDependency
	Environment map[string]string
	Mounts      []ComposeMount
	User        string
	WorkingDir  string
}

// ComposeHealthcheck is a service's healthcheck. Test ["NONE"] turns off the image's.
type ComposeHealthcheck struct {
	Test         []string `json:"test"`
	IntervalS    int      `json:"interval_s,omitempty"`
	TimeoutS     int      `json:"timeout_s,omitempty"`
	Retries      int      `json:"retries,omitempty"`
	StartPeriodS int      `json:"start_period_s,omitempty"`
}

// ComposeDependency is one depends_on entry.
type ComposeDependency struct {
	Service   string `json:"service"`
	Condition string `json:"condition"` // service_started | service_healthy
}

// ComposeMount is a named volume mounted into a service.
type ComposeMount struct {
	Volume string
	Target string
}

// ComposeVolume is a named volume of the file, with the service that uses it.
type ComposeVolume struct {
	Name      string
	SizeMB    int64
	Service   string
	MountPath string
}

// ComposeProject is the outcome of ParseCompose. When Errors is not empty
// Services and Volumes may be incomplete.
type ComposeProject struct {
	Services []ComposeService
	Volumes  []ComposeVolume
	Warnings []ComposeIssue
	Errors   []ComposeIssue
}

// OK reports whether the file can be deployed.
func (p ComposeProject) OK() bool { return len(p.Errors) == 0 }

// FirstError is the first error as an API error, or nil.
func (p ComposeProject) FirstError() *apperr.Error {
	if len(p.Errors) == 0 {
		return nil
	}
	e := p.Errors[0].Err()
	if len(p.Errors) > 1 {
		e.Message += fmt.Sprintf(" (and %d more problems; validate_compose lists them all)", len(p.Errors)-1)
		e.WithDetail("errors", p.Errors)
	}
	return e
}

// Public is the public service, or nil when every service is a worker.
func (p ComposeProject) Public() *ComposeService {
	for i := range p.Services {
		if p.Services[i].Public {
			return &p.Services[i]
		}
	}
	return nil
}

// BuildsSource reports whether some service is built from the source.
func (p ComposeProject) BuildsSource() bool {
	for _, s := range p.Services {
		if s.Kind == "build" {
			return true
		}
	}
	return false
}

// ---- shared variable references ----

var envRefRe = regexp.MustCompile(`@@bitploy-env:([A-Za-z_][A-Za-z0-9_]*)@@`)

// EnvRef is the marker that stands for the shared variable name until the
// container starts.
func EnvRef(name string) string { return "@@bitploy-env:" + name + "@@" }

// ExpandEnvRefs fills in the EnvRef markers of s. A variable that is gone
// becomes the empty string.
func ExpandEnvRefs(s string, lookup func(name string) (string, bool)) string {
	if !strings.Contains(s, "@@bitploy-env:") {
		return s
	}
	return envRefRe.ReplaceAllStringFunc(s, func(m string) string {
		v, _ := lookup(envRefRe.FindStringSubmatch(m)[1])
		return v
	})
}

// ---- parsing ----

// Service names are DNS labels: they become network aliases and part of
// container names (bp-<slug>_<service>-<n>), where _ separates the service.
var serviceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ReservedServiceName reports whether a service name could stand for one of
// bitploy's own containers in Docker's DNS. A service name is a network alias
// on bp-<slug>, and Traefik, which sits on every web app's network, finds
// upstreams by container name (bp-<slug>-<n>): an alias bp-<other app>-<n> on
// a network that Docker searches first would take that app's traffic,
// password and cookies included. bitploy's own containers (bitploy,
// bitploy-traefik, bitploy-port-*) start with bitploy.
func ReservedServiceName(name string) bool {
	return strings.HasPrefix(name, "bp-") || strings.HasPrefix(name, "bitploy")
}

var imageRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]{0,254}$`)
var volumeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type composeParser struct {
	in       ComposeInput
	dir      string // folder of the compose file in the source, "." for the root
	file     string
	out      ComposeProject
	order    []string // service names in file order
	unset    map[string]bool
	exposeOf map[string]int // a build service's single EXPOSE, from its Dockerfile
	// per service: marked x-bitploy public, its ports targets and expose entries
	explicitPublic map[string]bool
	portsOf        map[string][]int
	exposeList     map[string][]int
}

// ParseCompose reads, checks and translates a compose file. It never
// fails: problems are in the result's Errors.
func ParseCompose(in ComposeInput) (out ComposeProject) {
	p := &composeParser{in: in, unset: map[string]bool{}, exposeOf: map[string]int{},
		explicitPublic: map[string]bool{}, portsOf: map[string][]int{}, exposeList: map[string][]int{}}
	p.file = cleanRel(in.Path)
	if p.file == "" || p.file == "." {
		p.file = "compose.yaml"
	}
	p.dir = path.Dir(p.file)
	defer func() {
		if r := recover(); r != nil { // compose-go asserts types in places; never take the server down
			out = p.out
			out.Errors = append(out.Errors, ComposeIssue{Code: CodeComposeInvalid,
				Message: fmt.Sprintf("bitploy could not read %s: it is not a valid compose file.", p.file),
				Hint:    p.fixHint()})
		}
	}()
	p.parse()
	return p.out
}

func (p *composeParser) errf(code, service, key, hint, format string, args ...any) {
	p.out.Errors = append(p.out.Errors, ComposeIssue{Code: code, Service: service, Key: key, Message: fmt.Sprintf(format, args...), Hint: hint})
}

func (p *composeParser) warnf(code, service, key, hint, format string, args ...any) {
	p.out.Warnings = append(p.out.Warnings, ComposeIssue{Code: code, Service: service, Key: key, Message: fmt.Sprintf(format, args...), Hint: hint})
}

func (p *composeParser) fixHint() string {
	return "Fix " + p.file + " and check it again with validate_compose before you deploy."
}

func svcPrefix(service string) string {
	if service == "" {
		return ""
	}
	return "Service " + service + ": "
}

func (p *composeParser) parse() {
	if len(p.in.Content) > MaxComposeBytes {
		p.errf(CodeComposeInvalid, "", "", "Keep the compose file small: only the services, their settings and volumes.",
			"%s is %d KB; bitploy reads compose files up to %d KB.", p.file, len(p.in.Content)>>10, MaxComposeBytes>>10)
		return
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(p.in.Content, &doc); err != nil {
		p.errf(CodeComposeInvalid, "", "", p.fixHint(), "%s is not valid YAML: %s.", p.file, strings.TrimPrefix(err.Error(), "yaml: "))
		return
	}
	var raw map[string]any
	if err := doc.Decode(&raw); err != nil || raw == nil {
		p.errf(CodeComposeInvalid, "", "", "A compose file starts with `services:` and lists each service under it.",
			"%s has no services: the top level must be a mapping with a services key.", p.file)
		return
	}
	p.order = serviceOrder(&doc)
	p.scanTop(raw)
	p.scanReferences(raw)
	p.scanImageVariables(raw)
	if len(p.out.Errors) > 0 {
		return
	}
	// The project name is bitploy's; version is obsolete (compose-go would log about it).
	delete(raw, "version")
	delete(raw, "name")

	pass2 := pass2Model(raw)
	proj, err := p.load(raw, func(name string) (string, bool) {
		v, ok := p.in.Env[name]
		if !ok {
			p.unset[name] = true
		}
		return v, ok
	}, false)
	if err != nil {
		p.loadError(err)
		return
	}
	// The second pass reads the values bitploy keeps (environment, command,
	// entrypoint, healthcheck) with markers in place of shared variables.
	refs, err := p.load(pass2, p.refLookup, true)
	if err != nil {
		p.loadError(err)
		return
	}
	p.unsetWarnings(raw)
	p.translate(proj, refs)
	p.checkFilledReferences()
}

// ---- references to other apps ----

// A compose file cannot hold ${{<alias>.VAR}}: compose would read it as a
// broken ${VAR}, and bitploy fills references only into the variables it
// keeps (set_env), when a container starts. So the file is refused with the
// way that works: a shared variable holding the reference, which reaches
// every service and which ${VAR} in environment, command, entrypoint and
// healthcheck can use.
var composeRefRe = regexp.MustCompile(`\$\{\{\s*([a-z0-9][a-z0-9-]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

func referenceHint(name, ref string) string {
	return "Leave it out of the compose file and set it with set_env instead: set_env " + name + "=" + ref +
		" (a shared variable reaches every service). To use it inside a command, write ${" + name + "} there."
}

// scanReferences refuses ${{alias.VAR}} anywhere in a service.
func (p *composeParser) scanReferences(raw map[string]any) {
	svcs, _ := raw["services"].(map[string]any)
	for _, name := range p.order {
		m, ok := svcs[name].(map[string]any)
		if !ok {
			continue
		}
		for _, k := range sortedKeys(m) {
			key, val, found := findReference(k, m[k])
			if !found {
				continue
			}
			ref := composeRefRe.FindString(val)
			if ref == "" {
				ref = "${{...}}"
			}
			varName := "VAR"
			if sm := composeRefRe.FindStringSubmatch(ref); sm != nil {
				varName = sm[2]
			}
			if k == "environment" {
				if strings.HasSuffix(key, "]") {
					if n, _, ok := strings.Cut(val, "="); ok && !strings.Contains(n, "$") {
						varName = strings.TrimSpace(n)
					}
				} else if strings.Contains(key, ".") {
					varName = key[strings.LastIndex(key, ".")+1:]
				}
			}
			p.errf(CodeComposeReference, name, key, referenceHint(varName, ref),
				"%s%s refers to another app with %s; references are not read from compose files.", svcPrefix(name), key, ref)
			break
		}
	}
}

// scanImageVariables refuses a shared variable that has a value in the
// image: of a service that pulls it. The image is shown in the deploy log,
// in errors and in the app's services, which read-only tokens and admins
// can read, while variable values are never shown. ${TAG:-latest} with TAG
// not set (or empty) is fine: the image then holds only the file's text.
// A variable without a value and without a default is refused too: the
// image would lose its name or tag, and setting the variable (what the
// unset warning would ask for) is refused above.
func (p *composeParser) scanImageVariables(raw map[string]any) {
	svcs, _ := raw["services"].(map[string]any)
	const hint = "Write the image and its tag in the compose file itself, like postgres:16-alpine. A ${VAR:-default} there works only while VAR is not set."
	for _, name := range p.order {
		m, ok := svcs[name].(map[string]any)
		if !ok || m["build"] != nil {
			continue // a build service ignores image:
		}
		img, ok := m["image"].(string)
		if !ok {
			continue
		}
		var set, empty []string
		for n, v := range template.ExtractVariables(map[string]any{"image": img}, template.DefaultPattern) {
			switch {
			case p.in.Env[n] != "":
				set = append(set, n)
			case v.DefaultValue == "" && v.PresenceValue == "":
				empty = append(empty, n)
			}
		}
		sort.Strings(set)
		sort.Strings(empty)
		switch {
		case len(set) > 0:
			p.errf(CodeComposeImageVariable, name, "image", hint,
				"%simage: takes the value of %s; variables are kept secret, but the image name is shown in the deploy log.", svcPrefix(name), strings.Join(set, ", "))
		case len(empty) > 0:
			p.errf(CodeComposeImageVariable, name, "image", hint+" Do not set "+strings.Join(empty, " or ")+" with set_env for this: bitploy does not fill image: from variables.",
				"%simage: uses %s, which has no value and no default, so the image has no complete name.", svcPrefix(name), strings.Join(empty, ", "))
		}
	}
}

// findReference finds the first string under v that holds "${{", with its
// key path below key.
func findReference(key string, v any) (string, string, bool) {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "${{") {
			return key, t, true
		}
	case map[string]any:
		for _, k := range sortedKeys(t) {
			if kk, val, ok := findReference(key+"."+k, t[k]); ok {
				return kk, val, ok
			}
		}
	case []any:
		for i, x := range t {
			if kk, val, ok := findReference(fmt.Sprintf("%s[%d]", key, i), x); ok {
				return kk, val, ok
			}
		}
	}
	return "", "", false
}

// checkFilledReferences refuses a ${VAR} whose shared variable holds a
// reference in a setting that is fixed before the containers start (image,
// build, user, working_dir): only environment, command, entrypoint and
// healthcheck are filled in at start.
func (p *composeParser) checkFilledReferences() {
	for _, sv := range p.out.Services {
		fields := []struct{ key, val string }{
			{"image", sv.Image}, {"build.context", sv.Context}, {"build.dockerfile", sv.Dockerfile}, {"build.target", sv.Target},
			{"user", sv.User}, {"working_dir", sv.WorkingDir},
		}
		args := make([]string, 0, len(sv.Args))
		for k := range sv.Args {
			args = append(args, k)
		}
		sort.Strings(args)
		for _, k := range args {
			fields = append(fields, struct{ key, val string }{"build.args." + k, sv.Args[k]})
		}
		for _, f := range fields {
			if !strings.Contains(f.val, "${{") {
				continue
			}
			ref := composeRefRe.FindString(f.val)
			if ref == "" {
				ref = "${{...}}"
			}
			p.errf(CodeComposeReference, sv.Name, f.key,
				"Use a plain value there, or use the variable only in environment, command, entrypoint or healthcheck, which get the value when the container starts.",
				"%s%s takes a variable that holds the reference %s; references are filled in only when a container starts, not in %s.",
				svcPrefix(sv.Name), f.key, ref, f.key)
			break
		}
	}
}

// refLookup returns the marker of a shared variable. An empty variable stays
// empty so ${VAR:-default} still takes the default.
func (p *composeParser) refLookup(name string) (string, bool) {
	v, ok := p.in.Env[name]
	if !ok || v == "" {
		return v, ok
	}
	return EnvRef(name), true
}

func (p *composeParser) load(model map[string]any, lookup func(string) (string, bool), second bool) (*types.Project, error) {
	env := types.Mapping{}
	for k := range p.in.Env {
		if second {
			env[k], _ = p.refLookup(k)
		} else {
			env[k] = p.in.Env[k]
		}
	}
	details := types.ConfigDetails{
		WorkingDir:  "/",
		ConfigFiles: []types.ConfigFile{{Filename: p.file, Config: deepCopy(model).(map[string]any)}},
		Environment: env,
	}
	return loader.LoadWithContext(context.Background(), details, func(o *loader.Options) {
		o.SetProjectName(composeProjectName, true)
		o.ResolvePaths = false
		o.SkipConsistencyCheck = true
		o.SkipResolveEnvironment = true
		o.SkipResolveLabels = true
		o.SkipInclude = true
		o.SkipExtends = true
		o.SkipValidation = second
		o.SkipNormalization = second
		o.Interpolate = &interp.Options{
			LookupValue: lookup,
			Substitute: func(s string, m template.Mapping) (string, error) {
				return template.SubstituteWithOptions(s, m, template.WithoutLogging)
			},
		}
	})
}

var loadErrPath = regexp.MustCompile(`services\.([A-Za-z0-9_-]+)\.([A-Za-z_]+)|services\[([A-Za-z0-9_-]+)\]\.([A-Za-z_]+)`)

func (p *composeParser) loadError(err error) {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	msg = strings.TrimPrefix(msg, "validating "+p.file+": ")
	msg = strings.TrimPrefix(msg, "decoding failed due to the following error(s): ")
	service, key := "", ""
	if m := loadErrPath.FindStringSubmatch(msg); m != nil {
		service, key = m[1]+m[3], m[2]+m[4]
	}
	if strings.Contains(msg, "required variable") || strings.Contains(msg, "is missing a value") {
		p.errf(CodeComposeInvalid, service, key, "Set the variable with set_env (bitploy does not read .env files or the server's environment for ${...}), then deploy again.",
			"%s needs a variable that is not set: %s.", p.file, msg)
		return
	}
	p.errf(CodeComposeInvalid, service, key, p.fixHint(), "%s is not a valid compose file: %s.", p.file, strings.TrimSuffix(msg, "."))
}

// unsetWarnings names the ${VAR}s that are not shared variables and have no default.
func (p *composeParser) unsetWarnings(raw map[string]any) {
	if len(p.unset) == 0 {
		return
	}
	vars := template.ExtractVariables(raw, template.DefaultPattern)
	var names []string
	for n := range p.unset {
		if v, ok := vars[n]; ok && v.DefaultValue == "" && v.PresenceValue == "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	p.warnf(CodeComposeVariableUnset, "", "", "Set them with set_env (shared variables fill ${...} in the compose file; bitploy does not read .env files for this), then deploy again.",
		"%s uses %s, which %s not set, so %s empty.", p.file, strings.Join(names, ", "), plural(len(names), "is", "are"), plural(len(names), "it is", "they are"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// serviceOrder lists the service names in the order of the file.
// ComposeServiceCount is how many services a compose file lists (0 when it
// is not YAML), whether or not they pass the checks.
func ComposeServiceCount(content []byte) int {
	if len(content) > MaxComposeBytes {
		return 0
	}
	var doc yaml.Node
	if yaml.Unmarshal(content, &doc) != nil {
		return 0
	}
	return len(serviceOrder(&doc))
}

func serviceOrder(doc *yaml.Node) []string {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "services" {
			continue
		}
		svcs := root.Content[i+1]
		if svcs.Kind != yaml.MappingNode {
			return nil
		}
		var out []string
		for j := 0; j+1 < len(svcs.Content); j += 2 {
			out = append(out, svcs.Content[j].Value)
		}
		return out
	}
	return nil
}

// pass2Model keeps only what the second pass reads, so values bitploy does
// not keep (ports, sizes) cannot fail to parse with markers in them.
func pass2Model(raw map[string]any) map[string]any {
	svcs, _ := raw["services"].(map[string]any)
	out := map[string]any{}
	for name, v := range svcs {
		m, _ := v.(map[string]any)
		keep := map[string]any{"image": "x"}
		for _, k := range []string{"environment", "command", "entrypoint"} {
			if val, ok := m[k]; ok {
				keep[k] = val
			}
		}
		if hc, ok := m["healthcheck"].(map[string]any); ok {
			if t, ok := hc["test"]; ok {
				keep["healthcheck"] = map[string]any{"test": t}
			}
		}
		out[name] = keep
	}
	return map[string]any{"services": out}
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopy(val)
		}
		return s
	}
	return v
}

// ---- the rules on the raw file ----

const (
	ruleForbidden = iota + 1
	ruleIgnored
	ruleSilent
	ruleHandled
)

// serviceRules says what bitploy does with each service key. Keys not listed
// are ignored with a warning.
var serviceRules = map[string]int{
	"image": ruleHandled, "build": ruleHandled, "environment": ruleHandled, "env_file": ruleHandled,
	"command": ruleHandled, "entrypoint": ruleHandled, "working_dir": ruleHandled, "user": ruleHandled,
	"depends_on": ruleHandled, "healthcheck": ruleHandled, "volumes": ruleHandled, "expose": ruleHandled,
	"ports": ruleHandled, "deploy": ruleHandled, "x-bitploy": ruleHandled, "mem_limit": ruleHandled,
	"cpus": ruleHandled, "profiles": ruleHandled, "scale": ruleHandled, "networks": ruleHandled,

	"restart": ruleSilent, "stdin_open": ruleSilent, "tty": ruleSilent, "develop": ruleSilent,
	"pull_policy": ruleSilent, "cap_drop": ruleSilent, "annotations": ruleSilent, "mem_reservation": ruleSilent,
	"labels": ruleSilent, "attach": ruleSilent,

	"privileged": ruleForbidden, "cap_add": ruleForbidden, "devices": ruleForbidden, "device_cgroup_rules": ruleForbidden,
	"network_mode": ruleForbidden, "net": ruleForbidden, "pid": ruleForbidden, "ipc": ruleForbidden, "uts": ruleForbidden,
	"userns_mode": ruleForbidden, "cgroup": ruleForbidden, "cgroup_parent": ruleForbidden, "sysctls": ruleForbidden,
	"security_opt": ruleForbidden, "extra_hosts": ruleForbidden, "secrets": ruleForbidden, "configs": ruleForbidden,
	"volumes_from": ruleForbidden, "use_api_socket": ruleForbidden, "gpus": ruleForbidden, "runtime": ruleForbidden,
	"extends": ruleForbidden, "external_links": ruleForbidden, "ulimits": ruleForbidden, "oom_kill_disable": ruleForbidden,
	"oom_score_adj": ruleForbidden, "isolation": ruleForbidden, "storage_opt": ruleForbidden, "credential_spec": ruleForbidden,
	"provider": ruleForbidden, "models": ruleForbidden, "group_add": ruleForbidden, "memswap_limit": ruleForbidden,
}

var forbiddenHints = map[string]string{
	"secrets":     "Store secrets as variables with set_env instead; every service gets them in its environment.",
	"configs":     "Put the file in the source and COPY it into the image in the Dockerfile, or pass the values with set_env.",
	"extends":     "Copy the settings into the service, or share them with a YAML anchor (x-common: &common, then <<: *common).",
	"extra_hosts": "Services reach each other by service name; other hosts by their public name.",
	"networks":    "Leave networks out: all services of the app share one private network and reach each other by service name.",
	"secrets_top": "Store secrets as variables with set_env instead; every service gets them in its environment.",
}

func isTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != "" && t != "false" && t != "0"
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	case int:
		return t != 0
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (p *composeParser) scanTop(raw map[string]any) {
	for _, k := range sortedKeys(raw) {
		v := raw[k]
		switch {
		case k == "services", k == "version", k == "name", strings.HasPrefix(k, "x-"):
		case k == "volumes":
			vols, ok := v.(map[string]any)
			if !ok && v != nil {
				p.errf(CodeComposeInvalid, "", "volumes", p.fixHint(), "The top-level volumes must be a mapping of volume names.")
				continue
			}
			for _, name := range sortedKeys(vols) {
				p.scanVolume(name, vols[name])
			}
		case k == "networks":
			if nets, ok := v.(map[string]any); ok {
				for _, n := range sortedKeys(nets) {
					if n != "default" || isTruthy(nets[n]) {
						p.errf(CodeComposeForbidden, "", "networks", forbiddenHints["networks"],
							"Custom networks (%s) are not allowed: bitploy gives each app one private network.", n)
					}
				}
			}
		case k == "secrets", k == "configs":
			p.errf(CodeComposeForbidden, "", k, forbiddenHints[k], "Top-level %s are not allowed: they read files from the server.", k)
		case k == "include":
			p.errf(CodeComposeForbidden, "", k, "Put every service in one compose file.", "include is not allowed: bitploy reads only the one compose file.")
		case k == "models", k == "jobs":
			p.errf(CodeComposeUnsupported, "", k, "Leave it out and deploy the services only.", "bitploy does not support top-level %s.", k)
		default:
			p.warnf(CodeComposeKeyIgnored, "", k, "", "The top-level key %s is not used by bitploy and is ignored.", k)
		}
	}
	svcs, ok := raw["services"].(map[string]any)
	if !ok || len(svcs) == 0 {
		p.errf(CodeComposeNoServices, "", "services", "List at least one service under services:, each with an image: or a build:.",
			"%s has no services.", p.file)
		return
	}
	for _, name := range p.order {
		m, ok := svcs[name].(map[string]any)
		if !ok {
			if svcs[name] != nil {
				p.errf(CodeComposeInvalid, name, "", p.fixHint(), "Service %s must be a mapping of settings.", name)
			}
			continue
		}
		p.scanService(name, m)
	}
}

func (p *composeParser) scanVolume(name string, v any) {
	if !volumeNameRe.MatchString(name) {
		p.errf(CodeComposeInvalid, "", "volumes."+name, "Use letters, digits, - and _ in volume names.", "The volume name %q is not allowed.", name)
	}
	m, _ := v.(map[string]any)
	for _, k := range sortedKeys(m) {
		switch k {
		case "driver", "driver_opts":
			if isTruthy(m[k]) && m[k] != "local" {
				p.errf(CodeComposeForbidden, "", "volumes."+name+"."+k, "Leave driver settings out: bitploy creates the volume on the server's disk.",
					"Volume %s: %s is not allowed.", name, k)
			}
		case "external":
			if isTruthy(m[k]) {
				p.errf(CodeComposeForbidden, "", "volumes."+name+".external", "Remove external: bitploy creates and keeps the app's volumes itself.",
					"Volume %s is external: an app can only use volumes bitploy created for it.", name)
			}
		case "name":
			p.warnf(CodeComposeKeyIgnored, "", "volumes."+name+".name", "", "Volume %s: name is ignored; bitploy names the Docker volume itself.", name)
		case "labels":
		case "x-bitploy":
			xm, ok := m[k].(map[string]any)
			for _, xk := range sortedKeys(xm) {
				if xk != "size_mb" {
					p.warnf(CodeComposeKeyIgnored, "", "volumes."+name+".x-bitploy."+xk, "x-bitploy of a volume knows size_mb.", "Volume %s: x-bitploy.%s is not known and is ignored.", name, xk)
				}
			}
			if !ok && m[k] != nil {
				p.errf(CodeComposeInvalid, "", "volumes."+name+".x-bitploy", "Write it as x-bitploy: {size_mb: 2048}.", "Volume %s: x-bitploy must be a mapping.", name)
			}
		default:
			if !strings.HasPrefix(k, "x-") {
				p.warnf(CodeComposeKeyIgnored, "", "volumes."+name+"."+k, "", "Volume %s: %s is not used by bitploy and is ignored.", name, k)
			}
		}
	}
}

func (p *composeParser) scanService(name string, m map[string]any) {
	if !serviceNameRe.MatchString(name) {
		p.errf(CodeComposeServiceNameInvalid, name, "services."+name,
			"Rename the service to lowercase letters, digits and - (at most 32 characters, starting with a letter or digit; celery_worker becomes celery-worker), and use the new name where other services connect to it.",
			"The service name %q is not allowed.", name)
	} else if ReservedServiceName(name) {
		p.errf(CodeComposeServiceNameInvalid, name, "services."+name,
			"Rename the service, for example to web, db or worker, and use the new name where other services connect to it.",
			"The service name %q is reserved by bitploy (names starting with bp- or bitploy).", name)
	}
	for _, k := range sortedKeys(m) {
		v := m[k]
		rule, known := serviceRules[k]
		switch {
		case strings.HasPrefix(k, "x-") && k != "x-bitploy":
		case rule == ruleForbidden:
			if !isTruthy(v) {
				continue
			}
			hint := forbiddenHints[k]
			if hint == "" {
				hint = "Remove " + k + " from the service. bitploy runs every container locked down, without extra privileges or access to the server."
			}
			p.errf(CodeComposeForbidden, name, k, hint, "%s%s is not allowed on bitploy.", svcPrefix(name), k)
		case k == "networks":
			if isTruthy(v) && !onlyDefaultNetwork(v) {
				p.errf(CodeComposeForbidden, name, k, forbiddenHints["networks"], "%sCustom networks are not allowed: bitploy gives each app one private network.", svcPrefix(name))
			}
		case k == "scale":
			if n, ok := v.(int); ok && n > 1 {
				p.errf(CodeComposeUnsupported, name, k, "Remove scale: bitploy runs one container per service.", "%sscale %d is not supported.", svcPrefix(name), n)
			}
		case k == "build":
			if bm, ok := v.(map[string]any); ok {
				p.scanBuild(name, bm)
			}
		case k == "deploy":
			if dm, ok := v.(map[string]any); ok {
				p.scanDeploy(name, dm)
			}
		case k == "x-bitploy":
			xm, ok := v.(map[string]any)
			if !ok && v != nil {
				p.errf(CodeComposeInvalid, name, k, "Write it as x-bitploy: {public: true, port: 3000, health_path: /health}.", "%sx-bitploy must be a mapping.", svcPrefix(name))
			}
			for _, xk := range sortedKeys(xm) {
				switch xk {
				case "public", "port", "health_path":
				default:
					p.warnf(CodeComposeKeyIgnored, name, "x-bitploy."+xk, "x-bitploy of a service knows public, port and health_path.", "%sx-bitploy.%s is not known and is ignored.", svcPrefix(name), xk)
				}
			}
		case k == "links":
			p.warnf(CodeComposeKeyIgnored, name, k, "", "%slinks are not needed and are ignored: services reach each other by service name.", svcPrefix(name))
		case k == "container_name":
			p.warnf(CodeComposeKeyIgnored, name, k, "Other services reach it by its service name, "+name+".", "%scontainer_name is ignored: bitploy names containers itself.", svcPrefix(name))
		case known:
		default:
			p.warnf(CodeComposeKeyIgnored, name, k, "", "%s%s is not used by bitploy and is ignored.", svcPrefix(name), k)
		}
	}
}

func onlyDefaultNetwork(v any) bool {
	switch t := v.(type) {
	case []any:
		return len(t) == 1 && t[0] == "default"
	case map[string]any:
		_, ok := t["default"]
		return len(t) == 1 && ok && !isTruthy(t["default"])
	}
	return false
}

func (p *composeParser) scanBuild(name string, m map[string]any) {
	for _, k := range sortedKeys(m) {
		v := m[k]
		key := "build." + k
		switch k {
		case "context", "dockerfile", "args", "target":
		case "network":
			if v != nil && v != "default" {
				p.errf(CodeComposeForbidden, name, key, "Remove build.network: builds run on bitploy's isolated builder.", "%sbuild.network is not allowed.", svcPrefix(name))
			}
		case "privileged", "entitlements", "ssh", "secrets", "extra_hosts", "additional_contexts":
			if isTruthy(v) {
				p.errf(CodeComposeForbidden, name, key, "Remove "+key+": builds run on bitploy's isolated builder, from your code's files only.", "%s%s is not allowed.", svcPrefix(name), key)
			}
		case "dockerfile_inline":
			p.errf(CodeComposeUnsupported, name, key, "Put the Dockerfile in your code and point build.dockerfile at it.", "%sbuild.dockerfile_inline is not supported.", svcPrefix(name))
		default:
			if !strings.HasPrefix(k, "x-") {
				p.warnf(CodeComposeKeyIgnored, name, key, "", "%s%s is not used by bitploy and is ignored.", svcPrefix(name), key)
			}
		}
	}
}

func (p *composeParser) scanDeploy(name string, m map[string]any) {
	for _, k := range sortedKeys(m) {
		v := m[k]
		switch k {
		case "resources":
			rm, _ := v.(map[string]any)
			for _, rk := range sortedKeys(rm) {
				switch rk {
				case "limits":
					lm, _ := rm[rk].(map[string]any)
					for _, lk := range sortedKeys(lm) {
						switch lk {
						case "memory", "cpus":
						case "devices":
							if isTruthy(lm[lk]) {
								p.errf(CodeComposeForbidden, name, "deploy.resources.limits.devices", "Remove the devices: apps cannot use the server's hardware.", "%sdevices are not allowed.", svcPrefix(name))
							}
						default:
							p.warnf(CodeComposeKeyIgnored, name, "deploy.resources.limits."+lk, "", "%sdeploy.resources.limits.%s is ignored.", svcPrefix(name), lk)
						}
					}
				case "reservations":
					if lm, ok := rm[rk].(map[string]any); ok && isTruthy(lm["devices"]) {
						p.errf(CodeComposeForbidden, name, "deploy.resources.reservations.devices", "Remove the devices: apps cannot use the server's hardware.", "%sdevices are not allowed.", svcPrefix(name))
						continue
					}
					p.warnf(CodeComposeKeyIgnored, name, "deploy.resources.reservations", "Set the memory and CPU a service gets with deploy.resources.limits.", "%sdeploy.resources.reservations is ignored.", svcPrefix(name))
				default:
					p.warnf(CodeComposeKeyIgnored, name, "deploy.resources."+rk, "", "%sdeploy.resources.%s is ignored.", svcPrefix(name), rk)
				}
			}
		case "replicas":
			if n, ok := v.(int); ok && n > 1 {
				p.errf(CodeComposeUnsupported, name, "deploy.replicas", "Remove replicas: bitploy runs one container per service.", "%sdeploy.replicas %d is not supported.", svcPrefix(name), n)
			}
		case "restart_policy", "labels":
		default:
			p.warnf(CodeComposeKeyIgnored, name, "deploy."+k, "", "%sdeploy.%s is not used by bitploy and is ignored.", svcPrefix(name), k)
		}
	}
}

// ---- translation ----

func (p *composeParser) translate(proj, refs *types.Project) {
	for _, name := range sortedNames(proj.DisabledServices) {
		p.warnf(CodeComposeServiceSkipped, name, "profiles", "Remove profiles from the service to deploy it.",
			"%sit only starts with a profile (%s), so bitploy leaves it out.", svcPrefix(name), strings.Join(proj.DisabledServices[name].Profiles, ", "))
	}
	var names []string
	for _, n := range p.order {
		if _, ok := proj.Services[n]; ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		p.errf(CodeComposeNoServices, "", "services", "Remove profiles from the services bitploy should run.", "%s has no service that starts without a profile.", p.file)
		return
	}
	if len(names) > MaxComposeServices {
		p.errf(CodeComposeTooManyServices, "", "services",
			fmt.Sprintf("Remove services until there are at most %d. Often a cache or queue can go: use SQLite, or a table in the database you already have.", MaxComposeServices),
			"%s has %d services; an app on bitploy can have at most %d.", p.file, len(names), MaxComposeServices)
		return
	}
	users := map[string][]string{} // volume -> services that mount it
	for _, n := range names {
		sc := proj.Services[n]
		svc, ok := p.service(n, sc, refs.Services[n], proj)
		if !ok {
			continue
		}
		for _, m := range svc.Mounts {
			users[m.Volume] = append(users[m.Volume], n)
		}
		p.out.Services = append(p.out.Services, svc)
	}
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	for _, s := range p.out.Services {
		for _, d := range s.DependsOn {
			if !known[d.Service] {
				if _, off := proj.DisabledServices[d.Service]; off {
					p.errf(CodeComposeInvalid, s.Name, "depends_on", "Remove profiles from "+d.Service+", or remove it from depends_on.",
						"%sit depends on %s, which bitploy leaves out because it only starts with a profile.", svcPrefix(s.Name), d.Service)
				} else {
					p.errf(CodeComposeInvalid, s.Name, "depends_on", "Name a service of this file in depends_on.", "%sit depends on %s, which is not a service in %s.", svcPrefix(s.Name), d.Service, p.file)
				}
			}
		}
	}
	p.checkCycles()
	p.volumes(proj, users)
	p.choosePublic()
	p.defaultMemory()
}

func sortedNames(m types.Services) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (p *composeParser) checkCycles() {
	deps := map[string][]string{}
	for _, s := range p.out.Services {
		for _, d := range s.DependsOn {
			deps[s.Name] = append(deps[s.Name], d.Service)
		}
	}
	state := map[string]int{}
	var visit func(n string, trail []string) bool
	visit = func(n string, trail []string) bool {
		switch state[n] {
		case 1:
			p.errf(CodeComposeInvalid, n, "depends_on", "Remove one of the depends_on entries so the services can start one after the other.",
				"The services depend on each other in a circle: %s.", strings.Join(append(trail, n), " -> "))
			return false
		case 2:
			return true
		}
		state[n] = 1
		for _, d := range deps[n] {
			if !visit(d, append(trail, n)) {
				return false
			}
		}
		state[n] = 2
		return true
	}
	for _, s := range p.out.Services {
		if !visit(s.Name, nil) {
			return
		}
	}
}

func (p *composeParser) service(name string, sc, ref types.ServiceConfig, proj *types.Project) (ComposeService, bool) {
	svc := ComposeService{Name: name, HealthPath: "/"}
	errs := len(p.out.Errors)
	switch {
	case sc.Build != nil:
		svc.Kind = "build"
		p.build(&svc, sc.Build)
		if sc.Image != "" {
			p.warnf(CodeComposeKeyIgnored, name, "image", "", "%sit has both build and image; bitploy builds it and ignores image.", svcPrefix(name))
		}
	case strings.TrimSpace(sc.Image) != "":
		svc.Kind = "image"
		svc.Image = strings.TrimSpace(sc.Image)
		if !imageRefRe.MatchString(svc.Image) {
			p.errf(CodeComposeInvalid, name, "image", "Use an image name like postgres:16-alpine or ghcr.io/owner/name:tag.",
				"%sthe image %q is not a valid image name.", svcPrefix(name), svc.Image)
		} else if forbiddenImage(svc.Image) {
			p.errf(CodeBaseForbidden, name, "image", "Use a public image, or build the service from your code with build:.",
				"%sthe image %s is not allowed: bitploy/* images belong to the apps on this server.", svcPrefix(name), svc.Image)
		}
	default:
		p.errf(CodeComposeInvalid, name, "image", "Give the service an image: (like postgres:16-alpine) or a build: (the folder with its Dockerfile).",
			"%sit has neither image nor build.", svcPrefix(name))
	}

	// x-bitploy
	if x, ok := sc.Extensions["x-bitploy"].(map[string]any); ok {
		if v, ok := x["public"]; ok {
			b, isBool := v.(bool)
			if !isBool {
				p.errf(CodeComposeInvalid, name, "x-bitploy.public", "Use public: true or public: false.", "%sx-bitploy.public must be true or false.", svcPrefix(name))
			}
			svc.Public = b
			if b {
				svc.Port = -1 // marked; port decided below
			}
		}
		if v, ok := x["port"]; ok {
			n, ok := toInt(v)
			if !ok || n < 1 || n > 65535 {
				p.errf(CodeComposeInvalid, name, "x-bitploy.port", "Use the port the service listens on, like 3000.", "%sx-bitploy.port must be a port number.", svcPrefix(name))
			}
			svc.Port = n
		}
		if v, ok := x["health_path"].(string); ok {
			if !strings.HasPrefix(v, "/") {
				p.errf(CodeComposeInvalid, name, "x-bitploy.health_path", "Use a path like /health.", "%sx-bitploy.health_path must start with /.", svcPrefix(name))
			}
			svc.HealthPath = v
		}
	}
	p.explicitPublic[name] = svc.Public

	// ports and expose (container ports only; nothing is published)
	var targets []int
	published := false
	for _, pc := range sc.Ports {
		if pc.Protocol != "" && pc.Protocol != "tcp" {
			continue
		}
		if pc.Target > 0 && !containsInt(targets, int(pc.Target)) {
			targets = append(targets, int(pc.Target))
		}
		if pc.Published != "" {
			published = true
		}
	}
	p.portsOf[name] = targets
	if published {
		p.warnf(CodeComposePortsIgnored, name, "ports", "",
			"%sbitploy does not open ports on the server; other services reach it at %s:<port> and the public service at the app's URL.", svcPrefix(name), name)
	}
	for _, e := range sc.Expose {
		if n, ok := leadingPort(e); ok && !containsInt(p.exposeList[name], n) {
			p.exposeList[name] = append(p.exposeList[name], n)
		}
	}

	// resources, within the bounds of a single app. The CPU count is checked
	// as a float, so a huge cpus: cannot overflow int64.
	mem, cpus := int64(0), float64(0)
	memKey, cpuKey := "deploy.resources.limits.memory", "deploy.resources.limits.cpus"
	if sc.Deploy != nil && sc.Deploy.Resources.Limits != nil {
		mem = int64(sc.Deploy.Resources.Limits.MemoryBytes)
		cpus = float64(sc.Deploy.Resources.Limits.NanoCPUs)
	}
	if mem == 0 && sc.MemLimit != 0 {
		mem, memKey = int64(sc.MemLimit), "mem_limit"
	}
	if cpus == 0 && sc.CPUS > 0 {
		cpus, cpuKey = float64(sc.CPUS), "cpus"
	}
	if mem > MaxMemoryMB<<20 {
		p.errf(CodeComposeInvalid, name, memKey, fmt.Sprintf("Use at most %d GB, like 512m or 2g.", MaxMemoryMB>>10),
			"%sthe memory limit is more than %d GB, the most one app can have.", svcPrefix(name), MaxMemoryMB>>10)
	} else if mem > 0 {
		svc.MemoryMB = (mem + (1<<20 - 1)) >> 20
		if svc.MemoryMB < composeMinMemoryMB {
			p.warnf(CodeComposeKeyIgnored, name, memKey, "", "%sthe memory limit is raised to the minimum of %d MB.", svcPrefix(name), composeMinMemoryMB)
			svc.MemoryMB = composeMinMemoryMB
		}
	}
	cpu := int64(0)
	if math.IsNaN(cpus) || cpus > MaxCPUMillis/1000 {
		p.errf(CodeComposeInvalid, name, cpuKey, fmt.Sprintf("Use at most %d CPUs, like \"0.5\" or \"2\".", MaxCPUMillis/1000),
			"%sthe CPU limit is more than %d CPUs, the most one app can have.", svcPrefix(name), MaxCPUMillis/1000)
	} else if cpus > 0 {
		cpu = int64(math.Round(cpus * 1000))
	}
	if cpu > 0 {
		svc.CPUMillis = cpu
		if cpu < composeMinCPUMillis {
			p.warnf(CodeComposeKeyIgnored, name, cpuKey, "", "%sthe CPU limit is raised to the minimum of 0.05.", svcPrefix(name))
			svc.CPUMillis = composeMinCPUMillis
		}
	} else {
		svc.CPUMillis = ComposeDefaultCPUMillis
	}

	// what runs
	if ref.Command != nil {
		svc.Command = []string(ref.Command)
	}
	if ref.Entrypoint != nil {
		svc.Entrypoint = []string(ref.Entrypoint)
	}
	svc.User, svc.WorkingDir = sc.User, sc.WorkingDir
	if sc.WorkingDir != "" && !strings.HasPrefix(sc.WorkingDir, "/") {
		p.errf(CodeComposeInvalid, name, "working_dir", "Use an absolute path like /app.", "%sworking_dir must be an absolute path.", svcPrefix(name))
	}
	if hc := sc.HealthCheck; hc != nil {
		svc.Healthcheck = healthcheckOf(hc, ref.HealthCheck)
	}
	for _, dn := range sortedDeps(sc.DependsOn) {
		d := sc.DependsOn[dn]
		cond := d.Condition
		switch cond {
		case "", types.ServiceConditionStarted:
			cond = types.ServiceConditionStarted
		case types.ServiceConditionHealthy:
		case types.ServiceConditionCompletedSuccessfully:
			p.errf(CodeComposeUnsupported, name, "depends_on."+dn,
				"bitploy has no one-off jobs. Run the step in the start command of "+name+" instead (for example sh -c \"npm run migrate && npm start\") and remove "+dn+".",
				"%sdepends_on with condition service_completed_successfully is not supported.", svcPrefix(name))
			continue
		default:
			p.errf(CodeComposeInvalid, name, "depends_on."+dn, "Use service_started or service_healthy.", "%sthe depends_on condition %q is not known.", svcPrefix(name), cond)
			continue
		}
		svc.DependsOn = append(svc.DependsOn, ComposeDependency{Service: dn, Condition: cond})
	}

	// environment: env_file first, then environment over it
	env := map[string]string{}
	for i, ef := range sc.EnvFiles {
		p.envFile(name, i, ef, env)
	}
	for k, v := range ref.Environment {
		if v != nil {
			env[k] = *v
		}
	}
	for k := range env {
		if reservedName(k) {
			delete(env, k)
			p.warnf(CodeComposeKeyIgnored, name, "environment."+k, "", "%s%s is set by bitploy itself and is ignored here.", svcPrefix(name), k)
		}
	}
	if len(env) > 0 {
		svc.Environment = env
	}

	// volumes
	p.mounts(&svc, sc, proj)

	if svc.Kind == "build" && p.in.Read != nil && svc.Dockerfile != "" {
		if df := p.in.Read(svc.Dockerfile); df != nil {
			res := CheckDockerfile(CheckInput{Dockerfile: df, DockerfilePath: svc.Dockerfile, AppType: "web"})
			if res.Port > 0 {
				p.exposeOf[name] = res.Port
			}
		}
	}
	return svc, len(p.out.Errors) == errs
}

func reservedName(n string) bool {
	u := strings.ToUpper(n)
	return u == "PORT" || strings.HasPrefix(u, "BITPLOY_")
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func leadingPort(s string) (int, bool) {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	return n, err == nil && n > 0 && n <= 65535
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case uint64:
		return int(t), true
	case float64:
		return int(t), t == math.Trunc(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

func sortedDeps(d types.DependsOnConfig) []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func secondsOf(d *types.Duration) int {
	if d == nil {
		return 0
	}
	return int(math.Ceil(time.Duration(*d).Seconds()))
}

func healthcheckOf(hc, ref *types.HealthCheckConfig) *ComposeHealthcheck {
	if hc.Disable {
		return &ComposeHealthcheck{Test: []string{"NONE"}}
	}
	test := []string(hc.Test)
	if ref != nil && len(ref.Test) > 0 {
		test = []string(ref.Test)
	}
	if len(test) == 0 {
		return nil
	}
	out := &ComposeHealthcheck{Test: test, IntervalS: secondsOf(hc.Interval), TimeoutS: secondsOf(hc.Timeout), StartPeriodS: secondsOf(hc.StartPeriod)}
	if hc.Retries != nil {
		out.Retries = int(*hc.Retries)
	}
	return out
}

// joinSource joins rel onto the compose file's folder; ok is false when the
// result leaves the source.
func (p *composeParser) joinSource(rel string) (string, bool) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if path.IsAbs(rel) || strings.HasPrefix(rel, "~") {
		return "", false
	}
	j := path.Clean(path.Join(p.dir, rel))
	if j == ".." || strings.HasPrefix(j, "../") {
		return "", false
	}
	return j, true
}

func (p *composeParser) build(svc *ComposeService, b *types.BuildConfig) {
	ctx := b.Context
	if ctx == "" {
		ctx = "."
	}
	if strings.Contains(ctx, "://") || strings.HasPrefix(ctx, "git@") || strings.HasSuffix(ctx, ".git") {
		p.errf(CodeComposeForbidden, svc.Name, "build.context", "Point build.context at a folder of your code, like . or ./backend.",
			"%sthe build context %s is not a folder of your code; bitploy builds only from the files you send.", svcPrefix(svc.Name), ctx)
		return
	}
	c, ok := p.joinSource(ctx)
	if !ok {
		p.errf(CodeComposeForbidden, svc.Name, "build.context", "Point build.context at a folder inside your code, like . or ./backend.",
			"%sthe build context %s is outside your code.", svcPrefix(svc.Name), ctx)
		return
	}
	svc.Context = c
	df := b.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	df = strings.ReplaceAll(df, "\\", "/")
	if path.IsAbs(df) {
		p.errf(CodeComposeForbidden, svc.Name, "build.dockerfile", "Give the Dockerfile's path relative to build.context.", "%sthe Dockerfile path %s is absolute.", svcPrefix(svc.Name), df)
		return
	}
	full := path.Clean(path.Join(c, df))
	if full == ".." || strings.HasPrefix(full, "../") {
		p.errf(CodeComposeForbidden, svc.Name, "build.dockerfile", "Keep the Dockerfile inside your code.", "%sthe Dockerfile %s is outside your code.", svcPrefix(svc.Name), df)
		return
	}
	svc.Dockerfile = full
	svc.Target = b.Target
	for k, v := range b.Args {
		if v == nil {
			continue
		}
		if svc.Args == nil {
			svc.Args = map[string]string{}
		}
		svc.Args[k] = *v
	}
}

func (p *composeParser) envFile(service string, i int, ef types.EnvFile, env map[string]string) {
	key := fmt.Sprintf("env_file[%d]", i)
	rel, ok := p.joinSource(ef.Path)
	if !ok {
		p.errf(CodeComposeForbidden, service, key, "Keep env files inside your code, or set the variables with set_env.",
			"%sthe env file %s is outside your code.", svcPrefix(service), ef.Path)
		return
	}
	if p.in.Read == nil {
		p.warnf(CodeComposeEnvFileMissing, service, key, "Deploy your code with that file, or set the variables with set_env.",
			"%sthe env file %s is not checked: only the compose file was sent.", svcPrefix(service), rel)
		return
	}
	data := p.in.Read(rel)
	if data == nil {
		if !bool(ef.Required) {
			return
		}
		p.errf(CodeComposeEnvFileMissing, service, key,
			"Add the file to your code, mark it required: false, or remove env_file and set the variables with set_env (best for secrets).",
			"%sthe env file %s is not in your code.", svcPrefix(service), rel)
		return
	}
	if len(data) > maxEnvFileBytes {
		p.errf(CodeComposeInvalid, service, key, "Keep env files small, or set the variables with set_env.", "%sthe env file %s is too big.", svcPrefix(service), rel)
		return
	}
	vals, err := dotenv.UnmarshalBytesWithLookup(data, func(name string) (string, bool) {
		if v, ok := env[name]; ok {
			return v, true
		}
		return p.refLookup(name)
	})
	if err != nil {
		p.errf(CodeComposeInvalid, service, key, "Write one NAME=value per line.", "%sthe env file %s cannot be read: %s.", svcPrefix(service), rel, err)
		return
	}
	for k, v := range vals {
		env[k] = v
	}
}

func (p *composeParser) mounts(svc *ComposeService, sc types.ServiceConfig, proj *types.Project) {
	for i, v := range sc.Volumes {
		key := fmt.Sprintf("volumes[%d]", i)
		switch v.Type {
		case types.VolumeTypeBind:
			if strings.Contains(v.Source, "docker.sock") || strings.Contains(v.Target, "docker.sock") {
				p.errf(CodeComposeForbidden, svc.Name, key, "Remove the mount. No app on bitploy gets control over Docker.",
					"%smounting the Docker socket (%s) is not allowed: it would give the app control over the whole server.", svcPrefix(svc.Name), v.Source)
				continue
			}
			p.errf(CodeComposeForbidden, svc.Name, key,
				"Use a named volume for data (volumes: [\"data:"+v.Target+"\"] and data: under the top-level volumes), and COPY files the app needs into the image in its Dockerfile.",
				"%sthe bind mount %s:%s is not allowed: apps cannot use folders of the server.", svcPrefix(svc.Name), v.Source, v.Target)
		case types.VolumeTypeTmpfs:
			p.warnf(CodeComposeKeyIgnored, svc.Name, key, "", "%sthe tmpfs mount at %s is ignored.", svcPrefix(svc.Name), v.Target)
		case types.VolumeTypeVolume:
			if v.Source == "" {
				p.warnf(CodeComposeKeyIgnored, svc.Name, key, "Give it a name (pgdata:"+v.Target+") and list it under the top-level volumes to keep its data.",
					"%sthe anonymous volume at %s is ignored; its data would be lost on every deploy.", svcPrefix(svc.Name), v.Target)
				continue
			}
			if _, ok := proj.Volumes[v.Source]; !ok {
				p.errf(CodeComposeInvalid, svc.Name, key, "Add "+v.Source+": under the top-level volumes.", "%sthe volume %s is not listed under the top-level volumes.", svcPrefix(svc.Name), v.Source)
				continue
			}
			if !path.IsAbs(v.Target) || path.Clean(v.Target) == "/" {
				p.errf(CodeComposeInvalid, svc.Name, key, "Mount the volume at a folder like /data.", "%sthe volume %s must be mounted at an absolute folder other than /.", svcPrefix(svc.Name), v.Source)
				continue
			}
			for _, m := range svc.Mounts {
				if m.Volume == v.Source {
					p.errf(CodeComposeUnsupported, svc.Name, key, "Mount each volume once.", "%sthe volume %s is mounted twice.", svcPrefix(svc.Name), v.Source)
				}
			}
			if v.ReadOnly {
				p.warnf(CodeComposeKeyIgnored, svc.Name, key, "", "%sread_only on the volume %s is ignored; it is mounted read-write.", svcPrefix(svc.Name), v.Source)
			}
			svc.Mounts = append(svc.Mounts, ComposeMount{Volume: v.Source, Target: path.Clean(v.Target)})
		default:
			p.errf(CodeComposeForbidden, svc.Name, key, "Use a named volume.", "%svolumes of type %s are not allowed.", svcPrefix(svc.Name), v.Type)
		}
	}
}

func (p *composeParser) volumes(proj *types.Project, users map[string][]string) {
	var names []string
	for n := range proj.Volumes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		u := users[n]
		if len(u) == 0 {
			p.warnf(CodeComposeKeyIgnored, "", "volumes."+n, "", "The volume %s is not mounted by any service and is not created.", n)
			continue
		}
		if len(u) > 1 {
			p.errf(CodeComposeVolumeShared, u[1], "volumes", "Give each service its own volume. Services share data through the database or over the network, not through a shared folder.",
				"The volume %s is mounted by %s; on bitploy a volume belongs to one service.", n, strings.Join(u, " and "))
			continue
		}
		size := int64(ComposeDefaultVolumeMB)
		if x, ok := proj.Volumes[n].Extensions["x-bitploy"].(map[string]any); ok {
			if v, ok := x["size_mb"]; ok {
				s, ok := toInt(v)
				if !ok || s < composeMinVolumeMB || s > composeMaxVolumeMB {
					p.errf(CodeComposeInvalid, "", "volumes."+n+".x-bitploy.size_mb", fmt.Sprintf("Use a size in MB from %d up, like 2048.", composeMinVolumeMB),
						"Volume %s: size_mb must be a number of MB, at least %d.", n, composeMinVolumeMB)
				} else {
					size = int64(s)
				}
			}
		}
		vol := ComposeVolume{Name: n, SizeMB: size, Service: u[0]}
		for _, s := range p.out.Services {
			for _, m := range s.Mounts {
				if s.Name == u[0] && m.Volume == n {
					vol.MountPath = m.Target
				}
			}
		}
		p.out.Volumes = append(p.out.Volumes, vol)
	}
}

// choosePublic decides the public service and its port (docs/compose-plan.md):
// the one marked x-bitploy public: true; else the one with ports (a service
// built from the source wins over images, which often publish a database port
// for local use); else the one service with expose or an EXPOSE in its
// Dockerfile. No candidate: every service is a worker.
func (p *composeParser) choosePublic() {
	svcs := p.out.Services
	var marked []int
	for i, s := range svcs {
		if p.explicitPublic[s.Name] {
			marked = append(marked, i)
		}
	}
	pick := -1
	switch {
	case len(marked) > 1:
		p.errf(CodeComposePublicAmbiguous, svcs[marked[1]].Name, "x-bitploy.public", "Mark only the service that serves the website with x-bitploy: {public: true}.",
			"Several services are marked public (%s); an app has one public service.", namesOf(svcs, marked))
		return
	case len(marked) == 1:
		pick = marked[0]
	default:
		var withPorts []int
		for i, s := range svcs {
			if len(p.portsOf[s.Name]) > 0 {
				withPorts = append(withPorts, i)
			}
		}
		if len(withPorts) > 1 {
			var built []int
			for _, i := range withPorts {
				if svcs[i].Kind == "build" {
					built = append(built, i)
				}
			}
			if len(built) == 1 {
				withPorts = built
			}
		}
		switch {
		case len(withPorts) == 1:
			pick = withPorts[0]
		case len(withPorts) > 1:
			p.errf(CodeComposePublicAmbiguous, "", "ports",
				"Add x-bitploy: {public: true} to the service that serves the website. The others stay private and are reached by service name.",
				"Several services have ports (%s), so it is not clear which one is the website.", namesOf(svcs, withPorts))
			return
		default:
			var exposing []int
			for i, s := range svcs {
				if len(p.exposeList[s.Name]) > 0 || p.exposeOf[s.Name] > 0 {
					exposing = append(exposing, i)
				}
			}
			if len(exposing) == 1 {
				pick = exposing[0]
			} else if len(exposing) > 1 {
				p.errf(CodeComposePublicAmbiguous, "", "expose",
					"Add x-bitploy: {public: true} (or ports: [\"<port>\"]) to the service that serves the website.",
					"Several services expose ports (%s), so it is not clear which one is the website.", namesOf(svcs, exposing))
				return
			}
		}
	}
	for i := range svcs {
		if i != pick {
			svcs[i].Public = false
			if svcs[i].Port == -1 {
				svcs[i].Port = 0
			}
		}
	}
	if pick < 0 {
		return
	}
	s := &svcs[pick]
	s.Public = true
	if s.Port <= 0 {
		s.Port = 0
		switch {
		case len(p.portsOf[s.Name]) > 0:
			s.Port = p.portsOf[s.Name][0]
			if len(p.portsOf[s.Name]) > 1 {
				p.warnf(CodeComposeKeyIgnored, s.Name, "ports", "Set the port with x-bitploy: {port: <number>} if it is another one.",
					"%sit has several ports; the website is served from the first, %d.", svcPrefix(s.Name), s.Port)
			}
		case len(p.exposeList[s.Name]) > 0:
			s.Port = p.exposeList[s.Name][0]
		case p.exposeOf[s.Name] > 0:
			s.Port = p.exposeOf[s.Name]
		}
	}
	if s.Port == ReservedPort {
		p.errf(CodePortReserved, s.Name, "ports", "Make the service listen on another port.", "%sport %d is reserved for bitploy's own proxy.", svcPrefix(s.Name), ReservedPort)
	}
}

// defaultMemory gives services without a memory limit the default: 256 MB,
// 512 MB for a public service built from the source.
func (p *composeParser) defaultMemory() {
	for i := range p.out.Services {
		s := &p.out.Services[i]
		if s.MemoryMB == 0 {
			s.MemoryMB = ComposeDefaultMemoryMB
			if s.Public && s.Kind == "build" {
				s.MemoryMB = ComposePublicBuildMemoryMB
			}
		}
	}
}

func namesOf(svcs []ComposeService, idx []int) string {
	out := make([]string, len(idx))
	for i, j := range idx {
		out[i] = svcs[j].Name
	}
	return strings.Join(out, ", ")
}
