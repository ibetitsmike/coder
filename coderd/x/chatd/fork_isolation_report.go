package chatd

// This file is fork code: it adds the worktree of a child spawned with
// isolation worktree, and a summary of the work on its branch, to the
// wait_agent result. appendForkTools wraps the upstream tool, so fork features
// never edit the wait_agent tool itself.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// worktreeSummaryTimeout bounds the agent request behind one wait_agent
// report, so a slow git never holds the parent's turn for long.
const worktreeSummaryTimeout = 30 * time.Second

// forkIsolationWaitAgentTool is wait_agent with the worktree report.
type forkIsolationWaitAgentTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkIsolationReport replaces the wait_agent tool in tools, if there is
// one, with forkIsolationWaitAgentTool.
func (p *Server) withForkIsolationReport(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	for i, tool := range tools {
		if tool.Info().Name == waitAgentToolName {
			tools[i] = forkIsolationWaitAgentTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t forkIsolationWaitAgentTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description += " For a child spawned with isolation worktree, the result also names its branch and worktree" +
		" and, once the child finishes, summarizes its commits and changes in diff_summary."
	return info
}

func (t forkIsolationWaitAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := t.AgentTool.Run(ctx, call)
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
		if !errors.Is(err, sql.ErrNoRows) {
			t.server.logger.Warn(ctx, "read isolation of awaited subagent",
				slog.F("chat_id", t.chat.ID), slog.F("child_chat_id", childID), slog.Error(err))
		}
		return resp, nil
	}
	if row.Isolation != subagentIsolationWorktree {
		return resp, nil
	}
	result["isolation"] = row.Isolation
	result["branch"] = row.Branch
	result["worktree_path"] = row.WorktreePath
	// A child that is still running may still change its worktree, and the
	// summary request would outlast a short wait timeout.
	if timedOut, _ := result["timed_out"].(bool); !timedOut {
		summary, err := t.server.worktreeSummary(ctx, childID, workspacesdk.ForkWorktreeSummaryRequest{
			Path:   row.WorktreePath,
			Branch: row.Branch,
		})
		if err != nil {
			result["diff_summary_error"] = err.Error()
		} else {
			result["diff_summary"] = summary
		}
	}
	content, err := json.Marshal(result)
	if err != nil {
		return resp, nil
	}
	resp.Content = string(content)
	return resp, nil
}

// worktreeSummary asks the workspace agent of the child chatID to summarize
// the work on its worktree.
func (p *Server) worktreeSummary(ctx context.Context, chatID uuid.UUID, req workspacesdk.ForkWorktreeSummaryRequest) (workspacesdk.ForkWorktreeSummary, error) {
	chat, err := p.db.GetChatByID(ctx, chatID)
	if err != nil {
		return workspacesdk.ForkWorktreeSummary{}, xerrors.Errorf("get child chat: %w", err)
	}
	if !chat.AgentID.Valid || p.agentConnFn == nil {
		return workspacesdk.ForkWorktreeSummary{}, xerrors.New("the child has no workspace agent to read its worktree from")
	}
	ctx, cancel := context.WithTimeout(ctx, worktreeSummaryTimeout)
	defer cancel()
	// A dial to an offline agent waits until its context ends.
	dialCtx, cancelDial := context.WithTimeout(ctx, p.dialTimeout)
	defer cancelDial()
	conn, release, err := p.agentConnFn(dialCtx, chat.AgentID.UUID)
	if err != nil {
		return workspacesdk.ForkWorktreeSummary{}, xerrors.Errorf("connect to workspace agent: %w", err)
	}
	defer release()
	summaries, ok := conn.(workspacesdk.ForkWorktreeSummaryConn)
	if !ok {
		return workspacesdk.ForkWorktreeSummary{}, xerrors.New("the workspace agent connection does not support worktree summaries")
	}
	summary, err := summaries.ForkWorktreeSummary(ctx, req)
	if sdkErr, ok := errors.AsType[*codersdk.Error](err); ok {
		// The agent's message names the problem, such as a removed
		// worktree, without the HTTP framing of sdkErr.Error.
		message := strings.TrimSuffix(sdkErr.Message, ".")
		if sdkErr.Detail != "" {
			return workspacesdk.ForkWorktreeSummary{}, xerrors.Errorf("summarize worktree: %s: %s", message, sdkErr.Detail)
		}
		return workspacesdk.ForkWorktreeSummary{}, xerrors.Errorf("summarize worktree: %s", message)
	}
	if err != nil {
		return workspacesdk.ForkWorktreeSummary{}, xerrors.Errorf("summarize worktree: %w", err)
	}
	return summary, nil
}
