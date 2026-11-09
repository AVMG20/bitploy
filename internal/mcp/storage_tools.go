package mcp

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
)

// The tools for storage: what takes the account's storage allowance, deleting
// a backup, the files in /data and the Dockerfile of an app deployed from
// files.

type deleteBackupIn struct {
	App string `json:"app" jsonschema:"The app's slug or ID (app_...)."`
	ID  string `json:"id" jsonschema:"The backup's id (bak_...), from get_storage or list_backups."`
}

type listFilesIn struct {
	App    string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Path   string `json:"path,omitempty" jsonschema:"Folder relative to /data, like uploads or db. Leave it out for /data itself."`
	Volume string `json:"volume,omitempty" jsonschema:"Only for an app with several volumes (get_app lists them): the volume's name. Default: the app's first volume."`
}

type setDockerfileIn struct {
	App     string `json:"app" jsonschema:"The app's slug or ID (app_...)."`
	Content string `json:"content" jsonschema:"The whole new Dockerfile (max 64 KB). It replaces the one at the app's dockerfile_path; every other file stays as it was."`
}

// storageBackup is one backup in get_storage.
type storageBackup struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	SizeMB    float64   `json:"size_mb"`
	CreatedAt time.Time `json:"created_at"`
	Volume    string    `json:"volume,omitempty"`
}

// storageApp is what one app takes of the storage allowance.
type storageApp struct {
	Slug         string          `json:"slug"`
	Name         string          `json:"name"`
	VolumeMB     int64           `json:"volume_mb"`      // reserved for /data, counts in full
	VolumeUsedMB *float64        `json:"volume_used_mb"` // what /data holds, measured hourly
	FilesMB      float64         `json:"files_mb"`       // kept images and stored source
	BackupsMB    float64         `json:"backups_mb"`
	TotalMB      float64         `json:"total_mb"`
	Backups      []storageBackup `json:"backups"`
}

func mb(bytes int64) float64 { return math.Round(float64(bytes)/(1<<20)*10) / 10 }

