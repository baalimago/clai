//go:build windows

package cloud

import "os/exec"

func detach(*exec.Cmd) {}
