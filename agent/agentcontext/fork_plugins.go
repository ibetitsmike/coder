package agentcontext

// This file is fork code: it discovers plugin packs (PRD 6.7, R7.1/R7.4/R7.5).
// A pack is a directory holding a Claude Code (.claude-plugin/plugin.json),
// Cursor (.cursor-plugin/plugin.json) or root (agent-plugins.org schema)
// manifest. Its declared skills and agents directories are discovered like
// ordinary skill and agent containers, so pack skills become skills and pack
// agents become custom subagent types without manual copying.
//
// Pack-contributed resources carry the pack's manifest path in SourcePath so
// coderd can drop them from the prompt and the context popup when the
// fork-plugin-packs experiment is off (coderd/x/chatd/fork_plugin_gate.go). The
// KindPlugin manifest resource itself stays agent-local: neither the DRPC
// body nor the workspace agent context body kinds carry a plugin payload
// yet, so PR1 pushes nothing for the manifest.
//
// Pack rows are emitted only once the server advertises the push-response
// capability (agent/proto/agent.proto fork_plugin_packs). During a rolling
// upgrade a newer agent can otherwise push pack rows to a pre-fork-plugin-packs
// coderd replica, whose prompt builder consumes every pinned skill without
// inspecting source_path and would serve pack content while the feature is
// disabled there. The advertised value is also re-checked on every resolve,
// so losing the capability (a deployment restart with the experiment off)
// stops emission without an agent restart.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Manifest paths checked inside a pack directory, in priority order.
const (
	claudePluginManifestRelPath = ".claude-plugin/plugin.json"
	cursorPluginManifestRelPath = ".cursor-plugin/plugin.json"
	rootPluginManifestRelPath   = "plugin.json"
)

// packManifestBaseName is the base name every plugin manifest shares. It is
// what the watcher keys re-resolution on and what coderd matches
// pack-contributed resources on, so the candidate list here and the marker
// in fork_plugin_gate.go must stay in sync.
const packManifestBaseName = "plugin.json"

// maxManifestBytes bounds a plugin manifest read. It mirrors the resolver's
// per-resource payload cap: a manifest needs only a name and optional
// directory declarations.
const maxManifestBytes = 64 * 1024

// maxSettingsBytes bounds a Claude Code user settings read. Settings carry
// permissions, hooks and other unrelated state, so they grow well past a
// manifest while the resolver needs only enabledPlugins.
const maxSettingsBytes = 1024 * 1024

// Default containers when the manifest declares none, matching the Cursor
// defaults and what pstack ships.
const (
	defaultPackSkillsDir = "skills"
	defaultPackAgentsDir = "agents"
)

// pluginManifest is the subset of the agent-plugins.org and Cursor plugin
// manifest schemas the resolver needs. The Cursor manifest declares its
// skill and agent directories ("skills": "./skills/"); the agent-plugins.org
// schema leaves them at the defaults. The Claude Code manifest accepts a
// string or an array of strings for both (manifestPaths).
type pluginManifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Description string        `json:"description"`
	Skills      manifestPaths `json:"skills"`
	Agents      manifestPaths `json:"agents"`
}

// manifestPaths is a manifest path declaration: one string or an array of
// strings. Any other shape decodes to no declaration, so a malformed skills
// or agents field of a Cursor or root manifest falls back to the default
// container. Claude Code fails such a plugin's validation instead, so
// readPluginManifest rejects a Claude manifest whose field is mistyped
// (claudePathFieldsTyped) before this lenient decoding matters.
type manifestPaths []string

func (p *manifestPaths) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*p = manifestPaths{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*p = many
		return nil
	}
	*p = nil
	return nil
}

// packRoot is a directory whose manifest exists and parsed cleanly.
type packRoot struct {
	Dir          string
	ManifestPath string
	Manifest     pluginManifest
	// Claude marks a Claude Code pack: one with a Claude manifest, or a
	// cached plugin without any manifest. Its declared skills add to the
	// default skills directory, where Cursor's replace it.
	Claude bool
	// SkipDefaultSkills is set for a cached plugin whose marketplace entry
	// takes the marketplace root as its source and lists skills: Claude
	// Code then loads only the listed skills, not the default directory.
	SkipDefaultSkills bool
}

// declaresAny reports whether a manifest path declaration has a non-empty
// entry, valid or not.
func declaresAny(declared manifestPaths) bool {
	return slices.ContainsFunc(declared, func(d string) bool { return strings.TrimSpace(d) != "" })
}

