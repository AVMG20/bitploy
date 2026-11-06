package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/deploy"
)

// This file turns service results into what an agent reads: a one-line
// summary, structured data and next_steps that name the tool to call next.

// limitsInfo lists the hard limits an agent must plan around.
func limitsInfo() map[string]any {
	return map[string]any{
		"deploy_files_max_files":      deploy.MaxInlineFiles,
		"deploy_files_max_bytes":      deploy.MaxInlineBytes,
		"zip_upload_max_bytes":        deploy.MaxZipCompressed,
		"zip_unpacked_max_bytes":      deploy.MaxZipUnpacked,
		"zip_max_files":               deploy.MaxZipFiles,
		"upload_url_valid_minutes":    15,
		"git_clone_max_bytes":         500 << 20,
		"build_timeout_minutes":       int(deploy.BuildTimeout.Minutes()),
		"image_max_bytes":             deploy.MaxImageBytes,
		"health_check_seconds":        60,
		"worker_counts_healthy_after": 30,
		"get_deployment_wait_max_sec": maxWaitSec,
		"logs_max_lines":              1000,
		"images_kept_for_rollback":    3,
		"env_vars_max":                100,
	}
}

// changeRules says which tool applies which kind of change.
func changeRules() map[string]string {
	return map[string]string{
		"code_or_dockerfile":      "linked repository: commit and push (deploys by itself when auto-deploy is on, otherwise deploy); files: deploy_files again with ALL files (it replaces the whole source); zip: create_upload, then deploy",
		"env_vars":                "set_env, then restart_app (app is running) or deploy with only app= (app is failed or never ran)",
		"volume_memory_cpu":       "update_app, then restart_app",
		"port_health_path_etc":    "update_app, then deploy with only app= (rebuilds the last source, no files needed)",
		"slug_rename":             "update_app slug: applies at once, the URL changes, a few seconds of downtime; files in /data are kept",
		"storage_limit":           "update_app volume_mb: applies at once, within the account's storage allowance",
		"persistent_files":        "new app: create_app volume=true; existing app: update_app volume=true, restart_app; the app writes to /data and its user must own /data",
		"back_to_working_version": "rollback (only when the user asks)",
	}
}

