package agentgit

// This file is fork code: it summarizes the work on a subagent's worktree
// branch for wait_agent, which upstream does not support.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func (a *worktreeAPI) handleSummary(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.rootErr != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Worktree root is unavailable.", a.rootErr)
		return
	}

	var req workspacesdk.ForkWorktreeSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, "Invalid request body.", err)
		return
	}
	wt, ok := a.checkLinkedWorktree(ctx, rw, req.Path, req.Branch)
	if !ok {
		return
	}

	// Work on another branch is not this branch's to report.
	if current, ok := a.onBranch(ctx, wt.path, req.Branch); !ok {
		writeWorktreeError(ctx, rw, http.StatusConflict, fmt.Sprintf("worktree %s is on %s, not branch %s", wt.path, current, req.Branch), nil)
		return
	}
	status, err := a.git(ctx, wt.path, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to read the worktree status.", err)
		return
	}
	// Branches cannot contain glob characters, so the branch name is safe
	// to use in --exclude.
	unique, err := a.git(ctx, wt.path, "rev-list", "--reverse", "--topo-order", "HEAD",
		"--not", "--exclude=refs/heads/"+req.Branch, "--glob=refs/heads/*", "--glob=refs/remotes/*", "--")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to list the branch commits.", err)
		return
	}
	commits := countLines(unique)

	base := "HEAD"
	if commits > 0 {
		oldest, _, _ := strings.Cut(unique, "\n")
		base = oldest + "^"
		if _, err := a.git(ctx, wt.path, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
			// The oldest commit is a root commit, so the branch adds every
			// file of the empty tree.
			base, err = a.gitWithInput(ctx, wt.path, strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
			if err != nil {
				writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to resolve the empty tree.", err)
				return
			}
		}
	}
	// numstat stays parseable where shortstat is localized. Without a second
	// revision the diff runs against the working tree, so it includes
	// uncommitted edits to tracked files.
	numstat, err := a.git(ctx, wt.path, "diff", "--numstat", "--ignore-submodules=none", base, "--")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to diff the branch.", err)
		return
	}
	summary := workspacesdk.ForkWorktreeSummary{
		Commits:            commits,
		UncommittedChanges: countLines(status),
	}
	if numstat != "" {
		for _, line := range strings.Split(numstat, "\n") {
			summary.FilesChanged++
			fields := strings.SplitN(line, "\t", 3)
			if len(fields) < 2 {
				continue
			}
			// Binary files report "-" for both counts.
			if n, err := strconv.Atoi(fields[0]); err == nil {
				summary.Insertions += n
			}
			if n, err := strconv.Atoi(fields[1]); err == nil {
				summary.Deletions += n
			}
		}
	}
	httpapi.Write(ctx, rw, http.StatusOK, summary)
}