// findPackManifest returns the first manifest candidate that exists in dir,
// or "" when none does.
func findPackManifest(dir string) string {
	for _, candidate := range []string{claudePluginManifestRelPath, cursorPluginManifestRelPath, rootPluginManifestRelPath} {
		path := filepath.Join(dir, candidate)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// readPluginManifest parses the manifest of a pack directory. ok is false
// when the directory holds no manifest, its JSON does not parse, or the
// manifest exceeds the per-resource payload cap; such a directory is not
// treated as a pack at all. The stat guard keeps a multi-gigabyte
// plugin.json from being read into memory during resolve and watcher
// synchronization.
func readPluginManifest(packDir string) (packRoot, bool) {
	manifestPath := findPackManifest(packDir)
	if manifestPath == "" {
		return packRoot{}, false
	}
	data, ok := readCappedRegularFile(manifestPath, maxManifestBytes)
	if !ok {
		return packRoot{}, false
	}
	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return packRoot{}, false
	}
	if strings.TrimSpace(manifest.Name) == "" {
		// A file that parses but declares no name is not a plugin
		// manifest: accepting it would stamp the directory's ordinary
		// context sources with the pack marker the fork-plugin gate
		// then filters.
		return packRoot{}, false
	}
	claude := manifestPath == filepath.Join(packDir, claudePluginManifestRelPath)
	if claude && !claudePathFieldsTyped(data) {
		return packRoot{}, false
	}
	return packRoot{Dir: packDir, ManifestPath: manifestPath, Manifest: manifest, Claude: claude}, true
}

// claudePathFieldsTyped reports whether the skills and agents fields of a
// Claude manifest or marketplace entry are absent, null, a string, or an
// array of strings. Claude Code fails a plugin whose field has any other
// type, so the pack must not load with default containers in its place.
func claudePathFieldsTyped(data []byte) bool {
	var fields struct {
		Skills json.RawMessage `json:"skills"`
		Agents json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}
	return isPathDeclaration(fields.Skills) && isPathDeclaration(fields.Agents)
}

func isPathDeclaration(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch v := value.(type) {
	case nil, string:
		return true
	case []any:
		for _, item := range v {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// readCappedRegularFile reads a file only when it is a regular file of at
// most maxBytes. Lstat (not Stat) so a symlinked or special file is
// never followed: a checked-in plugin.json -> /dev/zero or a FIFO must not
// be read at all, whatever its apparent size.
func readCappedRegularFile(path string, maxBytes int64) ([]byte, bool) {
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// readCappedFollowingSymlink is readCappedRegularFile for user-owned files
// that dotfile managers commonly symlink (settings.json,
// installed_plugins.json): it accepts a symlink that resolves to a regular
// file of at most maxBytes. The target is checked before it is opened, so a
// FIFO or device never blocks or streams, and the read stays bounded even if
// the file grows in between.
func readCappedFollowingSymlink(path string, maxBytes int64) ([]byte, bool) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, false
	}
	return data, true
}

// packRootsFor returns the pack roots of a scan root: rootPath itself when it
// holds a manifest, plus every direct child directory that holds one. The
// walk goes no deeper, except in the Claude Code plugin cache, whose packs
// sit three levels down (claudeCachePackRoots).
func packRootsFor(rootPath string) []packRoot {
	var out []packRoot
	if pack, ok := readPluginManifest(rootPath); ok {
		out = append(out, pack)
	}
	for _, dir := range childDirs(rootPath) {
		if pack, ok := readPluginManifest(dir); ok {
			out = append(out, pack)
		}
	}
	if isClaudePluginCache(rootPath) {
		out = append(out, claudeCachePackRoots(rootPath)...)
	}
	return out
}

// claudePluginCacheRel is the Claude Code plugin cache relative to the home
// directory. Claude Code copies each installed plugin version to
// cache/<marketplace>/<plugin>/<version>/ and keeps a superseded version,
// marked with claudeOrphanedMarker, for 14 days after an update or uninstall
// (https://code.claude.com/docs/en/plugins/loading#find-plugins-on-disk).
var claudePluginCacheRel = filepath.Join(".claude", "plugins", "cache")

const claudeOrphanedMarker = ".orphaned_at"

// claudeInstalledPluginsFile is Claude Code's install record, kept in the
// plugins directory next to the cache.
const claudeInstalledPluginsFile = "installed_plugins.json"

// isClaudePluginStateFile reports whether a file name is one of the Claude
// Code records that select which cached plugin versions load and with which
// components, so the watcher re-resolves on writes to it.
func isClaudePluginStateFile(name string) bool {
	return name == claudeInstalledPluginsFile || name == claudeKnownMarketplacesFile || name == claudeMarketplaceFileName
}

// claudePluginCacheDirEnv replaces the ~/.claude/plugins directory, so the
// cache moves to $CLAUDE_CODE_PLUGIN_CACHE_DIR/cache.
const claudePluginCacheDirEnv = "CLAUDE_CODE_PLUGIN_CACHE_DIR"

// claudeConfigDirEnv replaces the ~/.claude directory, so the cache moves to
// $CLAUDE_CONFIG_DIR/plugins/cache and the user settings to
// $CLAUDE_CONFIG_DIR/settings.json. claudePluginCacheDirEnv wins for the
// cache location.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// claudeConfigDir returns the directory selected by claudeConfigDirEnv, or ""
// when it is unset or empty.
func claudeConfigDir() string {
	return strings.TrimSpace(os.Getenv(claudeConfigDirEnv))
}

// claudePluginCacheOverride returns the cache directory selected by
// claudePluginCacheDirEnv or claudeConfigDirEnv, or "" when neither is set.
func claudePluginCacheOverride() string {
	if dir := strings.TrimSpace(os.Getenv(claudePluginCacheDirEnv)); dir != "" {
		return filepath.Join(dir, "cache")
	}
	if dir := claudeConfigDir(); dir != "" {
		return filepath.Join(dir, "plugins", "cache")
	}
	return ""
}

// claudePluginCacheRoot is the built-in scan root for the Claude Code plugin
// cache.
func claudePluginCacheRoot() string {
	if override := claudePluginCacheOverride(); override != "" {
		return override
	}
	return "~/" + filepath.ToSlash(claudePluginCacheRel)
}

// claudePluginsDirs returns the plugins directories Claude Code may keep its
// cache and install records in, most specific first: the cache variable's
// directory, the configuration directory's plugins, and ~/.claude/plugins.
func claudePluginsDirs() []string {
	var out []string
	if dir := strings.TrimSpace(os.Getenv(claudePluginCacheDirEnv)); dir != "" {
		out = append(out, filepath.Clean(dir))
	}
	if dir := claudeConfigDir(); dir != "" {
		out = append(out, filepath.Join(dir, "plugins"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".claude", "plugins"))
	}
	return out
}

// claudePluginsDirFor returns the plugins directory whose cache is cacheDir,
// or "" when cacheDir is not a Claude Code plugin cache. The built-in roots
// are canonicalized, so a cache reached through a symlinked ~/.claude or
// plugins directory arrives as its target: each candidate is compared both
// as written and symlink-resolved.
func claudePluginsDirFor(cacheDir string) string {
	clean := filepath.Clean(cacheDir)
	if strings.HasSuffix(clean, string(filepath.Separator)+claudePluginCacheRel) {
		return filepath.Dir(clean)
	}
	for _, dir := range claudePluginsDirs() {
		cache := filepath.Join(dir, "cache")
		if clean == cache {
			return dir
		}
		if resolved, err := filepath.EvalSymlinks(cache); err == nil && clean == resolved {
			return dir
		}
	}
	return ""
}

func isClaudePluginCache(rootPath string) bool {
	return claudePluginsDirFor(rootPath) != ""
}

// claudeUserSettingsPath returns the user settings file next to the plugins
// directory of cacheDir. The cache selected by claudePluginCacheDirEnv, and a
// cache elsewhere, keep their settings in claudeConfigDirEnv when set, and in
// the home directory's .claude otherwise: the cache variable moves only the
// plugins, even to a path that ends like a default cache. ok is false when no
// location is known.
func claudeUserSettingsPath(cacheDir string) (string, bool) {
	clean := filepath.Clean(cacheDir)
	envCache := ""
	if dir := strings.TrimSpace(os.Getenv(claudePluginCacheDirEnv)); dir != "" {
		envCache = filepath.Join(filepath.Clean(dir), "cache")
	}
	if clean != envCache && strings.HasSuffix(clean, string(filepath.Separator)+claudePluginCacheRel) {
		return filepath.Join(filepath.Dir(filepath.Dir(clean)), "settings.json"), true
	}
	if dir := claudeConfigDir(); dir != "" {
		return filepath.Join(dir, "settings.json"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".claude", "settings.json"), true
}

// claudeDisabledPlugins returns the plugin ids ("plugin@marketplace") that
// the user settings file explicitly disables in enabledPlugins. A missing,
// oversized or malformed file yields no information. Only the user settings
// are read: project, local and managed settings depend on the session's
// project, which the agent cannot know.
func claudeDisabledPlugins(cacheDir string) map[string]struct{} {
	path, ok := claudeUserSettingsPath(cacheDir)
	if !ok {
		return nil
	}
	data, ok := readCappedFollowingSymlink(path, maxSettingsBytes)
	if !ok {
		return nil
	}
	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}
	disabled := make(map[string]struct{})
	for id, enabled := range settings.EnabledPlugins {
		if on, isBool := enabled.(bool); isBool && !on {
			disabled[id] = struct{}{}
		}
	}
	return disabled
}

// claudeCachePackRoots returns one pack root per cached plugin: the version
// directory Claude Code's install record names, else the newest version
// directory that is not orphaned and loads as a pack. Emitting every cached
// version would serve a removed or superseded copy next to the current one.
// A plugin the user settings disable stays in the cache, so it is skipped by
// id, as is one installed only at project or local scope.
func claudeCachePackRoots(cacheDir string) []packRoot {
	var out []packRoot
	disabled := claudeDisabledPlugins(cacheDir)
	installed := claudeInstalledPaths(cacheDir)
	for _, marketplace := range childDirs(cacheDir) {
		entries := claudeMarketplaceEntries(cacheDir, filepath.Base(marketplace))
		for _, plugin := range childDirs(marketplace) {
			id := filepath.Base(plugin) + "@" + filepath.Base(marketplace)
			if _, off := disabled[id]; off {
				continue
			}
			install := installed[id]
			if install.projectOnly {
				continue
			}
			// The cache directory is named after the marketplace entry.
			entry := entries[filepath.Base(plugin)]
			if pack, ok := installedClaudeCacheVersion(plugin, install.path, entry); ok {
				out = append(out, pack)
				continue
			}
			if pack, ok := currentClaudeCacheVersion(plugin, entry); ok {
				out = append(out, pack)
			}
		}
	}
	return out
}

// claudeInstalledPlugins is the part of installed_plugins.json the resolver
// reads. Each plugin id maps to its install records, one per scope; an older
// layout holds a single record object instead of an array, so the value stays
// raw until claudeInstalledPaths decodes either shape.
type claudeInstalledPlugins struct {
	Plugins map[string]json.RawMessage `json:"plugins"`
}

type claudeInstallRecord struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
}

// isGlobalClaudeInstallScope reports whether an install record applies to
// every session. User and unscoped records do, and so do managed records,
// which an organization distributes to every machine it manages. Project and
// local records belong to a project the agent cannot identify.
func isGlobalClaudeInstallScope(scope string) bool {
	return scope == "" || scope == "user" || scope == "managed"
}

// claudeInstall is what installed_plugins.json says about one plugin id.
type claudeInstall struct {
	// path is the install path of the first global record that has one
	// (see isGlobalClaudeInstallScope). It is empty when those records name
	// no path.
	path string
	// projectOnly is set when the id has records but none is global, so the
	// plugin must not load for every chat.
	projectOnly bool
}

// claudeInstalledPaths returns what Claude Code recorded for each plugin id in
// installed_plugins.json, next to the cache. Only global records activate a
// plugin. An id absent from the map has no usable record: a
// missing, oversized or malformed file yields an empty map, and the caller
// falls back to the newest cached version.
func claudeInstalledPaths(cacheDir string) map[string]claudeInstall {
	pluginsDir := claudePluginsDirFor(cacheDir)
	if pluginsDir == "" {
		return nil
	}
	data, ok := readCappedFollowingSymlink(filepath.Join(pluginsDir, claudeInstalledPluginsFile), maxSettingsBytes)
	if !ok {
		return nil
	}
	var file claudeInstalledPlugins
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	out := make(map[string]claudeInstall, len(file.Plugins))
	for id, raw := range file.Plugins {
		var records []claudeInstallRecord
		if err := json.Unmarshal(raw, &records); err != nil {
			var one claudeInstallRecord
			if json.Unmarshal(raw, &one) != nil {
				continue
			}
			records = []claudeInstallRecord{one}
		}
		if len(records) == 0 {
			continue
		}
		install := claudeInstall{projectOnly: true}
		for _, record := range records {
			if !isGlobalClaudeInstallScope(record.Scope) {
				continue
			}
			install.projectOnly = false
			if record.InstallPath != "" {
				install.path = record.InstallPath
				break
			}
		}
		out[id] = install
	}
	return out
}

// claudeKnownMarketplacesFile is Claude Code's record of the marketplaces it
// has fetched, kept in the plugins directory next to the cache. Each record's
// installLocation is the marketplace's clone, or the path a marketplace added
// from a local file or directory was given.
const claudeKnownMarketplacesFile = "known_marketplaces.json"

// claudeMarketplaceFileName is the marketplace catalog, kept at
// claudeMarketplaceFileRel under the marketplace root.
const claudeMarketplaceFileName = "marketplace.json"

var claudeMarketplaceFileRel = filepath.Join(".claude-plugin", claudeMarketplaceFileName)

// maxMarketplaceBytes bounds a marketplace catalog read. A catalog lists
// every plugin of the marketplace with its display fields, so it outgrows a
// settings file.
const maxMarketplaceBytes = 4 * 1024 * 1024

// claudeEntryComponentFields are the marketplace entry fields that conflict
// with a plugin.json when the entry sets strict to false
// (https://code.claude.com/docs/en/plugins/marketplace-reference#strict-mode).
// themes also counts under experimental, where the manifest now keeps it.
var claudeEntryComponentFields = []string{"commands", "agents", "skills", "hooks", "outputStyles", "themes"}

// claudeMarketplaceEntry is what a marketplace's plugin entry says about the
// components of a cached plugin. The zero value means no entry is known.
type claudeMarketplaceEntry struct {
	found bool
	// strict is the entry's strict field, true unless it is false.
	strict bool
	// declaresComponents is set when the entry declares any of
	// claudeEntryComponentFields.
	declaresComponents bool
	// rootSource is set when the entry's source is the marketplace root.
	rootSource bool
	skills     manifestPaths
	agents     manifestPaths
}

// skipsDefaultSkills reports whether the entry limits the plugin to the
// skills it lists: an entry whose source is the marketplace root and that
// lists skills keeps Claude Code from scanning the default skills directory.
func (e claudeMarketplaceEntry) skipsDefaultSkills() bool {
	return e.rootSource && declaresAny(e.skills)
}

// claudeMarketplaceFiles returns the candidate catalog paths of a marketplace,
// most authoritative first: the installLocation recorded in
// known_marketplaces.json, then the copy Claude Code fetches into
// marketplaces/<name>/.
func claudeMarketplaceFiles(pluginsDir, marketplace string) []string {
	var out []string
	if location, rel, ok := claudeKnownMarketplaceLocation(pluginsDir, marketplace); ok {
		if info, err := os.Stat(location); err == nil && !info.IsDir() {
			// A marketplace added from a local file records the file.
			out = append(out, location)
		} else if rel != "" {
			out = append(out, filepath.Join(location, rel))
		}
	}
	return append(out, filepath.Join(pluginsDir, "marketplaces", marketplace, claudeMarketplaceFileRel))
}

// claudeKnownMarketplaceLocation returns the recorded install location of a
// marketplace and the catalog path inside it. A git or GitHub source may name
// a catalog path other than the default; one that escapes the clone yields
// an empty rel. ok is false when no usable record exists.
func claudeKnownMarketplaceLocation(pluginsDir, marketplace string) (location, rel string, ok bool) {
	data, ok := readCappedFollowingSymlink(filepath.Join(pluginsDir, claudeKnownMarketplacesFile), maxSettingsBytes)
	if !ok {
		return "", "", false
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(data, &known); err != nil {
		return "", "", false
	}
	var record struct {
		Source struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		} `json:"source"`
		InstallLocation string `json:"installLocation"`
	}
	if raw, found := known[marketplace]; !found || json.Unmarshal(raw, &record) != nil || !filepath.IsAbs(record.InstallLocation) {
		return "", "", false
	}
	rel = claudeMarketplaceFileRel
	if path := strings.TrimSpace(record.Source.Path); path != "" && (record.Source.Source == "github" || record.Source.Source == "git") {
		rel = containerRelPath(path, "")
	}
	return filepath.Clean(record.InstallLocation), rel, true
}

// claudeMarketplaceEntries returns the plugin entries of a cached
// marketplace keyed by plugin name, from the first candidate catalog that
// reads. A missing, oversized or malformed catalog yields no entries, and the
// plugins then load from their own files alone.
func claudeMarketplaceEntries(cacheDir, marketplace string) map[string]claudeMarketplaceEntry {
	pluginsDir := claudePluginsDirFor(cacheDir)
	if pluginsDir == "" {
		return nil
	}
	for _, path := range claudeMarketplaceFiles(pluginsDir, marketplace) {
		if data, ok := readCappedFollowingSymlink(path, maxMarketplaceBytes); ok {
			return parseClaudeMarketplaceEntries(data)
		}
	}
	return nil
}

// parseClaudeMarketplaceEntries decodes the plugin entries of a catalog.
// Claude Code validates each entry on its own, so an entry without a name or
// with a mistyped skills or agents field is skipped without failing the rest.
func parseClaudeMarketplaceEntries(data []byte) map[string]claudeMarketplaceEntry {
	var catalog struct {
		Plugins []json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil
	}
	out := make(map[string]claudeMarketplaceEntry, len(catalog.Plugins))
	for _, raw := range catalog.Plugins {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			continue
		}
		var name string
		if err := json.Unmarshal(fields["name"], &name); err != nil || name == "" {
			continue
		}
		if _, dup := out[name]; dup || !claudePathFieldsTyped(raw) {
			continue
		}
		var paths struct {
			Skills manifestPaths `json:"skills"`
			Agents manifestPaths `json:"agents"`
		}
		if err := json.Unmarshal(raw, &paths); err != nil {
			continue
		}
		entry := claudeMarketplaceEntry{
			found:              true,
			strict:             true,
			declaresComponents: declaresClaudeComponents(fields),
			rootSource:         isMarketplaceRootSource(fields["source"]),
			skills:             paths.Skills,
			agents:             paths.Agents,
		}
		var strict bool
		if err := json.Unmarshal(fields["strict"], &strict); err == nil && !strict {
			entry.strict = false
		}
		out[name] = entry
	}
	return out
}

func declaresClaudeComponents(fields map[string]json.RawMessage) bool {
	for _, key := range claudeEntryComponentFields {
		if isDeclaredJSON(fields[key]) {
			return true
		}
	}
	var experimental map[string]json.RawMessage
	if err := json.Unmarshal(fields["experimental"], &experimental); err == nil && isDeclaredJSON(experimental["themes"]) {
		return true
	}
	return false
}

// isDeclaredJSON reports whether a decoded field is present with a non-null
// value.
func isDeclaredJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// isMarketplaceRootSource reports whether an entry's source is the relative
// path of the marketplace root ("." or "./").
func isMarketplaceRootSource(raw json.RawMessage) bool {
	var source string
	if err := json.Unmarshal(raw, &source); err != nil {
		return false
	}
	source = strings.TrimSpace(source)
	return source != "" && filepath.Clean(source) == "."
}

// installedClaudeCacheVersion returns the pack at the version directory an
// install record names. The record is trusted only when that directory is a
// direct child of pluginDir (so it lies inside the cache and belongs to this
// plugin), is not orphaned, and loads as a pack. ok is false otherwise and
// the caller falls back to the newest-version rule.
func installedClaudeCacheVersion(pluginDir, installPath string, entry claudeMarketplaceEntry) (packRoot, bool) {
	if installPath == "" || !filepath.IsAbs(installPath) {
		return packRoot{}, false
	}
	clean := filepath.Clean(installPath)
	parent, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil {
		return packRoot{}, false
	}
	if plugin, err := filepath.EvalSymlinks(pluginDir); err != nil || parent != plugin {
		return packRoot{}, false
	}
	versionDir := filepath.Join(pluginDir, filepath.Base(clean))
	if info, err := os.Stat(versionDir); err != nil || !info.IsDir() || claudeVersionOrphaned(versionDir) {
		return packRoot{}, false
	}
	return readClaudeCacheVersion(versionDir, entry)
}

// readClaudeCacheVersion reads a cached plugin version as a pack. A Claude
// Code plugin needs no manifest: it loads the components in the standard
// layout and takes its name from the directory, so a version directory with
// no manifest of any kind is a Claude pack with the default component paths,
// and its marketplace entry, when one is known, serves as the manifest. A
// manifest that exists but does not load still rejects the version, and so
// does a conflict with the entry (withMarketplaceEntry).
func readClaudeCacheVersion(versionDir string, entry claudeMarketplaceEntry) (packRoot, bool) {
	if pack, ok := readPluginManifest(versionDir); ok {
		return pack.withMarketplaceEntry(entry)
	}
	if findPackManifest(versionDir) != "" {
		return packRoot{}, false
	}
	return packRoot{
		Dir:          versionDir,
		ManifestPath: filepath.Join(versionDir, claudePluginManifestRelPath),
		Manifest: pluginManifest{
			Name:   filepath.Base(filepath.Dir(versionDir)),
			Skills: entry.skills,
			Agents: entry.agents,
		},
		Claude:            true,
		SkipDefaultSkills: entry.skipsDefaultSkills(),
	}, true
}

// withMarketplaceEntry applies the marketplace entry of a cached Claude pack
// that has its own plugin.json. With strict unset or true the entry's skills
// and agents are appended to the manifest's, so entry agents replace the
// default agents directory just as manifest agents do. With strict false an
// entry that declares any component conflicts with plugin.json and the
// plugin fails to load, so ok is false. Entry paths are plugin-root relative
// and pass the same containment checks as manifest paths.
func (p packRoot) withMarketplaceEntry(entry claudeMarketplaceEntry) (packRoot, bool) {
	if !entry.found || !p.Claude {
		return p, true
	}
	if !entry.strict && entry.declaresComponents {
		return packRoot{}, false
	}
	p.Manifest.Skills = append(slices.Clone(p.Manifest.Skills), entry.skills...)
	p.Manifest.Agents = append(slices.Clone(p.Manifest.Agents), entry.agents...)
	p.SkipDefaultSkills = entry.skipsDefaultSkills()
	return p, true
}

func currentClaudeCacheVersion(pluginDir string, entry claudeMarketplaceEntry) (packRoot, bool) {
	var (
		best     packRoot
		bestTime time.Time
		found    bool
	)
	for _, versionDir := range claudeCacheLiveVersions(pluginDir) {
		info, err := os.Stat(versionDir)
		if err != nil {
			continue
		}
		pack, ok := readClaudeCacheVersion(versionDir, entry)
		if !ok {
			continue
		}
		// Ties break on the directory name so the choice is stable.
		if !found || info.ModTime().After(bestTime) || (info.ModTime().Equal(bestTime) && versionDir > best.Dir) {
			best, bestTime, found = pack, info.ModTime(), true
		}
	}
	return best, found
}

// claudeCacheLiveVersions returns the version directories of a cached
// plugin that Claude Code has not orphaned.
func claudeCacheLiveVersions(pluginDir string) []string {
	var out []string
	for _, versionDir := range childDirs(pluginDir) {
		if claudeVersionOrphaned(versionDir) {
			continue
		}
		out = append(out, versionDir)
	}
	return out
}

func claudeVersionOrphaned(versionDir string) bool {
	_, err := os.Lstat(filepath.Join(versionDir, claudeOrphanedMarker))
	return err == nil
}

// childDirs returns the paths of dir's immediate subdirectories. Symlinked
// entries are skipped, like everywhere else in pack discovery.
func childDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// containerRelPath validates a manifest-declared directory. It must be
// sibling-relative and stay inside the pack root (R7.5): absolute paths and
// anything climbing out with ".." are rejected. An empty declaration keeps
// the container's default.
func containerRelPath(declared, fallback string) string {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return fallback
	}
	cleaned := filepath.Clean(declared)
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return ""
	}
	return cleaned
}

