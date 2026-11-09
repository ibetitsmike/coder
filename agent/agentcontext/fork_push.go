package agentcontext

// This file is fork code: it bounds error text before a push so one
// oversized error cannot stall context sync.

import "unicode/utf8"

// maxPushErrorBytes bounds each error string the agent pushes. Errors can
// quote user-controlled text such as front matter names or symlink targets,
// and coderd rejects the whole push when any error exceeds its 4096-byte
// cap, which would stop every context resource from syncing.
const maxPushErrorBytes = 1024

// truncatePushError cuts s to at most maxPushErrorBytes on a UTF-8 rune
// boundary.
func truncatePushError(s string) string {
	if len(s) <= maxPushErrorBytes {
		return s
	}
	const suffix = " (truncated)"
	cut := maxPushErrorBytes - len(suffix)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}
