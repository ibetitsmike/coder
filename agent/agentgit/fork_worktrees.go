package agentgit

// This file is fork code: it adds the agent worktree API that chatd uses to
// give isolated subagents their own git worktree, which upstream does not
// support.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const maxWorktreeNameBytes = 128

// worktreeAPI creates and removes git worktrees under root, laid out as
// <root>/<repo>/<name>.
type worktreeAPI struct {
	logger slog.Logger
	execer agentexec.Execer
	gitBin string
	root   string
	// rootErr is set when root could not be determined; every request
	// then fails with it.
	rootErr error
}

func newWorktreeAPI(logger slog.Logger, opts ...Option) *worktreeAPI {
	h := NewHandler(logger, opts...)
	api := &worktreeAPI{
		logger: logger,
		execer: h.execer,
		gitBin: h.gitBin,
	}
	home, err := os.UserHomeDir()
	if err != nil {
		api.rootErr = xerrors.Errorf("resolve home directory: %w", err)
		return api
	}
	api.root = filepath.Join(home, ".coder", "worktrees")
	return api
}

func (a *worktreeAPI) routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/", a.handleCreate)
	r.Delete("/", a.handleDelete)
	return r
}

// gitError carries git's trimmed stderr so responses can explain failures.
type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

func (a *worktreeAPI) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := a.execer.CommandContext(ctx, a.gitBin, append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &gitError{args: args, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return strings.TrimSpace(string(out)), nil
}

func writeWorktreeError(ctx context.Context, rw http.ResponseWriter, status int, message string, err error) {
	resp := codersdk.Response{Message: message}
	if err != nil {
		resp.Detail = err.Error()
	}
	httpapi.Write(ctx, rw, status, resp)
}

// validWorktreeName reports whether name is a single safe path segment.
func validWorktreeName(name string) bool {
	return name != "" &&
		name != "." &&
		name != ".." &&
		len(name) <= maxWorktreeNameBytes &&
		!strings.HasPrefix(name, "-") &&
		!strings.ContainsAny(name, "/\\\x00")
}

func (a *worktreeAPI) handleCreate(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.rootErr != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Worktree root is unavailable.", a.rootErr)
		return
	}

	var req workspacesdk.ForkCreateWorktreeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, "Invalid request body.", err)
		return
	}
	if !filepath.IsAbs(req.RepoPath) {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("repo_path %q must be an absolute path.", req.RepoPath), nil)
		return
	}
	if !validWorktreeName(req.Name) {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("name %q must be a single path segment of at most %d bytes that does not start with \"-\".", req.Name, maxWorktreeNameBytes), nil)
		return
	}
	if req.Branch == "" || strings.HasPrefix(req.Branch, "-") {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("branch %q must be non-empty and must not start with \"-\".", req.Branch), nil)
		return
	}
	baseRef := req.BaseRef
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if strings.HasPrefix(baseRef, "-") {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("base_ref %q must not start with \"-\".", baseRef), nil)
		return
	}

	top, err := a.git(ctx, req.RepoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("repo_path %q is not inside a git work tree.", req.RepoPath), err)
		return
	}
	if resolved, evalErr := filepath.EvalSymlinks(filepath.FromSlash(top)); evalErr == nil {
		top = resolved
	}
	repoName := filepath.Base(top)
	if !validWorktreeName(repoName) {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("Repository %q has no usable directory name.", top), nil)
		return
	}

	// check-ref-format --branch expands @{-N}, so anything it rewrites is
	// not a literal branch name.
	normalized, err := a.git(ctx, top, "check-ref-format", "--branch", req.Branch)
	if err != nil || normalized != req.Branch {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("branch %q is not a valid branch name.", req.Branch), err)
		return
	}
	if _, err := a.git(ctx, top, "show-ref", "--verify", "--quiet", "refs/heads/"+req.Branch); err == nil {
		writeWorktreeError(ctx, rw, http.StatusConflict, fmt.Sprintf("branch %q already exists.", req.Branch), nil)
		return
	}
	// The leading "-" check above stands in for --end-of-options, which
	// rev-parse only accepts since git 2.30.
	baseCommit, err := a.git(ctx, top, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("base_ref %q does not resolve to a commit.", baseRef), err)
		return
	}

	if err := os.MkdirAll(a.root, 0o755); err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to create the worktree root.", err)
		return
	}
	root, err := filepath.EvalSymlinks(a.root)
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to resolve the worktree root.", err)
		return
	}
	parent := filepath.Join(root, repoName)
	path := filepath.Join(parent, req.Name)
	if _, err := os.Lstat(path); err == nil {
		writeWorktreeError(ctx, rw, http.StatusConflict, fmt.Sprintf("worktree path %s already exists.", path), nil)
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to inspect the worktree path.", err)
		return
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to create the worktree parent directory.", err)
		return
	}
	if resolved, err := filepath.EvalSymlinks(parent); err != nil || resolved != parent {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("worktree parent %s resolves outside the worktree root %s.", parent, root), err)
		return
	}

	if _, err := a.git(ctx, top, "worktree", "add", "-b", req.Branch, "--", path, baseCommit); err != nil {
		if _, statErr := os.Lstat(path); statErr == nil {
			a.discardWorktree(ctx, top, path, req.Branch)
		}
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to create the worktree.", err)
		return
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		a.discardWorktree(ctx, top, path, req.Branch)
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("worktree %s resolves outside the worktree root %s.", path, root), err)
		return
	}

	httpapi.Write(ctx, rw, http.StatusCreated, workspacesdk.ForkWorktree{
		Path:       path,
		Branch:     req.Branch,
		BaseCommit: baseCommit,
		RepoRoot:   top,
	})
}

