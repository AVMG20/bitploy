package app

// The build history (build_history): one row per deployment that used a
// build slot, written when the deployment ends, with the server's highest
// CPU and memory while it built. Admins see the builds on the server charts
// (AdminServerMetrics), the builds per person (AdminBuildStats) and the
// builds of one person (AdminListBuilds). Rows outlive their app; the nightly
// metrics prune deletes them after 90 days.

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	// maxChartBuilds caps the builds the server metrics return (the newest).
	maxChartBuilds = 500
	// maxPersonBuilds caps the builds of one person AdminListBuilds returns.
	maxPersonBuilds = 100
	// buildResultInProgress is the result of a build whose deployment has not ended.
	buildResultInProgress = "in_progress"
)

// buildWindows are the windows of the build stats.
var buildWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// BuildRecord is one build: a deployment that used a build slot.
type BuildRecord struct {
	DeploymentID string     `json:"deployment_id"`
	AppID        string     `json:"app_id"`
	App          string     `json:"app" doc:"The app's slug at the time of the build"`
	AppName      string     `json:"app_name"`
	PersonID     string     `json:"person_id" doc:"Who started the build, else the app's owner (a git push)"`
	Person       string     `json:"person" doc:"Their name; empty when the account is gone"`
	Channel      string     `json:"channel" doc:"dashboard, api, mcp or system (a git push)"`
	SourceKind   string     `json:"source_kind" doc:"files, upload or git"`
	QueuedAt     time.Time  `json:"queued_at"`
	StartedAt    time.Time  `json:"started_at" doc:"When it took a build slot"`
	EndedAt      *time.Time `json:"ended_at" doc:"When the build step ended: the image was built, else the deployment ended. Null while it builds"`
	FinishedAt   *time.Time `json:"finished_at" doc:"When the deployment ended; null while it runs"`
	WaitSeconds  int64      `json:"wait_seconds" doc:"From queued to started"`
	BuildSeconds int64      `json:"build_seconds" doc:"From started to ended_at, or to now while it builds"`
	Result       string     `json:"result" enum:"live,failed,cancelled,in_progress"`
	ErrorCode    *string    `json:"error_code"`
	PeakCPU      *float64   `json:"peak_cpu" doc:"Highest CPU of the whole server (%) while it built; null without samples or while it runs"`
	PeakMemoryMB *float64   `json:"peak_memory_mb" doc:"Highest memory used on the server (MB) while it built"`
}

// BuildPersonStats is what one person built in the window.
type BuildPersonStats struct {
	UserID              string     `json:"user_id"`
	Name                string     `json:"name" doc:"Empty when the account is gone"`
	Email               string     `json:"email"`
	Builds              int        `json:"builds"`
	Failed              int        `json:"failed"`
	TotalBuildSeconds   int64      `json:"total_build_seconds"`
	AvgBuildSeconds     int64      `json:"avg_build_seconds"`
	LongestBuildSeconds int64      `json:"longest_build_seconds"`
	LongestApp          string     `json:"longest_app" doc:"The slug of the app of the longest build"`
	PeakCPU             *float64   `json:"peak_cpu" doc:"Highest server CPU (%) during any of their builds"`
	PeakMemoryMB        *float64   `json:"peak_memory_mb" doc:"Highest server memory used (MB) during any of their builds"`
	LastBuildAt         *time.Time `json:"last_build_at"`
}

// BuildStats is GET /admin/builds/stats: the builds per person, most build time first.
type BuildStats struct {
	Window string             `json:"window" enum:"24h,7d,30d"`
	People []BuildPersonStats `json:"people"`
}

// ---- writing ----

// recordBuild writes the history row of a deployment that ended. dep is its
// row as it ended (finished_at is now when it has none: bitploy shutting
// down), app the app it belongs to. Rollbacks and deployments that never
// took a build slot have no row; a second call for the same deployment does
// nothing.
func (s *Service) recordBuild(ctx context.Context, dep db.Deployment, app db.App, result, code string) {
	if dep.SourceKind == "rollback" || !dep.StartedAt.Valid {
		return
	}
	finished := s.now()
	if dep.FinishedAt.Valid {
		finished = dep.FinishedAt.Int64
	}
	end := finished
	if dep.BuiltAt.Valid {
		end = dep.BuiltAt.Int64
	}
	person := app.UserID
	if dep.CreatedBy.Valid && dep.CreatedBy.String != "" {
		person = dep.CreatedBy.String
	}
	cpu, mem := s.hostPeak(ctx, dep.StartedAt.Int64, end)
	err := s.Q.InsertBuildHistory(ctx, db.InsertBuildHistoryParams{
		DeploymentID: dep.ID, AppID: app.ID, AppSlug: app.Slug, AppName: app.Name,
		OwnerID: app.UserID, StartedBy: dep.CreatedBy, PersonID: person,
		Channel: dep.Channel, SourceKind: dep.SourceKind,
		QueuedAt: dep.QueuedAt, StartedAt: dep.StartedAt.Int64, BuiltAt: dep.BuiltAt, FinishedAt: finished,
		Result: result, ErrorCode: nullStr(code), PeakCpu: cpu, PeakMemMb: mem,
	})
	if err != nil {
		s.Log.Error("write build history", "deployment", dep.ID, "err", err)
	}
}

