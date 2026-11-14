package chatd

// This file is fork code: it binds a subagent spawned with isolation worktree,
// and every shared subagent below it, to that worktree. appendForkTools wraps
// the child's file and execute tools, and createSubagentChatWithID names the
// worktree in the child's prompt and its workspace context.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"charm.land/fantasy"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
)

// subagentWorktree is the git worktree a subagent works in.
type subagentWorktree struct {
	path   string
	branch string
}

// chatWorktree returns the worktree chat works in: its own when it was
// spawned with isolation worktree, else the nearest such ancestor's when every
// chat between them is shared. ok is false when chat works in the workspace
// agent's checkout.
func (p *Server) chatWorktree(ctx context.Context, chat database.Chat) (subagentWorktree, bool, error) {
	if !chat.ParentChatID.Valid {
		return subagentWorktree{}, false, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return subagentWorktree{}, false, xerrors.Errorf("fork queries: %w", err)
	}
	current := chat
	for range maxAncestorWalk {
		row, err := fdb.GetChatSubagentByChatID(ctx, current.ID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// A shared child spawned without nested subagents, a custom
			// agent or isolation has no row.
		case err != nil:
			return subagentWorktree{}, false, xerrors.Errorf("get chat subagent %s: %w", current.ID, err)
		case row.Isolation == subagentIsolationWorktree:
			return subagentWorktree{path: row.WorktreePath, branch: row.Branch}, true, nil
		case row.Isolation != subagentIsolationShared:
			return subagentWorktree{}, false, nil
		}
		if !current.ParentChatID.Valid {
			return subagentWorktree{}, false, nil
		}
		current, err = p.db.GetChatByID(ctx, current.ParentChatID.UUID)
		if err != nil {
			return subagentWorktree{}, false, xerrors.Errorf("get parent chat: %w", err)
		}
	}
	return subagentWorktree{}, false, nil
}

// forkWorktreeAgent returns agent with its directory set to the worktree chat
// works in, if any, so the turn's workspace context names the worktree as the
// working directory. The directory only decorates that context, so a failed
// lookup keeps agent as it is.
func (p *Server) forkWorktreeAgent(ctx context.Context, chat database.Chat, agent database.WorkspaceAgent) database.WorkspaceAgent {
	if !p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) {
		return agent
	}
	worktree, ok, err := p.chatWorktree(ctx, chat)
	if err != nil {
		p.logger.Warn(ctx, "resolve chat worktree for workspace context", slog.F("chat_id", chat.ID), slog.Error(err))
		return agent
	}
	if ok {
		agent.Directory = worktree.path
		agent.ExpandedDirectory = worktree.path
	}
	return agent
}

// withSubagentWorktreePrompt returns the child's initial messages with a
// system message naming its worktree before the child's prompt, which is the
// last message.
func withSubagentWorktreePrompt(input chatstate.CreateChatInput, worktree subagentWorktree, inherited bool) ([]chatstate.Message, error) {
	text := fmt.Sprintf("This chat works in its own git worktree at %s, on branch %s.", worktree.path, worktree.branch)
	if inherited {
		text = fmt.Sprintf("This chat shares its parent chat's git worktree at %s, on branch %s.", worktree.path, worktree.branch)
	}
	text += " Make every change in that worktree and commit it on that branch; do not edit other checkouts." +
		" Relative paths in file tools, and execute's working directory, resolve to the worktree."
	content, err := chatprompt.MarshalParts([]codersdk.ChatMessagePart{codersdk.ChatMessageText(text)})
	if err != nil {
		return nil, xerrors.Errorf("marshal worktree prompt: %w", err)
	}
	message := systemMessage(content, input.LastModelConfigID)
	last := max(len(input.InitialMessages)-1, 0)
	messages := make([]chatstate.Message, 0, len(input.InitialMessages)+1)
	messages = append(messages, input.InitialMessages[:last]...)
	messages = append(messages, message)
	return append(messages, input.InitialMessages[last:]...), nil
}

