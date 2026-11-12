//go:build !windows

package bifrost

import (
	"os/exec"
	"syscall"
)

// detach puts the gateway in its own session so closing the terminal does not stop it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
