# Workspace isolation

This page is for a Coder Agents user who runs pstack skills, and shows how to give a child its own workspace.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.
Its code cites are on [pstack adapter evidence](https://github.com/ibetitsmike/coder/blob/main/docs/fork/pstack-adapter-evidence.md).

With the `fork-subagent-isolation` experiment, `isolation: "workspace"` gives the child its own new workspace.
`spawn_agent` creates it as the chat owner, from the active version of the parent workspace's template, with the parameters of the parent workspace's latest build.
`spawn_agent` returns only after the new workspace's agent is ready and its startup scripts have finished, so the call can take as long as a workspace build.
Then the child gets its prompt, and its tools run in the new workspace.
The new workspace starts from the template, so it has none of the parent's files, uncommitted changes, or local branches.
Tell the child where to get the code, such as a branch to clone, and to push its work, because you can't read the child's files.
The `spawn_agent` result adds `isolation` and `workspace_name`, and `wait_agent` on the child adds the same fields without a `diff_summary`.
When Coder Agents can't read the workspace, the result has `workspace_error` instead of `workspace_name`.
While the experiment is on, the web UI shows **Workspace:** and the name on the `spawn_agent` and `wait_agent` cards of the child, or the `workspace_error` text.

`isolation: "workspace"` fails for a `computer_use` child with `isolation workspace is not supported for computer use agents`, and in a chat without a workspace with `isolation workspace copies this chat's workspace, and this chat has none`.
`repo_path` or `base_branch` with it fails with `create child chat: repo_path and base_branch need isolation worktree`.
When the build fails, or the agent or its startup scripts don't get ready, `spawn_agent` fails with `create the child's workspace: <result>`, where `<result>` is the `create_workspace` result.
It fails the same way before any build when the parent workspace's template doesn't allow Coder Agents or has an externally managed agent, so spawn the child with `isolation: "shared"` or `isolation: "worktree"` in such a workspace.
After a failed spawn, Coder Agents archives the child and tries once to stop the workspace, if it created one, but it doesn't retry a failed archive or stop and never deletes the workspace.
So after a failed spawn, check the **Workspaces** page and delete any workspace that the spawn created.
When the spawn succeeds, Coder Agents doesn't stop the child's workspace until you archive the root chat, the deployment archives it as inactive, or, with `fork-nested-subagents`, `archive_agent` archives the child or one of its ancestors.
The workspace can still stop earlier on its own schedule, such as the deployment's chat workspace TTL or the template's default autostop.
If the experiment is still on at the archive, Coder Agents then stops each archived child's workspace in the background, after it tries to remove the archived worktrees, but it never deletes the workspace.
A worktree that it keeps doesn't prevent the stop, even when the worktree is in that workspace.
It doesn't retry a failed stop, and it skips a child that is bound to another workspace by then.
Delete the workspace on the **Workspaces** page or with `coder delete <workspace_name>` when you no longer need it.
