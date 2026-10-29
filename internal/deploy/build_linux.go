package deploy

import (
	"os/exec"
	"syscall"
)

// On Linux the docker CLI dies with bitploy (kill -9 included), so a build
// never outlives the process that tracks it.
func setChildAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}