// hostPeak is the highest server CPU (%) and memory used (MB) from start to
// end, from the stored 15-second host samples. A sample covers the interval
// before it, so the one after end counts too. Null without samples (off
// Linux, or the build was too long ago).
func (s *Service) hostPeak(ctx context.Context, start, end int64) (cpu, memMB sql.NullFloat64) {
	step := int64(s.metricsMgr().SampleInterval / time.Second)
	if step <= 0 {
		step = 15
	}
	row, err := s.Q.HostPeakBetween(ctx, db.HostPeakBetweenParams{Ts: start, Ts_2: end + step})
	if err != nil || row.Samples == 0 {
		return
	}
	return sql.NullFloat64{Float64: round1(row.Cpu), Valid: true}, sql.NullFloat64{Float64: round1(float64(row.MemUsedBytes) / mib), Valid: true}
}

// ---- reading ----

func buildRecord(h db.BuildHistory, person string) BuildRecord {
	end := h.FinishedAt
	if h.BuiltAt.Valid {
		end = h.BuiltAt.Int64
	}
	r := BuildRecord{
		DeploymentID: h.DeploymentID, AppID: h.AppID, App: h.AppSlug, AppName: h.AppName,
		PersonID: h.PersonID, Person: person, Channel: h.Channel, SourceKind: h.SourceKind,
		QueuedAt: ts(h.QueuedAt), StartedAt: ts(h.StartedAt), EndedAt: timePtr(ts(end)), FinishedAt: timePtr(ts(h.FinishedAt)),
		WaitSeconds: max(0, h.StartedAt-h.QueuedAt), BuildSeconds: max(0, end-h.StartedAt),
		Result: h.Result, ErrorCode: strPtr(h.ErrorCode),
	}
	if h.PeakCpu.Valid {
		r.PeakCPU = &h.PeakCpu.Float64
	}
	if h.PeakMemMb.Valid {
		r.PeakMemoryMB = &h.PeakMemMb.Float64
	}
	return r
}

func timePtr(t time.Time) *time.Time { return &t }

