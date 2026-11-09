package agentcontext

// This file is fork code: it discovers custom agent definition files for the
// fork's custom subagent types, which upstream does not support.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const agentDefinitionExt = ".md"

// maxAgentErrorBytes bounds a definition's error text, which can quote file
// content and grow with the number of warnings. coderd rejects a whole push
// when any resource error exceeds its 4096-byte cap, so one oversized error
// would stop every context resource from syncing.
const maxAgentErrorBytes = 1024

// agentContainerRelPaths are the directories, relative to a scan root, that
// hold agent definition markdown files. They cover the Claude Code and Cursor
// conventions; the plain "agents" entry makes ~/.coder/agents work.
var agentContainerRelPaths = []string{
	"agents",
	filepath.Join(".agents", "agents"),
	filepath.Join(".claude", "agents"),
	filepath.Join(".cursor", "agents"),
}

// agentContainersFor returns the existing agent-container directories under
// rootPath, plus rootPath itself when it is already an "agents" directory.
// A container that resolves outside rootPath is skipped: definitions are
// shipped whole, so a symlinked container must not pull in outside files.
func agentContainersFor(rootPath string) []string {
	var out []string
	if filepath.Base(rootPath) == "agents" {
		out = append(out, rootPath)
	}
	rootResolved := filepath.Clean(rootPath)
	if resolved, err := filepath.EvalSymlinks(rootResolved); err == nil {
		rootResolved = resolved
	}
	for _, rel := range agentContainerRelPaths {
		container := filepath.Join(rootPath, rel)
		resolved, err := filepath.EvalSymlinks(container)
		if err != nil || !pathHasPrefix(resolved, rootResolved) {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			out = append(out, container)
		}
	}
	return out
}

// emitAgents appends one KindSubagent resource per agent definition file in
// root's agent containers. The first valid definition with a given name wins,
// matching skills.
func (r *Resolver) emitAgents(root ScanRoot, out *[]Resource, seenID map[string]int) {
	for _, container := range agentContainersFor(root.Path) {
		entries, err := os.ReadDir(container)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != agentDefinitionExt {
				continue
			}
			path := filepath.Join(container, e.Name())
			// Lstat so a symlinked definition goes through
			// resolveReadTarget's scan-root boundary check.
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			res, ok := r.readAgentDefinition(root, path, info)
			if !ok || hasSource(*out, res.Source) || (res.Status == StatusOK && hasOKSubagent(*out, res.Name)) {
				continue
			}
			res.Error = truncateAgentError(res.Error)
			appendResource(out, seenID, res)
		}
	}
}

// readAgentDefinition reads and parses one agent definition file. ok is false
// for markdown without front-matter, which is not an agent definition.
func (r *Resolver) readAgentDefinition(root ScanRoot, path string, info fs.FileInfo) (Resource, bool) {
	readPath, readInfo, ok, status, errMsg := resolveReadTarget(path, info, root.Path)
	idPath := path
	if ok {
		idPath = readPath
	}
	res := Resource{
		ID:         resourceID(KindSubagent, idPath),
		Kind:       KindSubagent,
		Source:     idPath,
		SizeBytes:  safeUint64(info.Size()),
		SourcePath: root.UserSource,
	}
	if !ok {
		res.Status = status
		res.Error = errMsg
		return res, true
	}
	res.SizeBytes = safeUint64(readInfo.Size())
	if safeUint64(readInfo.Size()) > r.MaxResourceBytes {
		res.Status = StatusOversize
		res.Error = fmt.Sprintf("file size %d exceeds per-resource cap of %d bytes", readInfo.Size(), r.MaxResourceBytes)
		if data, err := readFileCapped(readPath, safeInt64(r.MaxResourceBytes)); err == nil {
			res.ContentHash = sha256.Sum256(data)
		}
		return res, true
	}
	data, err := os.ReadFile(readPath)
	if err != nil {
		res.Status = StatusUnreadable
		res.Error = err.Error()
		return res, true
	}
	res.ContentHash = sha256.Sum256(data)
	def, err := workspacesdk.ParseAgentDefinition(string(data), strings.TrimSuffix(filepath.Base(path), agentDefinitionExt))
	if errors.Is(err, workspacesdk.ErrNotAgentDefinition) {
		return Resource{}, false
	}
	if err != nil {
		res.Status = StatusInvalid
		res.Error = err.Error()
		return res, true
	}
	res.Name = def.Name
	res.Description = def.Description
	res.Payload = data
	res.Error = strings.Join(def.Warnings, "; ")
	return res, true
}

// truncateAgentError cuts s to at most maxAgentErrorBytes on a UTF-8 rune
// boundary.
func truncateAgentError(s string) string {
	if len(s) <= maxAgentErrorBytes {
		return s
	}
	const suffix = " (truncated)"
	cut := maxAgentErrorBytes - len(suffix)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// hasSource reports whether a resource already uses source. coderd rejects a
// push with duplicate sources, which an AGENTS.md in an "agents" scan root or
// a definition symlinked to an instruction file would otherwise produce.
func hasSource(resources []Resource, source string) bool {
	for _, existing := range resources {
		if existing.Source == source {
			return true
		}
	}
	return false
}

func hasOKSubagent(resources []Resource, name string) bool {
	for _, existing := range resources {
		if existing.Kind == KindSubagent && existing.Status == StatusOK && existing.Name == name {
			return true
		}
	}
	return false
}

// addAgentWatchDirs adds every agent container under rootPath to out.
// A missing container's existing parent (such as .claude) is watched too, so
// creating the container later triggers a re-resolve.
func addAgentWatchDirs(rootPath string, out map[string]struct{}) {
	for _, container := range agentContainersFor(rootPath) {
		out[container] = struct{}{}
	}
	for _, rel := range agentContainerRelPaths {
		parent := filepath.Dir(filepath.Join(rootPath, rel))
		if parent == rootPath {
			continue
		}
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			out[parent] = struct{}{}
		}
	}
}

// isAgentDefinitionPath reports whether path could be an agent definition
// file, so the watcher re-resolves on writes to it.
func isAgentDefinitionPath(path string) bool {
	return filepath.Ext(path) == agentDefinitionExt && filepath.Base(filepath.Dir(path)) == "agents"
}

func subagentBody(r Resource) *agentproto.ContextResource_Subagent {
	return &agentproto.ContextResource_Subagent{
		Subagent: &agentproto.SubagentBody{
			Definition:  append([]byte(nil), r.Payload...),
			Name:        r.Name,
			Description: r.Description,
		},
	}
}