// discardWorktree undoes a worktree add whose result failed validation.
func (a *worktreeAPI) discardWorktree(ctx context.Context, top, path, branch string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if _, err := a.git(cleanupCtx, top, "worktree", "remove", "--force", "--", path); err != nil {
		a.logger.Warn(ctx, "failed to discard worktree", slog.F("path", path), slog.Error(err))
	}
	if _, err := a.git(cleanupCtx, top, "branch", "-D", "--", branch); err != nil {
		a.logger.Warn(ctx, "failed to discard worktree branch", slog.F("branch", branch), slog.Error(err))
	}
}

// insideRoot reports whether path is exactly <root>/<repo>/<name>.
func insideRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 2 && parts[0] != ".."
}

func (a *worktreeAPI) handleDelete(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.rootErr != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Worktree root is unavailable.", a.rootErr)
		return
	}

	var req workspacesdk.ForkDeleteWorktreeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, "Invalid request body.", err)
		return
	}
	if !filepath.IsAbs(req.Path) {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("path %q must be an absolute path.", req.Path), nil)
		return
	}
	path, err := filepath.EvalSymlinks(req.Path)
	if errors.Is(err, fs.ErrNotExist) {
		writeWorktreeError(ctx, rw, http.StatusNotFound, fmt.Sprintf("worktree %s does not exist.", req.Path), nil)
		return
	}
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("Failed to resolve path %s.", req.Path), err)
		return
	}
	root, err := filepath.EvalSymlinks(a.root)
	if err != nil || !insideRoot(root, path) {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("path %s is not a worktree directory under %s.", req.Path, a.root), nil)
		return
	}
	if req.Branch == "" || strings.HasPrefix(req.Branch, "-") {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("branch %q must be non-empty and must not start with \"-\".", req.Branch), nil)
		return
	}

	top, err := a.git(ctx, path, "rev-parse", "--show-toplevel")
	if err == nil {
		top, err = filepath.EvalSymlinks(filepath.FromSlash(top))
	}
	if err != nil || top != path {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("path %s is not the top of a git worktree.", path), err)
		return
	}
	dirs, err := a.git(ctx, path, "rev-parse", "--git-dir", "--git-common-dir")
	gitDir, commonDir, ok := strings.Cut(dirs, "\n")
	if err != nil || !ok {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to read the worktree git directories.", err)
		return
	}
	gitDir = absGitPath(path, gitDir)
	commonDir = absGitPath(path, commonDir)
	if gitDir == commonDir {
		writeWorktreeError(ctx, rw, http.StatusBadRequest, fmt.Sprintf("path %s is a main working tree, not a linked worktree.", path), nil)
		return
	}

	status, err := a.git(ctx, path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to read the worktree status.", err)
		return
	}
	if changes := countLines(status); changes > 0 {
		writeWorktreeError(ctx, rw, http.StatusConflict, fmt.Sprintf("worktree %s has %d uncommitted or untracked changes; commit or discard them first", path, changes), nil)
		return
	}

	// Branches cannot contain glob characters, so the branch name is safe
	// to use in --exclude.
	ref, err := a.git(ctx, path, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || ref != "refs/heads/"+req.Branch {
		current := "a detached HEAD"
		if err == nil {
			current = strings.TrimPrefix(ref, "refs/heads/")
		}
		writeWorktreeError(ctx, rw, http.StatusConflict, fmt.Sprintf("worktree %s is on %s, not branch %s; switch back to it before removing the worktree", path, current, req.Branch), nil)
		return
	}
	oid, err := a.git(ctx, path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to read the worktree HEAD commit.", err)
		return
	}
	revList := []string{"rev-list", "--count", oid, "--not", "--exclude=refs/heads/" + req.Branch, "--glob=refs/heads/*", "--glob=refs/remotes/*", "--"}
	count, err := a.git(ctx, path, revList...)
	if err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to count unpushed commits.", err)
		return
	}
	if count != "0" {
		writeWorktreeError(ctx, rw, http.StatusConflict, fmt.Sprintf("branch %s has %s commits that are not on any other branch or remote; push or merge them first", req.Branch, count), nil)
		return
	}

	args := []string{"worktree", "remove"}
	// git refuses to remove a worktree with initialized submodules without
	// --force; the status check above already proved the tree clean.
	if _, err := os.Stat(filepath.Join(gitDir, "modules")); err == nil {
		args = append(args, "--force")
	}
	args = append(args, "--", path)
	// Running from the common git dir works without knowing where the main
	// working tree lives.
	if _, err := a.git(ctx, commonDir, args...); err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, "Failed to remove the worktree.", err)
		return
	}
	// Safe: the unpushed check covered this exact tip, and update-ref only
	// deletes the branch if its tip is unchanged.
	if _, err := a.git(ctx, commonDir, "update-ref", "-d", "refs/heads/"+req.Branch, oid); err != nil {
		writeWorktreeError(ctx, rw, http.StatusInternalServerError, fmt.Sprintf("Removed worktree %s but failed to delete branch %s.", path, req.Branch), err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func absGitPath(dir, p string) string {
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
