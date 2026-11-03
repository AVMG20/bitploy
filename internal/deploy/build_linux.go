package deploy

import (
	"os/exec"
	"syscall"
	"time"
)

// killGrace is how long a cancelled build gets to stop on SIGTERM before the
// whole process group is killed.
const killGrace = 5 * time.Second

// On Linux the docker CLI dies with bitploy (kill -9 included), so a build
// never outlives the process that tracks it. A cancelled build (the user
// cancels it, the timeout passes) is stopped as a group: the CLI and the
// buildx plugin it runs get SIGTERM, which makes buildx tell BuildKit to stop
// the build, and SIGKILL after killGrace if they are still there.
func setChildAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			return cmd.Process.Kill()
		}
		time.AfterFunc(killGrace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		return nil
	}
}
