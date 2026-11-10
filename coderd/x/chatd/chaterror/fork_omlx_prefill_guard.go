package chaterror

import "strings"

// oMLX's prefill memory guard rejects a prompt when the prefill would
// exceed the current memory ceiling. The ceiling depends on what other
// requests hold at that moment, so the same prompt usually succeeds a few
// seconds later. oMLX reports it either as an HTTP 400 before the stream
// starts or as an error event inside an HTTP 200 stream; both carry the same
// message text, so the match is keyed on that text and not on the status.
var omlxPrefillGuardPatterns = []string{
	"omlx prefill memory guard rejected this prompt",
	"prefill would require",
}

// isOMLXPrefillGuardRejection reports whether lowerText, already lowercased,
// contains every fragment of an oMLX prefill memory-guard rejection.
func isOMLXPrefillGuardRejection(lowerText string) bool {
	for _, pattern := range omlxPrefillGuardPatterns {
		if !strings.Contains(lowerText, pattern) {
			return false
		}
	}
	return true
}
