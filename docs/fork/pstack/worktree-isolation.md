# Worktree isolation

This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

When the deployment enables the `fork-subagent-isolation` experiment, `spawn_agent` also takes `isolation`, `repo_path`, and `base_branch`.
A custom type's definition can set the `isolation` of a spawn that sets none, as [Custom agent types](custom-agent-types.md) describes.
With `isolation: "shared"`, the default without such a definition, the child works in its parent's workspace and working directory.
With `isolation: "worktree"`, `spawn_agent` creates a git worktree for the child in its parent's workspace before the child starts.
The worktree comes from the git repository at `repo_path`, an absolute path.
When you omit `repo_path`, it comes from the worktree that the parent works in, if any, else from the workspace agent's directory.
Its new branch starts at `base_branch`, a branch or a commit, or at the repository's current `HEAD` when you omit `base_branch`.
The worktree is at `~/.coder/worktrees/<repository directory>/<child chat ID>`, on the branch `agent/<title>-<first 8 hex digits of the child chat ID>`, where `<title>` is a slug of the child's title.
A worktree that comes from another worktree is at `~/.coder/worktrees/<chat ID of that worktree's owner>/<child chat ID>`.
The `spawn_agent` result adds `isolation`, `worktree_path`, and `branch`.
`wait_agent` on a worktree child adds the same three fields.
Unless the wait times out, it also adds `diff_summary` with `commits`, `files_changed`, `insertions`, `deletions`, and `uncommitted_changes`.
`commits` counts the commits on the child's `HEAD` that are on no other branch or remote.
`files_changed`, `insertions`, and `deletions` cover the diff from the parent of the oldest of those commits to the working tree, so they include uncommitted edits to tracked files but not untracked files.
`uncommitted_changes` counts the changed and untracked paths that `git status` reports.
When Coder Agents can't read the worktree, for example because the child switched branches or the worktree is gone, the result has `diff_summary_error` instead.
Check `diff_summary` before you merge a child's branch, and tell the child to commit when `uncommitted_changes` isn't 0, because uncommitted work isn't on the branch.
While the experiment is on, the web UI shows **Branch:** and **Worktree:** lines on the `spawn_agent` and `wait_agent` cards of a worktree child.
It cuts a long path to fit and shows the full path when you hover over it.
The `wait_agent` card also shows `diff_summary` in a line such as **2 commits, 3 files changed, +40 -12, 1 uncommitted change**, or the `diff_summary_error` text.
The line leaves out the uncommitted changes when `uncommitted_changes` is 0.

The child works in its worktree.
A system message before its prompt names the worktree and the branch, and tells the child to make and commit every change there.
`execute` without a `workdir` runs in the worktree.
A relative `workdir` of `execute`, and a relative path of `read_file`, `write_file`, `edit_files`, `attach_file`, or `view_image`, resolves against the worktree.
A caller-supplied `view_image` is not bound, so a custom agent's tool policy still sees it as one.
An absolute path or an absolute `workdir` still reaches the rest of the workspace.
With `fork-nested-subagents`, a `shared` child of the worktree child works in the same worktree, and its system message says so.

`repo_path` or `base_branch` fails with `create child chat: repo_path and base_branch need isolation worktree` unless the call sets `isolation: "worktree"`, or sets no `isolation` and the custom type's definition sets `isolation: worktree`.
`isolation: "worktree"` fails for a `computer_use` child, in a chat without a workspace, and when you omit `repo_path`, the parent works in no worktree, and the workspace agent has no directory.
If the workspace agent is older than the server, `spawn_agent` fails with `this workspace's agent does not support worktree isolation`, so restart the workspace or spawn with `isolation: "shared"`.
If the spawn fails after `spawn_agent` creates the worktree, Coder Agents removes the worktree and its branch.
Otherwise Coder Agents keeps both until you archive the root chat, the deployment archives it as inactive, or, with `fork-nested-subagents`, `archive_agent` archives the child or one of its ancestors.
If the experiment is still on at the archive, Coder Agents then removes each archived child's worktree and branch, in the background after a root archive and before `archive_agent` returns.
It keeps a worktree that has uncommitted or untracked files, is no longer on its branch, or has submodule commits that are on no remote.
It also keeps a worktree when its branch, or the `HEAD` or branch reflog, reaches a commit that is on no other branch or remote, such as a commit that a reset moved the branch away from.
It keeps them all when it can't reach the workspace agent or the agent is older than the server.
After a root archive, it posts one notice in the root chat that lists the path, branch, and reason of each worktree it kept.
`archive_agent` lists them in `kept_worktrees` instead, each with `path`, `branch`, and `reason`, and a later archive of the root chat tries them again.
So merge or push each child's branch, push its submodule commits, and put any commit that a reset dropped on another branch before you archive the root chat or the child.
To remove a kept worktree yourself, run `git worktree remove <worktree_path>` and `git branch -D <branch>` in the repository.
Add `--force` to `git worktree remove` when the worktree still has uncommitted or untracked files.
Without the experiment, `spawn_agent` has no `isolation`, `repo_path`, or `base_branch` field.
