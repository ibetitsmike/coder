package agentcontext

// defaultBuiltinRoots returns the scan roots layered after
// user-added sources and before working-directory discovery. These mirror the
// agentcontextconfig API resolves at every chat hydrate. The
// list is intentionally tolerant of missing entries; the
// resolver silently skips canonicalization failures and
// non-existent paths.
func defaultBuiltinRoots() []string {
	return []string{
		// User-level Coder config.
		"~/.coder",
		"~/.coder/skills",
		// Installed plugin packs materialize here (fork
		// plugin packs, PRD 6.7); fork_plugins.go reads their
		// manifests.
		"~/.coder/plugins",
		// User rule directories (fork rules, PRD 6.9); fork_rules.go
		// reads their .mdc files.
		"~/.coder/rules",
		"~/.cursor/rules",
		// Claude Code plugin cache, picked up by the plugin
		// RFC follow-up. v1 ignores plugin manifests, but
		// watching the directory now prevents a surprise
		// dirty bit when the resolver eventually classifies
		// them.
		claudePluginCacheRoot(),
	}
}

// defaultAllowedRoots returns the allow-list applied to runtime
// AddSource calls when ManagerOptions.AllowedRoots is empty.
// The set matches the RFC's authorization section: the home
// directory's Coder and Claude config trees. The Manager
// appends the working directory lazily on every check, which
// picks up the workspace's resolved path even when the manifest
// is loaded after agent init.
func defaultAllowedRoots() []string {
	return []string{"~", "~/.coder", "~/.claude"}
}
