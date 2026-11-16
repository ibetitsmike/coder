//go:build !unix

package agentcontext

import "io/fs"

// fileIdentity has no portable inode outside unix; the stamp relies on the
// size and mtime there.
func fileIdentity(fs.FileInfo) (dev, ino uint64) {
	return 0, 0
}