// declaredRels returns the validated relative paths of a manifest
// declaration. Invalid entries are dropped, and a declaration with no
// non-empty entry keeps the default.
func declaredRels(declared manifestPaths, fallback string) []string {
	var out []string
	anyDeclared := false
	for _, d := range declared {
		if strings.TrimSpace(d) == "" {
			continue
		}
		anyDeclared = true
		if rel := containerRelPath(d, ""); rel != "" {
			out = append(out, rel)
		}
	}
	if !anyDeclared {
		return []string{fallback}
	}
	return out
}

// skillRels returns the pack's skills paths. A Claude pack scans the default
// skills directory and then its declared paths: Claude Code adds declared
// paths to the default, so declaring one never hides skills/, unless a
// marketplace entry limits the pack to its listed skills
// (SkipDefaultSkills). Any other manifest's declaration replaces the default.
func (p packRoot) skillRels() []string {
	declared := declaredRels(p.Manifest.Skills, defaultPackSkillsDir)
	if !p.Claude || p.SkipDefaultSkills {
		return declared
	}
	out := []string{defaultPackSkillsDir}
	for _, rel := range declared {
		if !slices.Contains(out, rel) {
			out = append(out, rel)
		}
	}
	return out
}

// skillPaths splits the pack's existing skill paths into containers of skill
// folders and single skill folders. A declared path holding SKILL.md itself
// is one skill (a manifest may declare "./review-pr"); any other directory is
// a container whose child folders are the skills.
func (p packRoot) skillPaths() (containers, folders []string) {
	if p.loadsRootSkill() {
		return nil, []string{p.Dir}
	}
	for _, rel := range p.skillRels() {
		for _, dir := range inPackContainers(p, rel) {
			if _, err := os.Lstat(filepath.Join(dir, skillMetaFileName)); err == nil {
				folders = append(folders, dir)
				continue
			}
			containers = append(containers, dir)
		}
	}
	return containers, folders
}

