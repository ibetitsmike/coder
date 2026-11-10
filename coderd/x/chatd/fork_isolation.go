package chatd

// This file is fork code: it adds the isolation option of the
// fork-subagent-isolation experiment to spawn_agent. appendForkTools wraps the
// upstream tool and createSubagentChatWithID creates the child's git
// worktree, so fork features never edit the spawn_agent tool itself.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/schema"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	subagentIsolationShared   = "shared"
	subagentIsolationWorktree = "worktree"

	maxWorktreeBranchSlugBytes = 40
	worktreeCleanupTimeout     = 30 * time.Second
)

type forkSpawnIsolationArgs struct {
	Isolation  string `json:"isolation,omitempty" enum:"shared,worktree" description:"Where the child works: shared (default) uses this chat's workspace and checkout; worktree gives the child its own git worktree on a new branch in this chat's workspace, so parallel children can edit the same files without conflicts."`
	RepoPath   string `json:"repo_path,omitempty" description:"With isolation worktree: absolute path of the git repository to branch from. Defaults to the workspace agent's directory."`
	BaseBranch string `json:"base_branch,omitempty" description:"With isolation worktree: branch or commit the child's branch starts from. Defaults to the repository's current HEAD."`
}

type subagentIsolationKey struct{}

// subagentIsolationRequest is the isolation a spawn_agent call asks for. The
// tool passes it to createSubagentChatWithID through the context.
type subagentIsolationRequest struct {
	isolation  string
	repoPath   string
	baseBranch string
}

func subagentIsolationFromContext(ctx context.Context) (subagentIsolationRequest, bool) {
	req, ok := ctx.Value(subagentIsolationKey{}).(subagentIsolationRequest)
	return req, ok
}

// forkSpawnAgentTool is spawn_agent with the isolation option. It passes
// every call to the upstream tool, which ignores the extra fields.
type forkSpawnAgentTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkSpawnIsolation replaces the spawn_agent tool in tools, if there is
// one, with forkSpawnAgentTool.
func (p *Server) withForkSpawnIsolation(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	for i, tool := range tools {
		if tool.Info().Name == spawnAgentToolName {
			tools[i] = forkSpawnAgentTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t forkSpawnAgentTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description += " Set isolation to worktree to give the child its own git worktree and branch in this chat's workspace."
	info.Parameters = maps.Clone(info.Parameters)
	maps.Copy(info.Parameters, schema.ToParameters(schema.Generate(reflect.TypeFor[forkSpawnIsolationArgs]())))
	return info
}

func (t forkSpawnAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args forkSpawnIsolationArgs
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid parameters: %s", err)), nil
	}
	req := subagentIsolationRequest{
		isolation:  strings.TrimSpace(args.Isolation),
		repoPath:   strings.TrimSpace(args.RepoPath),
		baseBranch: strings.TrimSpace(args.BaseBranch),
	}
	switch req.isolation {
	case "", subagentIsolationShared:
		if req.repoPath != "" || req.baseBranch != "" {
			return fantasy.NewTextErrorResponse("repo_path and base_branch need isolation worktree"), nil
		}
		return t.AgentTool.Run(ctx, call)
	case subagentIsolationWorktree:
	default:
		return fantasy.NewTextErrorResponse(fmt.Sprintf("isolation must be one of: %s, %s",
			subagentIsolationShared, subagentIsolationWorktree)), nil
	}
	resp, err := t.AgentTool.Run(context.WithValue(ctx, subagentIsolationKey{}, req), call)
	if err != nil || resp.IsError {
		return resp, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return resp, nil
	}
	rawChatID, _ := result["chat_id"].(string)
	childID, err := parseSubagentToolChatID(rawChatID)
	if err != nil {
		return resp, nil
	}
	row, err := t.server.subagentRow(ctx, childID)
	if err != nil {
		t.server.logger.Warn(ctx, "read isolation of spawned subagent",
			slog.F("chat_id", t.chat.ID), slog.F("child_chat_id", childID), slog.Error(err))
		return resp, nil
	}
	result["isolation"] = row.Isolation
	result["worktree_path"] = row.WorktreePath
	result["branch"] = row.Branch
	content, err := json.Marshal(result)
	if err != nil {
		return resp, nil
	}
	resp.Content = string(content)
	return resp, nil
}

func (p *Server) subagentRow(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return forkdb.ChatSubagent{}, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, chatID)
	if err != nil {
		return forkdb.ChatSubagent{}, xerrors.Errorf("get chat subagent %s: %w", chatID, err)
	}
	return row, nil
}

