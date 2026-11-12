package chatd

// This file is fork code: it attaches Cursor glob rules, instruction files
// whose body carries globs, only to turns whose history touched a file that
// matches one of the globs.

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
)

// instructionGlobs returns the globs of an instruction-file body. A body
// that does not decode yields nil so the caller's malformed path counts it.
func instructionGlobs(body json.RawMessage) []string {
	decoded, ok := decodeInstructionFileBody(body)
	if !ok {
		return nil
	}
	return decoded.GetGlobs()
}

// touchedFilePaths returns the distinct paths that file tool calls in rows
// read, wrote, or edited.
func touchedFilePaths(rows []database.ChatMessage) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, row := range rows {
		parts, err := chatprompt.ParseContent(row)
		if err != nil {
			continue
		}
		for _, part := range parts {
			if part.Type != codersdk.ChatMessagePartTypeToolCall {
				continue
			}
			for _, p := range toolCallPaths(part.ToolName, part.Args) {
				if _, ok := seen[p]; p == "" || ok {
					continue
				}
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	return out
}

func toolCallPaths(toolName string, args json.RawMessage) []string {
	switch toolName {
	case "read_file", chattool.WriteFileToolName:
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &a) == nil {
			return []string{a.Path}
		}
	case chattool.EditFilesToolName:
		var a struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &a) == nil {
			out := make([]string, 0, len(a.Files))
			for _, f := range a.Files {
				out = append(out, f.Path)
			}
			return out
		}
	}
	return nil
}

// scopedRuleParts returns a context-file part for every glob rule that
// matches a touched path. Globs are relative to the rule's project root, so
// relative touched paths are first resolved against directory.
func scopedRuleParts(rules []database.ChatContextResource, directory string, touched []string) []codersdk.ChatMessagePart {
	var parts []codersdk.ChatMessagePart
	for _, r := range rules {
		content, ok := decodeInstructionContent(r.Body)
		if !ok || content == "" || !ruleMatchesAny(r, directory, touched) {
			continue
		}
		parts = append(parts, codersdk.ChatMessagePart{
			Type:               codersdk.ChatMessagePartTypeContextFile,
			ContextFilePath:    r.Source,
			ContextFileContent: content,
		})
	}
	return parts
}

func ruleMatchesAny(r database.ChatContextResource, directory string, touched []string) bool {
	root := ruleProjectRoot(r.Source, directory)
	if root == "" {
		return false
	}
	globs := instructionGlobs(r.Body)
	for _, p := range touched {
		if !path.IsAbs(p) {
			if directory == "" {
				continue
			}
			p = path.Join(directory, p)
		}
		rel, ok := strings.CutPrefix(path.Clean(p), strings.TrimSuffix(root, "/")+"/")
		if !ok {
			continue
		}
		for _, glob := range globs {
			if ruleGlobMatch(glob, rel) {
				return true
			}
		}
	}
	return false
}

// ruleProjectRoot returns the directory that holds the rule's .cursor or
// .coder directory: the project for .cursor/rules, the home directory for
// ~/.coder/rules and ~/.cursor/rules. Rules from other directories fall
// back to directory.
func ruleProjectRoot(source, directory string) string {
	elems := strings.Split(path.Clean(source), "/")
	for i := len(elems) - 2; i >= 1; i-- {
		if elems[i] == "rules" && (elems[i-1] == ".cursor" || elems[i-1] == ".coder") {
			if root := strings.Join(elems[:i-1], "/"); root != "" {
				return root
			}
			return "/"
		}
	}
	return directory
}

// ruleGlobMatch matches a Cursor glob verbatim against a root-relative path,
// so "*.ts" matches only files at the rule's project root and "**/*.ts"
// matches every depth.
func ruleGlobMatch(glob, rel string) bool {
	glob = strings.TrimPrefix(strings.TrimPrefix(glob, "./"), "/")
	ok, err := doublestar.Match(glob, rel)
	return err == nil && ok
}