// loadsRootSkill reports whether a Claude pack loads as one skill: Claude
// Code takes a SKILL.md at the plugin root as the plugin's only skill when
// the plugin has no skills directory and declares no skills.
func (p packRoot) loadsRootSkill() bool {
	if !p.Claude || declaresAny(p.Manifest.Skills) {
		return false
	}
	if _, err := os.Lstat(filepath.Join(p.Dir, defaultPackSkillsDir)); err == nil {
		return false
	}
	_, err := os.Lstat(filepath.Join(p.Dir, skillMetaFileName))
	return err == nil
}

// agentContainers returns the existing agent-container directories of a
// pack, honoring the manifest's declared agents entries that are directories.
// Claude Code loads the default agents directory of a Claude pack
// recursively, so its subfolders are containers too (nestedAgentDirs).
//
// A Claude pack's declared agents entries must name .md files: Claude Code
// accepts no directory there, and a declaration replaces the default
// directory, so a declared directory loads nothing. Like an escaping or
// missing path, it costs only that entry, not the plugin
// (https://code.claude.com/docs/en/errors#path-escapes-plugin-directory).
func (p packRoot) agentContainers() []string {
	declared := declaresAny(p.Manifest.Agents)
	if p.Claude && declared {
		return nil
	}
	var out []string
	for _, rel := range declaredRels(p.Manifest.Agents, defaultPackAgentsDir) {
		out = append(out, inPackContainers(p, rel)...)
	}
	if !p.Claude {
		return out
	}
	var nested []string
	for _, container := range out {
		nested = append(nested, nestedAgentDirs(container)...)
	}
	return append(out, nested...)
}

