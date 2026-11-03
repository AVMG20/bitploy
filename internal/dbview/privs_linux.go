//go:build linux

package dbview

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	capSetgid        = 6
	capSetuid        = 7
	linuxCapVersion3 = 0x20080522
	sysCapget        = syscall.SYS_CAPGET
	sysCapset        = syscall.SYS_CAPSET
)

type capHeader struct {
	version uint32
	pid     int32
}

type capData struct{ effective, permitted, inheritable uint32 }

// dropPrivileges makes the process the given user. The helper container starts
// as root (to read the file's owner and to get at any folder) and keeps no
// capability to change user afterwards: a user other than root loses them all
// by switching, root gives up SETUID and SETGID. A database of root is opened
// as root (it keeps CHOWN and FOWNER, which SQLite uses for its side files).
func dropPrivileges(uid, gid uint32) error {
	switched := false
	if os.Geteuid() == 0 && uid != 0 {
		switched = true
		if err := syscall.Setgroups(nil); err != nil {
			return err
		}
		if err := syscall.Setgid(int(gid)); err != nil {
			return err
		}
		if err := syscall.Setuid(int(uid)); err != nil {
			return err
		}
		if os.Geteuid() != int(uid) {
			return fmt.Errorf("still user %d after switching to %d", os.Geteuid(), uid)
		}
	}
	if os.Geteuid() == 0 {
		if err := dropSetidCaps(); err != nil {
			return err
		}
	} else if !switched {
		return nil // not root to begin with (a test): nothing to give up
	}
	return checkNoSetid()
}

// dropSetidCaps removes SETUID and SETGID from every thread of the process.
func dropSetidCaps() error {
	hdr := capHeader{version: linuxCapVersion3}
	var data [2]capData
	if _, _, e := syscall.RawSyscall(sysCapget, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		return fmt.Errorf("capget: %v", e)
	}
	mask := ^(uint32(1)<<capSetuid | uint32(1)<<capSetgid)
	for i := range data {
		data[i].effective &= mask
		data[i].permitted &= mask
		data[i].inheritable &= mask
	}
	hdr.pid = 0
	if _, _, e := syscall.AllThreadsSyscall(sysCapset, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		return fmt.Errorf("capset: %v", e)
	}
	return nil
}

// checkNoSetid reads the capabilities of the process back and fails when it
// can still change user: better no answer than a helper that kept the power.
func checkNoSetid() error {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || (k != "CapEff" && k != "CapPrm") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
		if err != nil {
			return err
		}
		if n&(1<<capSetuid|1<<capSetgid) != 0 {
			return fmt.Errorf("the helper still has SETUID or SETGID (%s %x)", k, n)
		}
	}
	return nil
}

// limitMemory caps the data (heap and anonymous memory) of the process. SQLite's own heap limit
// does not count in this build, and a statement like hex(zeroblob(1e9)) would
// otherwise take what it wants. When the limit is hit, the allocation fails and
// the process ends with an error: the caller answers db_out_of_memory.
func limitMemory(bytes uint64) {
	_ = syscall.Setrlimit(syscall.RLIMIT_DATA, &syscall.Rlimit{Cur: bytes, Max: bytes})
}
