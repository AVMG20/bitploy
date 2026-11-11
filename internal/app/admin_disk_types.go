package app

// API types of the disk page (admin_disk.go).

import (
	"time"

	"github.com/AVMG20/bitploy/internal/diskuse"
)

// DiskReport says where the disk space of the server goes.
type DiskReport struct {
	MeasuredAt time.Time      `json:"measured_at"`
	Filesystem DiskFilesystem `json:"filesystem"`
	// BuildsPausedPercent: below this much free, builds, uploads and backups are refused.
	BuildsPausedPercent float64 `json:"builds_paused_percent"`
	DockerAvailable     bool    `json:"docker_available"`
	DockerError         string  `json:"docker_error,omitempty"`
	// Segments make up the used space, in a fixed order. Keys: images_in_use,
	// images_rollback, images_unused, images_other, build_cache, containers,
	// volumes_app, volumes_other, bitploy_files, other (the rest of the
	// filesystem). Empty ones are left out.
	Segments    []DiskSegment `json:"segments"`
	ImageGroups []DiskGroup   `json:"image_groups" doc:"Keys: in_use, rollback, unused, other"`
	// Images, Containers and Volumes are the largest 100 of each.
	Images     []DiskImageItem     `json:"images"`
	Containers []DiskContainerItem `json:"containers" doc:"Containers that are stopped or wrote files of their own"`
	Volumes    []DiskVolumeItem    `json:"volumes"`
	BuildCache DiskBuildCache      `json:"build_cache"`
	// Files are bitploy's own files in its data directory. Keys: database,
	// database_backups, app_backups, sources, uploads, builds, proxy, other.
	Files    []DiskGroup         `json:"files"`
	Cleanups []DiskCleanupOption `json:"cleanups"`
	Auto     DiskAutoCleanup     `json:"auto"`
	// Apps is what every app takes, largest first.
	Apps []DiskAppUsage `json:"apps"`
}

// DiskAppUsage is the disk space one app takes. Image bytes are the bytes
// only its images hold: layers shared with other images are not counted.
type DiskAppUsage struct {
	App            DiskAppRef `json:"app"`
	ImageBytes     int64      `json:"image_bytes" doc:"The live version's images"`
	RollbackBytes  int64      `json:"rollback_bytes" doc:"Older versions kept for a rollback"`
	RollbackCount  int        `json:"rollback_count"`
	VolumeBytes    int64      `json:"volume_bytes" doc:"/data and its services' volumes"`
	ContainerBytes int64      `json:"container_bytes" doc:"Files its containers wrote outside volumes"`
	TotalBytes     int64      `json:"total_bytes"`
}

type DiskFilesystem struct {
	TotalBytes int64 `json:"total_bytes"`
	UsedBytes  int64 `json:"used_bytes"`
	FreeBytes  int64 `json:"free_bytes"`
}

type DiskSegment struct {
	Key   string `json:"key"`
	Bytes int64  `json:"bytes"`
}

type DiskGroup struct {
	Key   string `json:"key"`
	Bytes int64  `json:"bytes"`
	Count int    `json:"count"`
}

// DiskAppRef is the app a disk item belongs to.
type DiskAppRef struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
}

type DiskImageItem struct {
	ID       string      `json:"id" doc:"Short image id"`
	Tags     []string    `json:"tags"`
	App      *DiskAppRef `json:"app,omitempty"`
	Category string      `json:"category" enum:"in_use,rollback,unused,other"`
	// SizeBytes includes layers shared with other images; UniqueBytes is what removing it frees.
	SizeBytes   int64     `json:"size_bytes"`
	UniqueBytes int64     `json:"unique_bytes"`
	InUse       bool      `json:"in_use" doc:"A container runs from it"`
	Removable   bool      `json:"removable" doc:"Its category's cleanup removes it"`
	CreatedAt   time.Time `json:"created_at"`
}

type DiskContainerItem struct {
	Name      string      `json:"name"`
	State     string      `json:"state"`
	Category  string      `json:"category" enum:"running,stopped,leftover,other"`
	App       *DiskAppRef `json:"app,omitempty"`
	Bytes     int64       `json:"bytes" doc:"Files the container wrote on top of its image"`
	Removable bool        `json:"removable"`
}

