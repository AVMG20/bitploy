//go:build unix

package metrics

import "syscall"

// DiskUsage returns total and free bytes of the filesystem holding path.
func DiskUsage(path string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize)
	return uint64(st.Blocks) * bs, uint64(st.Bavail) * bs, nil
}
