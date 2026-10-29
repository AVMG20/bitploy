//go:build !unix

package metrics

import "errors"

func DiskUsage(path string) (total, free uint64, err error) {
	return 0, 0, errors.New("disk stats are not supported on this platform")
}
