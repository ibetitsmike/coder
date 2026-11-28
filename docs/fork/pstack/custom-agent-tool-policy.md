# Custom agent tool policy

This page is for a Coder Agents user who runs pstack skills, and shows how to tell which tools a child of a custom agent type keeps.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

When Coder Agents spawns a custom type, it stores the definition's tool fields with the child chat.
On every turn of the child while the experiment is on, Coder Agents removes the tools that the stored fields don't allow.
A change to the definition doesn't change a child that already exists.
pstack's agents set none of these fields, so their children keep every tool of `general`.

- `readonly: true` keeps only the `explore` tools without the shell tools: `read_file`, `read_skill`, `read_skill_file`, and the deployment MCP tools allowed in subagents.
  It also keeps `view_image` when the `fork-view-image` experiment is on.
  It removes `execute` and `process_output`, because Coder Agents can't limit a shell command to reads.
  Of the provider tools, it keeps only `web_search`.
- `tools` lists the non-MCP tools that the child keeps, and an empty list keeps none.
  Claude Code names work: `Read`, `Write`, `Edit`, `MultiEdit`, `Bash`, `Grep`, `Glob`, `WebSearch`, and `Task`, which stands for the subagent tools.
  `Read` keeps `read_file` and `view_image`.
- `disallowed_tools`, or `disallowedTools`, removes each tool that it names, MCP tools included.
  A Claude Code ID, `mcp__<server>__<tool>`, matches the Coder Agents tool `<server>__<tool>`.
- `mcp: none` removes every MCP tool.
  A list of server names keeps only the MCP tools of those servers, and each name must match the server's name exactly, case included.
- `find_tools` follows `mcp`, and only `disallowed_tools` removes it by name.

A child without `spawn_agent`, `wait_agent`, or `list_agents` also loses the subagent orchestration guidance in its system prompt.
A child without `read_skill` loses the skill index in its system prompt.
Do not rely on the policy as a sandbox, because a child that keeps `execute` can run any command that the workspace allows, and an MCP tool can change state.
