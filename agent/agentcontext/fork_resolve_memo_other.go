//go:build !unix

package agentcontext

import "io/fs"

// fileIdentity reports no identity outside unix, which turns the memo off
// there.
func fileIdentity(fs.FileInfo) (dev, ino uint64, ok bool) {
	return 0, 0, false
}