func (s *srv) registerStorageTools() {
	svc := s.svc

	addTool(s, "get_storage", "Show what takes the storage",
		"The account's storage allowance and what takes it, per app the user owns: the space kept for /data (volume_mb counts in full, even when /data holds less, see volume_used_mb), the app's own files (kept images and stored source) and every backup with its id and size, biggest app first. "+
			"Call it after storage_quota_exceeded or storage_full, or when the user asks where their storage went, then suggest what to remove. Deleting is delete_backup (only with the user's go-ahead); a smaller volume_mb is update_app.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			me, err := svc.Me(ctx, a)
			if err != nil {
				return nil, err
			}
			apps, err := svc.ListApps(ctx, a)
			if err != nil {
				return nil, err
			}
			rows := make([]storageApp, 0, len(apps))
			for _, ap := range apps {
				if ap.OwnerID != me.User.ID {
					continue // counts towards its owner's allowance
				}
				r := storageApp{Slug: ap.Slug, Name: ap.Name, BackupsMB: ap.BackupsMB, Backups: []storageBackup{}}
				if ap.Volume {
					r.VolumeMB = ap.VolumeMB
					r.VolumeUsedMB = ap.VolumeUsedMB
				}
				if ap.FilesMB != nil {
					r.FilesMB = *ap.FilesMB
				}
				if l, err := svc.ListBackups(ctx, a, ap.Slug); err == nil {
					for _, b := range l.Items {
						r.Backups = append(r.Backups, storageBackup{ID: b.ID, Kind: b.Kind, Status: b.Status, SizeMB: mb(b.SizeBytes), CreatedAt: b.CreatedAt, Volume: b.Volume})
					}
				}
				r.TotalMB = math.Round((float64(r.VolumeMB)+r.FilesMB+r.BackupsMB)*10) / 10
				rows = append(rows, r)
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].TotalMB > rows[j].TotalMB })
			u := me.Usage
			taken := float64(u.VolumeReservedMB) + u.FilesMB + u.BackupsMB
			summary := fmt.Sprintf("Storage: %.0f MB taken (%d MB kept for /data, %.0f MB app files, %.0f MB backups)", taken, u.VolumeReservedMB, u.FilesMB, u.BackupsMB)
			if me.Quota.VolumeMB > 0 {
				summary += fmt.Sprintf(" of %d MB.", me.Quota.VolumeMB)
			} else {
				summary += "; this account has no storage limit."
			}
			return &result{
				Summary: summary,
				Data:    data("allowance_mb", me.Quota.VolumeMB, "taken_mb", math.Round(taken), "volume_reserved_mb", u.VolumeReservedMB, "files_mb", u.FilesMB, "backups_mb", u.BackupsMB, "apps", rows),
				Next: []string{
					"Tell the user in plain words what takes the most space and what could go. Delete nothing without their go-ahead.",
					"Old backups: delete_backup(app, id) frees their size at once. If automatic backups keep piling up, a smaller keep in set_backup_schedule stops that.",
					"volume_mb counts in full even when /data holds less (volume_used_mb): a lower volume_mb with update_app frees the difference. An app the user no longer needs: delete_app.",
					"If that is not enough, tell the user to ask their bitploy admin for more storage.",
				},
			}, nil
		})

	addTool(s, "delete_backup", "Delete a backup",
		"Permanently deletes one backup of an app's /data (or one database dump) and frees its space in the storage allowance. Cannot be undone. Only with the user's go-ahead, for a backup they agreed to; find ids and sizes with get_storage or list_backups. Refused with backup_busy while a backup or restore of the app runs.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in deleteBackupIn) (*result, error) {
			size := ""
			if l, err := svc.ListBackups(ctx, a, in.App); err == nil {
				for _, b := range l.Items {
					if b.ID == in.ID {
						size = fmt.Sprintf(" (%.1f MB freed)", mb(b.SizeBytes))
					}
				}
			}
			if err := svc.DeleteBackup(ctx, a, in.App, strings.TrimSpace(in.ID)); err != nil {
				return nil, err
			}
			return &result{
				Summary: "Deleted backup " + in.ID + " of " + in.App + size + ".",
				Data:    data("deleted", in.ID, "app", in.App),
				Next:    []string{"Tell the user which backup is gone. get_storage shows what is left."},
			}, nil
		})

	addTool(s, "list_files", "List files in /data",
		"Lists one folder of an app's /data volume: name, type (file, dir, symlink), size in bytes, modified_at; folders first, at most 1000 items (truncated says there are more). "+
			"Use it to find the SQLite file for query_db (often .db, .sqlite or .sqlite3), or to see what takes the space in /data. Needs the volume on and the app started once. Reading, downloading and uploading files are done by the user in the dashboard (Storage tab).",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in listFilesIn) (*result, error) {
			l, err := svc.ListFiles(ctx, a, in.App, in.Path, in.Volume)
			if err != nil {
				return nil, err
			}
			l.Items = nonNil(l.Items)
			where := "/data"
			if l.Path != "" {
				where += "/" + l.Path
			}
			summary := fmt.Sprintf("%d item(s) in %s of %s.", len(l.Items), where, in.App)
			if l.Truncated {
				summary += " There are more than this list holds."
			}
			next := []string{"Open a folder with list_files path=<folder>."}
			for _, f := range l.Items {
				if f.Type == "file" && isSQLiteName(f.Name) {
					p := f.Name
					if l.Path != "" {
						p = l.Path + "/" + f.Name
					}
					next = append([]string{"query_db with path=" + p + " reads that database."}, next...)
					break
				}
			}
			return &result{Summary: summary, Data: toMap(l), Next: next}, nil
		})

	addTool(s, "get_dockerfile", "Read the Dockerfile",
		"The Dockerfile bitploy builds the app from, out of the files of its newest deployment. Use it when you have no checkout of the code, or to fix only the Dockerfile with set_dockerfile without sending every file again. editable is false, with a reason, for an app that builds from a repository (push the fix instead), one built from bitploy's templates (build mode static, node or static_build, or auto without a Dockerfile), or one never deployed from files.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
			v, err := svc.GetAppDockerfile(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			next := []string{"To change it: validate_dockerfile with the new content, then set_dockerfile. Every other file stays as it was."}
			summary := "Dockerfile of " + in.App + " (" + v.Path + ")."
			if !v.Editable {
				summary = "The Dockerfile of " + in.App + " cannot be changed here: " + v.Reason
				next = []string{"Change it in the code and send it as usual (a push for an app linked to a repository, else deploy_files)."}
			}
			return &result{Summary: summary, Data: toMap(v), Next: next}, nil
		})

	addTool(s, "set_dockerfile", "Replace the Dockerfile and deploy",
		"Replaces only the Dockerfile in the files of the app's newest deployment and deploys them, so a Dockerfile fix needs neither a checkout nor every file sent again. Runs the same checks as deploy_files. Call validate_dockerfile first. Refused with git_source_linked (push the fix instead), dockerfile_not_used (the app is built from a template) or source_missing (send the files with deploy_files). Returns the queued deployment: follow it with get_deployment.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in setDockerfileIn) (*result, error) {
			d, err := svc.PutAppDockerfile(ctx, a, in.App, in.Content)
			if err != nil {
				return nil, err
			}
			r := deploymentResult("Saved the Dockerfile and queued deployment", d)
			r.Next = append(r.Next, "If the code folder has the Dockerfile too, save the same change there so the next deploy_files keeps it.")
			return r, nil
		})
}

func isSQLiteName(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range []string{".db", ".sqlite", ".sqlite3", ".db3"} {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
}