// Bounds on the subfolder walk of a Claude pack's default agents directory.
// Every folder found also becomes a watch, so a deep or wide tree must not
// exhaust the fsnotify budget.
const (
	maxPackAgentDepth = 4
	maxPackAgentDirs  = 64
)

// nestedAgentDirs returns the subfolders of container breadth-first, at most
// maxPackAgentDepth levels down and maxPackAgentDirs in total. Symlinked
// folders are skipped (childDirs), so the walk stays inside the pack.
//
// Claude Code joins the subfolder names into a scoped agent name
// (agents/team/reviewer.md is plugin:team:reviewer). Pack agents here carry
// no plugin namespace and coderd requires a kebab-case name, so a nested
// agent keeps its front-matter or file name, like a top-level one, and a
// clash is settled by the usual first-wins rule.
func nestedAgentDirs(container string) []string {
	var out []string
	level := []string{container}
	for depth := 0; depth < maxPackAgentDepth && len(level) > 0; depth++ {
		var next []string
		for _, dir := range level {
			for _, child := range childDirs(dir) {
				if len(out) >= maxPackAgentDirs {
					return out
				}
				out = append(out, child)
				next = append(next, child)
			}
		}
		level = next
	}
	return out
}

// agentFiles returns the declared agents entries that are single definition
// files inside the pack. Cursor and root manifests may name .md files next to
// directories; a Claude manifest names only files.
func (p packRoot) agentFiles() []string {
	var out []string
	for _, rel := range declaredRels(p.Manifest.Agents, defaultPackAgentsDir) {
		if filepath.Ext(rel) != agentDefinitionExt {
			continue
		}
		out = append(out, inPackFiles(p, rel)...)
	}
	return out
}