type DiskVolumeItem struct {
	Name      string      `json:"name"`
	Category  string      `json:"category" enum:"app,builder,orphan,other"`
	App       *DiskAppRef `json:"app,omitempty"`
	Bytes     int64       `json:"bytes" doc:"-1 when Docker did not measure it"`
	InUse     bool        `json:"in_use"`
	Removable bool        `json:"removable"`
}

type DiskBuildCache struct {
	BuilderBytes int64 `json:"builder_bytes" doc:"The cache of bitploy's isolated builder"`
	DockerBytes  int64 `json:"docker_bytes" doc:"Docker's own build cache"`
}

// DiskCleanupOption is one cleanup and about what it would free now.
type DiskCleanupOption struct {
	Kind    string   `json:"kind" enum:"build_cache,unused_images,stopped_containers,rollback_images,other_images,orphan_volumes"`
	Bytes   int64    `json:"bytes"`
	Count   int      `json:"count" doc:"Images, containers or volumes it removes; 0 for build cache"`
	Volumes []string `json:"volumes,omitempty" doc:"orphan_volumes: the volumes it deletes"`
}

// DiskAutoCleanup is the automatic cleanup's settings and its last runs.
type DiskAutoCleanup struct {
	Enabled        bool `json:"enabled"`
	MinFreePercent int  `json:"min_free_percent"`
	// BuildsMinFreePercent: below this much free, builds, uploads and
	// backups are refused, and the steps that only cost a slower next build run.
	BuildsMinFreePercent int              `json:"builds_min_free_percent"`
	Steps                []string         `json:"steps" doc:"The steps the automatic cleanup may take, in its order: build_cache, unused_images, stopped_containers, rollback_images, other_images"`
	BuildCacheDays       int              `json:"build_cache_days" doc:"Build cache no build used for this many days is deleted (checked every hour); 0: never"`
	WarnFreePercent      int              `json:"warn_free_percent" doc:"Admins get a warning once free space drops below this; 0: no warning"`
	RollbackVersions     int              `json:"rollback_versions" doc:"Versions every app keeps, the live one included (the others can be rolled back to)"`
	Dev                  bool             `json:"dev" doc:"Dev mode: the automatic cleanup does not run"`
	Runs                 []DiskCleanupRun `json:"runs" doc:"Newest first, at most 5"`
}

type DiskCleanupRun struct {
	StartedAt         time.Time            `json:"started_at"`
	FinishedAt        time.Time            `json:"finished_at"`
	Trigger           string               `json:"trigger" enum:"low_disk,build_refused,manual"`
	Result            string               `json:"result" enum:"recovered,still_low,failed"`
	FreedBytes        int64                `json:"freed_bytes"`
	FreeBeforePercent float64              `json:"free_before_percent"`
	FreeAfterPercent  float64              `json:"free_after_percent"`
	Steps             []diskuse.StepResult `json:"steps"`
}

type UpdateDiskCleanupInput struct {
	Enabled        *bool `json:"enabled,omitempty"`
	MinFreePercent *int  `json:"min_free_percent,omitempty" minimum:"2" maximum:"50"`
	// BuildsMinFreePercent: builds, uploads and backups are refused below it.
	BuildsMinFreePercent *int `json:"builds_min_free_percent,omitempty" minimum:"1" maximum:"50"`
	// Steps the automatic cleanup may take; at least one.
	Steps *[]string `json:"steps,omitempty"`
	// BuildCacheDays: build cache unused for this many days is deleted; 0 never.
	BuildCacheDays *int `json:"build_cache_days,omitempty" minimum:"0" maximum:"90"`
	// WarnFreePercent: warn admins below this much free; 0 never. Above min_free_percent.
	WarnFreePercent *int `json:"warn_free_percent,omitempty" minimum:"0" maximum:"80"`
	// RollbackVersions: versions every app keeps, the live one included.
	RollbackVersions *int `json:"rollback_versions,omitempty" minimum:"1" maximum:"10"`
}

type DiskCleanupInput struct {
	Kind    string   `json:"kind" enum:"build_cache,unused_images,stopped_containers,rollback_images,other_images,orphan_volumes"`
	Volumes []string `json:"volumes,omitempty" doc:"orphan_volumes only: the volumes to delete, as the report lists them"`
}

type DiskCleanupResult struct {
	Kind       string `json:"kind"`
	FreedBytes int64  `json:"freed_bytes"`
	Removed    int    `json:"removed"`
	Error      string `json:"error,omitempty" doc:"Part of the cleanup failed"`
}
