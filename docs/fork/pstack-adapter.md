# pstack on Coder Agents

This page maps pstack onto Coder Agents as this fork's `main` runs it.
It is for a Coder Agents user who wants to run pstack skills in a workspace's chats.
It tracks pstack v0.15.36 (`cursor/plugins` commit `c742dd61706f`, folder `pstack/`) and is modeled on pstack's Claude Code adapter, `docs/harness/claude-code.md`.

pstack skills use Cursor names.
In a Coder Agents chat, read this page once per chat, then map each Cursor name in a skill with the tables below.
`<pstack>` means the folder that holds pstack's `skills/`, `agents/` and `docs/`.
If a Cursor field has no row, drop it and say so.
Never pass a field or value that a tool schema rejects.

Each row names what Coder Agents offers today and what to do instead.
The last column names the pstack parity workstream (ibetitsmike/coder#4) whose merge removes the row.
"Stays" means no workstream removes it.
This page is fork-only: it isn't in `docs/manifest.json`.

## Setup

1. If `~/pstack-src` exists from an earlier setup, update it: `git -C ~/pstack-src fetch origin`.
   The fetch exits without an error.
2. Otherwise, clone pstack in the workspace: `git clone https://github.com/cursor/plugins ~/pstack-src`.
   The clone creates `~/pstack-src/pstack/`.
3. Pin it: `git -C ~/pstack-src checkout c742dd61706fbacc1b95f1a20b7b988cf096ce6c`.
   `git -C ~/pstack-src rev-parse HEAD` then prints that commit.
   `<pstack>` is `~/pstack-src/pstack`.
4. If `~/.coder/skills/` or `~/.coder/agents/` holds a name that steps 5 and 6 copy, move it out.
   Those steps replace a skill or agent that has the same name.
5. Copy the skills and the three `cursor-team-kit` skills that pstack calls: `mkdir -p ~/.coder/skills && cp -R ~/pstack-src/pstack/skills/. ~/pstack-src/cursor-team-kit/skills/deslop ~/pstack-src/cursor-team-kit/skills/control-ui ~/pstack-src/cursor-team-kit/skills/control-cli ~/.coder/skills/`.
   `ls ~/.coder/skills/*/SKILL.md | wc -l` then prints at least 61.
   The workspace agent reads `~/.coder/skills/<name>/SKILL.md`.
6. Copy pstack's agents: `mkdir -p ~/.coder/agents && cp ~/pstack-src/pstack/agents/*.md ~/.coder/agents/`.
   `ls ~/.coder/agents/*.md | wc -l` then prints at least 2.
   With the `fork-agent-definitions` experiment, chats also get them as [custom agent types](#custom-agent-types).
7. Download this page: `curl -fsSL -o ~/.coder/pstack-adapter.md https://raw.githubusercontent.com/ibetitsmike/coder/main/docs/fork/pstack-adapter.md`.
   `head -1 ~/.coder/pstack-adapter.md` then prints `# pstack on Coder Agents`.
8. If the downloaded page pins a different pstack commit than step 3, start again at step 1 with the downloaded page.
9. If `~/.coder/AGENTS.md` already holds a block between `<!-- pstack-adapter -->` and `<!-- /pstack-adapter -->`, delete that block.
10. Append the block below to `~/.coder/AGENTS.md`.
    Coder Agents loads `~/.coder/AGENTS.md` into every chat that uses the workspace, but it doesn't load this page, so the block carries the mappings that most skills need:

    ```markdown
    <!-- pstack-adapter -->
    pstack skills use Cursor names.
    Before you follow one, read ~/.coder/pstack-adapter.md with read_file and map each Cursor name with it.
    read_file needs an absolute path, so replace ~ with your home directory.
    - Task: spawn_agent, then wait_agent.
      readonly: true is type "explore" only for narrow code lookup.
      A custom subagent_type such as poteto-agent: that type if spawn_agent lists it, else type "general" with the body of ~/.coder/agents/<name>.md at the top of prompt.
    - AskQuestion: ask_user_question if you have it, else ask in plain text and end the turn.
    - Todo list: todo_write with the whole list if you have it, else keep the list in your reply.
    - Another pstack skill: read_file ~/.coder/skills/<name>/SKILL.md.
    - Work in the repository the user names, or else in the Working Directory of <workspace-context>.
      Write no files outside it unless the step names the path.
    - SendToUser: put the message in your reply.
    - A transcript of your own chat, a child, or a chat ID you were given: read_chat_transcript, if you have it.
    - Recent chats, or a top-level chat ID you don't have: search_chats, if you have it.
    - A child's chat ID you don't have: list_agents in its parent.
    - Transcripts and recent chats without those tools, wake-ups: write "Skipped in Coder Agents: <step>. <reason>." and go on.
      Never invent the skipped result.
    <!-- /pstack-adapter -->
    ```

11. Start a new chat.
    A chat pins `AGENTS.md` and the skills when it starts.
    A chat that was already open keeps its old copy until you select **Refresh context** in it.
12. Ask the chat to quote the `pstack-adapter` block without any tool call.
    The chat replies with the block's lines.
    This message is also the plain first message the chat needs, because a chat's first message can't run a skill.
13. If the chat can't quote the block, switch to a model server that keeps every system message.
    Coder Agents sends `AGENTS.md` as its own system message, after the base system prompt.
    A model server that keeps only the first system message drops the block without an error.
    Ollama's `qwen3-coder` renderer (Ollama 0.34.4) does this, but the oMLX GLM-5.3-Flash chat template keeps every system message.
14. If you switched model servers, start again at step 11.
15. Select the skill in the composer's `/` menu, which inserts `/workspace/<name>`.
    A bare `/<name>` loads nothing.

## Tools

| Cursor                                                                  | Coder Agents today                                                                                                                                                                                                  | Do this                                                                                                                                                                                                                                                                       | Removed by                   |
|-------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------|
| `Task`                                                                  | `spawn_agent` starts a child and returns at once, and `wait_agent` returns its final reply                                                                                                                          | Spawn with `spawn_agent`, then collect with `wait_agent`.                                                                                                                                                                                                                     | Stays                        |
| `AskQuestion`                                                           | `ask_user_question` in some root chats only. Refer to [Questions](#questions)                                                                                                                                       | If you have `ask_user_question`, use it. Otherwise ask in plain text and end the turn.                                                                                                                                                                                        | W2 (questions from children) |
| Open a todolist                                                         | `todo_write` and `todo_read`, only with the `fork-chat-todos` experiment. Refer to [Todo list](#todo-list)                                                                                                          | Send the whole list with `todo_write` and update it at each phase. Without the tools, keep the list in your reply and restate it after a compaction.                                                                                                                          | W4                           |
| Run another pstack skill (`/name` or "the **name** skill")              | 57 of pstack's 58 skills set `disable-model-invocation: true`, so `read_skill` and the skill index hide them from the model. You can still run one from the composer's `/` menu, but not in a chat's first message. | `read_file` `~/.coder/skills/<name>/SKILL.md`, with `~` replaced by your home directory, and follow it. Read its sibling files with `read_file` too. When you tell the user to type it, write `/workspace/<name>`, and in a new chat tell them to send a plain message first. | Stays                        |
| `Grep`, `Glob`                                                          | `execute`, `read_file`                                                                                                                                                                                              | `execute` with `rg` or `find`.                                                                                                                                                                                                                                                | Stays                        |
| Embed a screenshot or video inline, as `ketchup` and `ketchup-video` do | `attach_file`, for a workspace file of up to 10&nbsp;MiB. Refer to [Attachments](#attachments)                                                                                                                      | Save the file in the workspace, then call `attach_file` with its absolute path.                                                                                                                                                                                               | Stays                        |
| Available-tools map, `mcps/`                                            | When MCP servers are configured, their tools sit behind `find_tools`. It is absent when there are none, or when an MCP or client tool is itself named `find_tools`                                                  | If you have `find_tools`, call it to find a tool by name or purpose. If not, look for MCP tools in your tool list.                                                                                                                                                            | Stays                        |

### Questions

A root chat has `ask_user_question` in plan-mode turns, where the question ends the turn.
Outside plan mode, a root chat started from the web UI also has it.
There the call returns at once, and the answer arrives later as a user message, so keep working on what doesn't depend on the answer.
API chats outside plan mode and child chats don't have it.

### Todo list

`todo_write` and `todo_read` exist only when the deployment enables the `fork-chat-todos` experiment.
Plan-mode turns and `explore` children don't get them.
An item's status is `pending`, `in_progress`, `done`, or `cancelled`.
A `todo_write` with any other status, such as `completed`, fails and stores nothing.
The list survives a compaction.
The web UI shows a non-empty list in a pill in the composer.
In the conversation, the web UI shows each `todo_write` and `todo_read` call as a card, such as **Updated todos (1/3 done)**.
The card of a call that returns a non-empty list expands to the list, and the card of a failed call expands to its error.

### Attachments

`attach_file` copies a workspace file into the chat, so the user can open or download it from the conversation.
A file over 10&nbsp;MiB fails with `attachment exceeds 10 MiB size limit`.
The web UI shows a raster image, such as a PNG or a JPEG, as a thumbnail.
Plain text, Markdown, CSV, and JSON open in a text preview.
With the `fork-chat-todos` experiment, the web UI shows an MP4 as a player with a download link, both inline and in the turn's **Attachments** menu.
Without the experiment, an MP4 shows as a download card, as an SVG does.
Coder Agents doesn't send an attached file back to the model in later turns, so describe in your reply what the file shows.

## Subagent fields

| Cursor                                        | Coder Agents today                                                                                                                                                                                                                                            | Do this                                                                                                                                                                                                                                              | Removed by |
|-----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `subagent_type: generalPurpose`               | Types `general`, `explore`, `computer_use`, and custom types with the `fork-agent-definitions` experiment                                                                                                                                                     | `type: "general"`.                                                                                                                                                                                                                                   | Stays      |
| `poteto-agent`, `comment-sicko`               | Custom types only with the `fork-agent-definitions` experiment. Refer to [Custom agent types](#custom-agent-types)                                                                                                                                            | If the `spawn_agent` description lists `<name>` or `workspace/<name>`, pass that type. Otherwise `type: "general"`, and paste the body of `<pstack>/agents/<name>.md`, without its frontmatter, at the top of `prompt`.                              | W1         |
| `readonly: true`                              | `explore` has `read_file`, `execute`, `process_output`, `read_skill`, `read_skill_file` and the deployment MCP tools allowed in subagents, and no file-writing tool                                                                                           | `type: "explore"` for narrow code discovery, else `type: "general"`. Neither type enforces read-only, because `execute` can change files. Say "do not edit files or run commands that change state" in the prompt, and check `git status` afterward. | Stays      |
| `readonly: false`                             | `general`                                                                                                                                                                                                                                                     | `type: "general"`.                                                                                                                                                                                                                                   | Stays      |
| A subagent that spawns its own workers        | Only with the `fork-nested-subagents` experiment, up to a depth limit. Refer to [Nested subagents](#nested-subagents)                                                                                                                                         | If the child has `spawn_agent`, it spawns and collects its own workers. Otherwise the root spawns every worker itself and does the sub-coordinator's job.                                                                                            | W2         |
| More than 10 running subagents                | By default, at most 10 subagent turns run at once across the deployment, and more with an Agent Runtime Hours entitlement or the `fork-nested-subagents` experiment. Children over the cap wait for a free slot. Refer to [Subagent limits](#subagent-limits) | Spawn them all. Expect `wait_agent` to take longer when other chats hold slots.                                                                                                                                                                      | W2         |
| `environment: "cloud"`, a worktree per worker | A child shares its parent's workspace and working directory                                                                                                                                                                                                   | Run writers one at a time, or tell each writer to `git worktree add` its own checkout on its own branch and work only there.                                                                                                                         | W3         |
| `environment: "local"`                        | Not needed                                                                                                                                                                                                                                                    | Omit.                                                                                                                                                                                                                                                | Stays      |
| `cloud_base_branch: <b>`                      | No base branch argument                                                                                                                                                                                                                                       | Tell the worker to create its worktree from `<b>`.                                                                                                                                                                                                   | W3         |
| `run_in_background: true`                     | `spawn_agent` never blocks                                                                                                                                                                                                                                    | Omit. Call `wait_agent` when you need the result.                                                                                                                                                                                                    | Stays      |
| `resume`                                      | `message_agent` wakes an idle child with a follow-up                                                                                                                                                                                                          | `message_agent`, then `wait_agent`.                                                                                                                                                                                                                  | Stays      |
| `file_attachments`                            | No field                                                                                                                                                                                                                                                      | List the paths in the prompt.                                                                                                                                                                                                                        | Stays      |

### Custom agent types

When the deployment enables the `fork-agent-definitions` experiment, `spawn_agent` accepts a custom type for each agent definition that the workspace agent finds.
It reads the `*.md` files in `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root, including `~/.coder/agents/`.
A chat pins the definitions when it starts, like `AGENTS.md` and the skills, so start a new chat or select **Refresh context** after you add one.
`GET /api/v2/chats/{chat}` lists each definition file that the chat pinned in `context.resources`, with kind `subagent` and its `status`.
An `ok` file also reports `subagent_name`, `subagent_description`, and any parse warnings in `error`.
A file with any other status, such as `invalid`, reports the reason in `error` and adds no custom type, so check there when a type is missing from `spawn_agent`.
The `spawn_agent` description lists the custom types after the built-in ones.
The child gets the definition's body as a system message, so do not paste the body into `prompt`.
A definition without `model` runs on your model, and pstack's agents set none.
A `model` that matches no config in `list_subagent_models` fails the spawn.
An explicit `model_config_id` overrides the definition's `model`, and an explicit `reasoning_effort` overrides a valid definition effort.
A definition whose effort isn't a valid reasoning effort fails every spawn.
A custom type starts with the tools of its base type, which is `general` unless the definition sets `base_type: explore`.
Coder Agents then removes tools by the definition's `tools`, `disallowed_tools`, `readonly`, and `mcp` fields, as [Custom agent tool policy](#custom-agent-tool-policy) describes.
Coder Agents ignores a definition named `general`, `explore`, or `computer_use`.
With the experiment, the API also stores definitions for one user in an organization, for a whole organization, and for a chat project.
To manage your personal definitions, select **Settings** in the Agents sidebar, then **Agent definitions**, and pick the organization if you belong to more than one.
The page lists each definition's name and description, and its **Enabled** switch stops `spawn_agent` from offering a definition without deleting it.
**Delete** in a row's menu removes your personal definition and leaves workspace, organization, and project definitions with the same name alone.
The page doesn't list the workspace's definition files, such as the pstack agents that Setup copies to `~/.coder/agents/`.
`spawn_agent` also offers the chat owner's enabled personal definitions in the chat's organization, the enabled organization definitions that the owner can read, and the enabled definitions of the chat's project.
It offers project definitions only when the chat owner owns the project or the project is shared with them, and a child chat uses its root chat's project.
A name that only one of the workspace, personal, organization, and project sources defines keeps its bare name.
A name that more than one source defines is listed only as `workspace/<name>`, `personal/<name>`, `org/<name>`, or `project/<name>`.
A bare name that more than one source defines fails with `type must be one of:` and the listed types.
When you create an organization definition, Coder shares it with the whole organization, and a member who can share it can narrow who reads it with `PATCH /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition}/acl`.
`spawn_agent`, `wait_agent`, `message_agent`, `interrupt_agent`, and `list_agents` report a custom child's definition name in `type`, not `general`.
The chat list and `GET /api/v2/chats/{chat}` report each child chat's type in `agent_type`, and the Agents page shows it as a badge next to the child's title, except for `general`.
That name has no `workspace/`, `personal/`, `org/`, or `project/` prefix, so do not pass it back to `spawn_agent` when more than one source defines it.
Without the experiment, a custom type fails with `type must be one of:` and the built-in types.

### Custom agent tool policy

When Coder Agents spawns a custom type, it stores the definition's tool fields with the child chat.
On every turn of the child while the experiment is on, Coder Agents removes the tools that the stored fields don't allow.
A change to the definition doesn't change a child that already exists.
pstack's agents set none of these fields, so their children keep every tool of `general`.

- `readonly: true` keeps only the `explore` tools: `read_file`, `execute`, `process_output`, `read_skill`, `read_skill_file`, and the deployment MCP tools allowed in subagents.
  Of the provider tools, it keeps only `web_search`.
- `tools` lists the non-MCP tools that the child keeps, and an empty list keeps none.
  Claude Code names work: `Read`, `Write`, `Edit`, `MultiEdit`, `Bash`, `Grep`, `Glob`, `WebSearch`, and `Task`, which stands for the subagent tools.
- `disallowed_tools`, or `disallowedTools`, removes each tool that it names, MCP tools included.
  A Claude Code ID, `mcp__<server>__<tool>`, matches the Coder Agents tool `<server>__<tool>`.
- `mcp: none` removes every MCP tool.
  A list of server names keeps only the MCP tools of those servers, and each name must match the server's name exactly, case included.
- `find_tools` follows `mcp`, and only `disallowed_tools` removes it by name.

A child without `spawn_agent`, `wait_agent`, or `list_agents` also loses the subagent orchestration guidance in its system prompt.
A child without `read_skill` loses the skill index in its system prompt.
`readonly` doesn't make a child read-only, because `execute` can change files.
Do not rely on the policy as a sandbox, because a child that keeps `execute` can run any command that the workspace allows.

### Nested subagents

When the deployment enables the `fork-nested-subagents` experiment, a `general` child gets `spawn_agent` and the other subagent tools while its own children stay within `--chat-max-subagent-depth`.
The default depth of 2 allows a root, a coordinator, and its workers.
An organization's `max_depth` subagent limit can lower that depth, but not raise it.
An `explore` or `computer_use` child never gets them.
With the experiment, each chat can wait on, message, interrupt, and list only its direct children, so the root waits on the coordinator, not on its workers.
The experiment also makes **Stop** and `interrupt_agent` interrupt a chat's active descendants first.
Archiving a root whose family is still working interrupts the family, waits up to 30&nbsp;seconds for it to stop, then archives every member.
If a member is still working after 30&nbsp;seconds, the archive fails and archives nothing.
Without the experiment, or at the maximum depth, a child has no `spawn_agent`, and a call fails with `Tool not active in this turn: spawn_agent`.

### Subagent limits

By default, at most 10 subagent turns run at once across the deployment, and a child over that cap waits for a free slot.
An Enterprise deployment doesn't have that cap while its Agent Runtime Hours entitlement is enabled and below its hard limit.
A family is a root chat and all of its descendants.
With the `fork-nested-subagents` experiment, an administrator can also limit running subagents with `--chat-max-concurrent-subagents` across the deployment and `--chat-max-concurrent-subagents-per-family` in one family.
A child over one of these limits waits for a free slot, like a child over the default cap.
While a `running` child waits for a slot, `list_agents` shows it with `waiting_for_capacity: true`.
With the experiment, a child's own ancestors don't count against the default cap or these limits, so a coordinator that waits in `wait_agent` doesn't block its workers.
Because of that, more than 10 subagent turns can run at once.

With the experiment, `--chat-max-live-subagents-per-family` limits how many subagents in one family are live at once.
A subagent is live while it isn't archived and its status is `running`, `interrupting`, `requires_action`, or `paused`.
A child that finished doesn't count.
At the limit, `spawn_agent` fails with `cannot create a child agent: this chat family has` and the count, instead of queueing the child.
`message_agent` to a finished child fails the same way with `cannot resume the child agent:`.
After either error, call `list_agents`, and follow `has_more` with `offset` until you have every direct child.
If no child is live, retry one time without waiting.
If a child is `running`, `interrupting`, or `paused`, call `wait_agent` on it, and retry when it finishes.
A `paused` child waits for its owner to finish editing a queued message, so keep waiting on it unless the user says otherwise.
Otherwise, or if that retry fails, `requires_action` children or your children's own workers hold the limit.
`wait_agent` returns at once for a `requires_action` child, and `list_agents` doesn't show your children's workers.
In that case, call `interrupt_agent` on each child whose remaining work you don't need.
Whatever the child's status, `interrupt_agent` interrupts its `running`, `interrupting`, and `requires_action` descendants, and the child itself when it has a turn.
An interrupted `requires_action` child runs again before it stops, so call `wait_agent` on it before you retry.
Interrupted descendants count until they stop, and you can't wait on them, so if the retry fails, run `sleep 30` with `execute` and retry.
Tell the user that the family is at its live limit only after several retries fail.

These three flags default to 0, which means no limit.
An organization's `max_concurrent`, `max_concurrent_per_family`, and `max_live_per_family` subagent limits apply too, and the lower of the deployment and organization values wins.
An organization admin or auditor can read the flag values and the lower values in the `deployment` and `effective` fields of `GET /api/v2/chats/fork/organizations/{organization}/subagent-limits`.
Neither field includes the default cap of 10 subagent turns.
An organization admin can also change the organization values in the **Subagent limits** section of **AI Settings** > **Coder Agents**, and an auditor can view them there.

## Models

| Cursor                                                                 | Coder Agents today                                                                                                                                            | Do this                                                                                                                                                                                                                                                                                                                                                                          | Removed by         |
|------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------------|
| A model slug, such as `claude-opus-5-5-xhigh` or `grok-4.7-xhigh-fast` | `spawn_agent` takes `model_config_id` (a UUID from `list_subagent_models`) and `reasoning_effort` (`none` to `max`), with no aliases                          | Drop the slug's effort and `-fast` suffixes. Call `list_subagent_models` and pass the config whose `model` names the same model and version, ignoring any provider prefix, with the slug's effort as `reasoning_effort`. If the slug had `-fast`, pick that model's fast variant when one is listed. If none matches, omit `model_config_id` (refer to the next row) and say so. | W1 (model aliases) |
| `auto` or `inherit-parent`                                             | Omitting `model_config_id` runs the child on your model, unless the user or deployment set a subagent model for its type, in which case it runs on that model | If you know your own model's config in `list_subagent_models`, pass it as `model_config_id`, so a subagent model setting can't replace it. Otherwise omit `model_config_id`.                                                                                                                                                                                                     | Stays              |
| The `pstack-models.mdc` rule, `~/.cursor/rules/pstack-models.mdc`      | No rules directory, but `~/.coder/AGENTS.md` loads into every chat                                                                                            | Keep the role lines in `~/.coder/AGENTS.md` (refer to setup-pstack). If the file or the role's line is missing, use the skill's default slug.                                                                                                                                                                                                                                    | W6                 |
| A role value `<model> <effort>`                                        | As above                                                                                                                                                      | `model_config_id` of `<model>` and `reasoning_effort: <effort>`. Omit `reasoning_effort` when the config lists no `reasoning_efforts`. When a step compares model families, use the config's `model` from `list_subagent_models`, not its UUID.                                                                                                                                  | Stays              |

## setup-pstack

Follow its steps with these changes:

- Detect: the detected models are the configs that `list_subagent_models` returns.
  Write one `<model_config_id> <effort>` line per value in its `reasoning_efforts`, or one line with `none` when it lists none.
  The effort is the last token, so the budget step works as written.
  Keep `none` unchanged through the budget step.
- Defaults: map each default slug with the Models rows.
- Write: put the comment lines, the `# budget` line and one line per role between `<!-- pstack-models -->` and `<!-- /pstack-models -->` in `~/.coder/AGENTS.md`.
  Replace only that block, because the file holds other instructions.
- After the write, start a new chat or select **Refresh context** before you run another pstack skill.

## Paths

| Cursor                                                        | Coder Agents today                                                                                                                                                                                                                                   | Do this                                                                                                                                                                                                                                                                                             | Removed by |
|---------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `.cursor/skills/`, `~/.cursor/skills/`                        | Skills under `skills/`, `.agents/skills/`, `.claude/skills/` and `.codex/skills/` of each scan root, including `~/.coder/skills/`, but not `.cursor/skills/`. A skill must sit one level deep, at `<container>/<name>/SKILL.md`                      | Use `~/.coder/skills/` or the repository's `.agents/skills/`. Flatten a nested path such as `<category>/<name>/` to `<name>/`.                                                                                                                                                                      | Stays      |
| `.cursor/agents/`, `<pstack>/agents/`                         | The workspace agent reads `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root. With the `fork-agent-definitions` experiment, chats get them as custom types. Refer to [Custom agent types](#custom-agent-types) | Follow Setup, which copies `<pstack>/agents/` to `~/.coder/agents/`.                                                                                                                                                                                                                                | W1         |
| `~/.cursor/plugins/`, `.cursor-plugin/plugin.json`            | `~/.claude/plugins/cache` is watched, but plugin manifests are ignored                                                                                                                                                                               | Follow Setup.                                                                                                                                                                                                                                                                                       | W6         |
| `.cursor/rules/*.mdc`, always-applied rules                   | `AGENTS.md`, `CLAUDE.md` and `.cursorrules` at the top of each scan root, and no `.mdc` files                                                                                                                                                        | Put an always-applied rule's text in `AGENTS.md` or `~/.coder/AGENTS.md`.                                                                                                                                                                                                                           | W6         |
| `https://github.com/cursor/plugins/blob/main/pstack/<path>`   | Not needed                                                                                                                                                                                                                                           | Read `<pstack>/<path>` and never fetch it. Give the user the URL when they need a link.                                                                                                                                                                                                             | Stays      |
| The workspace root, the current directory                     | `execute` runs in the workspace agent's directory unless you pass `workdir`, and `<workspace-context>` names it as `Working Directory`                                                                                                               | Work in the repository the user names, or else in the Working Directory. Write no files outside it unless the step names the path.                                                                                                                                                                  | Stays      |
| Transcripts in `~/.cursor/projects/<slug>/agent-transcripts/` | `read_chat_transcript`, only with the `fork-chat-todos` experiment. Refer to [Chat transcripts](#chat-transcripts)                                                                                                                                   | If you have `read_chat_transcript`, read the chat you need by its ID. Find a missing top-level chat ID with `search_chats`, and a missing child ID with `list_agents` in its parent. Otherwise skip transcript reads with the skip line below. Use project memory, `git`, and `gh` for prior state. | W4         |

### Chat transcripts

When the deployment enables the `fork-chat-todos` experiment, `read_chat_transcript` reads the stored messages of a chat in pages.
Leave `chat_id` empty to read your own chat, including messages that compaction took out of your context.
Pass a child's `chat_id` from `spawn_agent` or `list_agents` to read that child, or the ID of any other chat your user can read.
With the same experiment, `search_chats` finds top-level chats with the chat list search syntax, such as `search:"<phrase>"` or `pr:<number>`, and an empty query lists the recent ones.
By default it searches only the chats your user created, so add `source:created_by_me,shared_with_me` to include chats shared with your user.
It returns no subagent chats, so get a child's ID from `list_agents` in its parent.
Plan-mode turns and `explore` children don't get either tool.
With the same experiment, `wait_agent` also takes `include_tool_calls`, from 1 to 20, and adds up to that many of the child's latest tool calls, oldest first, to its reply.
Without the experiment, there is no transcript or chat search tool.
Root chats in a project have `read_memory` and `save_memory`, but Setup creates no project.

## Missing and Cursor-only features

When a step needs a feature that Coder Agents lacks, put one line in your reply, `Skipped in Coder Agents: <step>. <reason>.`, then go on to the next step.
Do not stop, ask, or retry.
When a later step needs what the skipped step made, such as a URL or a key, leave a placeholder and name it in the line.

| Cursor                                                                                                   | Coder Agents today                                                                                                     | Do this                                                                                                                                                                                                                                                                                                                    | Removed by            |
|----------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------|
| Sticky `/poteto-mode`                                                                                    | No pinned skills                                                                                                       | Read `poteto-mode` again at the start of each turn while the mode is on.                                                                                                                                                                                                                                                   | W6                    |
| `/loop`, watchers that wake you, PR and CI subscriptions                                                 | A chat runs only when a message arrives                                                                                | Wait inside the turn (`wait_agent` timeouts, `gh pr checks --watch`). Skip a step that needs a later wake, and tell the user to send the next message.                                                                                                                                                                     | W5                    |
| `SendToUser`                                                                                             | No notification tool                                                                                                   | Put the message in your reply.                                                                                                                                                                                                                                                                                             | W5 (`notify_user`)    |
| `SendToUser` secret requests                                                                             | No masked input                                                                                                        | Skip with the line. Ask the user to put the secret in the workspace environment.                                                                                                                                                                                                                                           | W2 (secret questions) |
| Cursor Automations: routines, `update_state` calls, `api2.cursor.sh/automations` webhooks, routine wakes | None                                                                                                                   | Skip with the line.                                                                                                                                                                                                                                                                                                        | Stays                 |
| Bugbot                                                                                                   | Bugbot comments on a GitHub PR are ordinary review threads                                                             | Triage them with the forge command that the step gives. Skip a step that starts, waits for, or configures Bugbot when the PR has no Bugbot check or comment.                                                                                                                                                               | Stays                 |
| Cursor cloud agents                                                                                      | None                                                                                                                   | Refer to `environment: "cloud"`. Skip a step that needs the cloud agent itself, such as its dashboard.                                                                                                                                                                                                                     | Stays                 |
| A built-in PR tool                                                                                       | No PR tool                                                                                                             | Use the forge command that the step gives, such as `gh pr create`. That isn't a skip, so write no line.                                                                                                                                                                                                                    | Stays                 |
| Recent work across chats, such as in `recall`                                                            | `search_chats` and `read_chat_transcript`, only with `fork-chat-todos`. Refer to [Chat transcripts](#chat-transcripts) | If you have `search_chats`, list your recent chats with an empty query and read the ones you need with `read_chat_transcript`. Otherwise skip the transcript step with the line. Either way, report prior state from `git log`, `git status` and `gh pr list --author @me --state all`, and never invent files or results. | W4                    |
| Origin CLI                                                                                               | `execute` runs it when it is installed                                                                                 | Use it when `command -v origin` succeeds. Otherwise skip with the line.                                                                                                                                                                                                                                                    | Stays                 |
| `/create-skill`                                                                                          | No skill-creation tool                                                                                                 | For a project skill, write the repository's `.agents/skills/<name>/SKILL.md`. For a personal skill, write `~/.coder/skills/<name>/SKILL.md`. Give it frontmatter `name`, equal to the kebab-case directory name, and `description`.                                                                                        | Stays                 |
| Image generation                                                                                         | No built-in image tool                                                                                                 | Use an image tool if `find_tools` or your tool list offers one. Otherwise skip with the line.                                                                                                                                                                                                                              | Stays                 |

## Evidence

Checked against fork `main` at `23d5021361`:

- Subagent types and `spawn_agent` fields: `coderd/x/chatd/subagent_catalog.go:40-46`, `:55-159`.
- Spawn from a child: root-chat tools only for root chats, `coderd/x/chatd/turn_environment.go:731-740`.
  Subagent tools for a `general` child only with `fork-nested-subagents` and below the depth limit, `coderd/x/chatd/fork_subagents.go:258-275`, `:289-317`.
  Direct children only, `isSubagentTarget`, `fork_subagents.go:322-334`.
  Depth flag, `codersdk/fork_chat_config.go:45-52`, lowered by the organization's `max_depth`, `coderd/x/chatd/fork_limits.go:56-58`.
  Inactive tool error, `coderd/x/chatd/chatloop/chatloop.go:1459`.
  Upstream server check, `validateSubagentSpawnParent`, `subagent_catalog.go:254-262`.
- Interrupt cascade and archive of an active family, only with `fork-nested-subagents`: `coderd/x/chatd/fork_cascade.go:43-46`, `:97-100`, 30-second wait, `:26`.
- Agent definition directories: `agent/agentcontext/fork_agents.go:24-29`.
  Front-matter fields and the `general` default base type: `codersdk/workspacesdk/fork_agentdefinition.go:63-76`, `:99-103`, `:171-181`.
  Custom types only with `fork-agent-definitions`, listed after the built-in types, with built-in names winning: `coderd/x/chatd/fork_agent_definitions.go:37-45`, `:51-54`, `:102-112`, called from `subagent_catalog.go:185` and `:233`.
  The definition's body becomes the child's system prompt, `fork_agent_definitions.go:379`.
  The parent's model unless the definition sets `model`, an explicit `model_config_id` first, `:350-361`, and the model match, `:386-412`.
  Definition files in the chat context, only with `fork-agent-definitions`, `coderd/x/chatd/context_prompt.go:321-325`, `:411-414`, `:444-445`, with the name, description, and warnings of an `ok` file, `fork_agent_definitions.go:414-431`, the status and reason of any other file, `context_prompt.go:359-368`, on the single-chat response, `coderd/exp_chats.go:1875-1876`, and custom types only from `ok` files, `fork_agent_definitions.go:155-158`.
- Custom agent tool policy, only with `fork-agent-definitions`: stored at spawn, `fork_subagents.go:181-188`, `:242`, loaded for child chats only, `coderd/x/chatd/fork_agent_tool_policy.go:175-178`, and applied in `buildTurnEnvironment`, `turn_environment.go:753-756`, `:945-947`.
  The filter, with `readonly` and `web_search`, `fork_agent_tool_policy.go:139-170`, Claude Code aliases, `:48-61`, MCP IDs, `:76-89`, and MCP servers, `:95-114`.
  Orchestration guidance dropped, `fork_subagents.go:349-351`, `fork_agent_tool_policy.go:123-127`, and the skill index dropped, `turn_environment.go:757-759`.
- Attachments: `coderd/x/chatd/chattool/attachfile.go:26-31`, built with the file tools, `coderd/x/chatd/turn_environment.go:709-712`, with the size limit, `coderd/x/chatd/chattool/attachment.go:17`, `:48-50`.
  Not sent back to the model, `coderd/x/chatd/chatprompt/chatprompt.go:106-109`.
  Text preview and image thumbnail, `site/src/pages/AgentsPage/components/ChatConversation/AttachmentBlocks.tsx:115-117`, `:637`, `:682-691`, raster images only, `coderd/x/chatfiles/mime.go:131-133`.
  MP4 player only with `fork-chat-todos`, `ForkAttachmentPreview.tsx:37-58`, also in the Attachments menu, `TurnAttachments.tsx:116-118`.
- Child shares the parent workspace: `coderd/x/chatd/subagent_operations.go:474-479`.
- `wait_agent` returns the last assistant message: `subagent_operations.go:643-675`.
- Concurrency default and queued refusals: `coderd/x/chatd/agentadmission.go:13`, no cap when an entitlement unlocks it, `:60-65`, `enterprise/coderd/x/chatd/agentadmission.go:18-28`, `coderd/x/chatd/worker.go:274-282`.
  Subagent limits, only with `fork-nested-subagents`: flags, `codersdk/fork_chat_config.go:54-83`, the lower of the deployment and organization values, `fork_capacity.go:32-45`, `fork_limits.go:49-60`, `:64-72`, returned by the organization limits API, `coderd/fork_subagent_limits.go:30-51`, `:133-140`, without the default cap, `fork_capacity.go:119-130`.
  The organization settings section, only with `fork-nested-subagents` and for a user who can read the organization's chat model configs, `site/src/pages/AISettingsPage/CoderAgentsPage/ForkSubagentLimits.tsx:31-55`, read-only without edit permission, `components/ForkSubagentLimitsSettings.tsx:179-189`.
  Concurrency limits without the ancestors, `fork_capacity.go:75-117`, and `waiting_for_capacity` in `list_agents`, `coderd/x/chatd/fork_list_agents.go:34-40`, `:81-88`, from `fork_capacity.go:146-192`.
  `list_agents` pages of 10 and `paused`, `coderd/x/chatd/subagent.go:570-589`, and `wait_agent` on a `paused` child, `:479-487`.
  `wait_agent` returns at once for `requires_action`, `coderd/x/chatd/subagent_operations.go:629-640`, and `interrupt_agent` moves it to `running`, `:160-177`.
  `interrupt_agent` interrupts the active descendants first, for any status, `:160-162`, `coderd/x/chatd/chat_mutations.go:883`, `:893-895`, `coderd/x/chatd/fork_cascade.go:32-39`, `:83-92`.
  Live limit on spawn and resume, `fork_subagents.go:72-88`, `:106-139`, and its error, `:150-162`, with the live statuses, `coderd/database/forkdb/queries/chat_capacity.sql:30-37`.
  Stored definitions: user routes, `coderd/fork_agent_definitions.go:32-170`, organization routes, `coderd/fork_organization_agent_definitions.go:26-180`, shared with the whole organization at creation, `:76-80`, with the sharing routes, `coderd/fork_organization_agent_definitions_acl.go:35-159`, and project routes, `coderd/fork_project_agent_definitions.go:20-190`.
  Offered to `spawn_agent` when enabled and readable by the chat owner, `coderd/x/chatd/fork_agent_definitions.go:181-305`, and for a project the owner owns or that is shared with them, `:258-262`, from the root chat for a child, `coderd/x/chatd/project_instructions.go:17-51`, `coderd/x/chatd/projects.go:16-61`.
  The personal definitions page: the **Settings** button, `site/src/pages/AgentsPage/components/ChatsSidebar/chats/ChatsPanel.tsx:512-519`, the route, `site/src/forkRoutes.tsx:16`, and the nav item and page, only with `fork-agent-definitions`, `site/src/pages/AgentsPage/components/ChatsSidebar/settings/ForkSettingsNavItems.tsx:18-28`, `site/src/pages/AgentsPage/AgentSettingsPage/ForkAgentDefinitionsPage/ForkAgentDefinitionsPage.tsx:21-23`, with the organization picker, `:25-50`.
  Its table of the member's personal definitions, `site/src/api/forkAgentDefinitions.ts:11-23`, with the **Enabled** switch, `ForkAgentDefinitionsTableView.tsx:128-137`, and the **Delete** menu item and its dialog, `:158-163`, `:214-221`, calls the user routes, `forkAgentDefinitions.ts:26-48`.
  Disabled personal definitions offer no type, `coderd/x/chatd/fork_agent_definitions.go:213-216`.
  The definition name without its prefix in subagent tool results, stored at spawn, `coderd/x/chatd/fork_subagents.go:233-236`, and reported by `coderd/x/chatd/fork_subagent_types.go:25-51`, `:86-112`, `fork_tools.go:34`.
  A child chat's `agent_type` in the chat list and the single-chat response, only with the experiment, `coderd/exp_chats.go:616`, `:1939`, `coderd/fork_chat_agent_type.go:20-60`, with a built-in child's type from its mode, `coderd/database/forkdb/queries/chat_subagents.sql:15-29`, and the badge, `site/src/pages/AgentsPage/components/ForkAgentTypeBadge.tsx:12-24`, in the sidebar, `ChatsSidebar/tree/ChatTreeNode.tsx:327`, and the top bar, `ChatTopBar.tsx:279`.
  Bare names for one source and qualified names for several, `fork_agent_definitions.go:95-142`, `coderd/x/skills/skills.go:153-229`, and the error for an unknown or ambiguous type, `subagent_catalog.go:233-242`.
- `ask_user_question` in plan-mode root turns, and outside plan mode only in web UI root chats: `coderd/x/chatd/turn_environment.go:724-730`, async description, `coderd/x/chatd/chattool/askuserquestion.go:17-24`.
- Skill roots and containers: `agent/agentcontext/defaults.go:9-21`, `agent/agentcontext/resolve.go:66-71`.
- Instruction files: `resolve.go:48-52`.
- Skills one level deep, with `name` equal to the directory: `resolve.go:61-64`, `:632-647`.
- No skill in a chat's first message: `coderd/x/chatd/chatd.go:659-667`, `coderd/x/chatd/skill_invocation.go:37`.
- Working directory: `coderd/x/chatd/context_prompt.go:195-198`, `agent/agentproc/process.go:400-413`.
- `explore` tools: `turn_environment.go:2124-2133`.
- Hidden skills: `coderd/x/chatd/chattool/skill.go:55-62`, `turn_environment.go:2185-2201`.
- `find_tools`: `coderd/x/chatd/chattool/findtools.go:19`, only with MCP tools, `coderd/x/chatd/mcp_tool_search.go:121-136`, which also omits it on a name collision.
- Omitted `model_config_id`: subagent model override first, `subagent_catalog.go:60-77` and `:84-102`, else the parent's model, `subagent_operations.go:340-343`.
- Project memory for root chats: `coderd/x/chatd/memory.go:20-33`.
- Todo tools: `coderd/x/chatd/fork_chat_todos.go`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go`. The plan-mode and `explore` allowlists, `builtinPlanToolAllowed` and `exploreToolAllowed` in `turn_environment.go`, don't list them.
  Item statuses, `fork_chat_todos.go:41`, checked against the tool schema before the tool runs, `coderd/x/chatd/toolinput.go:22-30`, `:125-127`.
  Snapshot after a compaction, `todoSnapshotMessage`, `coderd/x/chatd/fork_context.go:105-108`.
  Composer pill, only with `fork-chat-todos` and a non-empty list, `site/src/pages/AgentsPage/components/ForkComposerSlot.tsx:18-26`, `ChatTodoPill.tsx:99-107`.
  Checklist card for each call, only with `fork-chat-todos`, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:20-35`, `:42-49`, with its labels, `ForkTodoTool.tsx:37-50`, `:67-77`, and its expandable list or error, `:78-99`, `:115-128`.
- Transcript tool: `coderd/x/chatd/fork_chat_transcript.go:68-85`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go:28-33`.
  Reads as the chat owner, `fork_chat_transcript.go:109-116`.
  `include_tool_calls` on `wait_agent`: `coderd/x/chatd/fork_wait_agent.go:31`, `:38`, `:84-85`, added with the same experiment, `fork_tools.go:32`.
  Chat search: `coderd/x/chatd/fork_search_chats.go:28-32`, `:51-67`, added with the same experiment, `fork_tools.go:31`, and run as the chat owner, `fork_search_chats.go:97-103`.
  The plan-mode and `explore` allowlists in `turn_environment.go` don't list either tool.
- No wake-up, notification, or PR tool: no matches in `coderd/x/chatd` for `schedule_wakeup`, `notify_user`, or `pull_request`.
