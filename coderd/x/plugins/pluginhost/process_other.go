//go:build !linux

package pluginhost

import "os/exec"

func setProcAttr(*exec.Cmd) {}
