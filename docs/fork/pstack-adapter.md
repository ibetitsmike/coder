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
    Ollama's `qwen3-coder` renderer (Ollama 0.34.4) does this, but the oMLX Qwen3.8 chat template keeps every system message.
14. If you switched model servers, start again at step 11.
15. Select the skill in the composer's `/` menu, which inserts `/workspace/<name>`.
    A bare `/<name>` loads nothing.

## Tools

| Cursor                                                     | Coder Agents today                                                                                                                                                                                                  | Do this                                                                                                                                                                                                                                                                       | Removed by                   |
|------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------|
| `Task`                                                     | `spawn_agent` starts a child and returns at once, and `wait_agent` returns its final reply                                                                                                                          | Spawn with `spawn_agent`, then collect with `wait_agent`.                                                                                                                                                                                                                     | Stays                        |
| `AskQuestion`                                              | `ask_user_question` in some root chats only. Refer to [Questions](#questions)                                                                                                                                       | If you have `ask_user_question`, use it. Otherwise ask in plain text and end the turn.                                                                                                                                                                                        | W2 (questions from children) |
| Open a todolist                                            | `todo_write` and `todo_read`, only with the `fork-chat-todos` experiment. Refer to [Todo list](#todo-list)                                                                                                          | Send the whole list with `todo_write` and update it at each phase. Without the tools, keep the list in your reply and restate it after a compaction.                                                                                                                          | W4                           |
| Run another pstack skill (`/name` or "the **name** skill") | 57 of pstack's 58 skills set `disable-model-invocation: true`, so `read_skill` and the skill index hide them from the model. You can still run one from the composer's `/` menu, but not in a chat's first message. | `read_file` `~/.coder/skills/<name>/SKILL.md`, with `~` replaced by your home directory, and follow it. Read its sibling files with `read_file` too. When you tell the user to type it, write `/workspace/<name>`, and in a new chat tell them to send a plain message first. | Stays                        |
| `Grep`, `Glob`                                             | `execute`, `read_file`                                                                                                                                                                                              | `execute` with `rg` or `find`.                                                                                                                                                                                                                                                | Stays                        |
| Available-tools map, `mcps/`                               | When MCP servers are configured, their tools sit behind `find_tools`. It is absent when there are none, or when an MCP or client tool is itself named `find_tools`                                                  | If you have `find_tools`, call it to find a tool by name or purpose. If not, look for MCP tools in your tool list.                                                                                                                                                            | Stays                        |

### Questions

A root chat has `ask_user_question` in plan-mode turns, where the question ends the turn.
Outside plan mode, a root chat started from the web UI also has it.
There the call returns at once, and the answer arrives later as a user message, so keep working on what doesn't depend on the answer.
API chats outside plan mode and child chats don't have it.

### Todo list

`todo_write` and `todo_read` exist only when the deployment enables the `fork-chat-todos` experiment.
Plan-mode turns and `explore` children don't get them.
The list survives a compaction.
The web UI shows a non-empty list in a pill in the composer.

## Subagent fields

| Cursor                                        | Coder Agents today                                                                                                                                                  | Do this                                                                                                                                                                                                                                              | Removed by |
|-----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `subagent_type: generalPurpose`               | Types `general`, `explore`, `computer_use`, and custom types with the `fork-agent-definitions` experiment                                                           | `type: "general"`.                                                                                                                                                                                                                                   | Stays      |
| `poteto-agent`, `comment-sicko`               | Custom types only with the `fork-agent-definitions` experiment. Refer to [Custom agent types](#custom-agent-types)                                                  | If the `spawn_agent` description lists `<name>`, pass `type: "<name>"`. Otherwise `type: "general"`, and paste the body of `<pstack>/agents/<name>.md`, without its frontmatter, at the top of `prompt`.                                             | W1         |
| `readonly: true`                              | `explore` has `read_file`, `execute`, `process_output`, `read_skill`, `read_skill_file` and the deployment MCP tools allowed in subagents, and no file-writing tool | `type: "explore"` for narrow code discovery, else `type: "general"`. Neither type enforces read-only, because `execute` can change files. Say "do not edit files or run commands that change state" in the prompt, and check `git status` afterward. | Stays      |
| `readonly: false`                             | `general`                                                                                                                                                           | `type: "general"`.                                                                                                                                                                                                                                   | Stays      |
| A subagent that spawns its own workers        | Only with the `fork-nested-subagents` experiment, up to a depth limit. Refer to [Nested subagents](#nested-subagents)                                               | If the child has `spawn_agent`, it spawns and collects its own workers. Otherwise the root spawns every worker itself and does the sub-coordinator's job.                                                                                            | W2         |
| More than 10 running subagents                | By default, at most 10 subagent turns run at once across the deployment. Children over the cap wait for a free slot                                                 | Spawn them all. Expect `wait_agent` to take longer when other chats hold slots.                                                                                                                                                                      | W2         |
| `environment: "cloud"`, a worktree per worker | A child shares its parent's workspace and working directory                                                                                                         | Run writers one at a time, or tell each writer to `git worktree add` its own checkout on its own branch and work only there.                                                                                                                         | W3         |
| `environment: "local"`                        | Not needed                                                                                                                                                          | Omit.                                                                                                                                                                                                                                                | Stays      |
| `cloud_base_branch: <b>`                      | No base branch argument                                                                                                                                             | Tell the worker to create its worktree from `<b>`.                                                                                                                                                                                                   | W3         |
| `run_in_background: true`                     | `spawn_agent` never blocks                                                                                                                                          | Omit. Call `wait_agent` when you need the result.                                                                                                                                                                                                    | Stays      |
| `resume`                                      | `message_agent` wakes an idle child with a follow-up                                                                                                                | `message_agent`, then `wait_agent`.                                                                                                                                                                                                                  | Stays      |
| `file_attachments`                            | No field                                                                                                                                                            | List the paths in the prompt.                                                                                                                                                                                                                        | Stays      |

### Custom agent types

When the deployment enables the `fork-agent-definitions` experiment, `spawn_agent` accepts a custom type for each agent definition that the workspace agent finds.
It reads the `*.md` files in `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root, including `~/.coder/agents/`.
A chat pins the definitions when it starts, like `AGENTS.md` and the skills, so start a new chat or select **Refresh context** after you add one.
The `spawn_agent` description lists the custom types after the built-in ones.
The child gets the definition's body as a system message, so do not paste the body into `prompt`.
A definition without `model` runs on your model, and pstack's agents set none.
A `model` that matches no config in `list_subagent_models` fails the spawn.
An explicit `model_config_id` or `reasoning_effort` overrides the definition.
`tools`, `readonly`, `mcp`, and `disallowedTools` aren't enforced yet.
A custom type gets the tools of its base type, which is `general` unless the definition sets `base_type: explore`.
Coder Agents ignores a definition named `general`, `explore`, or `computer_use`.
Without the experiment, a custom type fails with `type must be one of:` and the built-in types.

### Nested subagents

When the deployment enables the `fork-nested-subagents` experiment, a `general` child gets `spawn_agent` and the other subagent tools while its own children stay within `--chat-max-subagent-depth`.
The default depth of 2 allows a root, a coordinator, and its workers.
An `explore` or `computer_use` child never gets them.
With the experiment, each chat can wait on, message, interrupt, and list only its direct children, so the root waits on the coordinator, not on its workers.
The experiment also makes **Stop** and `interrupt_agent` interrupt a chat's active descendants first.
Archiving a root whose family is still working interrupts the family, waits up to 30&nbsp;seconds for it to stop, then archives every member.
If a member is still working after 30&nbsp;seconds, the archive fails and archives nothing.
Without the experiment, or at the maximum depth, a child has no `spawn_agent`, and a call fails with `Tool not active in this turn: spawn_agent`.

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

| Cursor                                                        | Coder Agents today                                                                                                                                                                                                              | Do this                                                                                                                                                                                                                         | Removed by |
|---------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `.cursor/skills/`, `~/.cursor/skills/`                        | Skills under `skills/`, `.agents/skills/`, `.claude/skills/` and `.codex/skills/` of each scan root, including `~/.coder/skills/`, but not `.cursor/skills/`. A skill must sit one level deep, at `<container>/<name>/SKILL.md` | Use `~/.coder/skills/` or the repository's `.agents/skills/`. Flatten a nested path such as `<category>/<name>/` to `<name>/`.                                                                                                  | Stays      |
| `.cursor/agents/`, `<pstack>/agents/`                         | Read from `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root, and used only with the `fork-agent-definitions` experiment. Refer to [Custom agent types](#custom-agent-types)              | Follow Setup, which copies `<pstack>/agents/` to `~/.coder/agents/`.                                                                                                                                                            | W1         |
| `~/.cursor/plugins/`, `.cursor-plugin/plugin.json`            | `~/.claude/plugins/cache` is watched, but plugin manifests are ignored                                                                                                                                                          | Follow Setup.                                                                                                                                                                                                                   | W6         |
| `.cursor/rules/*.mdc`, always-applied rules                   | `AGENTS.md`, `CLAUDE.md` and `.cursorrules` at the top of each scan root, and no `.mdc` files                                                                                                                                   | Put an always-applied rule's text in `AGENTS.md` or `~/.coder/AGENTS.md`.                                                                                                                                                       | W6         |
| `https://github.com/cursor/plugins/blob/main/pstack/<path>`   | Not needed                                                                                                                                                                                                                      | Read `<pstack>/<path>` and never fetch it. Give the user the URL when they need a link.                                                                                                                                         | Stays      |
| The workspace root, the current directory                     | `execute` runs in the workspace agent's directory unless you pass `workdir`, and `<workspace-context>` names it as `Working Directory`                                                                                          | Work in the repository the user names, or else in the Working Directory. Write no files outside it unless the step names the path.                                                                                              | Stays      |
| Transcripts in `~/.cursor/projects/<slug>/agent-transcripts/` | `read_chat_transcript`, only with the `fork-chat-todos` experiment. Refer to [Chat transcripts](#chat-transcripts)                                                                                                              | If you have `read_chat_transcript`, read the chat you need by its ID, and find a missing ID with `search_chats`. Otherwise skip transcript reads with the skip line below. Use project memory, `git`, and `gh` for prior state. | W4         |

### Chat transcripts

When the deployment enables the `fork-chat-todos` experiment, `read_chat_transcript` reads the stored messages of a chat in pages.
Leave `chat_id` empty to read your own chat, including messages that compaction took out of your context.
Pass a child's `chat_id` from `spawn_agent` or `list_agents` to read that child, or the ID of any other chat your user can read.
With the same experiment, `search_chats` finds the top-level chats your user can read with the chat list search syntax, such as `search:"<phrase>"` or `pr:<number>`, and an empty query lists the recent ones.
It returns no subagent chats, so get a child's ID from `list_agents` in its parent.
Plan-mode turns and `explore` children don't get either tool.
`wait_agent` still returns only a child's final reply.
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

Checked against fork `main` at `ab33ec5f07`:

- Subagent types and `spawn_agent` fields: `coderd/x/chatd/subagent_catalog.go:40-46`, `:55-159`.
- Spawn from a child: root-chat tools only for root chats, `coderd/x/chatd/turn_environment.go:731-740`.
  Subagent tools for a `general` child only with `fork-nested-subagents` and below the depth limit, `coderd/x/chatd/fork_subagents.go:142-155`, `:169-197`.
  Direct children only, `isSubagentTarget`, `fork_subagents.go:202-214`.
  Depth flag, `codersdk/fork_chat_config.go:33-40`.
  Inactive tool error, `coderd/x/chatd/chatloop/chatloop.go:1459`.
  Upstream server check, `validateSubagentSpawnParent`, `subagent_catalog.go:254-262`.
- Interrupt cascade and archive of an active family, only with `fork-nested-subagents`: `coderd/x/chatd/fork_cascade.go:43-46`, `:97-100`, 30-second wait, `:26`.
- Agent definition directories: `agent/agentcontext/fork_agents.go:24-29`.
  Front-matter fields and the `general` default base type: `codersdk/workspacesdk/fork_agentdefinition.go:63-76`, `:99-103`, `:171-181`.
  Custom types only with `fork-agent-definitions`, listed after the built-in types, with built-in names winning: `coderd/x/chatd/fork_agent_definitions.go:40-42`, `:61-64`, `:86-88`, called from `subagent_catalog.go:185` and `:233`.
  The definition's body becomes the child's system prompt, `fork_agent_definitions.go:177`.
  The parent's model unless the definition sets `model`, an explicit `model_config_id` first, `:147-159`, and the model match, `:187-210`.
- Child shares the parent workspace: `coderd/x/chatd/subagent_operations.go:471-476`.
- `wait_agent` returns the last assistant message: `subagent_operations.go:640-672`.
- Concurrency default and queued refusals: `coderd/x/chatd/agentadmission.go:13`, `coderd/x/chatd/worker.go:273-281`.
- `ask_user_question` in plan-mode root turns, and outside plan mode only in web UI root chats: `coderd/x/chatd/turn_environment.go:724-730`, async description, `coderd/x/chatd/chattool/askuserquestion.go:17-24`.
- Skill roots and containers: `agent/agentcontext/defaults.go:9-21`, `agent/agentcontext/resolve.go:66-71`.
- Instruction files: `resolve.go:48-52`.
- Skills one level deep, with `name` equal to the directory: `resolve.go:61-64`, `:632-647`.
- No skill in a chat's first message: `coderd/x/chatd/chatd.go:659-667`, `coderd/x/chatd/skill_invocation.go:37`.
- Working directory: `coderd/x/chatd/context_prompt.go:195-198`, `agent/agentproc/process.go:400-413`.
- `explore` tools: `turn_environment.go:2113-2122`.
- Hidden skills: `coderd/x/chatd/chattool/skill.go:55-62`, `turn_environment.go:2174-2190`.
- `find_tools`: `coderd/x/chatd/chattool/findtools.go:19`, only with MCP tools, `coderd/x/chatd/mcp_tool_search.go:121-136`, which also omits it on a name collision.
- Omitted `model_config_id`: subagent model override first, `subagent_catalog.go:60-77` and `:84-102`, else the parent's model, `subagent_operations.go:337-340`.
- Project memory for root chats: `coderd/x/chatd/memory.go:20-33`.
- Todo tools: `coderd/x/chatd/fork_chat_todos.go`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go`. The plan-mode and `explore` allowlists, `builtinPlanToolAllowed` and `exploreToolAllowed` in `turn_environment.go`, don't list them.
  Snapshot after a compaction, `todoSnapshotMessage`, `coderd/x/chatd/fork_context.go:105-108`.
  Composer pill, only with `fork-chat-todos` and a non-empty list, `site/src/pages/AgentsPage/components/ForkComposerSlot.tsx:18-26`, `ChatTodoPill.tsx:99-107`.
- Transcript tool: `coderd/x/chatd/fork_chat_transcript.go:68-85`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go:28-32`.
  Reads as the chat owner, `fork_chat_transcript.go:109-116`.
  Chat search: `coderd/x/chatd/fork_search_chats.go:28-32`, `:51-67`, added with the same experiment, `fork_tools.go:31`, and run as the chat owner, `fork_search_chats.go:97-103`.
  The plan-mode and `explore` allowlists in `turn_environment.go` don't list either tool.
- No wake-up, notification, or PR tool: no matches in `coderd/x/chatd` for `schedule_wakeup`, `notify_user`, or `pull_request`.
