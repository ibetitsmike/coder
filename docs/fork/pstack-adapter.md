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
    - SendToUser: put the message in your reply, and if you have notify_user, also call it with the message.
    - A transcript of your own chat, a child, or a chat ID you were given: read_chat_transcript, if you have it.
    - Recent chats, or a top-level chat ID you don't have: search_chats, if you have it.
    - A child's chat ID you don't have: list_agents in its parent.
    - /loop <interval> or a watcher: if you have schedule_wakeup, run the step once now, then call schedule_wakeup with interval_seconds.
      Set max_runs or expires_in_seconds, because no tool cancels a wake-up.
    - Background subagents you wait for across turns: if you have wait_for_events, call it and end the turn, then call wait_agent on the child that each wake-up message names.
    - A GitHub PR or its CI you wait for across turns: if you have wait_for_events, call it with pull_request_url and end the turn.
    - A secret from the user: in a child chat whose ask_user_question takes secret, ask one question with secret true, then write {{secret:<call ID>}} where the value belongs in execute, write_file, or edit_files.
    - Transcripts, recent chats, and wake-ups without those tools: write "Skipped in Coder Agents: <step>. <reason>." and go on.
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
| `AskQuestion`                                                           | `ask_user_question` in some root chats, and in children with `fork-nested-subagents`. Refer to [Questions](#questions)                                                                                              | If you have `ask_user_question`, use it. Otherwise ask in plain text and end the turn.                                                                                                                                                                                        | W2 (questions from children) |
| Open a todolist                                                         | `todo_write` and `todo_read`, only with the `fork-chat-todos` experiment. Refer to [Todo list](#todo-list)                                                                                                          | Send the whole list with `todo_write` and update it at each phase. Without the tools, keep the list in your reply and restate it after a compaction.                                                                                                                          | W4                           |
| Run another pstack skill (`/name` or "the **name** skill")              | 57 of pstack's 58 skills set `disable-model-invocation: true`, so `read_skill` and the skill index hide them from the model. You can still run one from the composer's `/` menu, but not in a chat's first message. | `read_file` `~/.coder/skills/<name>/SKILL.md`, with `~` replaced by your home directory, and follow it. Read its sibling files with `read_file` too. When you tell the user to type it, write `/workspace/<name>`, and in a new chat tell them to send a plain message first. | Stays                        |
| `Grep`, `Glob`                                                          | `execute`, `read_file`                                                                                                                                                                                              | `execute` with `rg` or `find`.                                                                                                                                                                                                                                                | Stays                        |
| Embed a screenshot or video inline, as `ketchup` and `ketchup-video` do | `attach_file`, for a workspace file of up to 10&nbsp;MiB. Refer to [Attachments](#attachments)                                                                                                                      | Save the file in the workspace, then call `attach_file` with its absolute path.                                                                                                                                                                                               | Stays                        |
| Available-tools map, `mcps/`                                            | When MCP servers are configured, their tools sit behind `find_tools`. It is absent when there are none, or when an MCP or client tool is itself named `find_tools`                                                  | If you have `find_tools`, call it to find a tool by name or purpose. If not, look for MCP tools in your tool list.                                                                                                                                                            | Stays                        |

### Questions

A root chat has `ask_user_question` in plan-mode turns, where the question ends the turn.
Outside plan mode, a root chat started from the web UI also has it.
There the call returns at once, and the answer arrives later as a user message, so keep working on what doesn't depend on the answer.
API chats outside plan mode don't have it.

With the `fork-nested-subagents` experiment, a child that isn't an `explore` child also has `ask_user_question`, except in plan-mode turns.
The call ends the child's turn, and the answer arrives as the child's next user message.
In the web UI, the root chat's page shows the oldest unanswered question from any descendant under "Question from" and the asking chat's title, with "1 of N" when more questions wait.
An answer there goes to the chat that asked, not to the root chat.
While the root chat has its own unanswered question, the page shows only that one, and a descendant's question appears after the user answers it.
If the child asks while you wait on it, `wait_agent` returns `needs_input: true` with the questions when the child's turn ends.
If you call `wait_agent` while the question is unanswered, it waits for the answer and then for the child's reply, and returns `needs_input: true` if its timeout comes first.
After `needs_input: true`, tell the user that the question waits on the root chat's page, then call `wait_agent` on the child again.
If the user answers in your chat instead, send the answer to the child with `message_agent`, then call `wait_agent` on the child again.
The user can also answer in the child's own chat, which the Agents sidebar lists under its parent.
An answer on the root chat's page or in the child's chat resumes only the child.
If your turn has ended, your chat continues only when the user sends you a message or a [wake-up](#wake-ups) runs, and then `wait_agent` returns the child's reply.
`wait_for_events` doesn't count a child with an unanswered question as done, so its message arrives after the user answers and the child's turn ends.
Without the experiment, child chats don't have `ask_user_question`.

With the experiment, a child that isn't a computer-use child can also ask for a value that it must not see, such as a token or a password.
Root chats can't ask for one.
Send one question with `secret: true`, a `header`, a `question`, and no options, or the call fails, for example with `a secret question takes no options`.
The user types the value into a masked field on the root chat's page and selects **Submit**, or selects **Decline**.
The value must be from 8&nbsp;bytes to 16&nbsp;KiB.
The child's next user message says that the user answered, or that the user declined.
Write `{{secret:<call ID>}}`, with the ID of the `ask_user_question` call, where the value belongs in `execute`, `write_file`, or `edit_files` input, and the tool gets the value.
The call's result and the answer message both name this placeholder.
Other tools, such as `spawn_agent` and `message_agent`, get the placeholder, not the value.
While the chat holds an answer, its tool results and errors show the placeholder in place of the value, and they leave out images, documents, and attachments.
That scrub finds the value as it is, shell-quoted, or escaped in JSON, so a command that encodes the value, such as with `base64`, can still show it.
`execute` refuses a value that holds `[` followed by `$` or a backtick.
An answer stays in the chat for 24&nbsp;hours, and a later question doesn't remove it.
Only the root chat's page can answer a secret question.
`message_agent` to the child fails with `cannot message the child agent: it waits for a secret answer, which only the user can give in the root chat`.
A message or an edit in the child's own chat fails with `The chat's question takes a secret answer, which a chat message cannot carry.`, and the child's chat shows `Answer this secret question in the question panel of the root chat.` on the question.
`wait_agent` returns `needs_input: true` with a note that only the user can give the value, so tell the user to answer in the question panel on the root chat's page.
Never ask the user to paste the value into a chat, because a message in the root chat puts the value in the transcript and doesn't answer the question.

### Todo list

`todo_write` and `todo_read` exist only when the deployment enables the `fork-chat-todos` experiment.
Plan-mode turns and `explore` children don't get them.
An item's status is `pending`, `in_progress`, `done`, or `cancelled`.
A `todo_write` with any other status, such as `completed`, fails and stores nothing.
The list survives a compaction.
The web UI shows a non-empty list in a pill in the composer.
In the conversation, the web UI shows each `todo_write` and `todo_read` call as a card, such as **Updated todos (1/3 done)**.
The card of a call that returns a non-empty list expands to the list, and the card of a failed call expands to its error.
With the experiment, `list_agents` returns each child's list in `todos`, and its card shows the child's progress, such as **1/3 todos done**.

### Attachments

`attach_file` copies a workspace file into the chat, so the user can open or download it from the conversation.
A file over 10&nbsp;MiB fails with `attachment exceeds 10 MiB size limit`.
The web UI shows a raster image, such as a PNG or a JPEG, as a thumbnail.
Plain text, Markdown, CSV, and JSON open in a text preview.
With the `fork-chat-todos` experiment, the web UI shows an MP4 as a player with a download link, both inline and in the turn's **Attachments** menu.
Without the experiment, an MP4 shows as a download card, as an SVG does.
Coder Agents doesn't send an attached file back to the model in later turns, so describe in your reply what the file shows.

### Wake-ups

When the deployment enables the `fork-chat-wakeups` experiment, a chat has `schedule_wakeup`, `wait_for_events`, and [`notify_user`](#notifications), except in plan-mode turns and in `explore` children.
A [custom agent tool policy](#custom-agent-tool-policy) can also remove them from a custom child.
A wake-up sends its `prompt` to the chat later, as a new user message from the chat owner.
Set exactly one of `delay_seconds`, `interval_seconds`, and `cron`, or the call fails with `set exactly one of delay_seconds, interval_seconds and cron`.
`delay_seconds` runs the wake-up once.
`interval_seconds` runs it every interval, starting one interval from now.
`cron` runs it on a 5-field schedule, such as `0 9 * * 1-5`, in `timezone`, an IANA time zone that defaults to UTC.
A delay can be up to 30&nbsp;days, and an interval from 1&nbsp;minute to 30&nbsp;days.
`max_runs` stops the wake-up after that many runs, and `expires_in_seconds` stops it that many seconds from now.
The prompt can hold up to 4000 bytes and can't start with `/loop`.
A chat can have at most 10 active or paused wake-ups.
The result holds the wake-up's `id`, `kind`, and `next_run_at` in its time zone, and `expires_at` and `max_runs` when you set them.

A message that holds only text and starts with `/loop` runs its prompt at once and then again on an interval.
Write it as `/loop [interval] <prompt>`.
The interval is a number followed by `s`, `m`, `h`, or `d`, such as `1h`, and defaults to `10m`.
The chat stores the message as `<prompt>`, and the wake-up sends `<prompt>` at each run.
`/loop` also works as a chat's first message.
A `/loop` without a prompt fails with `usage: /loop [interval] <prompt>`.

`wait_for_events` wakes the chat when its child agents are done, or when a GitHub pull request has new activity, so you can end your turn instead of waiting in `wait_agent`.
Call it, then end your turn.
`child_chat_ids` names up to 50 direct children of the chat, and defaults to every child that is still working.
A child is done when it's archived, or when it isn't running, interrupting, or paused, and it has no unanswered question and no `wait_for_events` wake-up of its own that still waits.
A child that you name is watched even when it's already done.
For each watched child, the chat gets one user message after that child is done, made of the `prompt`, an empty line, and `Child agent "<title>" (<id>) is done with status <status>.`
The default prompt asks you to read the child's result with `wait_agent` and continue, and a `prompt` you set has the limits of a `schedule_wakeup` prompt.
The chat gets the next message only after the turn that the previous message started has ended.
`expires_in_seconds` can be from 60&nbsp;seconds to 30&nbsp;days, and defaults to 24&nbsp;hours.
After it expires, the chat gets no message for children that weren't done.
The result holds the wake-up's `id`, `expires_at`, and `children`, with the `chat_id`, `title`, and `status` of each watched child.
A call leaves out children that an earlier call still watches, and fails with `these child agents are already watched by wake-up <id>: end your turn to wait for them` when it leaves out all of them.
With no child to watch, the call fails with `no running child agents to wait for`, and with more than 50 it fails with `cannot wait for <n> child agents at once, the maximum is 50`.
A `child_chat_ids` entry that isn't a child of the chat fails with `chat <id> is not a child of this chat`.
Each call counts toward the limit of 10 active or paused wake-ups.

With `pull_request_url`, `wait_for_events` also watches a GitHub pull request, such as `https://github.com/org/repo/pull/7`.
Without `child_chat_ids`, the call watches only the pull request, and with `child_chat_ids`, it watches both.
`pull_request_events` sets what wakes the chat, any of `review`, `comment`, `checks`, and `closed`, and defaults to all four.
`review` counts a submitted review, and `comment` counts a new comment on the pull request or on its code, by anyone other than the chat owner's linked GitHub account.
A comment from before the call doesn't count, even when someone edits it later.
`checks` counts CI that passed or failed after the call, on a new head commit, with a new result, or after a rerun.
`closed` counts a close or a merge.
The chat gets one message for the pull request, made of the `prompt`, an empty line, `Pull request <url> has new activity:`, and one line for each review and comment, such as `- review by <login>: <state> (<url>)` or `- comment by <login> (<url>)`.
After 20 such lines, the list ends with `- and <n> more`.
With `checks`, the message adds `- checks: <state>, <failed> of <total> failed, on <commit>`, with the first 12 characters of the head commit.
With `closed`, it adds `- state: closed` or `- state: merged` when the pull request isn't open.
The message leaves out review and comment bodies, so open the listed URLs to read them.
After that message, the call stops watching the pull request, so call `wait_for_events` again to keep watching.
When the call watches a pull request, the default prompt is `Something you were waiting for happened. Check it, then continue.`
The result also holds `pull_request`, with its `url`, the watched events in `on`, and the `head_sha` and `checks_state` at the time of the call.
Coder Agents reads the pull request once a minute with the chat owner's linked GitHub account, and checks the call's children once a minute too until the pull request's message arrives.
A failed read doesn't end the watch.
Coder Agents reads again after 5&nbsp;minutes, or after a GitHub rate limit resets, but at most 5&nbsp;minutes later while children still wait.
Activity after `expires_at` doesn't count.
The call reads the pull request before it returns, and fails with `read pull request <url>: <error>` when it can't.
The error can be `no git provider is configured for <url>` when no external auth provider of the deployment matches the URL, `<url> is not a pull request URL`, `the chat owner has no linked account for <host>`, or `pull request activity is only available for GitHub`.
A pull request that is already closed or merged fails with `pull request <url> is already closed` or `pull request <url> is already merged`.
`pull_request_events` without `pull_request_url` fails with `pull_request_events requires pull_request_url`, and an unknown event fails with `unknown pull_request_events value "<value>", valid values are review, comment, checks, closed`.

Coder Agents checks for due wake-ups every 15&nbsp;seconds, so expect a run to arrive some seconds after its time.
A wake-up never interrupts a turn.
While the chat is busy, the run waits until the chat is idle.
Coder Agents doesn't replay runs that it missed.
No tool, API, or page lists or cancels a wake-up.
To stop one early, archive the chat, and the wake-up stops at its next run without sending its prompt.

Before a run sends its prompt, Coder Agents checks four limits over the 24&nbsp;hours before the run.
A chat can get 288 wake-up runs in those 24&nbsp;hours, an average of one every 5&nbsp;minutes but not a fixed pace, and the chats of an organization can get 2880 together.
The spend limits are \$20 of model cost in the chat and \$200 across the organization's chats that have or had a wake-up.
The spend counts every message in those chats, not only the ones that wake-ups started, and a model without prices counts as zero.
The check only uses the cost recorded so far, so the run that it lets through can take the spend past a limit, and runs on several replicas at once can pass an organization limit by a few runs.
When the chat or its organization has reached a limit, the wake-up pauses without sending its prompt.
A wake-up also pauses when a prompt hook denies its prompt, or when its cron schedule or its events are invalid.
A paused wake-up never runs again, and it still counts toward the chat's limit of 10 active or paused wake-ups.
When a wake-up pauses, Coder Agents sends the chat owner a Coder notification titled `Chat wake-up paused` that names the chat and the reason.
It sends at most one per chat and reason each UTC day, unless the chat gets a new title in between, because the notification text includes the title.
It uses the **Custom Notification** template, so the owner doesn't get it after turning that template off, and the wake-up stays paused all the same.

### Notifications

With the `fork-chat-wakeups` experiment, `notify_user` sends the chat owner a Coder notification.
The owner gets it in the Coder notifications inbox when the deployment keeps the inbox on, and by email or webhook when the deployment sends notifications that way.
Pass the text as `message`, up to 2000 bytes, and an optional `title`, up to 120 bytes.
When you omit `title` or leave it blank, the notification uses the chat title, cut to its first 120 bytes.
The result is `{"sent":true}`.
When the chat has sent 10 notifications in the past hour, the next call fails with `notification limit reached: at most 10 per chat per hour`.
The wake-up pause notifications of the chat count toward those 10, but Coder Agents still sends a pause notification when the chat is at the limit.
An empty `message` fails with `message is required`, and a chat that a system user owns fails with `chats owned by a system user cannot send notifications`.
The result is also `{"sent":true}` when Coder drops the notification, because the owner turned off the **Custom Notification** template, the chat sent the same title and message earlier in the same clock minute, or the deployment has the inbox off and sends no email or webhook notifications.

## Subagent fields

| Cursor                                        | Coder Agents today                                                                                                                                                                                                                                                                                                        | Do this                                                                                                                                                                                                                                                                                                                                       | Removed by |
|-----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `subagent_type: generalPurpose`               | Types `general`, `explore`, `computer_use`, and custom types with the `fork-agent-definitions` experiment                                                                                                                                                                                                                 | `type: "general"`.                                                                                                                                                                                                                                                                                                                            | Stays      |
| `poteto-agent`, `comment-sicko`               | Custom types only with the `fork-agent-definitions` experiment. Refer to [Custom agent types](#custom-agent-types)                                                                                                                                                                                                        | If the `spawn_agent` description lists `<name>` or `workspace/<name>`, pass that type. Otherwise `type: "general"`, and paste the body of `<pstack>/agents/<name>.md`, without its frontmatter, at the top of `prompt`.                                                                                                                       | W1         |
| `readonly: true`                              | `explore` has `read_file`, `execute`, `process_output`, `read_skill`, `read_skill_file` and the deployment MCP tools allowed in subagents, and no file-writing tool                                                                                                                                                       | `type: "explore"` for narrow code discovery, else `type: "general"`. Neither type enforces read-only, because `execute` can change files. Say "do not edit files or run commands that change state" in the prompt, and check `git status` afterward.                                                                                          | Stays      |
| `readonly: false`                             | `general`                                                                                                                                                                                                                                                                                                                 | `type: "general"`.                                                                                                                                                                                                                                                                                                                            | Stays      |
| A subagent that spawns its own workers        | Only with the `fork-nested-subagents` experiment, up to a depth limit. Refer to [Nested subagents](#nested-subagents)                                                                                                                                                                                                     | If the child has `spawn_agent`, it spawns and collects its own workers. Otherwise the root spawns every worker itself and does the sub-coordinator's job.                                                                                                                                                                                     | W2         |
| More than 10 running subagents                | By default, at most 10 subagent turns run at once across the deployment, and more with an Agent Runtime Hours entitlement or the `fork-nested-subagents` experiment. Children over the cap wait for a free slot. Refer to [Subagent limits](#subagent-limits)                                                             | Spawn them all. Expect `wait_agent` to take longer when other chats hold slots.                                                                                                                                                                                                                                                               | W2         |
| `environment: "cloud"`, a worktree per worker | A child shares its parent's workspace and working directory. With the `fork-subagent-isolation` experiment, `spawn_agent` can give the child its own git worktree or its own new workspace, and the child works in it. Refer to [Worktree isolation](#worktree-isolation) and [Workspace isolation](#workspace-isolation) | With the experiment, spawn each writer with `isolation: "worktree"`, or with `isolation: "workspace"` when it needs a machine of its own, and then tell it which branch to clone and to push its work. Otherwise run writers one at a time, or tell each writer to `git worktree add` its own checkout on its own branch and work only there. | W3         |
| `environment: "local"`                        | Not needed                                                                                                                                                                                                                                                                                                                | Omit.                                                                                                                                                                                                                                                                                                                                         | Stays      |
| `cloud_base_branch: <b>`                      | `base_branch` only with `isolation: "worktree"` and the `fork-subagent-isolation` experiment                                                                                                                                                                                                                              | With the experiment, pass `base_branch: "<b>"` with `isolation: "worktree"`. Otherwise tell the worker to create its worktree from `<b>`.                                                                                                                                                                                                     | W3         |
| `run_in_background: true`                     | `spawn_agent` never blocks. With the `fork-chat-wakeups` experiment, `wait_for_events` wakes the parent when a child is done                                                                                                                                                                                              | Omit. Call `wait_agent` when you need the result. To end the turn while children work, call `wait_for_events` if you have it, then end the turn, and call `wait_agent` on the child that each wake-up message names.                                                                                                                          | Stays      |
| `resume`                                      | `message_agent` wakes an idle child with a follow-up                                                                                                                                                                                                                                                                      | `message_agent`, then `wait_agent`.                                                                                                                                                                                                                                                                                                           | Stays      |
| `file_attachments`                            | No field                                                                                                                                                                                                                                                                                                                  | List the paths in the prompt.                                                                                                                                                                                                                                                                                                                 | Stays      |

### Custom agent types

When the deployment enables the `fork-agent-definitions` experiment, `spawn_agent` accepts a custom type for each agent definition that the workspace agent finds.
It reads the `*.md` files in `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root, including `~/.coder/agents/`.
A chat pins the definitions when it starts, like `AGENTS.md` and the skills, so start a new chat or select **Refresh context** after you add one.
`GET /api/v2/chats/{chat}` lists each definition file that the chat pinned in `context.resources`, with kind `subagent` and its `status`.
An `ok` file also reports `subagent_name`, `subagent_description`, and any parse warnings in `error`.
A file with any other status, such as `invalid`, reports the reason in `error` and adds no custom type, so check there when a type is missing from `spawn_agent`.
The context usage indicator in the chat composer lists the same files under **Agent definitions**, with each `ok` definition's warnings under its name and the other files as issues.
A definition with warnings turns the indicator to its warning color.
The `spawn_agent` description lists the custom types after the built-in ones.
The child gets the definition's body as a system message, so do not paste the body into `prompt`.
A definition without `model` runs on your model, and pstack's agents set none.
A `model` that matches no config in `list_subagent_models` fails the spawn.
An explicit `model_config_id` overrides the definition's `model`, and an explicit `reasoning_effort` overrides a valid definition effort.
A definition whose effort isn't a valid reasoning effort fails every spawn.
The experiment also lets `type: "computer_use"` take `model_config_id` and `reasoning_effort`.
That config must support computer use, and it replaces the organization's computer use model and its reasoning effort.
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
The chat list, `GET /api/v2/chats/{chat}`, and the `created` event of `GET /api/v2/chats/watch` report each child chat's type in `agent_type`, and the Agents page shows it as a badge next to the child's title, except for `general`.
In the conversation, the `spawn_agent` card of a custom child shows **Agent type:** and the name.
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
With the experiment, the chat list and a root chat's `GET /api/v2/chats/{chat}` include every descendant in the root's `children` list, and each entry's `parent_chat_id` names its parent.
The Agents sidebar shows each worker under its coordinator, and selecting a descendant opens its own `/agents/:id` page.
Without the experiment, or at the maximum depth, a child has no `spawn_agent`, and a call fails with `Tool not active in this turn: spawn_agent`.

### Subagent limits

By default, at most 10 subagent turns run at once across the deployment, and a child over that cap waits for a free slot.
An Enterprise deployment doesn't have that cap while its Agent Runtime Hours entitlement is enabled and below its hard limit.
A family is a root chat and all of its descendants.
With the `fork-nested-subagents` experiment, an administrator can also limit running subagents with `--chat-max-concurrent-subagents` across the deployment and `--chat-max-concurrent-subagents-per-family` in one family.
A child over one of these limits waits for a free slot, like a child over the default cap.
While a `running` child waits for a slot, `list_agents` shows it with `waiting_for_capacity: true`, and its card marks the child **waiting for capacity**.
When you expand your root chat in the Agents sidebar, each of its waiting subagents shows an hourglass and **Waiting for a free subagent slot**.
The top bar of a waiting subagent's own chat shows the same hourglass and label, or only the hourglass on a narrow screen.
With the experiment, a child's own ancestors don't count against the default cap or these limits, so a coordinator that waits in `wait_agent` doesn't block its workers.
Because of that, more than 10 subagent turns can run at once.

With the experiment, `--chat-max-live-subagents-per-family` limits how many subagents in one family are live at once.
A subagent is live while it isn't archived and its status is `running`, `interrupting`, `requires_action`, or `paused`.
A child that finished doesn't count.
At the limit, `spawn_agent` fails with `cannot create a child agent: this chat family has` and the count, instead of queueing the child.
With the experiment, the card of a failed `spawn_agent` call shows its error.
While the family is at the limit, the Agents sidebar shows a chip such as **live 2/2** next to the title of the expanded root chat.
For a root chat, `GET /api/v2/chats/fork/{chat}/family` returns the family's `live_subagents`, its `max_live_subagents` limit, where 0 means no limit, and the IDs of its subagents that wait for a slot in `waiting_for_capacity`.
For a child chat, it fails with status 400.
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

### Worktree isolation

When the deployment enables the `fork-subagent-isolation` experiment, `spawn_agent` also takes `isolation`, `repo_path`, and `base_branch`.
With `isolation: "shared"`, the default, the child works in its parent's workspace and working directory.
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
A relative `workdir` of `execute`, and a relative path of `read_file`, `write_file`, `edit_files`, or `attach_file`, resolves against the worktree.
An absolute path or an absolute `workdir` still reaches the rest of the workspace.
With `fork-nested-subagents`, a `shared` child of the worktree child works in the same worktree, and its system message says so.

`repo_path` or `base_branch` without `isolation: "worktree"` fails with `repo_path and base_branch need isolation worktree`.
`isolation: "worktree"` fails for a `computer_use` child, in a chat without a workspace, and when you omit `repo_path`, the parent works in no worktree, and the workspace agent has no directory.
If the workspace agent is older than the server, `spawn_agent` fails with `this workspace's agent does not support worktree isolation`, so restart the workspace or spawn with `isolation: "shared"`.
If the spawn fails after `spawn_agent` creates the worktree, Coder Agents removes the worktree and its branch.
Otherwise Coder Agents keeps both until you archive the root chat or the deployment archives it as inactive.
If the experiment is still on at the archive, Coder Agents then removes each child's worktree and branch in the background.
It keeps a worktree that has uncommitted or untracked files, is no longer on its branch, or has submodule commits that are on no remote.
It also keeps a worktree when its branch, or the `HEAD` or branch reflog, reaches a commit that is on no other branch or remote, such as a commit that a reset moved the branch away from.
It keeps them all when it can't reach the workspace agent or the agent is older than the server.
Then it posts one notice in the root chat that lists the path, branch, and reason of each worktree it kept.
So merge or push each child's branch, push its submodule commits, and put any commit that a reset dropped on another branch before you archive the root chat.
To remove a kept worktree yourself, run `git worktree remove <worktree_path>` and `git branch -D <branch>` in the repository.
Add `--force` to `git worktree remove` when the worktree still has uncommitted or untracked files.
Without the experiment, `spawn_agent` has no `isolation`, `repo_path`, or `base_branch` field.

### Workspace isolation

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
`repo_path` or `base_branch` with it fails with `repo_path and base_branch need isolation worktree`.
When the build fails, or the agent or its startup scripts don't get ready, `spawn_agent` fails with `create the child's workspace: <result>`, where `<result>` is the `create_workspace` result.
It fails the same way before any build when the parent workspace's template doesn't allow Coder Agents or has an externally managed agent, so spawn the child with `isolation: "shared"` or `isolation: "worktree"` in such a workspace.
After a failed spawn, Coder Agents archives the child and tries once to stop the workspace, if it created one, but it doesn't retry a failed archive or stop and never deletes the workspace.
So after a failed spawn, check the **Workspaces** page and delete any workspace that the spawn created.
When the spawn succeeds, Coder Agents doesn't stop the child's workspace until you archive the root chat or the deployment archives it as inactive.
The workspace can still stop earlier on its own schedule, such as the deployment's chat workspace TTL or the template's default autostop.
If the experiment is still on at the archive, Coder Agents then stops each child's workspace in the background, after it tries to remove the family's worktrees, but it never deletes the workspace.
A worktree that it keeps doesn't prevent the stop, even when the worktree is in that workspace.
It doesn't retry a failed stop, and it skips a child that is bound to another workspace by then.
Delete the workspace on the **Workspaces** page or with `coder delete <workspace_name>` when you no longer need it.

## Models

| Cursor                                                                 | Coder Agents today                                                                                                                                                                                                                                                 | Do this                                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Removed by |
|------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| A model slug, such as `claude-opus-5-5-xhigh` or `grok-4.7-xhigh-fast` | `spawn_agent` takes `model_config_id` (a UUID from `list_subagent_models`) and `reasoning_effort` (`none` to `max`). With `fork-agent-definitions`, an agent definition's `model` can also name an alias that `list_subagent_models` shows in a config's `aliases` | Drop the slug's effort and `-fast` suffixes. Call `list_subagent_models` and pass the config whose `model` names the same model and version, ignoring any provider prefix, with the slug's effort as `reasoning_effort`. If the slug had `-fast`, pick that model's fast variant when one is listed. If none matches, omit `model_config_id` (refer to the next row) and say so. In an agent definition, set `model` to a listed alias, such as `fast`, instead of a slug. | Stays      |
| `auto` or `inherit-parent`                                             | Omitting `model_config_id` runs the child on your model, unless the user or deployment set a subagent model for its type, in which case it runs on that model                                                                                                      | If you know your own model's config in `list_subagent_models`, pass it as `model_config_id`, so a subagent model setting can't replace it. Otherwise omit `model_config_id`.                                                                                                                                                                                                                                                                                               | Stays      |
| The `pstack-models.mdc` rule, `~/.cursor/rules/pstack-models.mdc`      | No rules directory, but `~/.coder/AGENTS.md` loads into every chat                                                                                                                                                                                                 | Keep the role lines in `~/.coder/AGENTS.md` (refer to setup-pstack). If the file or the role's line is missing, use the skill's default slug.                                                                                                                                                                                                                                                                                                                              | W6         |
| A role value `<model> <effort>`                                        | As above                                                                                                                                                                                                                                                           | `model_config_id` of `<model>` and `reasoning_effort: <effort>`. Omit `reasoning_effort` when the config lists no `reasoning_efforts`. When a step compares model families, use the config's `model` from `list_subagent_models`, not its UUID.                                                                                                                                                                                                                            | Stays      |

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
In the conversation, the web UI shows each `search_chats` call as a card, such as **Found 3 chats**, that expands to the query and a link to each chat with its status.
It shows each `read_chat_transcript` call as a card, such as **Read 4 messages from** and the chat's title, that expands to a link to the chat and one line per message with its role and a preview.
A card says **More chats available** or **More messages available** when the tool has another page, and the card of a failed call expands to its error.
Plan-mode turns and `explore` children don't get either tool.
With the same experiment, `wait_agent` also takes `include_tool_calls`, from 1 to 20, and adds up to that many of the child's latest tool calls, oldest first, to its reply.
Its card then shows them under a line such as **2 recent tool calls** that expands to each call's tool name and arguments, and shows the `tool_calls_error` text when Coder can't read them.
Without the experiment, there is no transcript or chat search tool.
Root chats in a project have `read_memory` and `save_memory`, but Setup creates no project.

## Missing and Cursor-only features

When a step needs a feature that Coder Agents lacks, put one line in your reply, `Skipped in Coder Agents: <step>. <reason>.`, then go on to the next step.
Do not stop, ask, or retry.
When a later step needs what the skipped step made, such as a URL or a key, leave a placeholder and name it in the line.

| Cursor                                                                                                   | Coder Agents today                                                                                                                                                                                                                                                                                             | Do this                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Removed by                          |
|----------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------|
| Sticky `/poteto-mode`                                                                                    | No pinned skills                                                                                                                                                                                                                                                                                               | Read `poteto-mode` again at the start of each turn while the mode is on.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | W6                                  |
| `/loop`, watchers that wake you, PR and CI subscriptions                                                 | With the `fork-chat-wakeups` experiment, `schedule_wakeup` and a `/loop` message wake the chat later with a prompt, and `wait_for_events` wakes it when a child agent is done or a GitHub PR gets a review, a comment, settled CI, or a close. Other forges have no PR wake-up. Refer to [Wake-ups](#wake-ups) | If you have `schedule_wakeup`, schedule the later step with a prompt that names it and its check, then end the turn. For `/loop 1h`, run the step once now, then pass `interval_seconds: 3600`. For a watcher, poll with `interval_seconds`, or call `wait_for_events` when it watches child agents or a GitHub PR, with `pull_request_url` for the PR. If that call fails, poll with `interval_seconds` instead. Always set `max_runs` or `expires_in_seconds`, and tell the prompt to reply only "done" once the check passes. Otherwise wait inside the turn (`wait_agent` timeouts, `gh pr checks --watch`), skip a step that needs a later wake, and tell the user to send the next message. | W5                                  |
| `SendToUser`                                                                                             | With the `fork-chat-wakeups` experiment, `notify_user` sends the chat owner a Coder notification. Refer to [Notifications](#notifications)                                                                                                                                                                     | Put the message in your reply. If you have `notify_user`, also call it with the message.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | W5                                  |
| `SendToUser` secret requests                                                                             | With `fork-nested-subagents`, a child's `ask_user_question` can ask for a secret that the user types into a masked field on the root chat's page. Root chats have no masked input. Refer to [Questions](#questions)                                                                                            | In a child chat whose `ask_user_question` takes `secret`, ask one question with `secret: true` and no options, then write `{{secret:<call ID>}}` where the value belongs in `execute`, `write_file`, or `edit_files`. Otherwise skip with the line and ask the user to put the secret in the workspace environment.                                                                                                                                                                                                                                                                                                                                                                               | W2 (secret questions in root chats) |
| Cursor Automations: routines, `update_state` calls, `api2.cursor.sh/automations` webhooks, routine wakes | None                                                                                                                                                                                                                                                                                                           | Skip with the line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Stays                               |
| Bugbot                                                                                                   | Bugbot comments on a GitHub PR are ordinary review threads                                                                                                                                                                                                                                                     | Triage them with the forge command that the step gives. Skip a step that starts, waits for, or configures Bugbot when the PR has no Bugbot check or comment.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Stays                               |
| Cursor cloud agents                                                                                      | None                                                                                                                                                                                                                                                                                                           | Refer to `environment: "cloud"`. Skip a step that needs the cloud agent itself, such as its dashboard.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Stays                               |
| A built-in PR tool                                                                                       | No tool that creates or changes a PR. With `fork-chat-wakeups`, `wait_for_events` only watches a GitHub PR                                                                                                                                                                                                     | Use the forge command that the step gives, such as `gh pr create`. That isn't a skip, so write no line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Stays                               |
| Recent work across chats, such as in `recall`                                                            | `search_chats` and `read_chat_transcript`, only with `fork-chat-todos`. Refer to [Chat transcripts](#chat-transcripts)                                                                                                                                                                                         | If you have `search_chats`, list your recent chats with an empty query and read the ones you need with `read_chat_transcript`. Otherwise skip the transcript step with the line. Either way, report prior state from `git log`, `git status` and `gh pr list --author @me --state all`, and never invent files or results.                                                                                                                                                                                                                                                                                                                                                                        | W4                                  |
| Origin CLI                                                                                               | `execute` runs it when it is installed                                                                                                                                                                                                                                                                         | Use it when `command -v origin` succeeds. Otherwise skip with the line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Stays                               |
| `/create-skill`                                                                                          | No skill-creation tool                                                                                                                                                                                                                                                                                         | For a project skill, write the repository's `.agents/skills/<name>/SKILL.md`. For a personal skill, write `~/.coder/skills/<name>/SKILL.md`. Give it frontmatter `name`, equal to the kebab-case directory name, and `description`.                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Stays                               |
| Image generation                                                                                         | No built-in image tool                                                                                                                                                                                                                                                                                         | Use an image tool if `find_tools` or your tool list offers one. Otherwise skip with the line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | Stays                               |

## Evidence

Checked against fork `main` at `4b0b4a0706`:

- Subagent types and `spawn_agent` fields: `coderd/x/chatd/subagent_catalog.go:40-46`, `:55-159`.
- Spawn from a child: root-chat tools only for root chats, `coderd/x/chatd/turn_environment.go:732-741`.
  Subagent tools for a `general` child only with `fork-nested-subagents` and below the depth limit, `coderd/x/chatd/fork_subagents.go:423-440`, `:454-482`.
  Direct children only, `isSubagentTarget`, `fork_subagents.go:487-499`.
  Depth flag, `codersdk/fork_chat_config.go:45-52`, lowered by the organization's `max_depth`, `coderd/x/chatd/fork_limits.go:56-58`.
  Inactive tool error, `coderd/x/chatd/chatloop/chatloop.go:1460`.
  Upstream server check, `validateSubagentSpawnParent`, `subagent_catalog.go:254-262`.
- Interrupt cascade and archive of an active family, only with `fork-nested-subagents`: `coderd/x/chatd/fork_cascade.go:43-46`, `:86-89`, 30-second wait, `:26`.
- Every descendant embedded under its root, only with `fork-nested-subagents`: `coderd/fork_chat_tree.go:30-37`, `:52-71`, from `coderd/exp_chats.go:584` and `:1890`, grouped by `root_chat_id`, `coderd/database/db2sdk/db2sdk.go:2311-2318`.
  Each descendant placed in the sidebar under its own parent, `site/src/pages/AgentsPage/components/ChatsSidebar/tree/chatTree.ts:51-60`.
- Agent definition directories: `agent/agentcontext/fork_agents.go:24-29`.
  Front-matter fields and the `general` default base type: `codersdk/workspacesdk/fork_agentdefinition.go:63-76`, `:99-103`, `:171-181`.
  Custom types only with `fork-agent-definitions`, listed after the built-in types, with built-in names winning: `coderd/x/chatd/fork_agent_definitions.go:37-45`, `:51-57`, `:108-118`, called from `subagent_catalog.go:185` and `:233`.
  The definition's body becomes the child's system prompt, `fork_agent_definitions.go:382`.
  The parent's model unless the definition sets `model`, an explicit `model_config_id` first, `:353-364`, and the model match, `:389-412`.
  A model alias as the last match, preferring the parent chat's AI provider, `coderd/x/chatd/fork_model_aliases.go`, listed by `list_subagent_models`, `subagent.go:409-411`.
  Aliases are set in a model config's `aliases`, `codersdk/chats.go:1953`, and checked on create and update, `coderd/fork_chat_model_aliases.go`.
  Definition files in the chat context, only with `fork-agent-definitions`, `coderd/x/chatd/context_prompt.go:321-325`, `:411-414`, `:444-445`, with the name, description, and warnings of an `ok` file, `fork_agent_definitions.go:414-431`, the status and reason of any other file, `context_prompt.go:359-368`, on the single-chat response, `coderd/exp_chats.go:1874-1875`, and custom types only from `ok` files, `fork_agent_definitions.go:158-161`.
  Listed in the context usage indicator, `site/src/pages/AgentsPage/components/ContextUsageIndicator.tsx:240-280`, `:393-406`, `:433-435`, `:721-727`, which turns to its warning color for a definition with warnings, `:446-458`.
  `computer_use` takes `model_config_id` and `reasoning_effort` only with the experiment, `coderd/x/chatd/subagent.go:618-620`, on a config that supports computer use, `fork_computer_use_overrides.go:36-77`.
- Custom agent tool policy, only with `fork-agent-definitions`: stored at spawn, `fork_subagents.go:309-317`, `:398`, loaded for child chats only, `coderd/x/chatd/fork_agent_tool_policy.go:175-178`, and applied in `buildTurnEnvironment`, `turn_environment.go:754-757`, `:946-948`.
  The filter, with `readonly` and `web_search`, `fork_agent_tool_policy.go:139-170`, Claude Code aliases, `:48-61`, MCP IDs, `:76-89`, and MCP servers, `:95-114`.
  Orchestration guidance dropped, `fork_subagents.go:514-516`, `fork_agent_tool_policy.go:123-127`, and the skill index dropped, `turn_environment.go:758-760`.
- Attachments: `coderd/x/chatd/chattool/attachfile.go:26-31`, built with the file tools, `coderd/x/chatd/turn_environment.go:710-713`, with the size limit, `coderd/x/chatd/chattool/attachment.go:17`, `:48-50`.
  Not sent back to the model, `coderd/x/chatd/chatprompt/chatprompt.go:106-109`.
  Text preview and image thumbnail, `site/src/pages/AgentsPage/components/ChatConversation/AttachmentBlocks.tsx:115-117`, `:637`, `:682-691`, raster images only, `coderd/x/chatfiles/mime.go:131-133`.
  MP4 player only with `fork-chat-todos`, `ForkAttachmentPreview.tsx:37-58`, also in the Attachments menu, `TurnAttachments.tsx:116-118`.
- Child shares the parent workspace: `coderd/x/chatd/subagent_operations.go:466-471`.
- Worktree isolation, only with `fork-subagent-isolation`: `coderd/x/chatd/fork_tools.go:49-55`, arguments, `coderd/x/chatd/fork_isolation.go:42-46`, `:82-88`, checks, `:101-115`, result fields, `:135-141`.
  Worktree creation and its errors, `fork_isolation.go:172-220`, `:245-247`, from the worktree that the parent works in when `repo_path` is omitted, `:181-188`, removed only when the spawn fails, `:223-236`, called at spawn, `coderd/x/chatd/fork_subagents.go:294`, `:333-346`.
  Removed after a root archive with the experiment on, `coderd/x/chatd/fork_family_lock.go:23-35`, also an automatic one, `coderd/x/chatd/auto_archive.go:148-152`, `coderd/x/chatd/fork_isolation_cleanup.go:48-139`, unless the agent refuses, `agent/agentgit/fork_worktrees.go:300-353`, or is unreachable or older, `fork_isolation_cleanup.go:141-201`, with a notice in the root chat, `:203-225`.
  The worktree fields and `diff_summary` in `wait_agent`, `coderd/x/chatd/fork_isolation_report.go:48-103`, added with the experiment, `fork_tools.go:51`, counted by the agent, `agent/agentgit/fork_worktree_summary.go:34-96`, with the fields in `codersdk/workspacesdk/fork_worktrees.go:103-117`.
  The **Branch:** and **Worktree:** lines and the diff summary on the cards, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:72-120`, `:151`, `:171`, `:179-202`.
  Branch name, `fork_isolation.go:258-280`, path, `agent/agentgit/fork_worktrees.go:63`, `:178`, `:221-222`.
  The worktree of a child, or of its nearest worktree ancestor through `shared` chats, `coderd/x/chatd/fork_isolation_binding.go:39-70`, named in a system message, `:95-112`, added at spawn, `fork_subagents.go:295-305`, `:342-345`.
  Relative paths and the default `workdir`, `fork_isolation_binding.go:116-131`, `:140-210`.
- Workspace isolation, only with `fork-subagent-isolation`: the `isolation` values and description, `coderd/x/chatd/fork_isolation.go:42-46`, `:82-89`, checks, `:108-111`, result fields, `:135-141`, and in `wait_agent`, `coderd/x/chatd/fork_isolation_report.go:78-82`.
  **Workspace:** or the error on the cards, only with the experiment, `ForkSubagentFields.tsx:121-134`, `:151`, `:171`, `:194`.
  The template and the latest build's parameters, and the errors, `coderd/x/chatd/fork_isolation_workspace.go:35-70`, created with `create_workspace` as the chat owner and refused without a ready agent and finished startup scripts, `:102-130`, then the prompt, `:72-86`, `:132-167`, called at spawn, `coderd/x/chatd/fork_subagents.go:322-332`, `:411-416`.
  The template checks, `coderd/x/chatd/chattool/createworkspace.go:178-190`, and the chat workspace TTL or the template default, `:192-213`.
  Archived and stopped once on failure, with each failure only logged, `fork_isolation_workspace.go:88-100`, `:170-227`, and the name, `:229-251`.
  Stopped after a root archive with the experiment on, also an automatic one, `fork_family_lock.go:23-35`, `auto_archive.go:148-152`, after the worktree cleanup, whatever it keeps, once and only logged, and skipped for a child bound to another workspace, `fork_isolation_cleanup.go:48-61`, `:90-95`, `:124-139`.
  No delete: nothing in `coderd/x/chatd` deletes a workspace, and the workspace tools are root-only, `coderd/x/chatd/turn_environment.go:732-741`.
- `wait_agent` returns the last assistant message: `subagent_operations.go:635-667`.
- Concurrency default and queued refusals: `coderd/x/chatd/agentadmission.go:13`, no cap when an entitlement unlocks it, `:60-65`, `enterprise/coderd/x/chatd/agentadmission.go:18-28`, `coderd/x/chatd/worker.go:275-283`.
  Subagent limits, only with `fork-nested-subagents`: flags, `codersdk/fork_chat_config.go:54-83`, the lower of the deployment and organization values, `fork_capacity.go:32-45`, `fork_limits.go:49-60`, `:64-72`, returned by the organization limits API, `coderd/fork_subagent_limits.go:30-51`, `:133-140`, without the default cap, `fork_capacity.go:119-130`.
  The organization settings section, only with `fork-nested-subagents` and for a user who can read the organization's chat model configs, `site/src/pages/AISettingsPage/CoderAgentsPage/ForkSubagentLimits.tsx:31-55`, read-only without edit permission, `components/ForkSubagentLimitsSettings.tsx:179-189`.
  Concurrency limits without the ancestors, `fork_capacity.go:75-117`, and `waiting_for_capacity` in `list_agents`, `coderd/x/chatd/fork_list_agents.go:34-40`, `:81-88`, from `fork_capacity.go:146-192`.
  The `list_agents` card's **waiting for capacity**, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkListAgentFields.tsx:16-27`, in each row, `ListAgentsTool.tsx:58`.
  The family API, only with the experiment, `coderd/fork_chat_routes.go:45-48`, status 400 for a child chat, `coderd/fork_chat_family.go:29-34`, and its fields, `codersdk/fork_chat_family.go:16-28`, from `coderd/x/chatd/fork_family.go:22-67`.
  The sidebar hourglass and subtitle for a waiting child, and the chip at the live limit for an expanded root chat with a live subagent, only with the experiment and for the chat owner, `site/src/pages/AgentsPage/components/ChatsSidebar/tree/ForkFamilyCapacity.tsx:30-34`, `:48-91`, `:98-115`, polled every 5&nbsp;seconds, `site/src/api/queries/forkChatFamily.ts:12-19`, in each row, `ChatTreeNode.tsx:142-159`, `:347-349`.
  The same hourglass and label in the top bar of a `running` subagent's chat that waits, only with the experiment and for the chat owner, `site/src/pages/AgentsPage/components/ForkSubagentSlotBadge.tsx:26-51`, `ChatTopBar.tsx:281-286`.
  `list_agents` pages of 10 and `paused`, `coderd/x/chatd/subagent.go:565-584`, and `wait_agent` on a `paused` child, `:481-489`.
  `wait_agent` returns at once for `requires_action`, `coderd/x/chatd/subagent_operations.go:621-632`, and `interrupt_agent` moves it to `running`, `:159-169`.
  `interrupt_agent` interrupts the active descendants first, for any status, `:159-162`, `coderd/x/chatd/chat_mutations.go:902`, `:920`, `coderd/x/chatd/fork_cascade.go:32-39`, `:60-80`.
  Live limit on spawn and resume, `fork_subagents.go:73-89`, `:222-251`, and its error, `:263-275`, with the live statuses, `coderd/database/forkdb/queries/chat_capacity.sql:30-37`.
  The error on a failed `spawn_agent` card, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:160-163`, `:172-174`, `forkToolUtils.ts:14-17`, in the card, `SubagentTool.tsx:260-264`, from `Tool.tsx:542`.
  Stored definitions: user routes, `coderd/fork_agent_definitions.go:32-170`, organization routes, `coderd/fork_organization_agent_definitions.go:26-180`, shared with the whole organization at creation, `:76-80`, with the sharing routes, `coderd/fork_organization_agent_definitions_acl.go:35-159`, and project routes, `coderd/fork_project_agent_definitions.go:20-190`.
  Offered to `spawn_agent` when enabled and readable by the chat owner, `coderd/x/chatd/fork_agent_definitions.go:187-311`, and for a project the owner owns or that is shared with them, `:264-268`, from the root chat for a child, `coderd/x/chatd/project_instructions.go:17-51`, `coderd/x/chatd/projects.go:16-61`.
  The personal definitions page: the **Settings** button, `site/src/pages/AgentsPage/components/ChatsSidebar/chats/ChatsPanel.tsx:512-519`, the route, `site/src/forkRoutes.tsx:16`, and the nav item and page, only with `fork-agent-definitions`, `site/src/pages/AgentsPage/components/ChatsSidebar/settings/ForkSettingsNavItems.tsx:18-28`, `site/src/pages/AgentsPage/AgentSettingsPage/ForkAgentDefinitionsPage/ForkAgentDefinitionsPage.tsx:21-23`, with the organization picker, `:25-50`.
  Its table of the member's personal definitions, `site/src/api/forkAgentDefinitions.ts:11-23`, with the **Enabled** switch, `ForkAgentDefinitionsTableView.tsx:128-137`, and the **Delete** menu item and its dialog, `:158-163`, `:214-221`, calls the user routes, `forkAgentDefinitions.ts:26-48`.
  Disabled personal definitions offer no type, `coderd/x/chatd/fork_agent_definitions.go:219-222`.
  The definition name without its prefix in subagent tool results, stored at spawn, `coderd/x/chatd/fork_subagents.go:376-379`, and reported by `coderd/x/chatd/fork_subagent_types.go:25-51`, `:86-112`, `fork_tools.go:48`.
  A child chat's `agent_type` in the chat list, the single-chat response, and a `created` watch event, only with the experiment, `coderd/exp_chats.go:614`, `:1934`, `:331`, `coderd/fork_chat_agent_type.go:17-72`, with a built-in child's type from its mode, `coderd/database/forkdb/queries/chat_subagents.sql:15-29`, and the badge, `site/src/pages/AgentsPage/components/ForkAgentTypeBadge.tsx:12-24`, in the sidebar, `ChatsSidebar/tree/ChatTreeNode.tsx:346`, and the top bar, `ChatTopBar.tsx:280`.
  **Agent type:** on the `spawn_agent` card of a custom child, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:155-159`, `:166-170`.
  Bare names for one source and qualified names for several, `fork_agent_definitions.go:98-145`, `coderd/x/skills/skills.go:153-229`, and the error for an unknown or ambiguous type, `subagent_catalog.go:233-242`.
- `ask_user_question` in plan-mode root turns, and outside plan mode only in web UI root chats: `coderd/x/chatd/turn_environment.go:725-731`, async description, `coderd/x/chatd/chattool/askuserquestion.go:17-24`.
  For a child that isn't an `explore` child, only with `fork-nested-subagents`, `coderd/x/chatd/fork_descendant_questions.go:41-43`, `fork_tools.go:40-44`, ending the turn, `fork_descendant_questions.go:47-57`, `coderd/x/chatd/turn_environment.go:1096`, and not in plan-mode child turns, `:2053-2062`.
  Unanswered until the child gets a user message after the question, and a secret question until the message of its answer or decline, `coderd/database/forkdb/queries/chat_descendant_questions.sql:10-44`.
  `needs_input` and the questions in `wait_agent`, `fork_descendant_questions.go:363-377`, which first waits for the answer when the question is unanswered at the call, `:331-350`, `:382-403`, added with the experiment, `fork_tools.go:45-47`.
  The family's unanswered questions, oldest first, `chat_descendant_questions.sql:46-80`, from `GET /api/v2/chats/fork/{chat}/questions`, `coderd/fork_chat_routes.go:54`, `coderd/fork_chat_questions.go:53-102`.
  The root chat page shows the oldest one only with the experiment and only on a root chat the viewer can act on, `site/src/pages/AgentsPage/components/ForkDescendantQuestionDock.tsx:51-59`, `:93-94`, `:137-150`, and posts the answer to the asking chat, `site/src/api/queries/forkChatQuestions.ts:66-68`.
  The root chat's own open question hides it until that answer is queued, `site/src/pages/AgentsPage/components/ChatPageContent.tsx:224-229`, `:288-302`.
  Secret questions, not in a computer-use child, with the description and the schema, `coderd/x/chatd/fork_descendant_questions.go:75-125`, the errors, `:140-160`, and the placeholder in the result, `:177-181`, `coderd/x/chatd/fork_secret_answers.go:40-44`.
  The answer route, `coderd/fork_chat_routes.go:55`, with its 8-byte and 16&nbsp;KiB limits, `coderd/fork_chat_questions.go:35-38`, `:118-155`, the answer message, `:186-193`, and the decline message, `:212-260`, posted from the root chat's masked field, note, **Submit**, and **Decline**, `site/src/pages/AgentsPage/components/ForkDescendantQuestionDock.tsx:153-158`, `:191-325`, `site/src/api/forkChatQuestions.ts:31`.
  The value only in `execute`, `write_file`, and `edit_files`, `coderd/x/chatd/fork_secret_answers.go:31-34`, the scrubbed results and errors without media, `:36-80`, `:128-176`, the forms the scrub finds, `:399-408`, and the `execute` refusal, `:219`.
  The answers kept for 24&nbsp;hours, `coderd/database/forkdb/queries/chat_secret_answers.sql:5-9`, `:14-27`, and not removed by a later question, `fork_descendant_questions.go:223-226`.
  The `message_agent` error, `coderd/x/chatd/fork_subagents.go:216-218`, the refused send or edit in the child's chat, `coderd/fork_chat_questions.go:391-395`, `coderd/exp_chats.go:3367-3368`, `:3522-3523`, the notice on the child's question, `site/src/pages/AgentsPage/components/ChatElements/tools/Tool.tsx:697-710`, and the `wait_agent` note, `fork_descendant_questions.go:33-36`, `:370-377`.
- Skill roots and containers: `agent/agentcontext/defaults.go:9-21`, `agent/agentcontext/resolve.go:66-71`.
- Instruction files: `resolve.go:48-52`.
- Skills one level deep, with `name` equal to the directory: `resolve.go:61-64`, `:632-647`.
- No skill in a chat's first message: `coderd/x/chatd/chatd.go:663-671`, `coderd/x/chatd/skill_invocation.go:37`.
- Working directory: `coderd/x/chatd/context_prompt.go:195-198`, `agent/agentproc/process.go:400-413`.
- `explore` tools: `turn_environment.go:2126-2135`.
- Hidden skills: `coderd/x/chatd/chattool/skill.go:55-62`, `turn_environment.go:2187-2203`.
- `find_tools`: `coderd/x/chatd/chattool/findtools.go:19`, only with MCP tools, `coderd/x/chatd/mcp_tool_search.go:121-136`, which also omits it on a name collision.
- Omitted `model_config_id`: subagent model override first, `subagent_catalog.go:60-77` and `:84-102`, else the parent's model, `subagent_operations.go:332-335`.
- Project memory for root chats: `coderd/x/chatd/memory.go:20-33`.
- Todo tools: `coderd/x/chatd/fork_chat_todos.go`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go`. The plan-mode and `explore` allowlists, `builtinPlanToolAllowed` and `exploreToolAllowed` in `turn_environment.go`, don't list them.
  Item statuses, `fork_chat_todos.go:41`, checked against the tool schema before the tool runs, `coderd/x/chatd/toolinput.go:22-30`, `:125-127`.
  Snapshot after a compaction, `todoSnapshotMessage`, `coderd/x/chatd/fork_context.go:105-108`.
  Composer pill, only with `fork-chat-todos` and a non-empty list, `site/src/pages/AgentsPage/components/ForkComposerSlot.tsx:18-26`, `ChatTodoPill.tsx:99-107`.
  Checklist card for each call, only with `fork-chat-todos`, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:21-33`, `:40-46`, with its labels, `ForkTodoTool.tsx:12-25`, `:42-52`, and its expandable list or error, `:53-74`, `:90-101`.
  The children's lists in `list_agents`, `coderd/x/chatd/fork_list_agents.go:28`, `:50`, `:70-74`, and the progress on its card, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkListAgentFields.tsx:17-19`, `:28-30`.
- Transcript tool: `coderd/x/chatd/fork_chat_transcript.go:68-85`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go:29-34`.
  Reads as the chat owner, `fork_chat_transcript.go:109-116`.
  `include_tool_calls` on `wait_agent`: `coderd/x/chatd/fork_wait_agent.go:31`, `:38`, `:84-85`, `:101-106`, added with the same experiment, `fork_tools.go:33`.
  The recent tool calls and `tool_calls_error` on the `wait_agent` card, only with the same experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:36-70`, `:186-191`, `:203-206`.
  Chat search: `coderd/x/chatd/fork_search_chats.go:28-32`, `:51-67`, added with the same experiment, `fork_tools.go:32`, and run as the chat owner, `fork_search_chats.go:97-103`.
  Cards for both calls, only with `fork-chat-todos`, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:21-33`, `:47-56`, with the search card's labels, query, chat links, and more-results note, `ForkSearchChatsTool.tsx:56-67`, `:80-99`, and the transcript card's labels, chat link, message lines, and more-results note, `ForkChatTranscriptTool.tsx:70-78`, `:93-125`.
  The plan-mode and `explore` allowlists in `turn_environment.go` don't list either tool.
- No tool that creates or changes a PR: in the Go files of `coderd/x/chatd`, `pull_request` appears only in the read-only watch of `wait_for_events`, `coderd/x/chatd/fork_wait_for_events.go` and `coderd/x/chatd/fork_pull_request_events.go`.
- Wake-ups, only with `fork-chat-wakeups`: `schedule_wakeup`, `wait_for_events`, and `notify_user`, `coderd/x/chatd/fork_tools.go:35-39`, not in the plan-mode or `explore` allowlists, `turn_environment.go:2054-2069`, `:2126-2135`.
  Arguments and result, `coderd/x/chatd/fork_schedule_wakeup.go:54-70`, `:99-113`, the description, `:77-82`, one schedule, `:119-142`, and the limits, `:31-35`, `:160-202`, `:226-228`.
  `/loop`, `coderd/x/chatd/fork_loop_command.go:26-31`, `:44-59`, `:100-135`, scheduled with the message, `coderd/x/chatd/chat_mutations.go:88`, `:222`, or with a new chat, `coderd/x/chatd/chatd.go:679`, `:799`.
  Delivery every 15&nbsp;seconds, `coderd/x/chatd/fork_wakeups.go:28`, `:43-61`, as the chat owner, `:241-260`, after a busy chat goes idle, `:138-146`, canceled for an archived chat, `:134-135`, and no missed runs replayed, `:329-365`.
  `wait_for_events` arguments, defaults, and result, `coderd/x/chatd/fork_wait_for_events.go:28-56`, the description, `:82-92`, the watched children and the errors, `:206-267`, the expiry and the prompt, which `validate` checks like a `schedule_wakeup` prompt, `:269-297`, `fork_schedule_wakeup.go:160-169`.
  One message per done child, while the chat is idle, `fork_wait_for_events.go:313-419`, `fork_wakeups.go:138-148`, and completed after the last child, `fork_wait_for_events.go:421-438`, polling every 15&nbsp;seconds, `fork_wakeups.go:349-350`.
  Done children, `fork_wait_for_events.go:487-500`, with the children that wait for their own `wait_for_events` or for an answer read in one snapshot, `:502-546`.
  The pull request arguments, `fork_wait_for_events.go:41-42`, the description, `:90-92`, the check of `pull_request_events` before the read, `:145-171`, the result, `:191-195`, and the default prompt, `:292-297`.
  The events, the prompt, the poll and retry times, and the 20-line cap, `coderd/x/chatd/fork_pull_request_events.go:25-55`, and the read at the call with the unknown event and closed errors, `:69-126`.
  What wakes the chat, `:169-181`, without activity after the expiry, `:134-167`, the message without bodies, `:183-223`, one per pull request, `:128-132`, `fork_wait_for_events.go:371-398`, the poll once a minute while it waits, `:401-404`, and the failed reads, `fork_pull_request_events.go:225-252`.
  The read with the chat owner's linked account and its errors, `coderd/fork_chat_pull_requests.go:24-84`, wired in `coderd/coderd.go:977`, only for GitHub, `coderd/externalauth/gitprovider/fork_github_activity.go:85`, `:101-104`, without the owner's own reviews and comments, draft reviews, or older comments, `:140-200`, and the CI state, `:202-295`.
  No wake-up route or page: `wakeup` has no match in `coderd/*.go` or `site/src/pages`.
  The daily limits, `coderd/x/chatd/fork_wakeup_caps.go:22-38`, `:51-96`, checked before each send, `fork_wakeups.go:150-152`, `fork_wait_for_events.go:408-410`, over delivered runs and every message cost, `coderd/database/forkdb/queries/chat_wakeups.sql:187-220`.
  The other pauses, `fork_wakeups.go:307-310`, `:342-347`, `fork_wait_for_events.go:325`, and the notification after each pause, `fork_wakeups.go:205-207`, `:299-301`, `:381-383`, with its title, reasons, and the chat title in its text, once per chat, reason, and title a UTC day, `fork_wakeup_caps.go:36-49`, `:98-128`, by the dedupe hash, `coderd/database/migrations/000245_notifications_dedupe.up.sql:13-26`.
  No resume or removal of a paused wake-up: nothing in `coderd/x/chatd` calls `UpdateChatWakeupStatus` or `CancelChatWakeupsByChatID`, and the limit of 10 counts paused wake-ups, `fork_schedule_wakeup.go:213-228`.
- Notifications, only with `fork-chat-wakeups`: `notify_user`, `fork_tools.go:38`, with its arguments, result, and hourly limit, `coderd/x/chatd/fork_notify_user.go:23-38`, `:40-56`, the checks and errors, with the default title cut to 120 bytes and pause notifications in the count, `:72-123`, `:147-158`, and the send with a dedupe bucket per minute, `:124-143`, which enqueues nothing without a delivery method, `coderd/notifications/enqueuer.go:104-117`, removed by a custom agent tool policy, `fork_agent_tool_policy.go:208-229`, with pause notifications sent past the limit, `fork_wakeup_caps.go:102-128`, counted in `chat_wakeups.sql:223-229`.
  The title and message limits, `codersdk/notifications.go:299-321`, the inbox and the deployment's method, `coderd/notifications/enqueuer.go:104-116`, and the notifications that a turned-off template or the dedupe drops without an error, `:138-165`.
