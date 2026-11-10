package agentcontext

// This file is fork code: it asserts the built-in scan roots include the
// installed plugin pack root.

import (
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

func TestDefaultBuiltinRootsIncludePackRoot(t *testing.T) {
	t.Parallel()
	for _, root := range defaultBuiltinRoots() {
		if root == "~/.coder/plugins" {
			return
		}
	}
	t.Fatal("~/.coder/plugins is missing from defaultBuiltinRoots; installed packs would not be discovered")
}

func TestWatcherEventRelevant(t *testing.T) {
	t.Parallel()
	w := &Watcher{}
	cases := []struct {
		name  string
		event fsnotify.Event
		want  bool
	}{
		{"manifest write re-resolves", fsnotify.Event{Name: "/p/pstack/plugin.json", Op: fsnotify.Write}, true},
		{"manifest create re-resolves", fsnotify.Event{Name: "/p/pstack/plugin.json", Op: fsnotify.Create}, true},
		{"cursor manifest write re-resolves", fsnotify.Event{Name: "/p/pstack/.cursor-plugin/plugin.json", Op: fsnotify.Write}, true},
		{"skill edit re-resolves", fsnotify.Event{Name: "/p/pstack/skills/x/SKILL.md", Op: fsnotify.Write}, true},
		{"ordinary write ignored", fsnotify.Event{Name: "/p/pstack/README.md", Op: fsnotify.Write}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, w.eventRelevant(tc.event))
		})
	}
}