// chartBuilds are the builds that overlap the window from since to now, oldest
// first: the ones in the history and the ones that run now (their ended_at is
// null until the image is built). At most maxChartBuilds, the newest.
func (s *Service) chartBuilds(ctx context.Context, since time.Time) ([]BuildRecord, error) {
	now := s.now()
	live, err := s.Q.ListDeploymentsBuildingNow(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BuildRecord, 0, len(live))
	seen := make(map[string]bool, len(live))
	for _, d := range live {
		if d.BuiltAt.Valid && d.BuiltAt.Int64 < since.Unix() {
			continue
		}
		person := d.OwnerID
		if d.CreatedBy.Valid && d.CreatedBy.String != "" {
			person = d.CreatedBy.String
		}
		started := d.StartedAt.Int64
		r := BuildRecord{
			DeploymentID: d.ID, AppID: d.AppID, App: d.AppSlug, AppName: d.AppName, PersonID: person, Person: d.PersonName,
			Channel: d.Channel, SourceKind: d.SourceKind, QueuedAt: ts(d.QueuedAt), StartedAt: ts(started),
			WaitSeconds: max(0, started-d.QueuedAt), BuildSeconds: max(0, now-started), Result: buildResultInProgress,
		}
		if d.BuiltAt.Valid {
			r.EndedAt = timePtr(ts(d.BuiltAt.Int64))
			r.BuildSeconds = max(0, d.BuiltAt.Int64-started)
		}
		out = append(out, r)
		seen[d.ID] = true
	}
	rows, err := s.Q.ListBuildHistoryOverlapping(ctx, db.ListBuildHistoryOverlappingParams{Since: since.Unix(), MaxRows: maxChartBuilds})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if !seen[r.BuildHistory.DeploymentID] {
			out = append(out, buildRecord(r.BuildHistory, r.PersonName))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > maxChartBuilds {
		out = out[:maxChartBuilds]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func buildWindow(window string) (string, time.Duration, error) {
	window = strings.ToLower(strings.TrimSpace(window))
	if window == "" {
		window = "24h"
	}
	d, ok := buildWindows[window]
	if !ok {
		return "", 0, apperr.BadRequest("invalid_window", "The window "+quote(window)+" is not supported.", "Use 24h, 7d or 30d.")
	}
	return window, d, nil
}

// AdminBuildStats is what each person built in the window (24h | 7d | 30d):
// the builds that ended in it, counted for who started them (the app's owner
// for a git push), most build time first.
func (s *Service) AdminBuildStats(ctx context.Context, a Actor, window string) (BuildStats, error) {
	if err := RequireAdmin(a); err != nil {
		return BuildStats{}, err
	}
	window, dur, err := buildWindow(window)
	if err != nil {
		return BuildStats{}, err
	}
	rows, err := s.Q.ListBuildHistorySince(ctx, s.Now().Add(-dur).Unix())
	if err != nil {
		return BuildStats{}, apperr.Internal(err)
	}
	return BuildStats{Window: window, People: buildStatsPerPerson(rows)}, nil
}

// buildStatsPerPerson adds up the builds per person, most build time first
// (then most builds, then by name).
func buildStatsPerPerson(rows []db.ListBuildHistorySinceRow) []BuildPersonStats {
	by := map[string]*BuildPersonStats{}
	for _, row := range rows {
		h := row.BuildHistory
		st := by[h.PersonID]
		if st == nil {
			st = &BuildPersonStats{UserID: h.PersonID, Name: row.PersonName, Email: row.PersonEmail}
			by[h.PersonID] = st
		}
		r := buildRecord(h, row.PersonName)
		st.Builds++
		if h.Result == "failed" {
			st.Failed++
		}
		st.TotalBuildSeconds += r.BuildSeconds
		if st.Builds == 1 || r.BuildSeconds > st.LongestBuildSeconds {
			st.LongestBuildSeconds, st.LongestApp = r.BuildSeconds, h.AppSlug
		}
		if r.PeakCPU != nil && (st.PeakCPU == nil || *r.PeakCPU > *st.PeakCPU) {
			st.PeakCPU = r.PeakCPU
		}
		if r.PeakMemoryMB != nil && (st.PeakMemoryMB == nil || *r.PeakMemoryMB > *st.PeakMemoryMB) {
			st.PeakMemoryMB = r.PeakMemoryMB
		}
		if st.LastBuildAt == nil || r.StartedAt.After(*st.LastBuildAt) {
			st.LastBuildAt = timePtr(r.StartedAt)
		}
	}
	out := make([]BuildPersonStats, 0, len(by))
	for _, st := range by {
		st.AvgBuildSeconds = (st.TotalBuildSeconds + int64(st.Builds)/2) / int64(st.Builds)
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.TotalBuildSeconds != y.TotalBuildSeconds {
			return x.TotalBuildSeconds > y.TotalBuildSeconds
		}
		if x.Builds != y.Builds {
			return x.Builds > y.Builds
		}
		if x.Name != y.Name {
			return x.Name < y.Name
		}
		return x.UserID < y.UserID
	})
	return out
}

// AdminListBuilds is the builds of one person (started by them, or a git push
// to their app) that ended in the window, newest first, at most 100.
func (s *Service) AdminListBuilds(ctx context.Context, a Actor, userID, window string) ([]BuildRecord, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	_, dur, err := buildWindow(window)
	if err != nil {
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, apperr.BadRequest("user_required", "Say whose builds to list.", "Add ?user=<user id>, from GET /api/v1/admin/users or /admin/builds/stats.")
	}
	rows, err := s.Q.ListBuildHistoryByPerson(ctx, db.ListBuildHistoryByPersonParams{
		PersonID: userID, FinishedAt: s.Now().Add(-dur).Unix(), Limit: maxPersonBuilds,
	})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]BuildRecord, len(rows))
	for i, r := range rows {
		out[i] = buildRecord(r.BuildHistory, r.PersonName)
	}
	return out, nil
}

// AdminListUserAuditPage is the audit log of what one person did, newest
// first, like AdminListAuditPage.
func (s *Service) AdminListUserAuditPage(ctx context.Context, a Actor, userID string, limit int, beforeID int64) ([]AuditEntry, bool, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, false, err
	}
	if beforeID <= 0 {
		beforeID = 1<<63 - 1
	}
	limit = acctClampLimit(limit)
	rows, err := s.Q.ListAuditPageByUser(ctx, db.ListAuditPageByUserParams{UserID: nullStr(userID), ID: beforeID, Limit: int64(limit) + 1})
	if err != nil {
		return nil, false, apperr.Internal(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return acctAuditEntries(rows), more, nil
}