// inPackContainers returns container when it exists, is a directory, and
// resolves inside the pack root, mirroring the boundary check
// agentContainersFor applies to scan roots. Definitions are shipped whole,
// so a symlinked container must not pull in outside files.
func inPackContainers(p packRoot, rel string) []string {
	return inPackPaths(p, rel, true)
}

// inPackFiles is inPackContainers for a single regular file.
func inPackFiles(p packRoot, rel string) []string {
	return inPackPaths(p, rel, false)
}

func inPackPaths(p packRoot, rel string, wantDir bool) []string {
	path := filepath.Join(p.Dir, rel)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil
	}
	packResolved := p.Dir
	if resolvedPack, err := filepath.EvalSymlinks(p.Dir); err == nil {
		packResolved = resolvedPack
	}
	if !pathHasPrefix(resolved, packResolved) {
		return nil
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() != wantDir {
		return nil
	}
	return []string{path}
}

// packRowsAdvertised reports whether the server's push-response
// capability is known on. A nil callback (unwired resolver, or an older
// embedder that never learned the option) counts as not advertised.
func (r *Resolver) packRowsAdvertised() bool {
	return r.PackRowsAdvertised != nil && r.PackRowsAdvertised()
}

// PackAgentDefinitionsOn reports whether the fork-agent-definitions
// capability is on: chatd drops every subagent row while agent
// definitions are off, so emitted pack agents would be invisible rows
// that still occupy the index-based snapshot count cap ahead of live
// MCP and ACP resources. The Manager wires the callback alongside
// PackRowsAdvertised; nil omits the rows for unwired resolvers, since
// the capability must be earned from the server like PackRowsAdvertised.
func (r *Resolver) PackAgentDefinitionsOn() bool {
	return r.PackAgentDefinitionsAdvertised != nil && r.PackAgentDefinitionsAdvertised()
}

// emitPacks appends the pack-contributed resources of a scan root after the
// ordinary discovery. A pack skill or agent sharing a name with an ordinary
// one or an earlier unmanaged pack's loses the usual first-wins dedup; managed
// packs are settled by splitManagedPackDuplicates and coderd. Emission waits
// for the server's push-response capability: an upgrade window with a newer
// agent and an older coderd replica must not persist pack rows, because the
// older replica serves pinned skills without a pack gate. The callback is
// re-read per root, so a capability flip applies to the next resolve.
func (r *Resolver) emitPacks(root ScanRoot, out *[]Resource, seenID map[string]int) {
	if !r.packRowsAdvertised() {
		return
	}
	for _, pack := range packRootsFor(root.Path) {
		if len(pack.ManifestPath) > maxSourcePathBytes {
			// The manifest path is the marker every pack row carries;
			// a path over coderd's source_path transport limit would
			// sink the whole snapshot (and pushWithRetry would retry
			// that rejection forever), so this pack contributes
			// nothing instead.
			continue
		}
		r.emitPackSkills(pack, root, out, seenID)
		r.emitPackAgents(pack, root, out, seenID)
	}
}

// emitPackSkills emits one Skill resource per skill directory in the pack's
// skill containers, plus one per single skill folder (declared, or a Claude
// pack's root, see loadsRootSkill). The loop mirrors emitSkillsFromContainer
// but stamps SourcePath with the manifest path so the coderd fork-plugin gate
// can identify pack contributions.
func (r *Resolver) emitPackSkills(pack packRoot, root ScanRoot, out *[]Resource, seenID map[string]int) {
	containers, folders := pack.skillPaths()
	for _, container := range containers {
		entries, err := os.ReadDir(container)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			r.emitPackSkill(pack, root, filepath.Join(container, e.Name()), out, seenID)
		}
	}
	for _, folder := range folders {
		r.emitPackSkill(pack, root, folder, out, seenID)
	}
}

// emitPackSkill emits the skill in dir when it holds a SKILL.md.
func (r *Resolver) emitPackSkill(pack packRoot, root ScanRoot, dir string, out *[]Resource, seenID map[string]int) {
	meta := filepath.Join(dir, skillMetaFileName)
	// Lstat (not Stat) so a symlinked SKILL.md is detected and routed
	// through resolveReadTarget, which enforces the pack-root boundary: a
	// pack must not import skills from outside itself even when the target
	// stays inside the scan root.
	info, err := os.Lstat(meta)
	if err != nil {
		return
	}
	dirName := filepath.Base(dir)
	if dir == pack.Dir {
		// A plugin-root skill has no skill directory to match: Claude
		// Code names it from its front-matter, and a cached version
		// directory is named after the version.
		dirName = ""
	}
	res, ok := r.readSkillMetaNamed(pack.Dir, meta, info, root.UserSource, dirName)
	if !ok {
		return
	}
	res = r.withClaudePluginRoot(pack, res)
	res.SourcePath = pack.ManifestPath
	appendResource(out, seenID, res)
}

// emitPackAgents emits one KindSubagent resource per agent definition file in
// the pack's agent containers. The loop mirrors emitAgents over the pack's
// containers. It emits nothing while the fork-agent-definitions capability is
// unadvertised: chatd drops every subagent row then, so the rows would be
// invisible inventory that still occupies the snapshot count cap ahead of live
// MCP and ACP resources (the cap is index-based, so even excluded rows consume
// slots; a 500-agent pack would fill DefaultMaxResources on its own). Omitting
// mirrors how pack skills stay omitted while the pack capability is
// unadvertised, and enabling the capability flips the advertisement, which
// triggers the resync that adds the rows back.
func (r *Resolver) emitPackAgents(pack packRoot, root ScanRoot, out *[]Resource, seenID map[string]int) {
	if !r.PackAgentDefinitionsOn() {
		return
	}
	for _, container := range pack.agentContainers() {
		entries, err := os.ReadDir(container)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != agentDefinitionExt {
				continue
			}
			r.emitPackAgentFile(pack, root, filepath.Join(container, e.Name()), out, seenID)
		}
	}
	for _, path := range pack.agentFiles() {
		r.emitPackAgentFile(pack, root, path, out, seenID)
	}
}

// emitPackAgentFile emits the agent definition at path, stamped with the
// pack's manifest path.
func (r *Resolver) emitPackAgentFile(pack packRoot, root ScanRoot, path string, out *[]Resource, seenID map[string]int) {
	// Lstat so a symlinked definition goes through
	// resolveReadTarget's scan-root boundary check.
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	// The pack directory is the read boundary: a definition may symlink
	// only within the pack, even when the target stays inside the
	// enclosing scan root.
	res, ok := r.readAgentDefinition(ScanRoot{Path: pack.Dir, UserSource: root.UserSource}, path, info)
	if ok {
		res = r.withClaudePluginRoot(pack, res)
	}
	// Agents of a managed pack are never first-wins winners: see
	// splitManagedPackDuplicates.
	managed := isManagedPackManifest(pack.ManifestPath)
	if !ok || hasSource(*out, res.Source) || (!managed && res.Status == StatusOK && hasOKSubagent(*out, res.Name)) {
		return
	}
	res.SourcePath = pack.ManifestPath
	appendResource(out, seenID, res)
}

