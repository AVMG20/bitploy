//go:build !unix

package dbview

import "os"

func ownerOf(os.FileInfo) (uid, gid uint32) { return 0, 0 }
