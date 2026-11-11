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
	"encoding/json"
	"os"
	"path/filepath"
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
// or agents field falls back to the default container instead of rejecting
// the whole manifest.
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
	return packRoot{Dir: packDir, ManifestPath: manifestPath, Manifest: manifest}, true
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

func isClaudePluginCache(rootPath string) bool {
	clean := filepath.Clean(rootPath)
	if strings.HasSuffix(clean, string(filepath.Separator)+claudePluginCacheRel) {
		return true
	}
	override := claudePluginCacheOverride()
	if override == "" {
		return false
	}
	// The built-in root is canonicalized, so compare the resolved path too.
	if clean == filepath.Clean(override) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(override)
	return err == nil && clean == resolved
}

// claudeUserSettingsPath returns the user settings file next to the plugins
// directory of cacheDir. A cache elsewhere keeps its settings in
// claudeConfigDirEnv when set, and in the home directory's .claude otherwise.
// ok is false when no location is known.
func claudeUserSettingsPath(cacheDir string) (string, bool) {
	clean := filepath.Clean(cacheDir)
	if strings.HasSuffix(clean, string(filepath.Separator)+claudePluginCacheRel) {
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
	data, ok := readCappedRegularFile(path, maxSettingsBytes)
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

// claudeCachePackRoots returns one pack root per cached plugin: its newest
// version directory that is not orphaned and holds a manifest. Emitting
// every cached version would serve a removed or superseded copy next to
// the current one. A plugin the user settings disable stays in the cache, so
// it is skipped by id.
func claudeCachePackRoots(cacheDir string) []packRoot {
	var out []packRoot
	disabled := claudeDisabledPlugins(cacheDir)
	for _, marketplace := range childDirs(cacheDir) {
		for _, plugin := range childDirs(marketplace) {
			id := filepath.Base(plugin) + "@" + filepath.Base(marketplace)
			if _, off := disabled[id]; off {
				continue
			}
			if pack, ok := currentClaudeCacheVersion(plugin); ok {
				out = append(out, pack)
			}
		}
	}
	return out
}

func currentClaudeCacheVersion(pluginDir string) (packRoot, bool) {
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
		pack, ok := readPluginManifest(versionDir)
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
		if _, err := os.Lstat(filepath.Join(versionDir, claudeOrphanedMarker)); err == nil {
			continue
		}
		out = append(out, versionDir)
	}
	return out
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

// skillContainers returns the existing skill-container directories of a
// pack, honoring the manifest's declared skills directories.
func (p packRoot) skillContainers() []string {
	var out []string
	for _, rel := range declaredRels(p.Manifest.Skills, defaultPackSkillsDir) {
		out = append(out, inPackContainers(p, rel)...)
	}
	return out
}

// agentContainers returns the existing agent-container directories of a
// pack, honoring the manifest's declared agents entries that are directories.
func (p packRoot) agentContainers() []string {
	var out []string
	for _, rel := range declaredRels(p.Manifest.Agents, defaultPackAgentsDir) {
		out = append(out, inPackContainers(p, rel)...)
	}
	return out
}

// agentFiles returns the declared agents entries that are single definition
// files inside the pack. Claude Code manifests may name .md files instead of
// directories.
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
// ordinary discovery, so a pack skill sharing a name with an ordinary skill
// or an earlier pack's skill loses the usual first-wins dedup. Emission waits
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
// skill containers. The loop mirrors emitSkillsFromContainer but stamps
// SourcePath with the manifest path so the coderd fork-plugin gate can
// identify pack contributions.
func (r *Resolver) emitPackSkills(pack packRoot, root ScanRoot, out *[]Resource, seenID map[string]int) {
	for _, container := range pack.skillContainers() {
		entries, err := os.ReadDir(container)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			meta := filepath.Join(container, e.Name(), skillMetaFileName)
			// Lstat (not Stat) so a symlinked SKILL.md is
			// detected and routed through resolveReadTarget,
			// which enforces the pack-root boundary: a pack
			// must not import skills from outside itself even
			// when the target stays inside the scan root.
			info, err := os.Lstat(meta)
			if err != nil {
				continue
			}
			res, ok := r.readSkillMeta(pack.Dir, meta, info, root.UserSource)
			if !ok {
				continue
			}
			res.SourcePath = pack.ManifestPath
			appendResource(out, seenID, res)
		}
	}
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
	if !ok || hasSource(*out, res.Source) || (res.Status == StatusOK && hasOKSubagent(*out, res.Name)) {
		return
	}
	res.SourcePath = pack.ManifestPath
	appendResource(out, seenID, res)
}

// addPackWatchDirs adds every pack root and its declared skill and agent
// containers under rootPath to out, plus one level inside skill containers,
// so creating a pack or editing its files re-resolves the snapshot. Agent
// containers also go to agentDirs, so overwriting a definition in a
// declared container with any name re-resolves. The watcher calls this
// next to addAgentWatchDirs.
//
// A manifest may declare a container that does not exist yet
// ("skills": "components/skills" while only components/ is present).
// fsnotify is non-recursive, so with only the pack root watched, creating
// the container later fires no event and the pack stays undiscovered until
// an unrelated resync. Watching the deepest existing ancestor of each
// declared path arms that event, and the watcher re-collects after every
// re-resolve, so the watch walks down as the tree is built out.
func addPackWatchDirs(rootPath string, out, agentDirs map[string]struct{}) {
	for _, pack := range packRootsFor(rootPath) {
		out[pack.Dir] = struct{}{}
		// Nested manifests (.claude-plugin/plugin.json) live in their
		// own directory: fsnotify reports edits there, and watching
		// only the pack root never sees them, so declared container
		// changes would stay stale until an unrelated event. The
		// manifest exists, so its parent does too.
		if parent := filepath.Dir(pack.ManifestPath); parent != pack.Dir {
			out[parent] = struct{}{}
		}
		for _, container := range pack.skillContainers() {
			out[container] = struct{}{}
			addPackImmediateSubdirs(container, out)
		}
		for _, container := range pack.agentContainers() {
			out[container] = struct{}{}
			agentDirs[container] = struct{}{}
		}
		for _, file := range pack.agentFiles() {
			dir := filepath.Dir(file)
			out[dir] = struct{}{}
			agentDirs[dir] = struct{}{}
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
		// Watching every level down to the version directories
		// fires the events for a new install, a new version, and
		// an orphan marker, which all change the chosen pack.
		for _, marketplace := range childDirs(rootPath) {
			out[marketplace] = struct{}{}
			for _, plugin := range childDirs(marketplace) {
				out[plugin] = struct{}{}
				for _, version := range claudeCacheLiveVersions(plugin) {
					out[version] = struct{}{}
					dirs = append(dirs, packManifestCandidateDirs(version)...)
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
	return append(declaredRels(p.Manifest.Skills, defaultPackSkillsDir), declaredRels(p.Manifest.Agents, defaultPackAgentsDir)...)
}

// addPackImmediateSubdirs adds the immediate subdirectories of dir to out,
// the way the watcher watches one level inside skill containers.
func addPackImmediateSubdirs(dir string, out map[string]struct{}) {
	for _, child := range childDirs(dir) {
		out[child] = struct{}{}
	}
}
