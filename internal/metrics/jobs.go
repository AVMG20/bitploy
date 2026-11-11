package metrics

import (
	"fmt"
	"strings"
)

// The ids of the scheduled jobs as Manager.OnJob reports them. The admin's
// Jobs page (internal/app/jobs.go) defines a row for each.
const (
	JobCollect     = "metrics_collect"
	JobRollup      = "metrics_rollup"
	JobVolumeQuota = "volume_quota"

	// The steps of the nightly job, each reported on its own.
	JobPruneMetrics = "nightly_prune_metrics"
	JobMeasureDisk  = "nightly_measure_disk"
	JobDockerPrune  = "nightly_docker_prune"
	JobBackupDB     = "nightly_db_backup"
	JobPruneAudit   = "nightly_prune_audit"
	JobOldUploads   = "nightly_old_uploads"
)

// countText is a job summary such as "deleted 3 uploads", or "nothing to
// delete" for none.
func countText(n int64, noun, verb string) string {
	if n == 0 {
		return "nothing to " + strings.TrimSuffix(verb, "d")
	}
	if n != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%s %d %s", verb, n, noun)
}
