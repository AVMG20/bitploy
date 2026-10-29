//go:build !linux

package deploy

import "os/exec"

func setChildAttrs(cmd *exec.Cmd) {}
