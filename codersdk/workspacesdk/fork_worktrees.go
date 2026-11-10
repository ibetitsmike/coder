package workspacesdk

// This file is fork code: it adds the client for the agent worktree API that
// chatd uses to isolate subagents, which upstream does not support.

import (
	"context"
	"net/http"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/tracing"
)

// ForkCreateWorktreeRequest asks the agent to create a git worktree.
type ForkCreateWorktreeRequest struct {
	// RepoPath is an absolute path inside a git work tree.
	RepoPath string `json:"repo_path"`
	// Name is the worktree directory name, a single path segment.
	Name string `json:"name"`
	// Branch is the new branch to create.
	Branch string `json:"branch"`
	// BaseRef is the commit the branch starts at. Empty means HEAD of
	// RepoPath.
	BaseRef string `json:"base_ref,omitempty"`
}

// ForkWorktree describes a created worktree.
type ForkWorktree struct {
	// Path is the absolute worktree path.
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// BaseCommit is the full SHA the branch starts at.
	BaseCommit string `json:"base_commit"`
	// RepoRoot is the toplevel of the request's RepoPath.
	RepoRoot string `json:"repo_root"`
}

// ForkDeleteWorktreeRequest asks the agent to remove a worktree it created.
type ForkDeleteWorktreeRequest struct {
	Path string `json:"path"`
	// Branch is the branch create returned for this worktree. The agent
	// deletes only this branch.
	Branch string `json:"branch"`
}

// ForkWorktreeConn is implemented by the AgentConn that DialAgent returns.
// Callers type-assert an AgentConn to it.
type ForkWorktreeConn interface {
	ForkCreateWorktree(ctx context.Context, req ForkCreateWorktreeRequest) (ForkWorktree, error)
	ForkDeleteWorktree(ctx context.Context, req ForkDeleteWorktreeRequest) error
}

var _ ForkWorktreeConn = (*agentConn)(nil)

// ForkCreateWorktree creates a git worktree on the agent. Agents without
// the worktree API answer with a *codersdk.Error carrying status 404.
func (c *agentConn) ForkCreateWorktree(ctx context.Context, req ForkCreateWorktreeRequest) (ForkWorktree, error) {
	ctx, span := tracing.StartSpan(ctx)
	defer span.End()

	res, err := c.apiRequest(ctx, http.MethodPost, "/api/v0/git/worktrees", req)
	if err != nil {
		return ForkWorktree{}, xerrors.Errorf("do request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return ForkWorktree{}, readAgentError(res)
	}
	var wt ForkWorktree
	if err := decodeAgentJSON(res, &wt); err != nil {
		return ForkWorktree{}, xerrors.Errorf("decode response body: %w", err)
	}
	return wt, nil
}

// ForkDeleteWorktree removes a worktree the agent created. The agent
// refuses worktrees with uncommitted changes or unpushed commits.
func (c *agentConn) ForkDeleteWorktree(ctx context.Context, req ForkDeleteWorktreeRequest) error {
	ctx, span := tracing.StartSpan(ctx)
	defer span.End()

	res, err := c.apiRequest(ctx, http.MethodDelete, "/api/v0/git/worktrees", req)
	if err != nil {
		return xerrors.Errorf("do request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return readAgentError(res)
	}
	return nil
}

// ForkWorktreeSummaryRequest asks the agent to summarize the work on a
// worktree it created.
type ForkWorktreeSummaryRequest struct {
	Path string `json:"path"`
	// Branch is the branch create returned for this worktree. Commits on
	// any other branch or remote do not count as its work.
	Branch string `json:"branch"`
}

// ForkWorktreeSummary describes the work on a worktree's branch.
type ForkWorktreeSummary struct {
	// Commits counts the commits on HEAD that are on no other branch or
	// remote.
	Commits int `json:"commits"`
	// FilesChanged, Insertions and Deletions describe the diff from the
	// parent of the oldest such commit, or HEAD without one, to the working
	// tree. Untracked files are not part of it.
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
	// UncommittedChanges counts the changed and untracked paths that git
	// status reports.
	UncommittedChanges int `json:"uncommitted_changes"`
}

// ForkWorktreeSummaryConn is implemented by the AgentConn that DialAgent
// returns. Callers type-assert an AgentConn to it.
type ForkWorktreeSummaryConn interface {
	ForkWorktreeSummary(ctx context.Context, req ForkWorktreeSummaryRequest) (ForkWorktreeSummary, error)
}

var _ ForkWorktreeSummaryConn = (*agentConn)(nil)

// ForkWorktreeSummary summarizes the work on a worktree the agent created.
// Agents without the summary API answer with a *codersdk.Error carrying
// status 404.
func (c *agentConn) ForkWorktreeSummary(ctx context.Context, req ForkWorktreeSummaryRequest) (ForkWorktreeSummary, error) {
	ctx, span := tracing.StartSpan(ctx)
	defer span.End()

	res, err := c.apiRequest(ctx, http.MethodPost, "/api/v0/git/worktrees/summary", req)
	if err != nil {
		return ForkWorktreeSummary{}, xerrors.Errorf("do request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ForkWorktreeSummary{}, readAgentError(res)
	}
	var summary ForkWorktreeSummary
	if err := decodeAgentJSON(res, &summary); err != nil {
		return ForkWorktreeSummary{}, xerrors.Errorf("decode response body: %w", err)
	}
	return summary, nil
}