// claudePluginRootVar is the path variable Claude Code substitutes with the
// plugin root in skill, command and agent Markdown.
const claudePluginRootVar = "${CLAUDE_PLUGIN_ROOT}"

// withClaudePluginRoot substitutes claudePluginRootVar with the pack root in
// the Markdown body of a Claude pack's skill or agent, so the served content
// points at the plugin's files the way Claude Code's does. Front-matter is
// left alone: a path with YAML-significant characters must not break the
// definition coderd parses again. The payload, its size and its hash stay
// coherent: a substitution that grows the file past the per-resource cap
// yields an oversize row, as a file of that size would. Other packs pass
// through unchanged.
func (r *Resolver) withClaudePluginRoot(pack packRoot, res Resource) Resource {
	if !pack.Claude || res.Status != StatusOK {
		return res
	}
	// Claude Code substitutes forward-slash paths on Windows too.
	payload, changed := substituteMarkdownBody(res.Payload, claudePluginRootVar, filepath.ToSlash(pack.Dir))
	if !changed {
		return res
	}
	res.ContentHash = sha256.Sum256(payload)
	res.SizeBytes = safeUint64(int64(len(payload)))
	if res.SizeBytes > r.MaxResourceBytes {
		res.Status = StatusOversize
		res.Error = fmt.Sprintf("size %d after %s substitution exceeds per-resource cap of %d bytes", res.SizeBytes, claudePluginRootVar, r.MaxResourceBytes)
		res.Payload = nil
		res.Name = ""
		res.Description = ""
		return res
	}
	res.Payload = payload
	return res
}

// substituteMarkdownBody replaces old with replacement after the closing
// front-matter delimiter of data, matching how the workspacesdk parsers
// split front-matter. changed is false, and data is returned as is, when
// data has no front-matter or its body holds no old.
func substituteMarkdownBody(data []byte, old, replacement string) ([]byte, bool) {
	start := markdownBodyOffset(data)
	if start < 0 || !bytes.Contains(data[start:], []byte(old)) {
		return data, false
	}
	body := bytes.ReplaceAll(data[start:], []byte(old), []byte(replacement))
	out := make([]byte, 0, start+len(body))
	out = append(out, data[:start]...)
	return append(out, body...), true
}

// markdownBodyOffset returns the offset just past the line that closes
// data's front-matter, or -1 when data does not open with a "---" line or
// never closes it.
func markdownBodyOffset(data []byte) int {
	rest := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	offset := len(data) - len(rest)
	opened := false
	for len(rest) > 0 {
		line, next, found := bytes.Cut(rest, []byte("\n"))
		offset += len(line)
		if found {
			offset++
		}
		delimiter := string(bytes.TrimSpace(line)) == "---"
		switch {
		case !opened && !delimiter:
			return -1
		case !opened:
			opened = true
		case delimiter:
			return offset
		}
		rest = next
	}
	return -1
}

// managedPackRoot is the directory, relative to the home directory, that
// coderd writes installed packs into, one subdirectory per pack. Keep in sync
// with managedPackRoot in coderd/x/chatd/fork_plugin_gate.go, which filters
// the rows of these packs per chat.
const managedPackRoot = "/.coder/plugins/"

