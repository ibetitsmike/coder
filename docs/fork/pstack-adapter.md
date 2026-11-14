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
    - A deeper descendant's chat ID you don't have: list_agents with recursive true, if it takes recursive.
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

The workspace keeps the copies from steps 5 to 10, so a change to this page on fork `main` doesn't reach it.
When this page changes, repeat steps 1 to 10, then start a new chat or select **Refresh context** in an open chat.
Steps 5 and 6 only add and replace files, so before step 3 of a repeat, while `~/pstack-src` is still at the earlier commit, delete the earlier copies: `cd ~/pstack-src/pstack && for n in skills/* agents/*.md; do rm -rf ~/.coder/"$n"; done && rm -rf ~/.coder/skills/deslop ~/.coder/skills/control-ui ~/.coder/skills/control-cli`.
Otherwise a skill or agent that the new pstack commit removes or renames stays in the workspace.

`execute` runs each command with `sh -c` in the workspace agent's environment, so it doesn't read `~/.bashrc` or `~/.profile`.
If a tool is on `PATH` only through one of those files, set `PATH` in the `env` of the template's `coder_agent`, such as `"$HOME/.local/bin:$PATH"`.
For a tool from `~/.profile`, you can instead start the command with `. ~/.profile &&`.
Use `.` and not `source`, because `source` fails in shells such as Dash, the `/bin/sh` of Debian and Ubuntu.

## Tools

