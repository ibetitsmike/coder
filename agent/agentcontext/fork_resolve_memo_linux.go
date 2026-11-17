//go:build linux

package agentcontext

import (
	"io/fs"
	"syscall"
)

func fileIdentity(info fs.FileInfo) (fileID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	//nolint:unconvert // Dev, Ino and Timespec field widths differ by architecture.
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino), ctime: int64(st.Ctim.Sec)*1e9 + int64(st.Ctim.Nsec)}, true
}