// withForkWorktreeBinding binds the file and execute tools in tools to the
// worktree chat works in, if any.
func (p *Server) withForkWorktreeBinding(ctx context.Context, tools []fantasy.AgentTool, chat database.Chat) ([]fantasy.AgentTool, error) {
	worktree, ok, err := p.chatWorktree(ctx, chat)
	if err != nil {
		return nil, xerrors.Errorf("resolve chat worktree: %w", err)
	}
	if !ok {
		return tools, nil
	}
	for i, tool := range tools {
		switch tool.Info().Name {
		case chattool.ExecuteToolName, "read_file", chattool.WriteFileToolName, chattool.EditFilesToolName, "attach_file":
			tools[i] = worktreeBoundTool{AgentTool: tool, dir: worktree.path}
		case viewImageToolName:
			// A caller-supplied view_image stays a *dynamicTool, which the
			// custom agent tool policy needs to recognize it.
			if _, dynamic := tool.(*dynamicTool); !dynamic {
				tools[i] = worktreeBoundTool{AgentTool: tool, dir: worktree.path}
			}
		}
	}
	return tools, nil
}

// worktreeBoundTool resolves the relative paths of a file or execute tool call
// against dir, and runs execute in dir when the call names no workdir.
type worktreeBoundTool struct {
	fantasy.AgentTool
	dir string
}

func (t worktreeBoundTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil || args == nil {
		// The tool reports the malformed input itself.
		return t.AgentTool.Run(ctx, call)
	}
	switch t.Info().Name {
	case chattool.ExecuteToolName:
		raw, ok := args["workdir"]
		if !ok {
			raw = json.RawMessage(`""`)
		}
		args["workdir"] = t.resolveRawPath(raw, true)
	case chattool.EditFilesToolName:
		var files []map[string]json.RawMessage
		if err := json.Unmarshal(args["files"], &files); err != nil {
			return t.AgentTool.Run(ctx, call)
		}
		for _, file := range files {
			if raw, ok := file["path"]; ok {
				file["path"] = t.resolveRawPath(raw, false)
			}
		}
		raw, err := json.Marshal(files)
		if err != nil {
			return t.AgentTool.Run(ctx, call)
		}
		args["files"] = raw
	default:
		if raw, ok := args["path"]; ok {
			args["path"] = t.resolveRawPath(raw, false)
		}
	}
	input, err := json.Marshal(args)
	if err != nil {
		return t.AgentTool.Run(ctx, call)
	}
	call.Input = string(input)
	return t.AgentTool.Run(ctx, call)
}

// resolveRawPath resolves raw, a JSON path value, against dir. An empty or
// null path becomes dir when emptyIsDir and stays as it is otherwise. A value
// that is not a string is returned unchanged for the tool to reject.
func (t worktreeBoundTool) resolveRawPath(raw json.RawMessage, emptyIsDir bool) json.RawMessage {
	var p string
	if err := json.Unmarshal(raw, &p); err != nil {
		return raw
	}
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		if !emptyIsDir {
			return raw
		}
		p = t.dir
	case isWindowsAbs(t.dir):
		// The agent returns native paths, so a Windows worktree needs
		// Windows path rules: path.IsAbs treats C:\x as relative.
		if !isWindowsAbs(p) && !strings.HasPrefix(p, "/") {
			p = t.dir + `\` + p
		}
	case !path.IsAbs(p):
		p = path.Join(t.dir, p)
	}
	resolved, err := json.Marshal(p)
	if err != nil {
		return raw
	}
	return resolved
}

// isWindowsAbs reports whether p is an absolute Windows path: a drive path
// such as C:\x or C:/x, or a UNC or rooted path starting with a backslash.
func isWindowsAbs(p string) bool {
	if strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return false
	}
	letter := p[0] | 0x20
	return 'a' <= letter && letter <= 'z'
}