| Cursor                                                                  | Coder Agents today                                                                                                                                                                                                  | Do this                                                                                                                                                                                                                                                                       | Removed by                   |
|-------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------|
| `Task`                                                                  | `spawn_agent` starts a child and returns at once, and `wait_agent` returns its final reply                                                                                                                          | Spawn with `spawn_agent`, then collect with `wait_agent`.                                                                                                                                                                                                                     | Stays                        |
| `AskQuestion`                                                           | `ask_user_question` in some root chats, and in children with `fork-nested-subagents`. Refer to [Questions](#questions)                                                                                              | If you have `ask_user_question`, use it. Otherwise ask in plain text and end the turn.                                                                                                                                                                                        | W2 (questions from children) |
| Open a todolist                                                         | `todo_write` and `todo_read`, only with the `fork-chat-todos` experiment. Refer to [Todo list](#todo-list)                                                                                                          | Send the whole list with `todo_write` and update it at each phase. Without the tools, keep the list in your reply and restate it after a compaction.                                                                                                                          | W4                           |
| Keep notes across a long task                                           | `notes`, only with the `fork-token-budget` experiment. Refer to [Notes](#notes)                                                                                                                                     | Keep a checkpoint in `checkpoint.md` with the goal, the decisions, the progress and the next steps, and update it as you work. Without the tool, restate the checkpoint in your reply after a compaction.                                                                     | Stays                        |
| Find what scrolled out of context                                       | `chat_history`, only with the `fork-token-budget` experiment. Refer to [Chat history](#chat-history)                                                                                                                | Call `list_windows`, then `search` or `list_items`, then `read_item`. Without the tool, use `read_chat_transcript` if you have it.                                                                                                                                            | Stays                        |
| Start over in a fresh context                                           | `new_context`, only with the `fork-token-budget` experiment. Refer to [Context windows](#context-windows)                                                                                                           | When a `context_budget` request arrives, save `checkpoint.md` with `notes`, finish the step, and call `new_context`. Without the tool, the chat summarizes when it fills up.                                                                                                  | Stays                        |
| Run another pstack skill (`/name` or "the **name** skill")              | 57 of pstack's 58 skills set `disable-model-invocation: true`, so `read_skill` and the skill index hide them from the model. You can still run one from the composer's `/` menu, but not in a chat's first message. | `read_file` `~/.coder/skills/<name>/SKILL.md`, with `~` replaced by your home directory, and follow it. Read its sibling files with `read_file` too. When you tell the user to type it, write `/workspace/<name>`, and in a new chat tell them to send a plain message first. | Stays                        |
| `Grep`, `Glob`                                                          | `execute`, `read_file`                                                                                                                                                                                              | `execute` with `rg` or `find`.                                                                                                                                                                                                                                                | Stays                        |
| Embed a screenshot or video inline, as `ketchup` and `ketchup-video` do | `attach_file`, for a workspace file of up to 10&nbsp;MiB. Refer to [Attachments](#attachments)                                                                                                                      | Save the file in the workspace, then call `attach_file` with its absolute path.                                                                                                                                                                                               | Stays                        |
| Look at an image file, as `Read` does for a screenshot                  | `view_image`, only with the `fork-view-image` experiment. Refer to [View images](#view-images)                                                                                                                      | Call `view_image` with the image's absolute path. Without the tool, ask the user what the image shows.                                                                                                                                                                        | Stays                        |
| Available-tools map, `mcps/`                                            | When MCP servers are configured, their tools sit behind `find_tools`. It is absent when there are none, or when an MCP or client tool is itself named `find_tools`                                                  | If you have `find_tools`, call it to find a tool by name or purpose. If not, look for MCP tools in your tool list.                                                                                                                                                            | Stays                        |

### Questions

A root chat has `ask_user_question` in plan-mode turns, where the question ends the turn.
Outside plan mode, a root chat started from the web UI also has it.
There the call returns at once, and the answer arrives later as a user message, so keep working on what doesn't depend on the answer.
With the `fork-nested-subagents` experiment, a root chat started through the API has the same async `ask_user_question`, and its owner answers on the chat's page in the web UI.
Without the experiment, API chats outside plan mode don't have it.

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
`message_agent` to the child fails with `cannot message the child agent: it waits for a secret answer, which only the user can enter, in the question panel above the root chat's input; never ask the user to send the value as a chat message`.
A message or an edit in the child's own chat fails with `The chat's question takes a secret answer, which a chat message cannot carry.`, and the child's chat shows `Answer this secret question in the question panel of the root chat.` on the question.
`wait_agent` returns `needs_input: true` with a note that the user enters the value in the question panel above the root chat's input and that you must never ask for it in chat, so tell the user to answer there.
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

### Notes

`notes` exists only when the deployment enables the `fork-token-budget` experiment.
Plan-mode turns, `explore` children and read-only custom agent types don't get it.
A chat's notes belong to that chat, and no other chat can read them, a parent or a child included.
The actions are `list`, `read`, `write`, which replaces the note, `append`, which adds the content as a new line, and `delete`.
A path has 1 to 128 characters from `a-z`, `0-9`, `.`, `_`, `/` and `-`, and starts with a letter or a digit, such as `checkpoint.md` or `findings/auth.md`.
A note holds 16384 bytes at most, and a chat keeps 20 notes at most.
A write or an append that breaks a limit fails and stores nothing.
When Coder Agents runs a batch of tool calls again after a restart, a `write`, `append` or `delete` that already ran returns its first result and changes nothing, so an append adds its line once.
The notes survive a compaction, so keep the goal, the decisions, the progress and the next steps in `checkpoint.md`.

### Chat history

`chat_history` exists only when the deployment enables the `fork-token-budget` experiment.
Plan-mode turns, `explore` children and read-only custom agent types don't get it.
It reads only its own chat.
The chat's history is a list of context windows, `w:1`, `w:2` and on.
A compaction starts the next window, and the last window is the current one.
The history never reaches past a clear of the chat, so after a clear the windows start again at `w:1`.
The actions are:

- `list_windows` lists the windows, with what started each one, `chat_start`, `chat_summarized` or `chat_cleared`, and their first and last message IDs.
- `list_items` with a `window` lists its messages with a short preview, 20 at a time and 50 at most with `limit`. Pass `next_after_id` as `after_id` for the next page.
- `search` with a `query` of 2 to 200 bytes finds the newest messages whose text, tool calls or tool results contain it, in any case. Pass `window` to look in one window, and `next_before_id` as `before_id` for older matches.
- `read_item` with an `id` reads one message, with long parts cut. `part_bytes` gives the full size of each part it returns, and `omitted_parts` counts the trailing parts that did not fit. Add `part`, starting at 1, to read that part in full, a slice at a time, and pass `next_offset` as `offset` until `has_more` is false.

Each result holds 16&nbsp;KiB at most.
It skips its own calls and those of `read_chat_transcript`, and messages that only the model or only the user sees.
A window that a [rollover](#context-windows) started shows `chat_summarized`, as one a summary started does.

### Context windows

With the `fork-token-budget` experiment, a chat that has `notes`, `chat_history` and `new_context` works in context windows instead of summaries.
Plan-mode turns, `explore` children, read-only custom agent types and custom agent types whose tool policy drops `notes`, `chat_history` or `new_context` keep the summary.
Each chat has its own windows and its own notes, a child included, and a child's window doesn't change its parent's.

The points of a window come from the model's context limit and the chat's compression threshold, which is the model's threshold or the user's override of it:

- The usable limit is the context limit minus 8192 tokens, or minus a quarter of the limit when that is less.
- The final point is 10192 tokens below the usable limit, or half of it when that is more.
- The handoff point is the threshold, or the final point when that is lower. A threshold below 10% counts as 10%.

When the last prompt of the window reaches the handoff point, the chat gets a model-only message that starts with `<context_budget request="handoff">`.
At the final point it gets one that starts with `<context_budget request="final">`.
Each goes out once per window.
A window whose first prompt is already past the handoff point gets neither.
Save the goal, the decisions, the progress and the next steps in `checkpoint.md`, then call `new_context`.
`new_context` takes no arguments and fails until a note was written or appended in this window.
After the other calls of the step finish, the next window starts.

If the chat doesn't call it, the window rolls over by itself when a prompt reaches the usable limit.
A rollover waits for the results of the step's tool calls, so a call and its result stay in the same window.
The next window starts with a model-only message that starts with `<context_window id="w:2">`, with the window's ID.
It lists the notes and says to read `checkpoint.md` first, and holds nothing of the earlier conversation.
A rollover makes no model call, and the conversation shows a divider line, `Context window w:2 started after a checkpoint` when the chat called `new_context`, or `Context window w:2 started at the context limit` when the window filled up.
Without the experiment, the same entry shows as a summary card that says `Context window w:2 started from the notes of this chat. No summary was made.`
Use `chat_history` to find a message of an earlier window.

A threshold of 100% turns off the requests and the rollover, and `new_context` still works.
A compaction model override doesn't apply to a rollover, since it makes no summary.
A manual compaction from the composer still makes a summary.

### Attachments

`attach_file` copies a workspace file into the chat, so the user can open or download it from the conversation.
A file over 10&nbsp;MiB fails with `attachment exceeds 10 MiB size limit`.
The web UI shows a raster image, such as a PNG or a JPEG, as a thumbnail.
Plain text, Markdown, CSV, and JSON open in a text preview.
With the `fork-chat-todos` experiment, the web UI shows an MP4 as a player with a download link, both inline and in the turn's **Attachments** menu.
Without the experiment, an MP4 shows as a download card, as an SVG does.
Coder Agents doesn't send an attached file back to the model in later turns, so describe in your reply what the file shows.
To look at an image file yourself, use [`view_image`](#view-images).

### View images

`view_image` exists only when the deployment enables the `fork-view-image` experiment.
It loads a PNG, JPEG, GIF, or WebP file from the workspace into the model's context as an image.
It detects the type from the file's content, not its name, so a text file named `shot.png` fails with `/path/shot.png is text/plain, not a PNG, JPEG, GIF or WebP image. Use read_file for text files`.
On the Anthropic provider, and on Bedrock through the Anthropic API, a file over 3,932,160&nbsp;bytes fails, so that its base64 form fits Anthropic's 5&nbsp;MiB per-image limit.
On other models, a file over 10&nbsp;MiB, the chat tool media limit, fails.
The error names the limit and its reason, such as `... is larger than 3932160 bytes, the view_image limit: ...`.
An image wider or taller than 8000&nbsp;pixels fails, because Anthropic rejects one.
An image over 25,000,000&nbsp;pixels in total fails too, which bounds the memory of the decode below.
A truncated or damaged file fails with `... is not a valid image/png image: ...`, because the tool checks the file's structure: the PNG chunk checksums, the JPEG and GIF end markers, and the WebP chunks, which must fit in the file and hold image data.
The tool then decodes a PNG, a JPEG, or the first frame of a GIF, so damaged image data behind an intact structure fails as well.
WebP gets only the structure check, because Go has no WebP decoder in its standard library.
An animated GIF fails with `... is an animated GIF with N frames, and view_image accepts only still images`, because OpenAI rejects animated GIFs.
One model response can call `view_image` at most 8 times, since only 8 viewed images stay in context.
Further calls in that response fail with `view_image runs at most 8 times per response`.
The tool doesn't resize images, so scale a large one down first.
The tool uses the path as given, as `read_file` does, so it doesn't trim spaces from it.

If the chat's model doesn't take images, the call fails with `model "<model>" on provider "<provider>" does not accept image input. Switch to a model with image input to view images`.
Google models don't take GIF, so a GIF fails there with `does not accept image/gif images`.
The OpenAI-compatible, OpenRouter, and Vercel providers report that they take any image, so Coder Agents can't tell a text-only model behind them, and the provider gets the image anyway.

The image stays in the chat's stored history like other tool results, and later turns send it again.
Each request carries at most the 8 newest `view_image` images, always with the newest one.
Together with the prompt's other images, such as `computer` screenshots and attachments, including those of the message being answered, a request carries at most 20 images, because Anthropic tightens its per-image limits above 20.
Their size and the size of those other images add up to at most 10&nbsp;MiB, so the base64 form fits Bedrock's 20&nbsp;MB request limit, except that the newest one stays even when it goes over.
An older one becomes the note `[image no longer in context: only the newest viewed images are kept. Call view_image again to see it.]`, so call `view_image` again to see it.
After a switch to a model with a lower size limit, such as from OpenAI to Anthropic, and in the summary request of a compaction model override with a lower limit, an earlier image over the new limit becomes the note `[image omitted: ... is larger than the view_image limit of ... bytes for this model. ...]` and doesn't count toward the 8 images.
While the experiment is off, a chat whose client supplies its own `view_image` tool keeps that tool's results as they are, on turns that offer the client's tools.
After a compaction, the images before the summary are no longer sent, like any other tool result.
The web UI shows the call as a tool card with the image.
Plan-mode turns and `explore` children don't get `view_image`.

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
An invalid `/loop` fails with status 400, `Invalid /loop command.`, and the reason in the detail, such as `usage: /loop [interval] <prompt>` for a `/loop` without a prompt.

`wait_for_events` wakes the chat when its child agents are done, when a GitHub pull request has new activity, or when a background process exits, prints lines that match a pattern, or can no longer be watched, so you can end your turn instead of waiting in `wait_agent`.
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
When the call watches a pull request or a process, the default prompt is `Something you were waiting for happened. Check it, then continue.`
The result also holds `pull_request`, with its `url`, the watched events in `on`, and the `head_sha` and `checks_state` at the time of the call.
Coder Agents reads the pull request once a minute with the chat owner's linked GitHub account, and checks the call's children once a minute too until the pull request's message arrives.
A failed read doesn't end the watch.
Coder Agents reads again after 5&nbsp;minutes, or after a GitHub rate limit resets, but at most 5&nbsp;minutes later while children still wait.
Activity after `expires_at` doesn't count.
The call reads the pull request before it returns, and fails with `read pull request <url>: <error>` when it can't.
The error can be `no git provider is configured for <url>` when no external auth provider of the deployment matches the URL, `<url> is not a pull request URL`, `the chat owner has no linked account for <host>`, or `pull request activity is only available for GitHub`.
A pull request that is already closed or merged fails with `pull request <url> is already closed` or `pull request <url> is already merged`.
`pull_request_events` without `pull_request_url` fails with `pull_request_events requires pull_request_url`, and an unknown event fails with `unknown pull_request_events value "<value>", valid values are review, comment, checks, closed`.

With `process_ids`, `wait_for_events` also watches up to 10 background processes of the chat, named by the `background_process_id` that `execute` returns.
Without `child_chat_ids`, the call watches only the processes, and the pull request when it names one.
For each process, the chat gets one message after the process exits, made of the `prompt`, an empty line, and ``Background process <id> (`<command>`) exited with code <code>.``, or `exited without an exit code (killed by a signal).` for a killed process.
The message ends with up to the last 40 lines of the process's output, at most 4&nbsp;KiB.
A process that already exited when you call the tool wakes the chat at the first check after your turn ends.
When the workspace stops or is rebuilt, or the workspace agent no longer knows the process, the message says ``Lost the watch on background process <id> (`<command>`): <reason>.`` instead.
With `process_output_pattern`, a regular expression in RE2 syntax of up to 1024&nbsp;bytes, the chat also gets a message while a process runs and has printed new complete lines, ending in a newline, that match.
The first check also matches the lines that the process printed before the call.
That message is made of the `prompt`, an empty line, ``Background process <id> (`<command>`) is still running and printed <n> new lines matching `<pattern>`.``, and up to the last 40 of those lines, at most 4&nbsp;KiB.
`process_output_cooldown_seconds`, from 0 to 86400 and 60 by default, sets how long such a message waits after the wake-up's last message, so it collects the lines printed meanwhile.
`process_output_max_events`, from 1 to 100 and 10 by default, caps those messages per process, and the exit message still arrives.
The workspace agent keeps only the first and the last 16&nbsp;KiB of a process's output, and once a process has printed more, matching sees only the kept last 16&nbsp;KiB, so earlier lines can be missed.
It also cuts a line longer than 2048&nbsp;bytes and ends it with `... [truncated]`, so a match in the cut part doesn't wake the chat.
The result also holds `processes`, with the `process_id`, `command`, and `output_pattern` of each watched process.
Coder Agents reads the processes from the workspace agent every 15&nbsp;seconds, or once a minute while the call also waits for a pull request.
After a failed read of that pull request, the processes wait for the same retry as the pull request.
A failed read doesn't end the watch, and when every entry that still waits is a process whose read failed, Coder Agents reads again after a minute.
At `expires_at`, one last check reports the processes that exited by then, and no more matching lines.
That check can also report an exit in the 15&nbsp;seconds after `expires_at`, but a check that runs later, such as after a busy turn, leaves out exits after `expires_at`.
Such a late check finds an exit in the workspace agent's list of the chat's processes, which holds 10, running ones first and then the newest, so it also misses an exit before `expires_at` when other processes push the process out of that list.
The call reads each process before it returns, and fails with `process "<id>" is not a process of this chat: pass the background_process_id that execute returned` when the chat's workspace agent doesn't know it, or with `this chat has no workspace agent: start the process with execute first`.
More than 10 processes fail with `cannot wait for <n> processes at once, the maximum is 10`.
The call leaves out processes that an earlier call still watches, and fails with `these processes are already watched by wake-up <id>: end your turn to wait for them` when it leaves out all of them.
`process_output_pattern` without `process_ids` fails with `process_output_pattern requires process_ids`, and the cool-down or the limit without a pattern fails with `process_output_cooldown_seconds and process_output_max_events require process_output_pattern`.

Coder Agents checks for due wake-ups every 15&nbsp;seconds, so expect a run to arrive some seconds after its time.
A wake-up never interrupts a turn.
While the chat is busy, the run waits until the chat is idle.
Coder Agents doesn't replay runs that it missed.
No tool lists or changes a wake-up, so set `max_runs` or `expires_in_seconds`, or ask the user to cancel it.
With the experiment, the API lists a chat's wake-ups with `GET /api/v2/chats/fork/{chat}/wakeups`, oldest first, including completed and canceled ones.
A user who can change the chat can pause, resume, or cancel one with `PATCH /api/v2/chats/fork/{chat}/wakeups/{wakeup}` and a `status` of `paused`, `active`, or `canceled`.
Setting the status that the wake-up already has changes nothing.
A change to a completed or canceled wake-up fails with status 409, `This wake-up has already finished.`, and a change in an archived chat fails with status 400, `Cannot change the wake-ups of an archived chat.`
A pause keeps the next run time and sets `status_reason` to `paused_by_user`, a cancel sets it to `canceled_by_user`, and a resume keeps a run time that is still ahead.
Otherwise a resumed interval or cron wake-up continues its schedule from now, and a resumed delay runs at once, or completes when it has no run left.
While the experiment is on, the composer of a chat with active or paused wake-ups shows a pill with their number.
Selecting the pill lists each one with its schedule, its prompt, its next run or check or **Paused**, and its run count, with **Pause** or **Resume** and **Cancel** buttons that make the same changes as the API.
A wake-up that a limit, a prompt hook, or an invalid cron schedule or events paused also shows the reason, such as **Paused: daily run limit reached**.
The pill reads the list again every 30&nbsp;seconds, so a change that a run makes can take that long to show.
When the list fails to load, the composer shows a warning pill instead.
While the experiment is on, the conversation shows each `schedule_wakeup`, `wait_for_events`, and `notify_user` call as a card.
The title of a `schedule_wakeup` card names the schedule, such as **Scheduled a wake-up every 5m**, and a cron schedule shows its time zone, which is UTC by default.
The card lists the prompt, the next run, and the run limit and expiry when the call sets them.
The title of a `wait_for_events` card names what the call waits for, such as **Waiting for 2 child agents and a pull request** or **Waiting for 1 background process**.
The card links each child with its title and status, and the pull request with its watched events, lists each process by its command, with the pattern when the call sets one, and shows when the wait stops.
A `notify_user` card shows **Sent a notification** with the message, and the title when the call sets one.
A failed call shows **Failed to schedule a wake-up**, **Failed to wait for events**, or **Failed to send a notification** with the tool's error.
A result that a card can't read shows the generic tool card.
While the experiment is on, archiving a chat cancels the active and paused wake-ups of every chat in its family, `archive_agent` cancels those of each chat it archives, and unarchiving doesn't resume them.
A wake-up of a chat archived without the experiment stops at its next run without sending its prompt.

Before a run sends its prompt, Coder Agents checks four limits over the 24&nbsp;hours before the run.
A chat can get 288 wake-up runs in those 24&nbsp;hours, an average of one every 5&nbsp;minutes but not a fixed pace, and the chats of an organization can get 2880 together.
The spend limits are \$20 of model cost in the chat and \$200 across the organization's chats that have or had a wake-up.
An organization admin can set each limit higher or lower with `PUT /api/v2/chats/fork/organizations/{organization}/wakeup-caps`, with spend in millionths of a dollar, and a limit that the organization doesn't set keeps its default.
The spend counts every message in those chats, not only the ones that wake-ups started, and a model without prices counts as zero.
The check only uses the cost recorded so far, so the run that it lets through can take the spend past a limit, and runs on several replicas at once can pass an organization limit by a few runs.
When the chat or its organization has reached a limit, the wake-up pauses without sending its prompt.
A wake-up also pauses when a prompt hook denies its prompt, or when its cron schedule or its events are invalid.
A paused wake-up runs again only after a user resumes it through the API or the composer pill, and it still counts toward the chat's limit of 10 active or paused wake-ups.
A resumed wake-up that still hits a limit pauses again at its next run, and a canceled one no longer counts toward the limit.
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
| A worker that messages its parent or sibling  | Only with the `fork-family-messages` experiment, `message_family_chat` sends to any chat in the family, and `list_family_chats` lists their IDs. Refer to [Family messages](#family-messages)                                                                                                                             | Put the worker's results in its final reply, as pstack expects. If the worker has `message_family_chat`, it can also send its parent a status, a blocker, or a question and keep working, or send a sibling what it needs.                                                                                                                    | W14        |
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
With the `fork-subagent-isolation` experiment, a definition can also set `isolation` to `shared`, `worktree`, or `workspace`, and a spawn of its type that sets no `isolation` uses that value, as [Worktree isolation](#worktree-isolation) and [Workspace isolation](#workspace-isolation) describe.
An `isolation` in the `spawn_agent` call, `shared` included, wins over the definition.
Without the experiment, a spawn ignores the definition's `isolation`.
Any other `isolation` value makes the definition invalid, even without the experiment.
A workspace file with one gets the status `invalid` and adds no custom type, and the API refuses to create or update a stored definition with one with status 400, `Invalid agent definition content.`
Coder Agents then removes tools by the definition's `tools`, `disallowed_tools`, `readonly`, and `mcp` fields, as [Custom agent tool policy](#custom-agent-tool-policy) describes.
Coder Agents ignores a definition named `general`, `explore`, or `computer_use`.
With the experiment, the API also stores definitions for one user in an organization, for a whole organization, and for a chat project.
To manage your personal definitions, select **Agents** in the Agents sidebar, or **Settings** and then **Agent definitions**, and pick the organization if you belong to more than one.
When the list is empty, it explains that chats can also use agent files at `.claude/agents/<name>.md` in their workspace, and **New agent definition** there opens the editor too if you can create one.
The page lists each definition's name and description, and its **Enabled** switch stops `spawn_agent` from offering a definition without deleting it.
**Delete** in a row's menu removes your personal definition and leaves workspace, organization, and project definitions with the same name alone.
**New agent definition** opens a dialog with **Name**, **Description**, **Model**, and **Body** fields, and **Create agent definition** saves them as a Claude Code agent markdown file.
An empty **Model** runs the definition on the chat's model.
**Import file** fills the fields from a Claude Code or Cursor agent markdown file and keeps its other front matter, such as `tools` or `isolation`, which the dialog has no field for.
**Edit** in a row's menu opens the same dialog with the saved definition, where **Name** can't change, and **Save agent definition** keeps every front matter key except the name, description, and model as the file has them.
If the page already shows that the definition changed or was deleted after the dialog opened, the dialog doesn't save and asks you to copy your changes and reopen it.
The save itself has no version check, so it can overwrite a change from another tab or the API that the page hasn't loaded.
The dialog shows the server's errors, such as `Invalid agent definition content.` or `An agent definition with this name already exists.`
The page doesn't list the workspace's definition files, such as the pstack agents that Setup copies to `~/.coder/agents/`.
`spawn_agent` also offers the chat owner's enabled personal definitions in the chat's organization, the enabled organization definitions that the owner can read, and the enabled definitions of the chat's project.
It offers project definitions only when the chat owner owns the project or the project is shared with them, and a child chat uses its root chat's project.
To manage a project's definitions, open the project from the Agents sidebar and select the **Agent definitions** row in its details panel.
If you can edit the project, you get **New agent definition**, **Edit**, **Delete**, and the **Enabled** switch.
Otherwise, you see the list read-only.
A name that only one of the workspace, personal, organization, and project sources defines keeps its bare name.
A name that more than one source defines is listed only as `workspace/<name>`, `personal/<name>`, `org/<name>`, or `project/<name>`.
A bare name that more than one source defines fails with `type must be one of:` and the listed types.
To start a chat as an agent type, set `agent_type` on `POST /api/v2/chats` to `general`, `explore`, or a personal, organization, or project definition that `spawn_agent` would offer the chat.
The chat keeps its usual system prompt with the definition's body after it, and it follows the definition's tool rules from its first turn.
If the type takes away `spawn_agent`, as `explore` and `readonly: true` do, the prompt also drops the guidance on subagents, and it drops the guidance on ACP subagents if the type takes away an `acp_` tool.
The prompt leaves out the advice to create a workspace if the type takes away `list_templates` or `create_workspace`, and it doesn't name the workspace tools if the type takes away `execute`, `read_file`, or `write_file`.
With `explore` or `readonly: true`, the prompt leaves out both.
A tool from your request's `unsafe_dynamic_tools` that the type takes away fails as an unknown tool if the model calls it.
It runs on the definition's model and reasoning effort, unless your request sets `model_config_id` or `reasoning_effort`.
A definition that sets `model` can start a chat in an organization without a default chat model.
A root chat ignores the definition's `isolation`.
A workspace definition can't start a chat, because Coder reads a chat's workspace definitions only after its workspace agent connects.
Such a name, an unknown name, another built-in type such as `computer_use`, and a definition that you can't read fail with status 400, and so does `agent_type` without the `fork-agent-definitions` experiment.
To list the types before a chat exists, call `GET /api/v2/chats/fork/organizations/{organization}/agent-types` with the experiment on.
Add `project=<id>` for the project's definitions, `workspace=<id>` for the definitions that the workspace's agent reports, and `model_config_id=<id>` for the model that the chat would set, which wins over a definition's model as it does at creation.
Each row gives the `name` to pass, with the same prefixes as `spawn_agent`, and its `source`, `description`, `model`, and `base_type`.
`root_available` reports whether `agent_type` can start a chat as the type, and when it can't, `root_unavailable_reason` says why, as for every workspace definition.
With the experiment, the web UI shows the same list when you select **Settings** in the Agents sidebar and then **Available agents**.
The page lists the types of one organization that you belong to, and the **No project** and **No workspace** menus add the definitions of a project or of one of your workspaces in that organization.
Each row shows the type's name and description, its source under **Tier**, and under **Availability** either `Available` or the reason that it can't start a chat.
**Recent chats** links up to three of the most recently updated chats in that organization that started or were spawned as the type, out of the chats that the Agents sidebar's default list has loaded.
A chat stores the type's name without its prefix, so a name that several sources define shows the chats of each of them.
With the experiment, the form that starts a new chat also has an agent type picker, which shows **Default agent** until you choose a type.
It lists the same types for the form's organization, project, workspace, and model, grouped by source, and you can search them by name or description.
A type that can't start a chat can't be chosen, and it shows the reason instead of its description.
The chosen type becomes `agent_type` on `POST /api/v2/chats`, and the chat starts as the default agent instead if the list no longer offers the type as available when you send, such as after you change the project or the model.
When the form's model picker shows a model, the form sends it as `model_config_id`, so the chat runs on that model and not on the definition's `model`.
**Manage agent definitions…** in the picker opens your personal definitions page.
With the experiment, you can delegate from the web UI by typing `@` in the composer at the start of a line or after a space and choosing an agent.
Coder inserts `Delegate to the <name> agent (spawn_agent type <name>):` and a space, with the name that `spawn_agent` accepts.
In a child chat, the menu opens only in a `general` child with `fork-nested-subagents`.
When you create an organization definition, Coder shares it with the whole organization, and a member who can share it can narrow who reads it with `PATCH /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition}/acl`.
To manage an organization's definitions, open **AI Settings** > **Agent definitions** and pick the organization.
The link and the page at `/ai/settings/agent-definitions` need the experiment and a user who can read the organization skills of an organization, such as an organization admin or auditor.
The picker lists the organizations whose skills you can read, and the `org` search parameter names the selected one.
An `org` that you can't read shows `You don't have permission to view this page` instead of another organization's definitions.
The table and the dialog work like those of the personal page, and the organization skill permissions decide what you can change.
Without the permission to create, the page has no **New agent definition**.
Without the permission to update, the rows have no **Edit** and show the enabled state without a switch.
Without the permission to delete, the rows have no **Delete**.
**Delete** removes the definition for everyone in the organization and leaves personal, project, and workspace definitions with the same name alone.
A member who can share a definition selects **Manage permissions** in its row's menu, adds members or groups in **Agent definition permissions**, and selects **Save permissions**.
The dialog offers the members and groups that `GET .../acl/available` lists.
`spawn_agent`, `wait_agent`, `message_agent`, `interrupt_agent`, and `list_agents` report a custom child's definition name in `type`, not `general`.
The chat list, `GET /api/v2/chats/{chat}`, and the `created` event of `GET /api/v2/chats/watch` report each child chat's type, and the type that a root chat started as, in `agent_type`, and so does the `POST /api/v2/chats` response.
The Agents page shows the type as a badge next to the chat's title, except for `general`.
In the conversation, the `spawn_agent` card of a custom child shows **Agent type:** and the name.
That name has no `workspace/`, `personal/`, `org/`, or `project/` prefix, so do not pass it back to `spawn_agent` when more than one source defines it.
Without the experiment, a custom type fails with `type must be one of:` and the built-in types.

### Custom agent tool policy

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

### Nested subagents

When the deployment enables the `fork-nested-subagents` experiment, a `general` child gets `spawn_agent` and the other subagent tools while its own children stay within `--chat-max-subagent-depth`.
The default depth of 2 allows a root, a coordinator, and its workers.
An organization's `max_depth` subagent limit can lower that depth, but not raise it.
An `explore` or `computer_use` child never gets them.
With the experiment, `wait_agent`, `message_agent`, `interrupt_agent`, and `list_agents` reach only a chat's direct children, so the root waits on the coordinator, not on its workers.
With the experiment, `list_agents` also takes a `chat_id` to list the children of that chat instead, and `recursive: true` to list every descendant instead of only the children.
The `chat_id` must be your own chat or one of your descendants, so `list_agents` never lists chats outside your subtree.
With either option, each entry also has `parent_chat_id`.
With the `fork-chat-todos` experiment, you can pass any listed ID to `read_chat_transcript`.
To message other chats in the family, refer to [Family messages](#family-messages).
The experiment also makes **Stop** and `interrupt_agent` interrupt a chat's active descendants first.
Archiving a root whose family is still working interrupts the family, waits up to 30&nbsp;seconds for it to stop, then archives every member.
If a member is still working after 30&nbsp;seconds, the archive fails and archives nothing.
With the experiment, a chat that has `interrupt_agent` also gets `archive_agent`, which archives one direct child and all of that child's descendants for good.
It takes the child's `chat_id`, and any other chat fails with `chat_id must be a direct child of this chat`.
It interrupts the child's subtree, waits up to 30&nbsp;seconds for it to stop, and then archives the whole subtree at once.
If a chat of the subtree is still working, it fails with `the child agent or one of its descendants is still busy or paused` and archives nothing.
The result lists the subtree in `archived_chat_ids`, the child last.
The archived chats leave `list_agents`, stop counting toward the family's live limit, and refuse `message_agent`, while your own chat and the child's siblings stay open.
So collect what you need from the child before you archive it.
While the root chat stays unarchived, the chats that `archive_agent` archived stay archived, because only a root chat's archive state can change.
To restore them, archive the root chat and then unarchive it, which unarchives the whole family.
A custom type's `tools` and `disallowed_tools` treat `archive_agent` as part of `Task`.
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
`wait_agent` returns at once for a `requires_action` child, and `list_agents` shows your children's workers only with `recursive: true`.
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

### Family messages

When the deployment enables the `fork-family-messages` experiment, each chat gets `message_family_chat` and `list_family_chats`, except in plan-mode turns, in an `explore` chat, and in a chat of a custom type with `readonly: true`.
A custom type's `tools` and `disallowed_tools` treat both tools as part of `Task`.
`list_family_chats` lists up to 100 unarchived chats of the caller's family, the root first, with each chat's `chat_id`, `title`, and `status`, and its `parent_chat_id` when it has a parent.
Each entry's `relation` is `self`, `parent`, `root`, `child`, `sibling`, or `other`, and `truncated` is `true` when the list leaves chats out.
`message_family_chat` takes a `chat_id` from that list and a `message` of up to 16384&nbsp;bytes, and it sends the message to the parent, a sibling, or any other chat under the same root.
It doesn't wait for a reply.
An idle recipient starts a new turn with the message, and a busy one gets it before its next model call without an interrupt, as with `message_agent`.
The result names the recipient's `chat_id`, `title`, and `status`, and `queued` is `true` when the recipient was busy.
The recipient gets the message inside `<agent-message from_chat_id="<id>" from_title="<title>">` with a note that another agent in the family sent it, not the user, and how to reply.
A chat outside the family, an ID that doesn't exist, and the chat itself are refused, and so is an archived chat.
Each chat can send at most 60 messages in any hour.
With `fork-nested-subagents`, a message that wakes a finished child counts toward the family's live limit, as [Subagent limits](#subagent-limits) describes, and a child that waits for a secret answer refuses messages.
With the experiment, the sender's conversation shows **Sent message to** or **Queued message for** and the recipient's title, with a link to the recipient and the message text.
A `list_family_chats` call shows **Found** and the number of family chats, with a link to each chat other than the caller.
The recipient's chat shows the message with **Message from agent** and a link to the sender.

### Worktree isolation

When the deployment enables the `fork-subagent-isolation` experiment, `spawn_agent` also takes `isolation`, `repo_path`, and `base_branch`.
A custom type's definition can set the `isolation` of a spawn that sets none, as [Custom agent types](#custom-agent-types) describes.
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

## Models

| Cursor                                                                 | Coder Agents today                                                                                                                                                                                                                                                 | Do this                                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Removed by |
|------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| A model slug, such as `claude-opus-5-5-xhigh` or `grok-4.7-xhigh-fast` | `spawn_agent` takes `model_config_id` (a UUID from `list_subagent_models`) and `reasoning_effort` (`none` to `max`). With `fork-agent-definitions`, an agent definition's `model` can also name an alias that `list_subagent_models` shows in a config's `aliases` | Drop the slug's effort and `-fast` suffixes. Call `list_subagent_models` and pass the config whose `model` names the same model and version, ignoring any provider prefix, with the slug's effort as `reasoning_effort`. If the slug had `-fast`, pick that model's fast variant when one is listed. If none matches, omit `model_config_id` (refer to the next row) and say so. In an agent definition, set `model` to a listed alias, such as `fast`, instead of a slug. | Stays      |
| `auto` or `inherit-parent`                                             | Omitting `model_config_id` runs the child on your model, unless the user or deployment set a subagent model for its type, in which case it runs on that model                                                                                                      | If you know your own model's config in `list_subagent_models`, pass it as `model_config_id`, so a subagent model setting can't replace it. Otherwise omit `model_config_id`.                                                                                                                                                                                                                                                                                               | Stays      |
| The `pstack-models.mdc` rule, `~/.cursor/rules/pstack-models.mdc`      | The workspace agent loads a rule with `alwaysApply: true` from `~/.cursor/rules/` into every chat, as it loads `~/.coder/AGENTS.md`                                                                                                                                | Let setup-pstack write the rule in the workspace (refer to setup-pstack), and read the role lines from it. If the rule or the role's line is missing, use the skill's default slug.                                                                                                                                                                                                                                                                                        | W6         |
| A role value `<model> <effort>`                                        | As above                                                                                                                                                                                                                                                           | `model_config_id` of `<model>` and `reasoning_effort: <effort>`. Omit `reasoning_effort` when the config lists no `reasoning_efforts`. When a step compares model families, use the config's `model` from `list_subagent_models`, not its UUID.                                                                                                                                                                                                                            | Stays      |

## setup-pstack

Follow its steps with these changes:

- Detect: the detected models are the configs that `list_subagent_models` returns.
  Write one `<model_config_id> <effort>` line per value in its `reasoning_efforts`, or one line with `none` when it lists none.
  The effort is the last token, so the budget step works as written.
  Keep `none` unchanged through the budget step.
- Defaults: map each default slug with the Models rows.
- Write: write `~/.cursor/rules/pstack-models.mdc` in the workspace as the skill says, with each role's value as a line from the Detect step.
  If `~/.coder/AGENTS.md` still holds a block between `<!-- pstack-models -->` and `<!-- /pstack-models -->` from an earlier setup, delete that block, so chats get one set of role lines.
- With one config in `list_subagent_models`, every role runs on that config, including the roles that pstack spreads across model families, such as `arena runners` and `arena cross-judge pool`.
  Say so when you confirm the write, and in the report of each run that uses one of those roles.
- After the write, start a new chat or select **Refresh context** before you run another pstack skill.

## Paths

| Cursor                                                        | Coder Agents today                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | Do this                                                                                                                                                                                                                                                                                             | Removed by |
|---------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|
| `.cursor/skills/`, `~/.cursor/skills/`                        | Skills under `skills/`, `.agents/skills/`, `.claude/skills/` and `.codex/skills/` of each scan root, including `~/.coder/skills/`, but not `.cursor/skills/`. A skill must sit one level deep, at `<container>/<name>/SKILL.md`                                                                                                                                                                                                                                                                                                                                                                                   | Use `~/.coder/skills/` or the repository's `.agents/skills/`. Flatten a nested path such as `<category>/<name>/` to `<name>/`.                                                                                                                                                                      | Stays      |
| `.cursor/agents/`, `<pstack>/agents/`                         | The workspace agent reads `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root. With the `fork-agent-definitions` experiment, chats get them as custom types. Refer to [Custom agent types](#custom-agent-types)                                                                                                                                                                                                                                                                                                                                                              | Follow Setup, which copies `<pstack>/agents/` to `~/.coder/agents/`.                                                                                                                                                                                                                                | W1         |
| `~/.cursor/plugins/`, `.cursor-plugin/plugin.json`            | With the `fork-plugin-packs` experiment, the workspace agent reads a pack's `.claude-plugin/plugin.json`, `.cursor-plugin/plugin.json` or root `plugin.json` at a scan root or one level below it, and the pack's skills become skills. Its agents become custom types when `fork-agent-definitions` is also on. An organization admin installs a pack from Git, a project or a chat enables it, and coderd writes each enabled pack to `~/.coder/plugins/<name>/` in the chat's workspace. Without the experiment, chats ignore pack contents                                                                    | Follow Setup.                                                                                                                                                                                                                                                                                       | W6         |
| `.cursor/rules/*.mdc`, always-applied rules                   | `AGENTS.md`, `CLAUDE.md` and `.cursorrules` at the top of each scan root, and `.mdc` rules in `.cursor/rules/` of each scan root, in `~/.cursor/rules/`, and in `~/.coder/rules/`, with their subfolders. A rule with `alwaysApply: true` loads into every chat. A rule with `globs` loads into a turn while the history that the turn sends to the model holds a `read_file`, `write_file`, or `edit_files` call on a matching path, relative to the directory that holds the rule's `.cursor` or `.coder` directory. After a compaction drops that call, the rule stops loading. A rule with neither is ignored | Keep the rules in `.cursor/rules/` or `~/.cursor/rules/` of the workspace.                                                                                                                                                                                                                          | W6         |
| `https://github.com/cursor/plugins/blob/main/pstack/<path>`   | Not needed                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | Read `<pstack>/<path>` and never fetch it. Give the user the URL when they need a link.                                                                                                                                                                                                             | Stays      |
| The workspace root, the current directory                     | `execute` runs in the workspace agent's directory unless you pass `workdir`, and `<workspace-context>` names it as `Working Directory`                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Work in the repository the user names, or else in the Working Directory. Write no files outside it unless the step names the path.                                                                                                                                                                  | Stays      |
| Transcripts in `~/.cursor/projects/<slug>/agent-transcripts/` | `read_chat_transcript`, only with the `fork-chat-todos` experiment. Refer to [Chat transcripts](#chat-transcripts)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | If you have `read_chat_transcript`, read the chat you need by its ID. Find a missing top-level chat ID with `search_chats`, and a missing child ID with `list_agents` in its parent. Otherwise skip transcript reads with the skip line below. Use project memory, `git`, and `gh` for prior state. | W4         |

### Chat transcripts

When the deployment enables the `fork-chat-todos` experiment, `read_chat_transcript` reads the stored messages of a chat in pages.
Leave `chat_id` empty to read your own chat, including messages that compaction took out of your context.
Pass a child's `chat_id` from `spawn_agent` or `list_agents` to read that child, or the ID of any other chat your user can read.
With the same experiment, `search_chats` finds top-level chats with the chat list search syntax, such as `search:"<phrase>"` or `pr:<number>`, and an empty query lists the recent ones.
By default it searches only the chats your user created, so add `source:created_by_me,shared_with_me` to include chats shared with your user.
It returns no subagent chats, so get a child's ID from `list_agents` in its parent.
With the `fork-nested-subagents` experiment, `list_agents` with `recursive: true` also lists your children's workers and their own children.
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

| Cursor                                                                                                   | Coder Agents today                                                                                                                                                                                                                                                                                                                                                                                                                                              | Do this                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Removed by                          |
|----------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------|
| Sticky `/poteto-mode`                                                                                    | With the `fork-pinned-skills` experiment, the chat owner can pin skills to a chat, and Coder Agents keeps their bodies in the prompt. Refer to [Pinned skills](#pinned-skills)                                                                                                                                                                                                                                                                                  | When the newest pinned skills message has a `<pinned-skill>` block named `workspace/poteto-mode`, follow it on every turn, and do not read its `SKILL.md` again. Otherwise, read `poteto-mode` again at the start of each turn while the mode is on.                                                                                                                                                                                                                                                                                                                                                                                                                                              | W6                                  |
| `/loop`, watchers that wake you, PR and CI subscriptions                                                 | With the `fork-chat-wakeups` experiment, `schedule_wakeup` and a `/loop` message wake the chat later with a prompt, and `wait_for_events` wakes it when a child agent is done or a GitHub PR gets a review, a comment, settled CI, or a close. Other forges have no PR wake-up. No tool cancels a wake-up, but the user can pause, resume, or cancel one in the composer's wake-ups pill or through the API. Refer to [Wake-ups](#wake-ups)                     | If you have `schedule_wakeup`, schedule the later step with a prompt that names it and its check, then end the turn. For `/loop 1h`, run the step once now, then pass `interval_seconds: 3600`. For a watcher, poll with `interval_seconds`, or call `wait_for_events` when it watches child agents or a GitHub PR, with `pull_request_url` for the PR. If that call fails, poll with `interval_seconds` instead. Always set `max_runs` or `expires_in_seconds`, and tell the prompt to reply only "done" once the check passes. Otherwise wait inside the turn (`wait_agent` timeouts, `gh pr checks --watch`), skip a step that needs a later wake, and tell the user to send the next message. | W5                                  |
| `SendToUser`                                                                                             | With the `fork-chat-wakeups` experiment, `notify_user` sends the chat owner a Coder notification. Refer to [Notifications](#notifications)                                                                                                                                                                                                                                                                                                                      | Put the message in your reply. If you have `notify_user`, also call it with the message.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | W5                                  |
| `SendToUser` secret requests                                                                             | With `fork-nested-subagents`, a child's `ask_user_question` can ask for a secret that the user types into a masked field on the root chat's page. Root chats have no masked input. Refer to [Questions](#questions)                                                                                                                                                                                                                                             | In a child chat whose `ask_user_question` takes `secret`, ask one question with `secret: true` and no options, then write `{{secret:<call ID>}}` where the value belongs in `execute`, `write_file`, or `edit_files`. Otherwise skip with the line and ask the user to put the secret in the workspace environment.                                                                                                                                                                                                                                                                                                                                                                               | W2 (secret questions in root chats) |
| Cursor Automations: routines, `update_state` calls, `api2.cursor.sh/automations` webhooks, routine wakes | None                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Skip with the line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Stays                               |
| Bugbot                                                                                                   | Bugbot comments on a GitHub PR are ordinary review threads                                                                                                                                                                                                                                                                                                                                                                                                      | Triage them with the forge command that the step gives. Skip a step that starts, waits for, or configures Bugbot when the PR has no Bugbot check or comment.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Stays                               |
| Cursor cloud agents                                                                                      | None                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Refer to `environment: "cloud"`. Skip a step that needs the cloud agent itself, such as its dashboard.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Stays                               |
| A built-in PR tool                                                                                       | No tool that creates or changes a PR. With `fork-chat-wakeups`, `wait_for_events` only watches a GitHub PR                                                                                                                                                                                                                                                                                                                                                      | Use the forge command that the step gives, such as `gh pr create`. That isn't a skip, so write no line. If `gh pr create` fails with `Resource not accessible by integration (createPullRequest)`, the workspace's GitHub token can't open a PR, so push the branch, write the title and body to files, and ask the user to open the PR.                                                                                                                                                                                                                                                                                                                                                          | Stays                               |
| The PRs of a chat                                                                                        | The chat's top bar and its entry in the sidebar link the PR of each repository and branch where the chat ran a git command that asked Coder for credentials, such as `git push`, or list them in a menu labeled with their count, such as **2 PRs**. An entry keeps its PR after the PR merges or closes, and switches to a newer open PR on the same branch only when the chat's diff is read, as the diff panel does, through `GET /api/v2/chats/{chat}/diff` | For each branch that the run pushed, list its PRs with `gh pr list --repo <owner>/<repo> --head <branch> --state all` instead of reading them from the top bar. Pass the repository that the PRs target as `--repo`, which for a branch pushed to a fork is the repository that the fork was made from.                                                                                                                                                                                                                                                                                                                                                                                           | Stays                               |
| Recent work across chats, such as in `recall`                                                            | `search_chats` and `read_chat_transcript`, only with `fork-chat-todos`. Refer to [Chat transcripts](#chat-transcripts)                                                                                                                                                                                                                                                                                                                                          | If you have `search_chats`, list your recent chats with an empty query and read the ones you need with `read_chat_transcript`. Otherwise skip the transcript step with the line. Either way, report prior state from `git log`, `git status` and `gh pr list --author @me --state all`, and never invent files or results.                                                                                                                                                                                                                                                                                                                                                                        | W4                                  |
| Origin CLI                                                                                               | `execute` runs it when it is installed                                                                                                                                                                                                                                                                                                                                                                                                                          | Use it when `command -v origin` succeeds. Otherwise skip with the line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Stays                               |
| `/create-skill`                                                                                          | No skill-creation tool                                                                                                                                                                                                                                                                                                                                                                                                                                          | For a project skill, write the repository's `.agents/skills/<name>/SKILL.md`. For a personal skill, write `~/.coder/skills/<name>/SKILL.md`. Give it frontmatter `name`, equal to the kebab-case directory name, and `description`.                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Stays                               |
| Image generation                                                                                         | No built-in image tool                                                                                                                                                                                                                                                                                                                                                                                                                                          | Use an image tool if `find_tools` or your tool list offers one. Otherwise skip with the line.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | Stays                               |

### Pinned skills

With the `fork-pinned-skills` experiment, the chat owner can pin up to 8 skills to a chat, including skills hidden from the model.
To pin one, select the **Pinned skills** pill in the composer of an open chat, then select the skill.
The API takes the whole list with `PUT /api/v2/chats/fork/{chat}/pinned-skills`.
No chat tool pins or unpins a skill, and a child chat doesn't get its parent's pins.

Coder Agents puts the pins in the prompt as a model-only message that starts with `The user pinned these skills to this chat.`, with each pinned skill's body in a block such as `<pinned-skill name="workspace/poteto-mode">`.
Coder Agents sends a new message whenever the prompt no longer shows the current pins, such as after a compaction or a change to the pins or their bodies.
The newest message replaces any earlier list, and when no pinned skill is left to show, its list is `(none)`.
A pin that the owner can't use, such as a skill that was deleted, turned off, or unshared, is left out.
The bodies share 64&nbsp;KiB, and a skill that doesn't fit is only named, after `Not shown because the pinned skills exceed the size limit:`.
Without the experiment, there are no pinned skills.

When a newer pinned skills message drops `poteto-mode`, the mode is off.
If the user wants `poteto-mode` to stay on, tell them that with the experiment they can pin it in the **Pinned skills** pill.

## Evidence

Checked against fork `main` at `b3c4911b16`, except `fork_agent_tool_policy.go`, which is checked against #202, and `fork_tools.go`, which is checked against the change that gives API root chats `ask_user_question`.
The family messages entry is checked against fork `main` at `2bf9127b59`.
The pinned skills entry is checked against fork `main` at `9dbe879138`.
The `list_agents` lines on `chat_id`, `recursive`, `waiting_for_capacity`, and the children's lists are checked against the change that adds `chat_id` and `recursive` to `list_agents`.
The skill roots, instruction files, and rules entries, and the wake-ups entry's lines on the `wait_for_events` arguments, the pull request arguments, background processes, and the tool cards are checked against fork `main` at `525bb3d68b`:

- Subagent types and `spawn_agent` fields: `coderd/x/chatd/subagent_catalog.go:40-46`, `:55-159`.
- Spawn from a child: root-chat tools only for root chats, `coderd/x/chatd/turn_environment.go:732-741`.
  Subagent tools for a `general` child only with `fork-nested-subagents` and below the depth limit, `coderd/x/chatd/fork_subagents.go:425-442`, `:456-484`.
  Direct children only, `isSubagentTarget`, `fork_subagents.go:489-501`.
  The `chat_id` and `recursive` options of `list_agents`, only with `fork-nested-subagents`, `coderd/x/chatd/fork_tools.go:83`, `fork_list_agents.go:50-53`, `:74-84`, limited to the chat and its descendants, `:105-113`, walked through unarchived chats, `:114-118`, `:145-172`, with `parent_chat_id`, `:135-141`.
  Depth flag, `codersdk/fork_chat_config.go:45-52`, lowered by the organization's `max_depth`, `coderd/x/chatd/fork_limits.go:56-58`.
  Inactive tool error, `coderd/x/chatd/chatloop/chatloop.go:1460`.
  Upstream server check, `validateSubagentSpawnParent`, `subagent_catalog.go:254-262`.
- Interrupt cascade and archive of an active family, only with `fork-nested-subagents`: `coderd/x/chatd/fork_cascade.go:43-46`, `:86-89`, 30-second wait, `:26`.
  `archive_agent`, checked against the change that adds it, only with `fork-nested-subagents` and next to `interrupt_agent`, `coderd/x/chatd/fork_tools.go:54-56`, `coderd/x/chatd/fork_archive_agent.go:58-61`, for a direct child only, `:98-100`, interrupted with a 30-second wait, `:105-115`, `fork_cascade.go:27`, `:102-151`, archived in one transaction in the family archive's lock order, skipping chats archived already, `fork_archive_agent.go:147-196`, `coderd/x/chatd/fork_family_lock.go:105-133`, or refused while busy, `fork_archive_agent.go:118-120`, with the result fields, `:34-45`, `:127-130`.
  Archived chats leave the live count, `coderd/database/forkdb/queries/chat_capacity.sql:30-37`.
  Only a root chat's archive state changes through the API, and not to its current value, `coderd/exp_chats.go:2843-2857`, and unarchiving the root unarchives every member, `coderd/x/chatd/chatstate/family.go:110-143`.
  Part of `Task`, `coderd/x/chatd/fork_agent_tool_policy.go:59-62`.
- Every descendant embedded under its root, only with `fork-nested-subagents`: `coderd/fork_chat_tree.go:30-37`, `:52-71`, from `coderd/exp_chats.go:584` and `:1898`, grouped by `root_chat_id`, `coderd/database/db2sdk/db2sdk.go:2311-2318`.
  Each descendant placed in the sidebar under its own parent, `site/src/pages/AgentsPage/components/ChatsSidebar/tree/chatTree.ts:51-60`.
- Agent definition directories: `agent/agentcontext/fork_agents.go:24-29`.
  Front-matter fields and the `general` default base type: `codersdk/workspacesdk/fork_agentdefinition.go:67-81`, `:108-112`, `:180-190`.
  Custom types only with `fork-agent-definitions`, listed after the built-in types, with built-in names winning: `coderd/x/chatd/fork_agent_definitions.go:37-45`, `:51-57`, `:114-119`, `:128-130`, called from `subagent_catalog.go:185` and `:233`.
  The definition's body becomes the child's system prompt, `fork_agent_definitions.go:399`.
  The parent's model unless the definition sets `model`, an explicit `model_config_id` first, `:368-379`, and the model match, `:414-437`.
  A model alias as the last match, preferring the parent chat's AI provider, `coderd/x/chatd/fork_model_aliases.go`, listed by `list_subagent_models`, `subagent.go:409-411`.
  Aliases are set in a model config's `aliases`, `codersdk/chats.go:1958`, and checked on create and update, `coderd/fork_chat_model_aliases.go`.
  Definition files in the chat context, only with `fork-agent-definitions`, `coderd/x/chatd/context_prompt.go:321-325`, `:411-414`, `:444-445`, with the name, description, and warnings of an `ok` file, `fork_agent_definitions.go:445-462`, the status and reason of any other file, `context_prompt.go:359-368`, on the single-chat response, `coderd/exp_chats.go:1882-1883`, and custom types only from `ok` files, `fork_agent_definitions.go:179-182`.
  Listed in the context usage indicator, `site/src/pages/AgentsPage/components/ContextUsageIndicator.tsx:240-280`, `:393-406`, `:433-435`, `:721-727`, which turns to its warning color for a definition with warnings, `:446-458`.
  `computer_use` takes `model_config_id` and `reasoning_effort` only with the experiment, `coderd/x/chatd/subagent.go:618-620`, on a config that supports computer use, `fork_computer_use_overrides.go:36-77`.
- Custom agent tool policy, only with `fork-agent-definitions`: stored at spawn, `fork_subagents.go:312-320`, `:400`, loaded for a chat with a `chat_subagents` row, `coderd/x/chatd/fork_agent_tool_policy.go:230-247`, and applied in `buildTurnEnvironment`, `turn_environment.go:750-753`, `:943-946`.
  The filter, with `readonly`, `fork_agent_tool_policy.go:178-187`, and `web_search`, `:193-224`, Claude Code aliases, `:50-63`, MCP IDs, `:78-91`, and MCP servers, `:97-116`.
  Orchestration guidance dropped, `fork_subagents.go:513`, `:539-542`, `fork_agent_tool_policy.go:125-129`, and the skill index dropped, `turn_environment.go:755-757`.
- Attachments: `coderd/x/chatd/chattool/attachfile.go:26-31`, built with the file tools, `coderd/x/chatd/turn_environment.go:710-713`, with the size limit, `coderd/x/chatd/chattool/attachment.go:17`, `:48-50`.
  Not sent back to the model, `coderd/x/chatd/chatprompt/chatprompt.go:106-109`.
  Text preview and image thumbnail, `site/src/pages/AgentsPage/components/ChatConversation/AttachmentBlocks.tsx:115-117`, `:637`, `:682-691`, raster images only, `coderd/x/chatfiles/mime.go:131-133`.
  MP4 player only with `fork-chat-todos`, `ForkAttachmentPreview.tsx:37-58`, also in the Attachments menu, `TurnAttachments.tsx:116-118`.
- View images, only with `fork-view-image`: `coderd/x/chatd/fork_view_image.go:68-105`, with the path as given, `:78-83`, at most 8 calls per response, `:69-71`, `:84-87`, added in `coderd/x/chatd/fork_tools.go:122-124`, with the turn's model, `coderd/x/chatd/turn_environment.go:743`.
  The model check, `fork_view_image.go:109-115`, against the route's media, `coderd/x/chatd/chatprovider/chatprovider.go:125-164`, where OpenAI-compatible, OpenRouter, and Vercel take any image, `:155-163`.
  The size limit, `fork_view_image.go:121-126`, from `codersdk.AnthropicInlineImageCapBytes` and `codersdk.MaxChatFileSizeBytes`, the type by content, `:146-149`, the structure check, `:154-160`, `:213-256`, `:338-386`, the pixel limits, `:34-42`, `:161-168`, the full decode, `:169-174`, `:258-282`, and no animated GIF, `:175-186`, `:284-336`.
  Stored as a media tool result, `coderd/x/chatd/chatprompt/chatprompt.go:555-556`, and sent again on later turns, `:1142-1157`.
  The bound on the prompt, `fork_view_image.go:43-52`, `:442-559`, with the other media and the pending tail first, counted by bytes and by images, `:458-494`, `coderd/x/chatd/turn_environment.go:743`, and the model's size limit, `fork_view_image.go:454-457`, `:508-510`, also with a compaction override model, `:432-440`, `coderd/x/chatd/generation.go:1339`, applied on every turn unless a caller-supplied tool owns the name, `fork_tools.go:64-70`, `fork_view_image.go:420-430`.
  The tool card with the image, `site/src/pages/AgentsPage/components/ChatElements/tools/Tool.tsx:965-979`.
  Kept by `Read` and `readonly: true`, `coderd/x/chatd/fork_agent_tool_policy.go:51`, `:185-189`, but not as a caller-supplied tool, `:211-215`, and bound to the worktree, `fork_isolation_binding.go:128-133`, unless it is a caller-supplied tool, `:131`.
- Child shares the parent workspace: `coderd/x/chatd/subagent_operations.go:466-471`.
- Worktree isolation, only with `fork-subagent-isolation`: `coderd/x/chatd/fork_tools.go:58-64`, arguments, `coderd/x/chatd/fork_isolation.go:43-47`, `:97-105`, checks, `:107-125`, `:72-74`, with the error prefix, `coderd/x/chatd/subagent_operations.go:493`, result fields, `fork_isolation.go:152-158`.
  The definition's `isolation`, checked at parse, `codersdk/workspacesdk/fork_agentdefinition.go:191-196`, used when the call sets none and the experiment is on, `coderd/x/chatd/fork_isolation.go:64-76`, and an explicit `shared` kept, `:119-121`.
  An invalid definition refused by the API, `coderd/fork_agent_definitions.go:206`, `:229`, `:244-254`, and `invalid` in the workspace's context, `agent/agentcontext/fork_agents.go:122-130`.
  Worktree creation and its errors, `fork_isolation.go:189-237`, `:262-264`, from the worktree that the parent works in when `repo_path` is omitted, `:198-205`, removed only when the spawn fails, `:240-253`, called at spawn, `coderd/x/chatd/fork_subagents.go:294`, `:335-348`.
  Removed after a root archive with the experiment on, `coderd/x/chatd/fork_family_lock.go:28-40`, also an automatic one, `coderd/x/chatd/auto_archive.go:148-152`, `coderd/x/chatd/fork_isolation_cleanup.go:48-139`, unless the agent refuses, `agent/agentgit/fork_worktrees.go:300-353`, or is unreachable or older, `fork_isolation_cleanup.go:141-201`, with a notice in the root chat, `:203-225`.
  Removed by `archive_agent` before it returns, with the same removal, and listed in `kept_worktrees`, checked against the change that adds `archive_agent`, `coderd/x/chatd/fork_archive_agent.go:131-138`, `fork_isolation_cleanup.go:85-137`, and already removed ones taken as gone, `:198-211`.
  The worktree fields and `diff_summary` in `wait_agent`, `coderd/x/chatd/fork_isolation_report.go:48-103`, added with the experiment, `fork_tools.go:60`, counted by the agent, `agent/agentgit/fork_worktree_summary.go:34-96`, with the fields in `codersdk/workspacesdk/fork_worktrees.go:103-117`.
  The **Branch:** and **Worktree:** lines and the diff summary on the cards, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:72-120`, `:151`, `:171`, `:179-202`.
  Branch name, `fork_isolation.go:275-297`, path, `agent/agentgit/fork_worktrees.go:63`, `:178`, `:221-222`.
  The worktree of a child, or of its nearest worktree ancestor through `shared` chats, `coderd/x/chatd/fork_isolation_binding.go:39-70`, named in a system message, `:95-112`, added at spawn, `fork_subagents.go:298-308`, `:344-347`.
  Relative paths and the default `workdir`, `fork_isolation_binding.go:116-137`, `:146-216`.
- Workspace isolation, only with `fork-subagent-isolation`: the `isolation` values and description, `coderd/x/chatd/fork_isolation.go:43-47`, `:97-101`, checks, `:117-118`, `:72-74`, result fields, `:152-158`, and in `wait_agent`, `coderd/x/chatd/fork_isolation_report.go:78-82`.
  **Workspace:** or the error on the cards, only with the experiment, `ForkSubagentFields.tsx:121-134`, `:151`, `:171`, `:194`.
  The template and the latest build's parameters, and the errors, `coderd/x/chatd/fork_isolation_workspace.go:35-70`, created with `create_workspace` as the chat owner and refused without a ready agent and finished startup scripts, `:102-130`, then the prompt, `:72-86`, `:132-167`, called at spawn, `coderd/x/chatd/fork_subagents.go:325-334`, `:413-418`.
  The template checks, `coderd/x/chatd/chattool/createworkspace.go:178-190`, and the chat workspace TTL or the template default, `:192-213`.
  Archived and stopped once on failure, with each failure only logged, `fork_isolation_workspace.go:88-100`, `:170-227`, and the name, `:229-251`.
  Stopped after a root archive with the experiment on, also an automatic one, `fork_family_lock.go:28-40`, `auto_archive.go:148-152`, after the worktree cleanup, whatever it keeps, once and only logged, and skipped for a child bound to another workspace, `fork_isolation_cleanup.go:48-61`, `:90-95`, `:124-139`.
  Stopped in the background after `archive_agent` with the same stop, checked against the change that adds `archive_agent`, `fork_archive_agent.go:137`, `:198-212`, `fork_isolation_cleanup.go:107-112`, `:139-157`.
  No delete: nothing in `coderd/x/chatd` deletes a workspace, and the workspace tools are root-only, `coderd/x/chatd/turn_environment.go:732-741`.
- `wait_agent` returns the last assistant message: `subagent_operations.go:635-667`.
- Concurrency default and queued refusals: `coderd/x/chatd/agentadmission.go:13`, no cap when an entitlement unlocks it, `:60-65`, `enterprise/coderd/x/chatd/agentadmission.go:18-28`, `coderd/x/chatd/worker.go:275-283`.
  Subagent limits, only with `fork-nested-subagents`: flags, `codersdk/fork_chat_config.go:54-83`, the lower of the deployment and organization values, `fork_capacity.go:32-45`, `fork_limits.go:49-60`, `:64-72`, returned by the organization limits API, `coderd/fork_subagent_limits.go:30-51`, `:133-140`, without the default cap, `fork_capacity.go:119-130`.
  The organization settings section, only with `fork-nested-subagents` and for a user who can read the organization's chat model configs, `site/src/pages/AISettingsPage/CoderAgentsPage/ForkSubagentLimits.tsx:31-55`, read-only without edit permission, `components/ForkSubagentLimitsSettings.tsx:179-189`.
  Concurrency limits without the ancestors, `fork_capacity.go:75-117`, and `waiting_for_capacity` in `list_agents`, `coderd/x/chatd/fork_list_agents.go:176-182`, `:223-230`, from `fork_capacity.go:146-192`.
  The `list_agents` card's **waiting for capacity**, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkListAgentFields.tsx:16-27`, in each row, `ListAgentsTool.tsx:58`.
  The family API, only with the experiment, `coderd/fork_chat_routes.go:45-48`, status 400 for a child chat, `coderd/fork_chat_family.go:29-34`, and its fields, `codersdk/fork_chat_family.go:16-28`, from `coderd/x/chatd/fork_family.go:22-67`.
  The sidebar hourglass and subtitle for a waiting child, and the chip at the live limit for an expanded root chat with a live subagent, only with the experiment and for the chat owner, `site/src/pages/AgentsPage/components/ChatsSidebar/tree/ForkFamilyCapacity.tsx:30-34`, `:48-91`, `:98-115`, polled every 5&nbsp;seconds, `site/src/api/queries/forkChatFamily.ts:12-19`, in each row, `ChatTreeNode.tsx:142-159`, `:347-349`.
  The same hourglass and label in the top bar of a `running` subagent's chat that waits, only with the experiment and for the chat owner, `site/src/pages/AgentsPage/components/ForkSubagentSlotBadge.tsx:26-51`, `ChatTopBar.tsx:281-286`.
  `list_agents` pages of 10 and `paused`, `coderd/x/chatd/subagent.go:565-584`, and `wait_agent` on a `paused` child, `:481-489`.
  `wait_agent` returns at once for `requires_action`, `coderd/x/chatd/subagent_operations.go:621-632`, and `interrupt_agent` moves it to `running`, `:159-169`.
  `interrupt_agent` interrupts the active descendants first, for any status, `:159-162`, `coderd/x/chatd/chat_mutations.go:902`, `:920`, `coderd/x/chatd/fork_cascade.go:32-39`, `:60-80`.
  Live limit on spawn and resume, `fork_subagents.go:73-89`, `:222-251`, and its error, `:263-275`, with the live statuses, `coderd/database/forkdb/queries/chat_capacity.sql:30-37`.
  The error on a failed `spawn_agent` card, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:160-163`, `:172-174`, `forkToolUtils.ts:14-17`, in the card, `SubagentTool.tsx:260-264`, from `Tool.tsx:543`.
  Stored definitions: user routes, `coderd/fork_agent_definitions.go:32-170`, organization routes, `coderd/fork_organization_agent_definitions.go:26-180`, shared with the whole organization at creation, `:76-80`, with the sharing routes, `coderd/fork_organization_agent_definitions_acl.go:35-181`, and project routes, `coderd/fork_project_agent_definitions.go:20-190`.
  Offered to `spawn_agent` when enabled and readable by the chat owner, `coderd/x/chatd/fork_agent_definitions.go:212-311`, and for a project the owner owns or that is shared with them, `:282-285`, from the root chat for a child, `coderd/x/chatd/project_instructions.go:17-51`, `coderd/x/chatd/projects.go:16-61`.
  The personal definitions page: the **Settings** button, `site/src/pages/AgentsPage/components/ChatsSidebar/chats/ChatsPanel.tsx:513-520`, the route, `site/src/forkRoutes.tsx:31`, and the nav item and page, only with `fork-agent-definitions`, `site/src/pages/AgentsPage/components/ChatsSidebar/settings/ForkSettingsNavItems.tsx:18-29`, `site/src/pages/AgentsPage/AgentSettingsPage/ForkAgentDefinitionsPage/ForkAgentDefinitionsPage.tsx:21-23`, with the organization picker, `:25-51`.
  The **Agents** row, only with `fork-agent-definitions`, `ChatsPanel.tsx:572-574`, `site/src/pages/AgentsPage/components/ChatsSidebar/chats/ForkSidebarNavItems.tsx:13-29`, and the empty state, `ForkAgentDefinitionsTableView.tsx:143-156`, `:178-190`.
  Its table of the member's personal definitions, `site/src/api/forkAgentDefinitions.ts:17-35`, with full access on this page, `ForkAgentDefinitionsTableView.tsx:118`, the **Enabled** switch, `:103-108`, the **Edit** and **Delete** menu items and the delete dialog, `:236-240`, `:250-257`, `:317-343`, `ForkAgentDefinitionsTable.tsx:25-31`, and **New agent definition**, `ForkAgentDefinitionsTableView.tsx:143-156`, `:275`, calls the user routes, `forkAgentDefinitions.ts:17-85`.
  The editor dialog, `ForkAgentDefinitionsTableView.tsx:310-315`, with its title, fields, import, and buttons, `ForkAgentDefinitionEditorDialog.tsx:197-217`, `:242-264`, `:351-387`, `:405-418`, the read-only name of an edit, `site/src/pages/AgentsPage/components/SkillEditor.tsx:482-484`, the other front matter kept, `agentDefinitionMarkdown.ts:165-216`, and the create, the refused save over a changed definition, and the save, `useForkAgentDefinitionEditor.ts:108-159`, `:160-269`, with no version in the update request, `codersdk/fork_agent_definitions.go:52-55`.
  The organization definitions page, only with `fork-agent-definitions`, `site/src/forkRoutes.tsx:37-42`, `site/src/pages/AISettingsPage/ForkAgentDefinitionsPage/ForkOrganizationAgentDefinitionsPage.tsx:27`, `:40-42`, linked from **AI Settings**, `site/src/modules/management/AISettingsSidebarView.tsx:174-181`, `:218-226`, `site/src/modules/management/forkAISettingsNav.ts:18-36`, for a user who can read organization skills, `site/permissions.json:102-105`, `coderd/rbac/roles.go:497`, `:585-587`, `:637`.
  Its organizations, the `org` parameter, and the denial, `ForkOrganizationAgentDefinitionsPage.tsx:44-66`, `:74-82`, `:113-128`, the access from the organization skill permissions, `:89-102`, `site/src/modules/permissions/organizations.ts:160-187`, applied by the table, `ForkAgentDefinitionsTableView.tsx:93-110`, `:158-159`, `:236-240`, `:250-257`, `:143-156`, `:275`, with the organization delete message, `ForkAgentDefinitionsTable.tsx:32-38`, and the organization routes, `site/src/api/forkAgentDefinitions.ts:87-149`.
  On the organization page, **Manage permissions** only for a member who can share, `site/src/pages/AISettingsPage/ForkAgentDefinitionsPage/ForkOrganizationAgentDefinitionsPage.tsx:67`, `:103-112`, `ForkAgentDefinitionsTableView.tsx:241-249`, opens **Agent definition permissions**, `ForkOrganizationAgentDefinitionsPage.tsx:131-138`, `ForkOrganizationAgentDefinitionSharingDialog.tsx:44-94`, which calls the sharing routes, `forkAgentDefinitions.ts:151-192`, and closes when the organization changes, `ForkOrganizationAgentDefinitionsPage.tsx:68-72`.
  The project's **Agent definitions** row, only with `fork-agent-definitions`, `site/src/pages/AgentsPage/components/ProjectPage/ProjectDetailsPanel.tsx:80-88`, `:142-156`, `ProjectDetailsPanelView.tsx:166-172`, opens the project's table, `ProjectAgentDefinitionsDialog.tsx:35-42`, with full access only for a user who can update the project, `ProjectDetailsPanel.tsx:214-220`, `site/src/pages/AgentsPage/utils/skills.ts:331-341`, with the project delete message, `ForkAgentDefinitionsTable.tsx:39-45`, and the project routes, `site/src/api/forkAgentDefinitions.ts:194-265`.
  Disabled personal definitions offer no type, `coderd/x/chatd/fork_agent_definitions.go:237-240`.
  The definition name without its prefix in subagent tool results, stored at spawn, `coderd/x/chatd/fork_subagents.go:393-398`, and reported by `coderd/x/chatd/fork_subagent_types.go:25-51`, `:86-112`, `fork_tools.go:57`.
  A child chat's `agent_type`, and a root chat's when it started as an agent type, in the chat list, the single-chat response, and a `created` watch event, only with the experiment, `coderd/exp_chats.go:614`, `:1942`, `:331`, `coderd/fork_chat_agent_type.go:18-71`, with a built-in child's type from its mode, `coderd/database/forkdb/queries/chat_subagents.sql:15-30`, and the badge, `site/src/pages/AgentsPage/components/ForkAgentTypeBadge.tsx:12-24`, in the sidebar, `ChatsSidebar/tree/ChatTreeNode.tsx:346`, and the top bar, `ChatTopBar.tsx:280`.
  **Agent type:** on the `spawn_agent` card of a custom child, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:155-159`, `:166-170`.
  A root chat started as an agent type, set by `agent_type`, `codersdk/chats.go:933-937`, only with the experiment, `coderd/fork_chat_root_agent.go:43-47`, failing with status 400, `:56-63`, from `coderd/exp_chats.go:1467-1470`, and reported in the create response, `:1627-1629`.
  Resolved without workspace definitions or other built-in types, `coderd/x/chatd/fork_root_agent.go:26`, `:83-119`, on the definition's model and reasoning effort, `:134-145`, `coderd/fork_chat_root_agent.go:69-78`, even without a default model, `:37-39`, `:73-75`, with the effort kept on a chat created without a message, `fork_root_agent.go:184-193`, `coderd/x/chatd/chatd.go:777-779`.
  With its prompt and mode, `fork_root_agent.go:126-129`, `:154-173`, `chatd.go:617`, without the orchestration guidance when it cannot spawn, `fork_root_agent.go:131`, `:175-182`, `chatd.go:653`, and with the workspace guidance limited to its tools, `fork_root_agent.go:132-133`, `:195-207`, `fork_agent_tool_policy.go:137-149`, `chatd.go:735`.
  The ACP guidance only with every ACP tool, `fork_agent_tool_policy.go:151-162`, `turn_environment.go:754`, a removed dynamic tool failing as an unknown tool, `fork_agent_tool_policy.go:164-176`, `turn_environment.go:946`, and its `chat_subagents` row at depth 0 with `shared` isolation, `fork_root_agent.go:209-231`, `chatd.go:810`.
  The agent types before a chat exists, only with the experiment, `coderd/fork_chat_routes.go:74-77`, `:87`, with the `project`, `workspace`, and `model_config_id` parameters, `coderd/fork_agent_types.go:37-105`, each type's root availability, `coderd/x/chatd/fork_agent_type_catalog.go:39-109`, and the fields, `codersdk/fork_agent_types.go:19-38`.
  The agent type picker of a new chat, only with `fork-agent-definitions` and an organization, `site/src/pages/AgentsPage/components/useForkAgentTypePicker.ts:29-30`, in the form's row of pickers, `site/src/pages/AgentsPage/components/AgentCreateForm.tsx:1003-1006`, `:1051-1056`, with the form's organization, project, workspace, and model, `:772-777`, `useForkAgentTypePicker.ts:32-39`, the **Default agent** label, `site/src/pages/AgentsPage/components/ChatElements/ForkCompactAgentTypeSelector.tsx:28`, `:71`, `:89-90`, the search and the groups by source, `:30-44`, `:108-113`, `:152-159`, the unavailable types, `:160`, `:168-170`, and the link, `:183-195`.
  The chosen type sent as `agent_type`, `AgentCreateForm.tsx:798`, `site/src/pages/AgentsPage/AgentCreatePage/AgentCreatePage.tsx:296`, `:319`, falling back to the default agent, `useForkAgentTypePicker.ts:40-46`, with the form's model, `AgentCreateForm.tsx:587-595`, `:789`, `AgentCreatePage.tsx:316`.
  The **Available agents** page, only with `fork-agent-definitions`, the route, `site/src/forkRoutes.tsx:32`, the nav item, `site/src/pages/AgentsPage/components/ChatsSidebar/settings/ForkSettingsNavItems.tsx:18-20`, `:30-36`, and the page, `site/src/pages/AgentsPage/AgentSettingsPage/ForkAvailableAgentsPage/ForkAvailableAgentsPage.tsx:103`, `:138-140`, with your organizations, `:96-98`, `:104-108`, `:201-213`, the menus of its projects and your workspaces, `:53-55`, `:70`, `:84`, `:110-114`, `:122-136`, `:207-229`, the columns, `:176-190`, `:234-243`, the source labels, `site/src/pages/AgentsPage/components/ChatElements/ForkCompactAgentTypeSelector.tsx:31-37`, and the recent chats from the sidebar's default list, matched by the name without its prefix, `ForkAvailableAgentsPage.tsx:46-51`, `:115-121`, `:143-174`.
  Bare names for one source and qualified names for several, `fork_agent_definitions.go:107-160`, `coderd/x/skills/skills.go:153-229`, and the error for an unknown or ambiguous type, `subagent_catalog.go:233-242`.
  The `@` menu in the composer, only with `fork-agent-definitions`, `site/src/pages/AgentsPage/components/ChatMessageInput/ForkAgentMentionPlugin.tsx:146`, `:234-236`, opened at the start of a line or after a space, `:46-58`, with the built-in types and each source's agents under the names that `spawn_agent` accepts, `:60-95`, `:97-116`, `:170-198`, and inserting the delegation, `:118-130`, in a child chat only for a `general` child with `fork-nested-subagents`, `:156-161`, with its root chat's project, `:162-169`, holding Enter while agents load, `:215-232`, and rendered in Lexical's listbox, `:238-268`, registered in `ChatMessageInput.tsx:1405-1409`.
- Family messages, only with `fork-family-messages`: `codersdk/fork_experiments.go:61-63`, `coderd/x/chatd/fork_tools.go:83-85`, without the plan-mode, `explore`, and `readonly` tools, `coderd/x/chatd/turn_environment.go:743`, `:880`, `:2048-2064`, `:883-887`, `:2120-2130`, `coderd/x/chatd/fork_agent_tool_policy.go:181-187`, `:203`, and part of `Task`, `:59-62`.
  `list_family_chats`, `coderd/x/chatd/fork_family_messages.go:254-342`, with `parent_chat_id` only for a chat with a parent, `:269-275`, `:327-329`, with each relation, `:344-362`, from the unarchived family, `coderd/database/forkdb/queries/chat_family_messages.sql:17-28`.
  `message_family_chat`, `fork_family_messages.go:32-41`, `:55-86`, with the refused sends, `:104-117`, `:146-161`, the hourly limit, `:168-180`, `chat_family_messages.sql:5-7`, the live limit and the secret question with `fork-nested-subagents`, `fork_family_messages.go:182-187`, `coderd/x/chatd/fork_subagents.go:161-164`, `:216-251`, the delivery that steers a busy recipient, `fork_family_messages.go:188-227`, `coderd/x/chatd/chatd.go:462-464`, and the envelope, `fork_family_messages.go:230-252`, also around a hook's prompt, `:373-394`, `coderd/x/chatd/chat_mutations.go:129`.
  The cards, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:87-96`, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkFamilyMessageTool.tsx:15-67`, `:89-153`, and the recipient's message, `site/src/pages/AgentsPage/components/ChatConversation/ForkFamilyMessageHeader.tsx:19-56`, `site/src/pages/AgentsPage/components/ChatElements/tools/forkFamilyMessage.ts:14-15`, `:38-49`, `site/src/pages/AgentsPage/components/ChatConversation/UserMessageContent.tsx:137-140`, `:162-164`.
- `ask_user_question` in plan-mode root turns, and outside plan mode in web UI root chats: `coderd/x/chatd/turn_environment.go:725-731`, async description, `coderd/x/chatd/chattool/askuserquestion.go:17-24`, and in API root chats with `fork-nested-subagents`, `coderd/x/chatd/fork_tools.go:46-53`.
  For a child that isn't an `explore` child, only with `fork-nested-subagents`, `coderd/x/chatd/fork_descendant_questions.go:44-46`, `fork_tools.go:41-45`, ending the turn, `fork_descendant_questions.go:50-60`, `coderd/x/chatd/turn_environment.go:1095`, and not in plan-mode child turns, `:2052-2061`.
  Unanswered until the child gets a user message after the question, and a secret question until the message of its answer or decline, `coderd/database/forkdb/queries/chat_descendant_questions.sql:10-44`.
  `needs_input` and the questions in `wait_agent`, `fork_descendant_questions.go:366-380`, which first waits for the answer when the question is unanswered at the call, `:334-353`, `:385-406`, added with the experiment, `fork_tools.go:54-56`.
  The family's unanswered questions, oldest first, `chat_descendant_questions.sql:46-80`, from `GET /api/v2/chats/fork/{chat}/questions`, `coderd/fork_chat_routes.go:54`, `coderd/fork_chat_questions.go:53-102`.
  The root chat page shows the oldest one only with the experiment and only on a root chat the viewer can act on, `site/src/pages/AgentsPage/components/ForkDescendantQuestionDock.tsx:51-59`, `:93-94`, `:137-150`, and posts the answer to the asking chat, `site/src/api/queries/forkChatQuestions.ts:66-68`.
  The root chat's own open question hides it until that answer is queued, `site/src/pages/AgentsPage/components/ChatPageContent.tsx:224-229`, `:288-302`.
  Secret questions, not in a computer-use child, with the description and the schema, `coderd/x/chatd/fork_descendant_questions.go:78-128`, the errors, `:143-163`, and the placeholder in the result, `:180-184`, `coderd/x/chatd/fork_secret_answers.go:40-44`.
  The answer route, `coderd/fork_chat_routes.go:55`, with its 8-byte and 16&nbsp;KiB limits, `coderd/fork_chat_questions.go:35-38`, `:118-155`, the answer message, `:186-193`, and the decline message, `:212-260`, posted from the root chat's masked field, note, **Submit**, and **Decline**, `site/src/pages/AgentsPage/components/ForkDescendantQuestionDock.tsx:153-158`, `:191-325`, `site/src/api/forkChatQuestions.ts:31`.
  The value only in `execute`, `write_file`, and `edit_files`, `coderd/x/chatd/fork_secret_answers.go:31-34`, the scrubbed results and errors without media, `:36-80`, `:128-176`, the forms the scrub finds, `:399-408`, and the `execute` refusal, `:219`.
  The answers kept for 24&nbsp;hours, `coderd/database/forkdb/queries/chat_secret_answers.sql:5-9`, `:14-27`, and not removed by a later question, `fork_descendant_questions.go:226-229`.
  The `message_agent` error, `coderd/x/chatd/fork_subagents.go:216-218`, the refused send or edit in the child's chat, `coderd/fork_chat_questions.go:391-395`, `coderd/exp_chats.go:3378-3379`, `:3533-3534`, the notice on the child's question, `site/src/pages/AgentsPage/components/ChatElements/tools/Tool.tsx:698-711`, and the `wait_agent` note, `fork_descendant_questions.go:36-39`, `:373-380`.
- Skill roots and containers: `agent/agentcontext/defaults.go:9-29`, `agent/agentcontext/resolve.go:66-71`.
- Instruction files: `resolve.go:48-52`.
- Rules: the user rule directories, `agent/agentcontext/defaults.go:18-21`, and `.cursor/rules` of each scan root, with their subfolders, `agent/agentcontext/fork_rules.go:26-29`, `:112-159`, read by the resolver, `agent/agentcontext/resolve.go:391`, as instruction files only with `alwaysApply` or `globs`, `fork_rules.go:161-233`, and glob rules pushed only while coderd advertises them, `:235-254`, `resolve.go:184`, which it always does, `coderd/agentapi/context.go:307`, `:328`.
  Glob rules kept out of the always-on block and attached by the touched paths, relative to the rule's project, `coderd/x/chatd/context_prompt.go:247-265`, `:300`, `coderd/x/chatd/fork_scoped_rules.go:30-80`, `:82-151`, `coderd/x/chatd/turn_environment.go:470`, from the prompt history, `:174`.
- Skills one level deep, with `name` equal to the directory: `resolve.go:61-64`, `:632-647`.
- No skill in a chat's first message: `coderd/x/chatd/chatd.go:671-679`, `coderd/x/chatd/skill_invocation.go:37`.
- Working directory: `coderd/x/chatd/context_prompt.go:195-198`, `agent/agentproc/process.go:400-413`.
- `execute` environment: the workspace agent's process API, `codersdk/toolsdk/workspacetools/execute.go:184-190`, `agent/agent.go:459`, runs `sh -c`, `agent/agentproc/process.go:150`, with the agent's environment, `:164-187`, `agent/agent.go:1527`, and the agent's `env` with `$PATH` expanded, `:1581-1588`.
- `explore` tools: `turn_environment.go:2124-2133`.
- Hidden skills: `coderd/x/chatd/chattool/skill.go:55-62`, `turn_environment.go:2185-2201`.
- `find_tools`: `coderd/x/chatd/chattool/findtools.go:19`, only with MCP tools, `coderd/x/chatd/mcp_tool_search.go:121-136`, which also omits it on a name collision.
- Omitted `model_config_id`: subagent model override first, `subagent_catalog.go:60-77` and `:84-102`, else the parent's model, `subagent_operations.go:332-335`.
- Project memory for root chats: `coderd/x/chatd/memory.go:20-33`.
- Todo tools: `coderd/x/chatd/fork_chat_todos.go`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go`. The plan-mode and `explore` allowlists, `builtinPlanToolAllowed` and `exploreToolAllowed` in `turn_environment.go`, don't list them.
  Item statuses, `fork_chat_todos.go:41`, checked against the tool schema before the tool runs, `coderd/x/chatd/toolinput.go:22-30`, `:125-127`.
  Snapshot after a compaction, `todoSnapshotMessage`, `coderd/x/chatd/fork_context.go:105-108`.
  Composer pill, only with `fork-chat-todos` and a non-empty list, `site/src/pages/AgentsPage/components/ForkComposerSlot.tsx:19-31`, `ChatTodoPill.tsx:94-108`.
  Checklist card for each call, only with `fork-chat-todos`, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:24-36`, `:43-49`, with its labels, `ForkTodoTool.tsx:12-25`, `:42-52`, and its expandable list or error, `:53-74`, `:90-101`.
  The children's lists in `list_agents`, `coderd/x/chatd/fork_list_agents.go:42`, `:192`, `:212-216`, and the progress on its card, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkListAgentFields.tsx:17-19`, `:28-30`.
- Notes tool: `coderd/x/chatd/fork_chat_notes.go:75-95`, added only with `fork-token-budget`, `codersdk/fork_experiments.go:69-72`, `coderd/x/chatd/fork_tools.go:119-121`. The plan-mode, `explore` and read-only custom agent allowlists, `builtinPlanToolAllowed` and `exploreToolAllowed` in `turn_environment.go` and `customAgentReadOnlyToolAllowed` in `fork_agent_tool_policy.go`, don't list it.
  The actions and their errors, `fork_chat_notes.go:97-187`, the path rule and the limits, `:22-35`, also checked by the table, `coderd/database/migrations/chatdmig/nodes/01a1d71e-84b7-78ae-99dd-2cb2e94742e3_chat_notes.up.sql`, and the note cap, `coderd/database/forkdb/queries/chat_notes.sql:13-48`.
  Each chat reads only its own notes, `chatNotesTool` binds the chat, `fork_chat_notes.go:75-81`.
  The calls of one step run one at a time in call order, `serialChatNotesTool`, `fork_chat_notes.go:89-95`.
  A replayed call that wrote, appended to or deleted a note returns its first result and changes nothing, `fork_chat_notes.go:138-177`, `:189-207`. The record of each call, `coderd/database/migrations/chatdmig/nodes/01a1d903-ff52-75e8-91c2-e50d900ac0b7_chat_note_calls.up.sql`, is written in the statement that changes the note, `chat_notes.sql:13-69`.
- Chat history tool: `coderd/x/chatd/fork_chat_history.go:141-158`, added only with `fork-token-budget`, `coderd/x/chatd/fork_tools.go:119-121`, and not listed by the plan-mode, `explore` and read-only custom agent allowlists.
  The windows and the clear floor, `fork_chat_history.go:189-227`, from `coderd/database/forkdb/queries/chat_history.sql:1-25`. The search, `fork_chat_history.go:394-470` with `chat_history.sql:27-49`. The slices of `read_item`, `fork_chat_history.go:532-589`. The 16&nbsp;KiB cap, `fork_chat_history.go:26-45`, and the fit of a whole message, `fork_chat_history.go:591-634`.
- Context windows: `new_context`, `coderd/x/chatd/fork_token_budget.go:300-331`, added only with `fork-token-budget`, `coderd/x/chatd/fork_tools.go:120`, and not listed by the plan-mode, `explore` and read-only custom agent allowlists. Eligible turns keep `notes`, `chat_history` and `new_context`, `fork_token_budget.go:98-114`.
  The points of a window, `newTokenBudget`, `fork_token_budget.go:76-89`. The requests, `tokenBudgetMessage` and `tokenBudgetRequestDue`, `fork_token_budget.go:363-496`, sent before each step, `coderd/x/chatd/fork_context.go:64-72`.
  The rollover trigger, `tokenBudgetTriggers`, `fork_token_budget.go:116-143`, called from `coderd/x/chatd/turn_environment.go:1042`. The lead-in without a model call, which keeps the model context of a `pre_compact` hook, `fork_token_budget.go:166-242` and `:267-298`, called from `coderd/x/chatd/generation.go:1360` for automatic compactions only. Unanswered tool calls run before a compaction, `decideGenerationAction`, `generation.go:198`.
  The window divider: a rollover names the window and its trigger, `fork_token_budget.go:199-202`, `:222`, in the `context_window` field of the `chat_summarized` result, `withContextWindowResult`, `fork_token_budget.go:244-265`, for the streamed result, `:239`, and the stored one, `coderd/x/chatd/message_conversion.go:399`. The field type, `coderd/x/chatd/chatloop/fork_context_window.go`, held by `CompactionResult`, `chatloop/compaction.go:116`.
  The divider replaces the summary card only with `fork-token-budget`, `withForkContextWindow`, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkContextWindowDivider.tsx:62-77`, hooked in `Tool.tsx:22`, `:1194`. A result without a window, or an error, keeps the card.
  Tests: `TestTokenBudgetWindows` in `coderd/x/chatd/fork_token_budget_test.go`, the unit tests in `fork_token_budget_internal_test.go`, `ForkContextWindowDivider.test.ts` and the stories in `ForkContextWindowDivider.stories.tsx`.
- Transcript tool: `coderd/x/chatd/fork_chat_transcript.go:68-85`, added only with `fork-chat-todos`, `coderd/x/chatd/fork_tools.go:30-35`.
  Reads as the chat owner, `fork_chat_transcript.go:109-116`.
  `include_tool_calls` on `wait_agent`: `coderd/x/chatd/fork_wait_agent.go:31`, `:38`, `:84-85`, `:101-106`, added with the same experiment, `fork_tools.go:34`.
  The recent tool calls and `tool_calls_error` on the `wait_agent` card, only with the same experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/ForkSubagentFields.tsx:36-70`, `:186-191`, `:203-206`.
  Chat search: `coderd/x/chatd/fork_search_chats.go:28-32`, `:51-67`, added with the same experiment, `fork_tools.go:33`, and run as the chat owner, `fork_search_chats.go:97-103`.
  Cards for both calls, only with `fork-chat-todos`, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:24-36`, `:50-59`, with the search card's labels, query, chat links, and more-results note, `ForkSearchChatsTool.tsx:58-69`, `:82-101`, and the transcript card's labels, chat link, message lines, and more-results note, `ForkChatTranscriptTool.tsx:70-78`, `:93-125`.
  The plan-mode and `explore` allowlists in `turn_environment.go` don't list either tool.
- A chat's PRs: one entry per chat, remote, and branch, `coderd/database/queries/chats.sql:2035-2058`, stored when a git command in the workspace asks Coder for a token, `coderd/workspaceagents.go:2286-2296`, `coderd/x/gitsync/worker.go:392-405`.
  The branch's open PR, `coderd/externalauth/gitprovider/github.go:207-248`, looked up only for an entry without a PR or with a PR from another branch, `coderd/x/gitsync/gitsync.go:298-317`, `:324-330`, and on a read of the chat's diff, which stores it, `coderd/exp_chats.go:4404`, `:4845-4876`, `:4759-4779`.
  The diff panel's read, `site/src/pages/AgentsPage/components/DiffViewer/RemoteDiffPanel.tsx:78-81`.
  The top bar, `site/src/pages/AgentsPage/components/ChatTopBar.tsx:212-216`, `:340-366`, and the sidebar, `ChatsSidebar/tree/statusConfig.ts:105-107`, `ChatsSidebar/tree/ChatTreeNode.tsx:263`.
- No tool that creates or changes a PR: in the Go files of `coderd/x/chatd`, `pull_request` appears only in the read-only watch of `wait_for_events`, `coderd/x/chatd/fork_wait_for_events.go` and `coderd/x/chatd/fork_pull_request_events.go`.
- Wake-ups, only with `fork-chat-wakeups`: `schedule_wakeup`, `wait_for_events`, and `notify_user`, `coderd/x/chatd/fork_tools.go:36-40`, not in the plan-mode or `explore` allowlists, `turn_environment.go:2052-2068`, `:2124-2133`.
  Arguments and result, `coderd/x/chatd/fork_schedule_wakeup.go:54-70`, `:99-113`, the description, `:77-82`, one schedule, `:119-142`, and the limits, `:31-35`, `:160-202`, `:226-228`.
  `/loop`, `coderd/x/chatd/fork_loop_command.go:26-31`, `:50-70`, `:111-146`, refused with status 400 and the reason, `:33-41`, `coderd/fork_chat_loop_errors.go:15-26`, for a new chat or a message, `coderd/exp_chats.go:1807-1809`, `:3375-3377`, scheduled with the message, `coderd/x/chatd/chat_mutations.go:88`, `:222`, or with a new chat, `coderd/x/chatd/chatd.go:687`, `:810`.
  Delivery every 15&nbsp;seconds, `coderd/x/chatd/fork_wakeups.go:28`, `:45-63`, as the chat owner, `:246-265`, after a busy chat goes idle, `:143-151`, canceled for an archived chat, `:139-140`, or at the archive, with every wake-up of the family, `coderd/x/chatd/fork_family_lock.go:44-46`, `:61-91`, `fork_wakeups.go:392-406`, or of each chat that `archive_agent` archives, `coderd/x/chatd/fork_archive_agent.go:187-190`, and no missed runs replayed, `fork_wakeups.go:334-370`.
  `wait_for_events` arguments, defaults, and result, `coderd/x/chatd/fork_wait_for_events.go:28-62`, the description, `:99-115`, the watched children and the errors, `:252-313`, the expiry and the prompt, which `validate` checks like a `schedule_wakeup` prompt, `:315-344`, `fork_schedule_wakeup.go:160-169`.
  One message per done child, while the chat is idle, `fork_wait_for_events.go:313-419`, `fork_wakeups.go:143-153`, and completed after the last child, `fork_wait_for_events.go:421-438`, polling every 15&nbsp;seconds, `fork_wakeups.go:354-355`.
  Done children, `fork_wait_for_events.go:487-500`, with the children that wait for their own `wait_for_events` or for an answer read in one snapshot, `:502-546`.
  The pull request arguments, `fork_wait_for_events.go:41-42`, the description, `:107-109`, the check of `pull_request_events` before the read, `:175-213`, the result, `:233-237`, and the default prompt, `:339-344`.
  The events, the prompt, the poll and retry times, and the 20-line cap, `coderd/x/chatd/fork_pull_request_events.go:25-55`, and the read at the call with the unknown event and closed errors, `:69-126`.
  What wakes the chat, `:169-181`, without activity after the expiry, `:134-167`, the message without bodies, `:183-223`, one per pull request, `:128-132`, `fork_wait_for_events.go:371-398`, the poll once a minute while it waits, `:401-404`, and the failed reads, `fork_pull_request_events.go:225-252`.
  The read with the chat owner's linked account and its errors, `coderd/fork_chat_pull_requests.go:24-84`, wired in `coderd/coderd.go:977`, only for GitHub, `coderd/externalauth/gitprovider/fork_github_activity.go:85`, `:101-104`, without the owner's own reviews and comments, draft reviews, or older comments, `:140-200`, and the CI state, `:202-295`.
  Background processes, `fork_wait_for_events.go:43-47`, `coderd/x/chatd/fork_process_events.go:30-54`, with the checks of the output pattern fields, `:62-98`, the read at the call and its errors, `:100-168`, the result, `:56-60`, `fork_wait_for_events.go:238-241`, and the default prompt, `:339-344`.
  The delivery, `fork_wait_for_events.go:453-492`, the last check at the expiry, `:394-402`, `:558-564`, with the late check's process list, `fork_process_events.go:466-482`, `agent/agentproc/api.go:138-150`, the exit, lost-watch, and matching-lines messages, `fork_process_events.go:287-337`, `:387-417`, `:484-534`, the cool-down and the limit, `:347-379`, the kept output, `:419-464`, `agent/agentproc/headtail.go:12-28`, `:259-303`, and the poll and retry times, `fork_wait_for_events.go:494-499`, `:439-442`, `fork_process_events.go:41-43`, `fork_wakeups.go:28`.
  The wake-up routes, only with the experiment, `coderd/fork_chat_routes.go:57-62`, the list, `coderd/fork_chat_wakeups.go:23-61`, and the change, as an update of the chat, with its errors, `:71-167`, `coderd/x/chatd/fork_wakeup_controls.go:20-76`, a pause that keeps the run time and the reasons, `:24-27`, `:78-92`, and a resume, `:94-114`, with the fields, `codersdk/fork_chat_wakeups.go:38-85`.
  The composer pill, only with the experiment, `site/src/pages/AgentsPage/components/ForkComposerSlot.tsx:32-34`, with the open wake-ups and their number, `ForkChatWakeupsPill.tsx:54-132`, each row and its buttons, `:135-150`, `:165-260`, read every 30&nbsp;seconds, `site/src/api/queries/forkChatWakeups.ts:13-22`.
  The tool cards, only with the experiment, `site/src/pages/AgentsPage/components/ChatElements/tools/forkToolRenderers.tsx:29-41`, `:70-84`, the `schedule_wakeup` card, `ForkScheduleWakeupTool.tsx:12-43`, `:53-99`, the `wait_for_events` card, `ForkWaitForEventsTool.tsx:20-64`, `:72-161`, and the `notify_user` card, `ForkNotifyUserTool.tsx:10`, `:17-62`.
  The daily limits, `coderd/x/chatd/fork_wakeup_caps.go:26-43`, `:117-156`, replaced by the organization's limits, `:53-91`, `coderd/fork_wakeup_caps.go`, `coderd/database/forkdb/queries/chat_organization_wakeup_caps.sql`, checked before each send, `fork_wakeups.go:155-157`, `fork_wait_for_events.go:408-410`, over delivered runs and every message cost, `coderd/database/forkdb/queries/chat_wakeups.sql:192-225`.
  The other pauses, `fork_wakeups.go:312-315`, `:347-352`, `fork_wait_for_events.go:325`, and the notification after each pause, `fork_wakeups.go:210-212`, `:304-306`, `:386-388`, with its title, reasons, and the chat title in its text, once per chat, reason, and title a UTC day, `fork_wakeup_caps.go:38-51`, `:93-115`, `:168-199`, by the dedupe hash, `coderd/database/migrations/000245_notifications_dedupe.up.sql:13-26`.
  The limit of 10 counts active and paused wake-ups, `fork_schedule_wakeup.go:213-228`.
- Notifications, only with `fork-chat-wakeups`: `notify_user`, `fork_tools.go:39`, with its arguments, result, and hourly limit, `coderd/x/chatd/fork_notify_user.go:23-38`, `:40-56`, the checks and errors, with the default title cut to 120 bytes and pause notifications in the count, `:72-123`, `:147-154`, and the send with a dedupe bucket per minute, `:124-143`, which enqueues nothing without a delivery method, `coderd/notifications/enqueuer.go:104-117`, removed by a custom agent tool policy, `fork_agent_tool_policy.go:263-284`, with pause notifications sent past the limit, `fork_wakeup_caps.go:102-128`, counted in `chat_wakeups.sql:228-234`.
  The title and message limits, `codersdk/notifications.go:299-321`, the inbox and the deployment's method, `coderd/notifications/enqueuer.go:104-116`, and the notifications that a turned-off template or the dedupe drops without an error, `:138-165`.
- Pinned skills, only with `fork-pinned-skills`: the routes, `coderd/fork_chat_routes.go:130-135`, owner only, `coderd/fork_chat_pinned_skills.go:63-68`, and at most 8, `:73-79`, `coderd/x/chatd/fork_pinned_skills.go:29`, `coderd/x/chatd/skill_invocation.go:23`.
  Pins written only by that handler, `fork_chat_pinned_skills.go:106-114`.
  The composer pill for an existing chat, `site/src/pages/AgentsPage/components/ForkComposerSlot.tsx:30-32`, `:44-49`, editable by the owner, `site/src/pages/AgentsPage/components/ForkPinnedSkillsPill.tsx:75-78`, `:145-146`.
  The prompt message, `fork_pinned_skills.go:38`, `:56-96`, with each block, `:135-145`, named by source and skill, `coderd/x/chatd/chatprompt/chatprompt.go:1071-1073`, synced before each step, `coderd/x/chatd/fork_context.go:58`, `coderd/x/chatd/generation.go:640`.
  Hidden skills load, `skill_invocation.go:61-62`, pins that the owner can't use are skipped, `fork_pinned_skills.go:98-101`, `:109-112`, the `(none)` list, `:42-44`, `:126-128`, and the 64&nbsp;KiB budget, `:34`, `:116-125`, `codersdk/workspacesdk/frontmatter.go:15`.
