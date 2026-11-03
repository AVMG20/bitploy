//go:build unix

package dbview

import (
	"os"
	"syscall"
)

// ownerOf is the user and group of a file.
func ownerOf(st os.FileInfo) (uid, gid uint32) {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return s.Uid, s.Gid
	}
	return uint32(os.Geteuid()), uint32(os.Getegid())
}
