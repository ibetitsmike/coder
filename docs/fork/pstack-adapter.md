# pstack on Coder Agents

This page maps pstack onto Coder Agents as this fork's `main` runs it. It tracks
pstack v0.15.36 (`cursor/plugins` commit `c742dd61706f`, folder `pstack/`) and
is modeled on pstack's Claude Code adapter, `docs/harness/claude-code.md`.

pstack skills use Cursor names. In a Coder Agents chat, read this page once per
chat, then map each Cursor name in a skill with the tables below. `<pstack>`
means the folder that holds pstack's `skills/`, `agents/` and `docs/`. If a
Cursor field has no row, drop it and say so. Never pass a field or value that a
tool schema rejects.

Each row names what Coder Agents offers today and what to do instead. The last
column names the pstack parity workstream (ibetitsmike/coder#4) whose merge
removes the row. "Stays" means no workstream removes it. This page is fork-only:
it is not in `docs/manifest.json`.

## Setup

1. Clone pstack in the workspace and pin it:
   `git clone https://github.com/cursor/plugins ~/pstack-src`, then
   `git -C ~/pstack-src checkout c742dd61706fbacc1b95f1a20b7b988cf096ce6c`.
   `<pstack>` is `~/pstack-src/pstack`.
2. Copy the skills: `mkdir -p ~/.coder/skills && cp -R ~/pstack-src/pstack/skills/. ~/.coder/skills/`.
   The workspace agent reads `~/.coder/skills/<name>/SKILL.md`. Start a new
   chat after you copy them.
3. Copy this page to `~/.coder/pstack-adapter.md` and add this line to
   `~/.coder/AGENTS.md`: `pstack skills use Cursor names. Before you follow
   one, read ~/.coder/pstack-adapter.md.` Coder Agents loads
   `~/.coder/AGENTS.md` into every chat that uses the workspace.

## Tools

| Cursor                                                     | Coder Agents today                                                                                                                                                                    | Do this                                                                                                                                                       | Removed by                                                          |
|------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------|
| `Task`                                                     | `spawn_agent` starts a child and returns at once; `wait_agent` returns its final reply                                                                                                | Spawn with `spawn_agent`, then collect with `wait_agent`.                                                                                                     | Stays                                                               |
| `AskQuestion`                                              | `ask_user_question` exists only in plan-mode turns of a root chat                                                                                                                     | Outside plan mode, ask in plain text and end the turn.                                                                                                        | W0b (async questions in web UI chats), W2 (questions from children) |
| Open a todolist                                            | No todo tool                                                                                                                                                                          | Keep the list in your reply and update it at each phase. Restate it after a compaction.                                                                       | W4                                                                  |
| Run another pstack skill (`/name` or "the **name** skill") | 57 of pstack's 58 skills set `disable-model-invocation: true`, so `read_skill` and the skill index hide them from the model. A person can still run one from the composer's `/` menu. | `read_file` `~/.coder/skills/<name>/SKILL.md` and follow it. Read its sibling files with `read_file` too. When you tell the user to type it, write `/<name>`. | Stays                                                               |
| `Grep`, `Glob`                                             | `execute`, `read_file`                                                                                                                                                                | `execute` with `rg` or `find`.                                                                                                                                | Stays                                                               |
| Available-tools map, `mcps/`                               | MCP tools sit behind `find_tools`                                                                                                                                                     | Call `find_tools` to find a tool by name or purpose.                                                                                                          | Stays                                                               |

## Subagent fields

| Cursor                                        | Coder Agents today                                                                                                   | Do this                                                                                                                                                       | Removed by |
|-----------------------------------------------|----------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `subagent_type: generalPurpose`               | Types `general`, `explore`, `computer_use`                                                                           | `type: "general"`.                                                                                                                                            | Stays      |
| `poteto-agent`, `comment-sicko`               | No custom types                                                                                                      | `type: "general"`. Paste the body of `<pstack>/agents/<name>.md`, without its frontmatter, at the top of `prompt`.                                            | W1         |
| `readonly: true`                              | `explore` has `read_file`, `execute`, `process_output`, `read_skill` and `read_skill_file`, and no file-writing tool | `type: "explore"` for narrow code discovery. For other read-only work use `general`, say "do not edit files" in the prompt, and check `git status` afterward. | Stays      |
| `readonly: false`                             | `general`                                                                                                            | `type: "general"`.                                                                                                                                            | Stays      |
| A subagent that spawns its own workers        | A child cannot spawn: "delegated chats cannot create child subagents"                                                | The root spawns every worker itself and does the sub-coordinator's job.                                                                                       | W2         |
| More than 10 live subagents                   | 10 concurrent subagents per deployment by default                                                                    | Fan out in waves of at most 10.                                                                                                                               | W2         |
| `environment: "cloud"`, a worktree per worker | A child shares its parent's workspace and working directory                                                          | Run writers one at a time, or tell each writer to `git worktree add` its own checkout on its own branch and work only there.                                  | W3         |
| `environment: "local"`                        | Not needed                                                                                                           | Omit.                                                                                                                                                         | Stays      |
| `cloud_base_branch: <b>`                      | No base branch argument                                                                                              | Tell the worker to create its worktree from `<b>`.                                                                                                            | W3         |
| `run_in_background: true`                     | `spawn_agent` never blocks                                                                                           | Omit. Call `wait_agent` when you need the result.                                                                                                             | Stays      |
| `resume`                                      | `message_agent` wakes an idle child with a follow-up                                                                 | `message_agent`, then `wait_agent`.                                                                                                                           | Stays      |
| `file_attachments`                            | No field                                                                                                             | List the paths in the prompt.                                                                                                                                 | Stays      |

## Models

| Cursor                                                                 | Coder Agents today                                                                                                              | Do this                                                                                                                                                                                                                          | Removed by         |
|------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------------|
| A model slug, such as `claude-opus-5-5-xhigh` or `grok-4.7-xhigh-fast` | `spawn_agent` takes `model_config_id` (a UUID from `list_subagent_models`) and `reasoning_effort` (`none` to `max`); no aliases | Call `list_subagent_models` and pass the config whose model matches the slug's family, with the slug's effort as `reasoning_effort`. If none matches, omit `model_config_id` so the child runs on the default model, and say so. | W1 (model aliases) |
| `auto` or `inherit-parent`                                             | Default model                                                                                                                   | Omit `model_config_id`.                                                                                                                                                                                                          | Stays              |
| The `pstack-models.mdc` rule, `~/.cursor/rules/pstack-models.mdc`      | No rules directory; `~/.coder/AGENTS.md` loads into every chat                                                                  | Keep the role lines in `~/.coder/AGENTS.md` (see setup-pstack). If the file or the role's line is missing, use the skill's default slug.                                                                                         | W6                 |
| A role value `<model> <effort>`                                        | As above                                                                                                                        | `model_config_id` of `<model>` and `reasoning_effort: <effort>`.                                                                                                                                                                 | Stays              |

## setup-pstack

Follow its steps with these changes:

- Detect. The detected models are the configs that `list_subagent_models`
  returns. Write each as `<model_config_id> <effort>`. The effort is the last
  token, so the budget step works as written.
- Defaults. Map each default slug with the Models rows.
- Write. Put the comment lines, the `# budget` line and one line per role
  between `<!-- pstack-models -->` and `<!-- /pstack-models -->` in
  `~/.coder/AGENTS.md`. Replace only that block; the file holds other
  instructions.

## Paths

| Cursor                                                        | Coder Agents today                                                                                                                                               | Do this                                                                                             | Removed by |
|---------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------|------------|
| `.cursor/skills/`, `~/.cursor/skills/`                        | Skills under `skills/`, `.agents/skills/`, `.claude/skills/` and `.codex/skills/` of each scan root, including `~/.coder/skills/`; `.cursor/skills/` is not read | Use `~/.coder/skills/` or the repository's `.agents/skills/`.                                       | Stays      |
| `.cursor/agents/`, `<pstack>/agents/`                         | Not read                                                                                                                                                         | See `poteto-agent` under Subagent fields.                                                           | W1         |
| `~/.cursor/plugins/`, `.cursor-plugin/plugin.json`            | `~/.claude/plugins/cache` is watched, but plugin manifests are ignored                                                                                           | Follow Setup.                                                                                       | W6         |
| `.cursor/rules/*.mdc`, always-applied rules                   | `AGENTS.md`, `CLAUDE.md` and `.cursorrules` at the top of each scan root; no `.mdc` files                                                                        | Put an always-applied rule's text in `AGENTS.md` or `~/.coder/AGENTS.md`.                           | W6         |
| `https://github.com/cursor/plugins/blob/main/pstack/<path>`   | Not needed                                                                                                                                                       | Read `<pstack>/<path>` and never fetch it. Give the user the URL when they need a link.             | Stays      |
| Transcripts in `~/.cursor/projects/<slug>/agent-transcripts/` | No transcript tool. `wait_agent` returns only a child's final reply. Root chats in a project have `read_memory` and `save_memory`.                               | Skip transcript reads with the skip line below. Use project memory, `git` and `gh` for prior state. | W4         |

## Missing and Cursor-only features

When a step needs a feature that Coder Agents lacks, put one line in your reply,
`Skipped in Coder Agents: <step>. <reason>.`, then go on to the next step. Do not
stop, ask, or retry. When a later step needs what the skipped step made, such as
a URL or a key, leave a placeholder and name it in the line.

| Cursor                                                                                                   | Coder Agents today                                         | Do this                                                                                                                                                      | Removed by            |
|----------------------------------------------------------------------------------------------------------|------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------|
| Sticky `/poteto-mode`                                                                                    | No pinned skills                                           | Read `poteto-mode` again at the start of each turn while the mode is on.                                                                                     | W6                    |
| `/loop`, watchers that wake you, PR and CI subscriptions                                                 | A chat runs only when a message arrives                    | Wait inside the turn (`wait_agent` timeouts, `gh pr checks --watch`). Skip a step that needs a later wake, and tell the user to send the next message.       | W5                    |
| `SendToUser`                                                                                             | No notification tool                                       | Put the message in your reply.                                                                                                                               | W5 (`notify_user`)    |
| `SendToUser` secret requests                                                                             | No masked input                                            | Skip with the line. Ask the user to put the secret in the workspace environment.                                                                             | W2 (secret questions) |
| Cursor Automations: routines, `update_state` calls, `api2.cursor.sh/automations` webhooks, routine wakes | None                                                       | Skip with the line.                                                                                                                                          | Stays                 |
| Bugbot                                                                                                   | Bugbot comments on a GitHub PR are ordinary review threads | Triage them with the forge command that the step gives. Skip a step that starts, waits for, or configures Bugbot when the PR has no Bugbot check or comment. | Stays                 |
| Cursor cloud agents                                                                                      | None                                                       | See `environment: "cloud"`. Skip a step that needs the cloud agent itself, such as its dashboard.                                                            | Stays                 |
| A built-in PR tool                                                                                       | No PR tool                                                 | Use the forge command that the step gives, such as `gh pr create`. That is not a skip, so write no line.                                                     | Stays                 |
| Origin CLI, image generation                                                                             | None                                                       | Skip with the line.                                                                                                                                          | Stays                 |

## Evidence

Checked against fork `main` at `b3a57df790`:

- Subagent types and `spawn_agent` fields: `coderd/x/chatd/subagent_catalog.go:40-46`, `:55-159`.
- No spawn from a child: `validateSubagentSpawnParent`, `subagent_catalog.go:254-262`.
- Child shares the parent workspace: `coderd/x/chatd/subagent_operations.go:465-470`.
- `wait_agent` returns the last assistant message: `subagent_operations.go:634-666`.
- Concurrency default: `coderd/x/chatd/agentadmission.go:13`.
- `ask_user_question` in plan-mode root turns only: `coderd/x/chatd/turn_environment.go:721-723`.
- Skill roots and containers: `agent/agentcontext/defaults.go:9-21`, `agent/agentcontext/resolve.go:66-71`.
- Instruction files: `resolve.go:48-52`.
- Hidden skills: `coderd/x/chatd/chattool/skill.go:55-62`, `turn_environment.go:2161-2177`.
- `find_tools`: `coderd/x/chatd/chattool/findtools.go:19`.
- Project memory for root chats: `coderd/x/chatd/memory.go:20-33`.
- No todo, wake-up, transcript, notification or PR tool: no matches in `coderd/x/chatd` for `todo_write`, `schedule_wakeup`, `read_chat_transcript`, `notify_user` or `pull_request`.
