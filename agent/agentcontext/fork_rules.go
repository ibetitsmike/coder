package agentcontext

// This file is fork code: it discovers Cursor-style .mdc rule files. Rules
// with alwaysApply join the instruction files; glob-scoped rules are parsed
// and kept on the snapshot until chatd can attach them by touched files.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/xerrors"
	"gopkg.in/yaml.v3"
)

const (
	ruleFileExt      = ".mdc"
	ruleContainerDir = "rules"
)

// ruleContainerRelPath is the project rule directory relative to a scan
// root. User rule directories (~/.coder/rules, ~/.cursor/rules) are built-in
// scan roots of their own, see defaultBuiltinRoots.
var ruleContainerRelPath = filepath.Join(".cursor", ruleContainerDir)

// errNoRuleFrontmatter marks a rule file without front-matter, which Cursor
// treats as a manually attached rule.
var errNoRuleFrontmatter = xerrors.New("rule has no front-matter")

// ruleFrontmatter is the parsed header of a rule file.
type ruleFrontmatter struct {
	Description string
	AlwaysApply bool
	Globs       []string
}

// parseRule splits a rule file into its front-matter and trimmed body.
func parseRule(content string) (ruleFrontmatter, string, error) {
	lines := strings.Split(strings.TrimPrefix(content, "\xef\xbb\xbf"), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return ruleFrontmatter{}, "", errNoRuleFrontmatter
	}
	closing := slices.IndexFunc(lines[1:], func(line string) bool {
		return strings.TrimSpace(line) == "---"
	}) + 1
	if closing == 0 {
		return ruleFrontmatter{}, "", xerrors.New("missing closing front-matter delimiter")
	}
	header := make([]string, 0, closing-1)
	for _, line := range lines[1:closing] {
		header = append(header, quoteBareGlobs(line))
	}
	var raw struct {
		Description string `yaml:"description"`
		Globs       any    `yaml:"globs"`
		AlwaysApply bool   `yaml:"alwaysApply"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(header, "\n")), &raw); err != nil {
		return ruleFrontmatter{}, "", xerrors.Errorf("parse front-matter: %w", err)
	}
	fm := ruleFrontmatter{
		Description: strings.TrimSpace(raw.Description),
		AlwaysApply: raw.AlwaysApply,
	}
	switch globs := raw.Globs.(type) {
	case nil:
	case string:
		fm.Globs = splitGlobs(globs)
	case []any:
		for _, glob := range globs {
			s, ok := glob.(string)
			if !ok {
				return ruleFrontmatter{}, "", xerrors.New("globs entries must be strings")
			}
			if s = strings.TrimSpace(s); s != "" {
				fm.Globs = append(fm.Globs, s)
			}
		}
	default:
		return ruleFrontmatter{}, "", xerrors.New("globs must be a string or a list of strings")
	}
	return fm, strings.TrimSpace(strings.Join(lines[closing+1:], "\n")), nil
}

// quoteBareGlobs quotes an unquoted inline globs value. Cursor writes
// headers such as "globs: **/*.ts", which YAML would read as an alias.
func quoteBareGlobs(line string) string {
	value, ok := strings.CutPrefix(line, "globs:")
	value = strings.TrimSpace(value)
	if !ok || value == "" || strings.ContainsAny(value[:1], `"'[`) {
		return line
	}
	return "globs: " + strconv.Quote(value)
}

// splitGlobs splits Cursor's comma-separated globs string.
func splitGlobs(s string) []string {
	var out []string
	for glob := range strings.SplitSeq(s, ",") {
		if glob = strings.TrimSpace(glob); glob != "" {
			out = append(out, glob)
		}
	}
	return out
}

