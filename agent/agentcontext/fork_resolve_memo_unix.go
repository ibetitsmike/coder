//go:build unix

package agentcontext

import (
	"io/fs"
	"syscall"
)

// fileIdentity returns the device and inode of a file, so a file replaced
// by another one with the same size and mtime still changes its stamp.
func fileIdentity(info fs.FileInfo) (dev, ino uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	//nolint:unconvert // Dev is int32 on darwin and uint64 on linux.
	return uint64(st.Dev), uint64(st.Ino)
}
