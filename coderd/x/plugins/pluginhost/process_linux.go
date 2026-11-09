package pluginhost

import (
	"os/exec"
	"syscall"
)

// setProcAttr kills the plugin host if coderd dies without closing it, and
// puts it in its own process group so terminal signals aimed at coderd do
// not reach it directly.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
		Setpgid:   true,
	}
}