// createSubagentWorktree creates the git worktree of the child chatID of
// parent through the parent's workspace agent. done releases the agent
// connection and, unless keep is true, removes the worktree again.
func (p *Server) createSubagentWorktree(
	ctx context.Context,
	chatID uuid.UUID,
	parent database.Chat,
	input chatstate.CreateChatInput,
	req subagentIsolationRequest,
) (workspacesdk.ForkWorktree, func(keep bool), error) {
	if input.Mode.Valid && input.Mode.ChatMode == database.ChatModeComputerUse {
		return workspacesdk.ForkWorktree{}, nil, xerrors.New("isolation worktree is not supported for computer use agents")
	}
	if !parent.AgentID.Valid {
		return workspacesdk.ForkWorktree{}, nil, xerrors.New("isolation worktree needs a workspace; this chat has none")
	}
	if p.agentConnFn == nil {
		return workspacesdk.ForkWorktree{}, nil, xerrors.New("isolation worktree needs workspace agent connections, which are not configured")
	}
	repoPath := req.repoPath
	if repoPath == "" {
		agent, err := p.db.GetWorkspaceAgentByID(ctx, parent.AgentID.UUID)
		if err != nil {
			return workspacesdk.ForkWorktree{}, nil, xerrors.Errorf("get workspace agent: %w", err)
		}
		repoPath = agent.ExpandedDirectory
		if repoPath == "" {
			repoPath = agent.Directory
		}
		if repoPath == "" {
			return workspacesdk.ForkWorktree{}, nil, xerrors.New("the workspace agent has no directory; set repo_path to the git repository to branch from")
		}
	}
	conn, release, err := p.agentConnFn(ctx, parent.AgentID.UUID)
	if err != nil {
		return workspacesdk.ForkWorktree{}, nil, xerrors.Errorf("connect to workspace agent: %w", err)
	}
	worktrees, ok := conn.(workspacesdk.ForkWorktreeConn)
	if !ok {
		release()
		return workspacesdk.ForkWorktree{}, nil, xerrors.New("the workspace agent connection does not support worktree isolation")
	}
	worktree, err := worktrees.ForkCreateWorktree(ctx, workspacesdk.ForkCreateWorktreeRequest{
		RepoPath: repoPath,
		Name:     chatID.String(),
		Branch:   forkWorktreeBranch(input.Title, chatID),
		BaseRef:  req.baseBranch,
	})
	if err != nil {
		release()
		return workspacesdk.ForkWorktree{}, nil, worktreeCreateError(err)
	}
	p.logger.Info(ctx, "created subagent worktree",
		slog.F("chat_id", chatID), slog.F("path", worktree.Path), slog.F("branch", worktree.Branch))
	done := func(keep bool) {
		defer release()
		if keep {
			return
		}
		// The spawn may have failed because its context was canceled, and
		// the worktree must still go.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worktreeCleanupTimeout)
		defer cancel()
		if err := worktrees.ForkDeleteWorktree(cleanupCtx, workspacesdk.ForkDeleteWorktreeRequest{Path: worktree.Path, Branch: worktree.Branch}); err != nil {
			p.logger.Warn(ctx, "remove worktree of subagent that failed to spawn",
				slog.F("chat_id", chatID), slog.F("path", worktree.Path), slog.Error(err))
		}
	}
	return worktree, done, nil
}

func worktreeCreateError(err error) error {
	var sdkErr *codersdk.Error
	if !errors.As(err, &sdkErr) {
		return xerrors.Errorf("create worktree: %w", err)
	}
	if sdkErr.StatusCode() == http.StatusNotFound {
		return xerrors.New("this workspace's agent does not support worktree isolation (it is older than the Coder server); " +
			"restart the workspace to update its agent, or spawn with isolation shared")
	}
	message := strings.TrimSuffix(sdkErr.Message, ".")
	if sdkErr.Detail != "" {
		return xerrors.Errorf("create worktree: %s: %s", message, sdkErr.Detail)
	}
	return xerrors.Errorf("create worktree: %s", message)
}

// forkWorktreeBranch returns the branch of the worktree of child chatID:
// agent/<slug of title>-<first 8 hex digits of chatID>.
func forkWorktreeBranch(title string, chatID uuid.UUID) string {
	var slug strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			dash = false
			slug.WriteRune(r)
			continue
		}
		dash = true
	}
	s := slug.String()
	if len(s) > maxWorktreeBranchSlugBytes {
		s = strings.TrimRight(s[:maxWorktreeBranchSlugBytes], "-")
	}
	suffix := chatID.String()[:8]
	if s == "" {
		return "agent/" + suffix
	}
	return "agent/" + s + "-" + suffix
}
