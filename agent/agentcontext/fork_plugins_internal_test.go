package agentcontext

// This file is fork code: it asserts the built-in scan roots include the
// installed plugin pack root.

import "testing"

func TestDefaultBuiltinRootsIncludePackRoot(t *testing.T) {
	t.Parallel()
	for _, root := range defaultBuiltinRoots() {
		if root == "~/.coder/plugins" {
			return
		}
	}
	t.Fatal("~/.coder/plugins is missing from defaultBuiltinRoots; installed packs would not be discovered")
}