// rulesDoc is the deploy rules markdown plus facts about this server and user.
func (s *srv) rulesDoc(ctx context.Context, a app.Actor) (string, error) {
	md, err := s.svc.DeployRules(ctx, a)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(md)
	b.WriteString("\n## This server\n\n")
	fmt.Fprintf(&b, "- Apps are served at %s (base domain %s).\n", s.svc.Cfg.AppURL("<slug>"), s.svc.Cfg.AppsDomain)
	if me, err := s.svc.Me(ctx, a); err == nil && len(me.AppDomains) > 1 {
		names := make([]string, 0, len(me.AppDomains)-1)
		for _, d := range me.AppDomains[1:] {
			names = append(names, d.Domain)
		}
		fmt.Fprintf(&b, "- This user may also put apps on: %s. Pass domain= to create_app, update_app or deploy_catalog_app, only when the user asks for it.\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "- Dashboard (API tokens, usage, and Settings > GitHub for private repos): %s\n", s.svc.Cfg.PublicURL)
	if me, err := s.svc.Me(ctx, a); err == nil {
		fmt.Fprintf(&b, "- Your token scope: %s (read can look only; deploy can change things).\n", me.Scope)
		fmt.Fprintf(&b, "- Your quota: %d apps, %d MB memory and %d CPU millis across all apps, %d deployments per hour, %d custom domains per app, %d MB storage for app files (kept images and source) plus the /data volumes (each app with a volume reserves its volume_mb, default 5120).\n",
			me.Quota.Apps, me.Quota.MemoryMB, me.Quota.CPUMillis, me.Quota.DeploysPerHour, me.Quota.DomainsPerApp, me.Quota.VolumeMB)
		fmt.Fprintf(&b, "- You use %d apps, %d MB memory and %.0f MB of storage right now (%.0f MB app files, %d MB kept for /data).\n",
			me.Usage.Apps, me.Usage.MemoryReservedMB, me.Usage.FilesMB+float64(me.Usage.VolumeReservedMB), me.Usage.FilesMB, me.Usage.VolumeReservedMB)
	}
	return b.String(), nil
}

// ---- apps ----

type deploymentBrief struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
}

func briefOf(d app.Deployment) *deploymentBrief {
	b := &deploymentBrief{ID: d.ID, Seq: d.Seq, State: d.State}
	if d.Error != nil {
		b.ErrorCode = d.Error.Code
	}
	return b
}

// appBrief is the compact per-app entry of list_apps.
type appBrief struct {
	Slug              string           `json:"slug"`
	Name              string           `json:"name"`
	Type              string           `json:"type"`
	URL               string           `json:"url"`
	Status            string           `json:"status"`
	StatusMessage     string           `json:"status_message,omitempty"`
	Healthy           bool             `json:"healthy"`
	Problem           string           `json:"problem,omitempty"`
	Source            string           `json:"source,omitempty"`
	Protected         bool             `json:"protected"`
	Static            bool             `json:"static,omitempty"`
	Volume            bool             `json:"volume"`
	VolumeMB          int64            `json:"volume_limit_mb,omitempty"`
	VolumeUsedMB      *float64         `json:"volume_used_mb,omitempty"`
	FilesMB           *float64         `json:"files_mb,omitempty"`
	CurrentDeployment *deploymentBrief `json:"current_deployment"`
	LatestDeployment  *deploymentBrief `json:"latest_deployment"`
	Domains           []string         `json:"domains"`
	RestartCount      int64            `json:"restart_count"`
	CPUPercent        *float64         `json:"cpu_percent,omitempty"`
	MemoryMB          *float64         `json:"memory_mb,omitempty"`
	MemoryLimitMB     int64            `json:"memory_limit_mb"`
	Project           string           `json:"project,omitempty"`
	Alias             string           `json:"alias,omitempty"`
	Template          string           `json:"template,omitempty"`
}

func newAppBrief(ap app.App, latest *deploymentBrief) appBrief {
	b := appBrief{
		Slug: ap.Slug, Name: ap.Name, Type: ap.Type, URL: ap.URL, Status: ap.Status, StatusMessage: ap.StatusMessage, Protected: ap.Protected, Static: ap.Static,
		Volume: ap.Volume, VolumeUsedMB: ap.VolumeUsedMB, FilesMB: ap.FilesMB, LatestDeployment: latest, Domains: []string{}, RestartCount: ap.RestartCount, MemoryLimitMB: ap.MemoryMB,
	}
	if ap.Volume {
		b.VolumeMB = ap.VolumeMB
	}
	b.Project, b.Alias = ap.Project.Name, ap.Alias
	if ap.Template != nil {
		b.Template = *ap.Template
	}
	if ap.Current != nil {
		b.CurrentDeployment = briefOf(*ap.Current)
	}
	switch {
	case ap.GitSource != nil:
		b.Source = "git " + ap.GitSource.RepoURL
		if ap.GitSource.PushReady {
			b.Source += " (deploys on every push to " + ap.GitSource.Branch + ")"
		}
	case ap.Current != nil:
		b.Source = ap.Current.SourceKind
	}
	for _, d := range ap.Domains {
		b.Domains = append(b.Domains, d.Hostname+" ("+d.Status+")")
	}
	if ap.Live != nil {
		cpu, mem := ap.Live.CPU, ap.Live.MemoryMB
		b.CPUPercent, b.MemoryMB = &cpu, &mem
	}
	switch ap.Status {
	case "failed":
		b.Problem = "the last deploy failed and nothing is live"
		if runtimeFailure(ap) {
			b.Problem = "the live version stopped working"
		}
	case "crashed":
		b.Problem = "the app stopped on its own"
	case "running":
		if latest != nil && latest.State == "failed" {
			b.Problem = "the newest deploy failed (" + latest.ErrorCode + "); the previous version is still live"
		} else if ap.Live != nil && ap.MemoryMB > 0 && ap.Live.MemoryMB > 0.9*float64(ap.MemoryMB) {
			b.Problem = fmt.Sprintf("memory almost full (%.0f of %d MB)", ap.Live.MemoryMB, ap.MemoryMB)
		}
	}
	if b.Problem != "" && ap.StatusMessage != "" {
		b.Problem += ": " + oneLine(ap.StatusMessage)
	}
	b.Healthy = ap.Status == "running" && b.Problem == ""
	return b
}

func listAppsResult(briefs []appBrief) *result {
	if len(briefs) == 0 {
		return &result{
			Summary: "0 apps.",
			Data:    data("apps", []appBrief{}, "counts", map[string]int{"total": 0, "healthy": 0, "need_attention": 0}),
			Next:    []string{"There are no apps yet. Call create_app to make one, then send the code with deploy_files."},
		}
	}
	healthy, attention, fresh := 0, 0, 0
	var lines []string
	var next []string
	for _, b := range briefs {
		line := fmt.Sprintf("- %s \"%s\" [%s] %s", b.Slug, b.Name, b.Status, b.URL)
		if b.Problem != "" {
			attention++
			line += " PROBLEM: " + b.Problem
			if len(next) < 3 {
				next = append(next, fmt.Sprintf("Check %s: call get_deployment with app=%s for the error and log tail, or get_logs with kind=runtime.", b.Slug, b.Slug))
			}
		} else if b.Healthy {
			healthy++
		} else if b.Status == "new" {
			fresh++
		}
		lines = append(lines, line)
	}
	next = append(next,
		"The app for the code is the one in its .bitploy file. Without .bitploy, only reuse an app if the user says it is already online and name AND url match (ask the user if unsure); otherwise create_app for a NEW app. Never overwrite an app only because its name is similar.",
		"Call get_app for the full settings of one app.")
	return &result{
		Summary: fmt.Sprintf("%d app(s): %d healthy, %d need attention, %d not deployed yet.\n%s", len(briefs), healthy, attention, fresh, strings.Join(lines, "\n")),
		Data:    data("apps", briefs, "counts", map[string]int{"total": len(briefs), "healthy": healthy, "need_attention": attention}),
		Next:    next,
	}
}

func appLine(ap app.App) string {
	url := ap.URL
	if url == "" {
		url = "no web address"
	}
	s := fmt.Sprintf("%s (%s, %s): status %s, %s", ap.Slug, ap.Name, ap.Type, ap.Status, url)
	if ap.Protected {
		s += ", password-protected"
	}
	if ap.PublicPort.Enabled {
		s += ", public port " + ap.PublicPort.Address + " open (close it with set_public_port when done)"
	}
	if ap.Static {
		s += ", static site: does not count towards memory/CPU"
	}
	if ap.Current != nil {
		s += ", deployment " + ap.Current.ID + " " + ap.Current.State
	}
	if ap.Project.ID != "" {
		s += ", in project " + ap.Project.Name + " as " + ap.Alias
		if ap.InternalHost != "" {
			s += " (" + ap.InternalHost + ")"
		}
	}
	if ap.Template != nil {
		s += ", service template " + *ap.Template + " " + ap.TemplateVersion
		if ap.TemplateUpdateAvailable {
			s += " (" + ap.TemplateLatest + " is available; not applied automatically)"
		}
	}
	if len(ap.Services) > 1 {
		names := make([]string, len(ap.Services))
		for i, sv := range ap.Services {
			names[i] = sv.Name
			if sv.Public {
				names[i] += " (public)"
			}
		}
		s += ", services " + strings.Join(names, ", ")
	}
	if ap.StatusMessage != "" {
		s += ". " + oneLine(ap.StatusMessage)
	}
	return s + "."
}

// updateHow says how to publish new code to this app.
func updateHow(slug, sourceKind string) string {
	switch sourceKind {
	case "git":
		return "commit and push to the repository's branch: with auto-deploy on that starts a deployment by itself (follow it with get_deployment, app=" + slug + ", wait_seconds=30); otherwise call deploy with app=" + slug + " after the push"
	case "upload":
		return "zip the folder again: create_upload with app=" + slug + ", run its curl command, then deploy with the new upload_id"
	}
	return "call deploy_files with app=" + slug + " and ALL files (it replaces the whole source, it is not a patch)"
}

// appUpdateHow says how to publish new code to a specific app: a linked
// repository always means pushing, whatever was deployed before.
func appUpdateHow(ap app.App) string {
	if ap.Template != nil {
		return "call deploy with app=" + ap.Slug + " to rebuild it from its template (its data stays)"
	}
	if ap.GitSource != nil {
		if ap.GitSource.PushReady {
			return "commit and push to " + ap.GitSource.Branch + " (bitploy deploys the push by itself; follow it with get_deployment, app=" + ap.Slug + ", wait_seconds=30)"
		}
		return "commit and push to " + ap.GitSource.Branch + ", then call deploy with app=" + ap.Slug
	}
	kind := ""
	if ap.Current != nil {
		kind = ap.Current.SourceKind
	}
	return updateHow(ap.Slug, kind)
}

// repoName is owner/name for GitHub repositories, else the address.
func repoName(g *app.GitSourceState) string {
	if g.Repo != "" {
		return g.Repo
	}
	return g.RepoURL
}

// gitStatusNext tells an agent what a linked repository means for updates and,
// when pushes cannot deploy yet, what the user has to do.
func gitStatusNext(ap app.App, settingsURL, adminURL string) []string {
	g := ap.GitSource
	if g == nil {
		return nil
	}
	switch g.PushStatus {
	case "ready":
		return []string{"Pushes to " + g.Branch + " deploy by themselves. To update the app, commit and push, then call get_deployment with app=" + ap.Slug + " and wait_seconds=30. Call deploy only to rebuild."}
	case "off":
		return []string{"Auto-deploy is off: after each push call deploy with app=" + ap.Slug + ". The user can turn it on in the app's Settings, or you can call set_git_source again with auto_deploy=true."}
	case "other_host":
		return []string{"This git host cannot tell bitploy about pushes: after each push call deploy with app=" + ap.Slug + "."}
	case "no_webhook":
		return []string{"GitHub cannot reach this bitploy, so pushes do not deploy by themselves: after each push call deploy with app=" + ap.Slug + "."}
	case "app_missing":
		return []string{"Pushes do not deploy by themselves because GitHub is not set up on this bitploy. Tell the user: an admin sets it up on the Admin page (" + adminURL + "), then each person connects GitHub in Settings > GitHub. Until then call deploy with app=" + ap.Slug + " after each push."}
	}
	return []string{"Pushes do not deploy by themselves yet: " + g.PushMessage + " Tell the user to open Settings > GitHub (" + settingsURL + "). Until then call deploy with app=" + ap.Slug + " after each push."}
}

func bitployFileStep(ap app.App) string {
	content := fmt.Sprintf(`{"app":%q}`, ap.Slug)
	if ap.URL != "" {
		content = fmt.Sprintf(`{"app":%q,"url":%q}`, ap.Slug, ap.URL)
	}
	return "Save " + content + " as the file .bitploy in the code folder (do not send it with the deploy) so a later session updates this app instead of creating a new one."
}

// runtimeFailure reports a failed status that came after the deploy: the
// current deployment went live and then ran out of memory, died, failed its
// health check or filled its volume.
func runtimeFailure(ap app.App) bool {
	return ap.Status == "failed" && ap.Current != nil && ap.Current.State == "running"
}

func appNext(ap app.App) []string {
	slug := ap.Slug
	switch ap.Status {
	case "new":
		return []string{"Nothing is deployed yet. Send the code with deploy_files (up to 2 MB and 200 files), create_upload (zip) or set_git_source then deploy."}
	case "failed":
		if runtimeFailure(ap) {
			return []string{
				"The deploy worked, but the live version stopped afterwards; status_message says why.",
				"Out of memory: update_app with a higher memory_mb (whoami shows the quota), then restart_app. The /data volume over its limit: ask the user to delete files from /data, then start_app.",
				"Otherwise call get_logs with app=" + slug + " and kind=runtime; fix a code bug, then " + appUpdateHow(ap) + ", or restart_app for a one-off failure.",
			}
		}
		return []string{
			"The last deploy failed and nothing is live. Call get_deployment with app=" + slug + " for the error, hint and log tail.",
			"Fix the cause, then " + appUpdateHow(ap) + ".",
		}
	case "crashed":
		return []string{
			"The app stopped on its own. Call get_logs with app=" + slug + " and kind=runtime to see why.",
			"A missing environment variable: set_env then restart_app. A code bug: fix it, then " + appUpdateHow(ap) + ". A one-off crash: restart_app.",
		}
	case "stopped":
		return []string{"The app is stopped on purpose. Call start_app only if the user wants it running again."}
	case "deploying":
		return []string{"A deploy is in progress. Call get_deployment with app=" + slug + " and wait_seconds=30 until it is running or failed."}
	}
	if ap.Template != nil {
		return templateNext(ap)
	}
	url := ap.URL
	if url == "" {
		url = "(worker, no web address)"
	}
	return []string{
		"The app is running at " + url + ". To publish changes: " + appUpdateHow(ap) + ".",
		"If the user reports a problem: get_logs with kind=runtime; for usage: get_metrics.",
	}
}

// templateNext is what to do with a running database (or cache) added with
// add_service: connect apps to it, never send it code.
func templateNext(ap app.App) []string {
	where := ""
	if ap.InternalHost != "" {
		where = " at " + ap.InternalHost
	}
	out := []string{fmt.Sprintf("This is a %s service: no web address and no code to send. The other apps of project %s reach it%s.", *ap.Template, ap.Project.Name, where)}
	if ap.MainVar != "" {
		out = append(out, fmt.Sprintf("To connect an app of the project: set_env app=<slug> set={\"%s\":\"${{%s.%s}}\"}, then restart_app. Its variables: %s.",
			ap.MainVar, ap.Alias, ap.MainVar, strings.Join(ap.ConnectionVars, ", ")))
	}
	if ap.TemplateUpdateAvailable {
		out = append(out, "bitploy has "+ap.TemplateLatest+" while this runs "+ap.TemplateVersion+". It is never upgraded automatically, because a new major version can need a dump and restore. Mention it only if the user asks about versions; there is no upgrade tool.")
	}
	if !ap.BackupsSupported {
		out = append(out, "bitploy makes no backups of it: its data can be rebuilt. Do not offer create_backup for it.")
	}
	return out
}

// validateComposeResult sums up validate_compose for an AI.
func validateComposeResult(p app.ComposePreview, slug string) *result {
	names := make([]string, len(p.Services))
	for i, sv := range p.Services {
		names[i] = sv.Name
		if sv.Public {
			names[i] += " (public)"
		}
	}
	d := toMap(p)
	if !p.OK {
		first := p.Errors[0]
		where := ""
		if first.Service != "" {
			where = " in service " + first.Service
		}
		if first.Key != "" {
			where += " at " + first.Key
		}
		next := []string{"Fix every error (each has a hint), then call validate_compose again."}
		for _, e := range p.Errors {
			if e.Code == "quota_exceeded" {
				next = append(next, "To fit the quota give the services less in the compose file (deploy.resources.limits.memory, a database or Redis often runs in 128M) or ask the user to stop an app; whoami shows the quota.")
				break
			}
		}
		return &result{
			Summary: fmt.Sprintf("The compose file has %d error(s); first: %s%s: %s", len(p.Errors), first.Code, where, first.Message),
			Data:    d,
			Next:    next,
		}
	}
	summary := fmt.Sprintf("The compose file is valid: %d service(s): %s. %s", len(p.Services), strings.Join(names, ", "), p.Quota.Message)
	if len(p.Warnings) > 0 {
		summary += fmt.Sprintf(" %d warning(s).", len(p.Warnings))
	}
	next := []string{"Read the warnings: they say what bitploy ignores."}
	if slug == "" {
		next = append(next, "Create the app with create_app build_mode=compose (or, for an app that has one service now, update_app build_mode=compose), then send the code with the compose file and every Dockerfile it builds.")
	} else {
		next = append(next, "Deploy it: push to the linked repository, deploy_files with ALL files (compose file included), or create_upload; then get_deployment with wait_seconds=30.")
	}
	return &result{Summary: summary, Data: d, Next: next}
}

func updateAppNext(in updateAppIn, ap app.App) []string {
	var out []string
	deployed := ap.Current != nil
	if in.BuildMode != nil && *in.BuildMode == "compose" {
		out = append(out, "From the next deploy on, "+ap.Slug+" runs every service of its compose file. Check the file with validate_compose app="+ap.Slug+", then deploy (deploy with only app= rebuilds the last source, or send new files).")
	}
	if in.Slug != nil && ap.URL != "" {
		out = append(out, "The app now lives at "+ap.URL+"; the old address stops working. Tell the user, and update the url in the .bitploy file.")
	}
	if in.Volume != nil && !*in.Volume {
		out = append(out, "/data is off: its files and the backups are deleted, and the app restarts without it.")
	}
	if in.MemoryMB != nil || in.CPUMillis != nil || (in.Volume != nil && *in.Volume) {
		if deployed {
			out = append(out, "Call restart_app with app="+ap.Slug+" to apply volume, memory and CPU (no rebuild, a few seconds).")
		} else {
			out = append(out, "These settings apply at the first deploy.")
		}
	}
	if in.Port != nil || in.HealthPath != nil || in.DockerfilePath != nil || in.ContextDir != nil || in.Type != nil || (in.BuildMode != nil && *in.BuildMode != "compose") || in.ComposePath != nil {
		if deployed || ap.Status == "failed" {
			out = append(out, "Call deploy with only app="+ap.Slug+" to rebuild with these settings (no files needed), then get_deployment with wait_seconds=30.")
		} else {
			out = append(out, "These settings apply at the first deploy.")
		}
	}
	if in.Volume != nil && *in.Volume {
		out = append(out, "The app must save files it needs to keep in /data, and its user must own that folder: in the Dockerfile add RUN mkdir /data && chown <user> /data before USER. Anything outside /data is lost on redeploy.")
	}
	if len(out) == 0 {
		out = append(out, "Done. Call get_app to see the current settings.")
	}
	return out
}

func setEnvNext(slug string, cur *app.App) []string {
	if cur == nil {
		return []string{"The values apply when the app next starts: call restart_app if it is running, or deploy with only app=" + slug + " if it is failed or never ran."}
	}
	switch {
	case cur.Status == "new":
		return []string{"Nothing is deployed yet; the first deploy will use these variables. Send the code with deploy_files."}
	case cur.Status == "stopped":
		return []string{"The app is stopped; the variables apply when it is started again (start_app, only if the user wants it running)."}
	case cur.Status == "deploying":
		return []string{"A deploy is in progress and may have read the old values. When it ends, call restart_app with app=" + slug + " to be sure."}
	case cur.Current == nil:
		return []string{"The app has no running version yet. Call deploy with only app=" + slug + " (rebuilds the last files, no resend needed), then get_deployment with wait_seconds=30."}
	}
	return []string{
		"Call restart_app with app=" + slug + " to apply the variables (no rebuild, a few seconds). The running app does not see them until then.",
		"Then call get_app, or get_logs with kind=runtime if it does not come up.",
	}
}

// ---- deployments ----

func redoStep(d app.Deployment) string {
	switch d.SourceKind {
	case "git":
		return "push the fix to the branch (a push deploys by itself when auto-deploy is on, otherwise call deploy with app=" + d.AppSlug + ")"
	case "upload":
		return "zip again with create_upload (app=" + d.AppSlug + ") and deploy with the new upload_id"
	case "rollback":
		return "call list_deployments to pick another deployment"
	}
	return "call deploy_files again with app=" + d.AppSlug + " and ALL files"
}

// failureSteps gives specific next steps for a failed deployment.
func failureSteps(d app.Deployment) []string {
	slug := d.AppSlug
	redo := redoStep(d)
	var out []string
	e := d.Error
	if e == nil {
		return []string{"Read log_tail for the cause, fix it, then " + redo + "."}
	}
	if e.Hint != "" && len(e.Hint) <= 300 && !strings.Contains(e.Hint, "\n") {
		out = append(out, "Fix: "+e.Hint)
	}
	switch e.Code {
	case "dockerfile_missing":
		out = append(out, "Call get_template for the stack named in the error, save its Dockerfile and .dockerignore in the code folder, then "+redo+".")
	case "port_unknown", "port_ambiguous", "port_reserved":
		out = append(out, "Either keep exactly one EXPOSE <port> in the last stage of the Dockerfile and "+redo+", or call update_app with port=<number> and then deploy with only app="+slug+".")
	case "build_failed":
		out = append(out, "Read log_tail: the first error line is the cause (a missing file, a wrong package name, a missing package-lock.json for npm ci). node-gyp, better-sqlite3, bcrypt or sharp errors: use FROM node:22-bookworm-slim instead of an -alpine image, not apk add. Prisma on Alpine: RUN apk add --no-cache openssl. Fix it, then "+redo+".")
	case "start_failed":
		out = append(out, "Check the Dockerfile's CMD/ENTRYPOINT, then "+redo+". If the message says it is a server problem, tell the user to contact the bitploy admin.")
	case "unhealthy", "out_of_memory", "app_crashed":
		switch {
		case strings.Contains(e.Message, "ran out of memory"):
			out = append(out, "Call update_app with a higher memory_mb (whoami shows the quota) and then deploy with only app="+slug+"; or make the app use less memory.")
		case strings.Contains(e.Hint, "0.0.0.0") && strings.Contains(e.Hint, "localhost"):
			out = append(out, "Change the code so the server listens on 0.0.0.0 and the PORT variable, then "+redo+".")
		case strings.Contains(e.Message, "stopped") || strings.Contains(e.Message, "worker"):
			out = append(out, "The program crashed or exited: read log_tail. A missing environment variable (KeyError, undefined, 'is required'): set_env, then deploy with only app="+slug+". Otherwise fix the code, then "+redo+".")
		case strings.Contains(e.Message, "HEALTHCHECK"):
			out = append(out, "Fix the HEALTHCHECK command in the Dockerfile (or remove it), then "+redo+".")
		default:
			out = append(out, "The app started but did not answer. Read log_tail. If it listens on another port, call update_app with port=<number>; if the health path returns 500, call update_app with health_path=/healthz (any path below 500); then deploy with only app="+slug+".")
		}
	case "image_too_large":
		out = append(out, "Shrink the image (smaller base, multi-stage build, a .dockerignore), then "+redo+".")
	case "deploy_rate_limited":
		out = append(out, "Wait as the hint says and tell the user; do not retry in a loop. Before the next deploy, fix and test as much as you can locally.")
	default:
		if strings.HasPrefix(e.Code, "source_") || strings.HasPrefix(e.Code, "github_") {
			out = append(out, "Check the repository address, branch and subfolder with set_git_source, then call deploy with app="+slug+".")
		} else {
			out = append(out, "Read log_tail for the cause, fix it, then "+redo+".")
		}
	}
	if e.Next != "" && !healthFailure(e.Code) {
		out = append(out, "Suggested next tool: "+e.Next+".")
	}
	return out
}

// healthFailure reports the deploy error codes of an app that started but did not come up.
func healthFailure(code string) bool {
	return code == "unhealthy" || code == "out_of_memory" || code == "app_crashed"
}

// deploymentResult renders a deployment with state-specific next steps.
func deploymentResult(verb string, d app.Deployment) *result {
	if d.Error != nil && healthFailure(d.Error.Code) && !strings.Contains(d.Error.Hint, "0.0.0.0") && deploy.MentionsLocalhost(d.LogTail) {
		// Safety net for the most common failure; the pipeline normally sets this hint itself.
		e := *d.Error
		e.Hint = deploy.LocalhostHint + ". For example Node: app.listen(process.env.PORT || 3000, \"0.0.0.0\"); Flask: app.run(host=\"0.0.0.0\", port=int(os.environ.get(\"PORT\", 8000))). Then deploy again."
		d.Error = &e
	}
	var next []string
	summary := fmt.Sprintf("%s %s of %s (#%d): %s", verb, d.ID, d.AppSlug, d.Seq, d.State)
	switch d.State {
	case "running":
		if d.URL != "" {
			summary += ". Live at " + d.URL
		} else {
			summary += ". The worker is running"
		}
		who := "the URL " + d.URL
		if d.URL == "" {
			who = "that the worker is running"
		}
		if d.SourceKind == "rollback" {
			next = []string{"The previous version is live again. Tell the user " + who + " and that it is the older version."}
		} else {
			next = []string{"Give the user " + who + " and one or two plain sentences on what you did."}
		}
		next = append(next,
			"To publish changes later: "+updateHow(d.AppSlug, d.SourceKind)+". The URL stays the same.",
			bitployFileStep(app.App{Slug: d.AppSlug, URL: d.URL}),
		)
		if len(d.Warnings) > 0 {
			next = append(next, "Mention the warnings only if they matter to the user.")
		}
	case "failed":
		if d.Error != nil {
			summary += " with " + d.Error.Code + ": " + oneLine(d.Error.Message)
		}
		next = failureSteps(d)
		next = append(next, "After 3 failures with the same error code, stop and explain the problem to the user in plain words.")
	case "cancelled":
		if d.Error != nil && d.Error.Code == "cancelled" {
			next = []string{"The deployment was cancelled; it is not a failure. The version that was live keeps running. Fix the cause if there was one, then deploy again when the user wants it."}
		} else {
			next = []string{"A newer deployment replaced this one. Call get_app to find the current deployment."}
		}
	case "superseded":
		next = []string{"A newer deployment replaced this one. Call get_app to find the current deployment."}
	default:
		if strings.HasPrefix(verb, "Cancel") {
			next = []string{"It is still stopping. Call get_deployment with id=" + d.ID + " and wait_seconds=10 until the state is cancelled."}
			break
		}
		next = []string{"Call get_deployment with id=" + d.ID + " and wait_seconds=30 until the state is running or failed (a first build can take a few minutes)."}
		if verb != "Deployment" {
			next = append(next, "While waiting there is nothing else to do; do not call deploy again.")
		}
	}
	if len(d.NextSteps) > 0 && !d.Terminal() {
		next = append(next, d.NextSteps...)
	}
	d.NextSteps = nil
	return &result{Summary: summary + ".", Data: data("deployment", d), Next: dedupe(next)}
}

// ---- domains ----

func domainRecordLines(d app.Domain) []string {
	var out []string
	for _, r := range d.Records {
		out = append(out, fmt.Sprintf("%s record, name %s, value %s", r.Type, r.Name, r.Value))
	}
	return out
}

// dnsMessage is a plain-language explanation the agent can pass to the user.
func dnsMessage(d app.Domain) string {
	if d.Status == "active" {
		return "Your domain " + d.Hostname + " is connected and live at https://" + d.Hostname + "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "To use %s for your site, add these records where you manage the domain (the company you bought it from, for example GoDaddy, Namecheap, TransIP or Cloudflare). Look for \"DNS settings\" or \"DNS records\" and add each line exactly as written:\n", d.Hostname)
	for _, l := range domainRecordLines(d) {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString("Some providers want only the first part of the name (for example www instead of www.example.com). ")
	b.WriteString("Changes need 5 to 30 minutes, sometimes up to an hour. Tell me when you have added them and I will check.")
	for _, n := range d.Notes {
		b.WriteString("\nNote: " + n)
	}
	return b.String()
}

// ---- metrics ----

func metricsResult(slug, window string, m app.Metrics) *result {
	var peakCPU, peakMem float64
	for _, p := range m.Points {
		peakCPU = maxf(peakCPU, maxf(p.CPU, p.CPUMax))
		peakMem = maxf(peakMem, maxf(p.MemoryMB, p.MemMaxMB))
	}
	total := len(m.Points)
	// Keep the answer small: at most 48 evenly spread samples.
	pts := m.Points
	if total > 48 {
		step := float64(total) / 48
		sampled := make([]app.MetricPoint, 0, 48)
		for i := 0; i < 48; i++ {
			sampled = append(sampled, m.Points[int(float64(i)*step)])
		}
		pts = sampled
	}
	m.Points = nonNil(pts)
	d := toMap(m)
	d["points_total"] = total
	d["peak_cpu_percent"] = peakCPU
	d["peak_memory_mb"] = peakMem
	summary := fmt.Sprintf("%d sample(s) for %s over %s.", total, slug, window)
	if m.Latest != nil {
		summary += fmt.Sprintf(" Now: CPU %.1f%%, memory %.0f of %d MB.", m.Latest.CPU, m.Latest.MemoryMB, m.MemoryLimitMB)
	}
	if total > 0 {
		summary += fmt.Sprintf(" Peak: CPU %.1f%%, memory %.0f MB.", peakCPU, peakMem)
	}
	var next []string
	switch {
	case total == 0:
		next = []string{"No samples: the app is not running or started moments ago. Call get_app to check its status."}
	case m.MemoryLimitMB > 0 && peakMem > 0.85*float64(m.MemoryLimitMB):
		next = []string{
			fmt.Sprintf("Memory peaked at %.0f of %d MB: close to the limit, the app may be killed (out of memory). Call update_app with a higher memory_mb (whoami shows the quota), then restart_app; or make the app use less.", peakMem, m.MemoryLimitMB),
		}
	default:
		next = []string{"Usage looks fine. CPU is a percentage of one core; the memory limit is " + fmt.Sprint(m.MemoryLimitMB) + " MB. Account totals are in whoami."}
	}
	return &result{Summary: summary, Data: d, Next: next}
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
