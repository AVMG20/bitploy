//go:build !linux

package dbview

// dropPrivileges does nothing: the helper only runs on Linux (in its container).
func dropPrivileges(uid, gid uint32) error { return nil }

// limitMemory does nothing: only Linux has a limit that Go can live with.
func limitMemory(bytes uint64) {}