// isManagedPackManifest reports whether a pack manifest path belongs to a pack
// installed by coderd: its pack directory is a direct child of managedPackRoot.
// It splits on both separators because the agent may run on Windows.
func isManagedPackManifest(manifestPath string) bool {
	normalized := strings.ReplaceAll(manifestPath, `\`, "/")
	i := strings.LastIndex(normalized, managedPackRoot)
	if i < 0 {
		return false
	}
	name, _, ok := strings.Cut(normalized[i+len(managedPackRoot):], "/")
	return ok && name != ""
}

// isManagedPackContribution reports whether a resource comes from a pack
// installed by coderd. Disk holds every such pack, enabled for a chat or not,
// so the agent cannot tell which of two same-named resources is live.
func isManagedPackContribution(resource Resource) bool {
	return isPackContribution(resource) && isManagedPackManifest(resource.SourcePath)
}

// splitManagedPackDuplicates settles name collisions that the agent can settle
// and defers the rest to coderd. A skill or agent of a managed pack is dropped
// when any other source (an ordinary resource, or a pack the user placed
// outside managedPackRoot) uses its name, whatever the scan-root order: those
// sources are never filtered per chat, so they are always live. Among managed
// packs the first resource with a name stays in resources and later ones are
// returned as duplicates, which appendManagedPackDuplicates adds back within
// spare capacity. coderd keeps the first duplicate of the packs enabled for a
// chat, which a disabled pack or a file removed by a pack update must not
// hide.
func splitManagedPackDuplicates(resources []Resource) (kept, duplicates []Resource) {
	type nameKey struct {
		kind ResourceKind
		name string
	}
	deferrable := func(resource Resource) bool {
		return (resource.Kind == KindSkill || resource.Kind == KindSubagent) &&
			resource.Status == StatusOK && isManagedPackContribution(resource)
	}
	winners := make(map[nameKey]struct{})
	for _, resource := range resources {
		if (resource.Kind == KindSkill || resource.Kind == KindSubagent) &&
			resource.Status == StatusOK && !deferrable(resource) {
			winners[nameKey{resource.Kind, resource.Name}] = struct{}{}
		}
	}
	managedSeen := make(map[nameKey]struct{})
	kept = resources[:0]
	for _, resource := range resources {
		if !deferrable(resource) {
			kept = append(kept, resource)
			continue
		}
		key := nameKey{resource.Kind, resource.Name}
		if _, shadowed := winners[key]; shadowed {
			continue
		}
		if _, dup := managedSeen[key]; dup {
			duplicates = append(duplicates, resource)
			continue
		}
		managedSeen[key] = struct{}{}
		kept = append(kept, resource)
	}
	return kept, duplicates
}

// appendManagedPackDuplicates adds the duplicates splitManagedPackDuplicates
// set aside. They are speculative copies for a chat that enables a later pack
// than the first one on disk, so they never displace anything: they are added
// after applyCaps and applyProviderCaps, skip the resource count cap, and take
// only the bytes the snapshot has left. Their count is bounded by twice
// MaxResources, the headroom coderd's per-push limit allows above the agent
// cap. Duplicates that do not fit are omitted, not stubbed.
func (r *Resolver) appendManagedPackDuplicates(resources, duplicates []Resource) []Resource {
	var used uint64
	for _, resource := range resources {
		used += uint64(len(resource.Payload))
	}
	for _, duplicate := range duplicates {
		if len(resources) >= 2*r.MaxResources {
			break
		}
		size := uint64(len(duplicate.Payload))
		if used+size > r.MaxSnapshotBytes {
			continue
		}
		used += size
		resources = append(resources, duplicate)
	}
	return resources
}

// packWatchPaths is the pack state the watcher keeps between syncs to tell
// relevant writes from unrelated ones in the directories it watches.
type packWatchPaths struct {
	// agentDirs holds the pack agent containers. A manifest may name any
	// contained directory, so a Markdown write there is relevant even when
	// the directory is not called "agents".
	agentDirs map[string]struct{}
	// settingsFiles holds the Claude Code user settings files whose
	// enabledPlugins select the cached plugins. Only these files are
	// relevant: the directory holding one also holds Claude Code's
	// history and session state, which change constantly.
	settingsFiles map[string]struct{}
}

func newPackWatchPaths() packWatchPaths {
	return packWatchPaths{agentDirs: make(map[string]struct{}), settingsFiles: make(map[string]struct{})}
}

// relevant reports whether a write to path can change the pack rows.
func (p packWatchPaths) relevant(path string) bool {
	if filepath.Ext(path) == agentDefinitionExt {
		if _, ok := p.agentDirs[filepath.Dir(path)]; ok {
			return true
		}
	}
	_, ok := p.settingsFiles[path]
	return ok
}

// addPackWatchDirs adds every pack root and its declared skill and agent
// containers under rootPath to out, plus one level inside skill containers,
// so creating a pack or editing its files re-resolves the snapshot. Agent
// containers, including the nested folders of a Claude pack's default agents
// directory, also go to watch.agentDirs, so overwriting a definition in a
// container with any name re-resolves. For the Claude Code plugin cache the
// user settings file goes to watch.settingsFiles and its directory to out,
// so toggling a plugin in enabledPlugins re-resolves. The watcher calls this
// next to addAgentWatchDirs.
//
// A manifest may declare a container that does not exist yet
// ("skills": "components/skills" while only components/ is present).
// fsnotify is non-recursive, so with only the pack root watched, creating
// the container later fires no event and the pack stays undiscovered until
// an unrelated resync. Watching the deepest existing ancestor of each
// declared path arms that event, and the watcher re-collects after every
// re-resolve, so the watch walks down as the tree is built out.
func addPackWatchDirs(rootPath string, out map[string]struct{}, watch packWatchPaths) {
	for _, pack := range packRootsFor(rootPath) {
		out[pack.Dir] = struct{}{}
		// Nested manifests (.claude-plugin/plugin.json) live in their
		// own directory: fsnotify reports edits there, and watching
		// only the pack root never sees them, so declared container
		// changes would stay stale until an unrelated event. The
		// manifest exists, so its parent does too.
		if parent := filepath.Dir(pack.ManifestPath); parent != pack.Dir {
			// A manifestless Claude cache entry has no manifest directory.
			if info, err := os.Stat(parent); err == nil && info.IsDir() {
				out[parent] = struct{}{}
			}
		}
		containers, folders := pack.skillPaths()
		for _, container := range containers {
			out[container] = struct{}{}
			addPackImmediateSubdirs(container, out)
		}
		for _, folder := range folders {
			out[folder] = struct{}{}
		}
		for _, container := range pack.agentContainers() {
			out[container] = struct{}{}
			watch.agentDirs[container] = struct{}{}
		}
		for _, file := range pack.agentFiles() {
			dir := filepath.Dir(file)
			out[dir] = struct{}{}
			watch.agentDirs[dir] = struct{}{}
		}
		for _, rel := range pack.declaredContainerRels() {
			if ancestor := existingAncestor(filepath.Join(pack.Dir, rel)); ancestor != "" {
				out[ancestor] = struct{}{}
			}
		}
	}
	// Candidate manifest directories are watched whenever they exist,
	// even when no manifest parses there: packRootsFor only returns
	// directories whose manifest exists, so a .claude-plugin/ created
	// ahead of its plugin.json (or after a deletion) would otherwise
	// get no watch, and creating the manifest would fire an event in a
	// directory fsnotify does not observe. Sync re-installs the watch
	// set on every resync, so the directory is picked up as soon as it
	// exists. Only nested candidates need this: the root-level
	// candidate lives in the directory itself, which the scan-root and
	// pack watches already cover.
	dirs := packManifestCandidateDirs(rootPath)
	for _, child := range childDirs(rootPath) {
		dirs = append(dirs, packManifestCandidateDirs(child)...)
	}
	if isClaudePluginCache(rootPath) {
		// The install record picks the current version, so its directory
		// is watched too.
		pluginsDir := claudePluginsDirFor(rootPath)
		if pluginsDir != "" {
			dirs = append(dirs, pluginsDir)
		}
		// enabledPlugins in the user settings disables cached plugins
		// (claudeDisabledPlugins).
		if settings, ok := claudeUserSettingsPath(rootPath); ok {
			dirs = append(dirs, filepath.Dir(settings))
			watch.settingsFiles[settings] = struct{}{}
		}
		// Watching every level down to the version directories
		// fires the events for a new install, a new version, and
		// an orphan marker, which all change the chosen pack. An
		// orphaned version stays watched so removing its marker is
		// noticed; only live versions get their nested manifest
		// directories watched.
		for _, marketplace := range childDirs(rootPath) {
			out[marketplace] = struct{}{}
			// A marketplace update can change an entry's components.
			for _, catalog := range claudeMarketplaceFiles(pluginsDir, filepath.Base(marketplace)) {
				dirs = append(dirs, filepath.Dir(catalog))
			}
			for _, plugin := range childDirs(marketplace) {
				out[plugin] = struct{}{}
				for _, version := range childDirs(plugin) {
					out[version] = struct{}{}
					if !claudeVersionOrphaned(version) {
						dirs = append(dirs, packManifestCandidateDirs(version)...)
					}
				}
			}
		}
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out[dir] = struct{}{}
		}
	}
}

// packManifestCandidateDirs returns the nested directories that may
// hold a plugin manifest for packDir, in candidate order.
func packManifestCandidateDirs(packDir string) []string {
	return []string{
		filepath.Join(packDir, filepath.Dir(claudePluginManifestRelPath)),
		filepath.Join(packDir, filepath.Dir(cursorPluginManifestRelPath)),
	}
}

// declaredContainerRels returns the manifest-validated relative paths of
// the pack's skills and agents containers whether or not they exist on
// disk. Invalid declarations ("../x", absolute) are rejected by
// containerRelPath and skipped, matching discovery.
func (p packRoot) declaredContainerRels() []string {
	return append(p.skillRels(), declaredRels(p.Manifest.Agents, defaultPackAgentsDir)...)
}

// addPackImmediateSubdirs adds the immediate subdirectories of dir to out,
// the way the watcher watches one level inside skill containers.
func addPackImmediateSubdirs(dir string, out map[string]struct{}) {
	for _, child := range childDirs(dir) {
		out[child] = struct{}{}
	}
}
