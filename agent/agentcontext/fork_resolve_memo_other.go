//go:build !linux && !darwin

package agentcontext

import "io/fs"

// fileIdentity reports no identity where the memo does not read one, which
// turns the memo off there.
func fileIdentity(fs.FileInfo) (fileID, bool) {
	return fileID{}, false
}