// ruleContainersFor returns the existing rule directories of rootPath:
// rootPath itself when it is a "rules" directory, plus .cursor/rules under
// it. A .cursor/rules that resolves outside rootPath is skipped because rule
// bodies are shipped whole.
func ruleContainersFor(rootPath string) []string {
	var out []string
	if filepath.Base(rootPath) == ruleContainerDir {
		out = append(out, rootPath)
	}
	rootResolved := filepath.Clean(rootPath)
	if resolved, err := filepath.EvalSymlinks(rootResolved); err == nil {
		rootResolved = resolved
	}
	container := filepath.Join(rootPath, ruleContainerRelPath)
	resolved, err := filepath.EvalSymlinks(container)
	if err != nil || !pathHasPrefix(resolved, rootResolved) {
		return out
	}
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		out = append(out, container)
	}
	return out
}

// emitRules appends one resource per always-apply or glob-scoped rule in
// root's rule directories. Rules without either are ignored.
func (r *Resolver) emitRules(root ScanRoot, out *[]Resource, seenID map[string]int) {
	for _, container := range ruleContainersFor(root.Path) {
		entries, err := os.ReadDir(container)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ruleFileExt {
				continue
			}
			path := filepath.Join(container, e.Name())
			// Lstat so a symlinked rule goes through
			// resolveReadTarget's scan-root boundary check.
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			res, ok := r.readRule(root, path, info)
			if !ok || hasSource(*out, res.Source) {
				continue
			}
			appendResource(out, seenID, res)
		}
	}
}

// readRule reads one rule file as an instruction file whose payload is the
// body after the front-matter. ok is false for rules that apply neither
// always nor by glob.
func (r *Resolver) readRule(root ScanRoot, path string, info fs.FileInfo) (Resource, bool) {
	res := r.readFileResource(KindInstructionFile, root.Path, path, info, root.UserSource)
	content := res.Payload
	switch res.Status {
	case StatusOK:
	case StatusOversize:
		// Classify by the capped prefix so an oversize rule that would
		// be ignored anyway does not surface as an issue.
		data, err := readFileCapped(res.Source, safeInt64(r.MaxResourceBytes))
		if err != nil {
			return res, true
		}
		content = data
	default:
		return res, true
	}
	fm, body, err := parseRule(string(content))
	switch {
	case errors.Is(err, errNoRuleFrontmatter):
		return Resource{}, false
	case err != nil:
		if res.Status == StatusOK {
			res.Status = StatusInvalid
			res.Error = err.Error()
			res.Payload = nil
		}
		return res, true
	case !fm.AlwaysApply && len(fm.Globs) == 0:
		return Resource{}, false
	}
	if !fm.AlwaysApply {
		res.Globs = fm.Globs
	}
	if res.Status == StatusOK {
		res.Payload = []byte(body)
		res.Description = fm.Description
		if res.Description == "" {
			res.Description = firstLine(body)
		}
	}
	return res, true
}

// splitScopedRules moves glob-scoped rules out of resources. They are not
// pushed: chatd would otherwise inject them on every turn as instruction
// files.
func splitScopedRules(resources []Resource) (rest, scoped []Resource) {
	rest = resources[:0]
	for _, res := range resources {
		if len(res.Globs) > 0 {
			scoped = append(scoped, res)
			continue
		}
		rest = append(rest, res)
	}
	slices.SortFunc(scoped, func(a, b Resource) int {
		return strings.Compare(a.ID, b.ID)
	})
	return rest, scoped
}

// addRuleWatchDirs adds rootPath's rule directories to out, plus an
// existing .cursor so creating .cursor/rules later triggers a re-resolve.
func addRuleWatchDirs(rootPath string, out map[string]struct{}) {
	for _, container := range ruleContainersFor(rootPath) {
		out[container] = struct{}{}
	}
	parent := filepath.Dir(filepath.Join(rootPath, ruleContainerRelPath))
	if info, err := os.Stat(parent); err == nil && info.IsDir() {
		out[parent] = struct{}{}
	}
}

// isRulePath reports whether path could be a rule file, so the watcher
// re-resolves on writes to it.
func isRulePath(path string) bool {
	return filepath.Ext(path) == ruleFileExt && filepath.Base(filepath.Dir(path)) == ruleContainerDir
}
