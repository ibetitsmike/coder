# Wake-ups

This page is for a Coder Agents user who runs pstack skills, and shows how to wake a chat later, or when its children or a pull request change.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.
Its code cites are on [pstack adapter evidence](https://github.com/ibetitsmike/coder/blob/main/docs/fork/pstack-adapter-evidence.md).

When the deployment enables the `fork-chat-wakeups` experiment, a chat has `schedule_wakeup`, `wait_for_events`, and [`notify_user`](notifications.md), except in plan-mode turns and in `explore` children.
A [custom agent tool policy](custom-agent-tool-policy.md) can also remove them from a custom child.
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
Coder Agents reads the pull request once a minute with the chat owner's linked GitHub account.
A failed read doesn't end the watch.
Coder Agents reads again after 5&nbsp;minutes, or after a GitHub rate limit resets but no sooner than a minute.
Activity after `expires_at` doesn't count.
The call reads the pull request before it returns, and fails with `read pull request <url>: <error>` when it can't.
The error can be `no git provider is configured for <url>` when no external auth provider of the deployment matches the URL, `<url> is not a pull request URL`, `the chat owner has no linked account for <host>`, or `pull request activity is only available for GitHub`.
A pull request that is already closed or merged fails with `pull request <url> is already closed` or `pull request <url> is already merged`.
`pull_request_events` without `pull_request_url` fails with `pull_request_events requires pull_request_url`, and an unknown event fails with `unknown pull_request_events value "<value>", valid values are review, comment, checks, closed`.

With `process_ids`, `wait_for_events` also watches up to 10 background processes of the chat, named by the `background_process_id` that `execute` returns.
Without `child_chat_ids`, the call watches only the processes, and the pull request when it names one.
For each process, the chat gets one message after the process exits, made of the `prompt`, an empty line, and ``Background process <id> (`<command>`) exited with code <code>.``, or `exited without an exit code (killed by a signal).` for a killed process.
The message ends with up to the last 40 lines of the process's output, at most 4&nbsp;KiB.
A process that already exited when you call the tool wakes the chat right after your turn ends.
The workspace agent reports the exit to Coder Agents as it happens, and keeps the report until Coder Agents confirms it, so a dropped connection delays the message but doesn't lose it.
When the workspace agent no longer knows the process at the call, the message says ``Lost the watch on background process <id> (`<command>`): <reason>.`` instead.
The same message arrives within 5&nbsp;minutes when the workspace stops or is rebuilt, or its agent restarts.
With `process_output_pattern`, a regular expression in RE2 syntax of up to 1024&nbsp;bytes, the chat also gets a message while a process runs and has printed new complete lines, ending in a newline, that match.
The workspace agent checks the output every second, and its first check also matches the lines that the process printed before the call.
That message is made of the `prompt`, an empty line, ``Background process <id> (`<command>`) is still running and printed <n> new lines matching `<pattern>`.``, and up to the last 40 of those lines, at most 4&nbsp;KiB.
`process_output_cooldown_seconds`, from 0 to 86400 and 60 by default, sets how long such a message waits after the wake-up's last message, so it collects the lines printed meanwhile.
The workspace agent also waits that long between two reports of matching lines of one process, and its first report goes out at once.
While the chat is busy, the reports collect, and one message carries their lines, at most the last 16384 characters of them.
An exit or a lost watch that happens while lines still wait for their message folds those lines into the exit or lost-watch message: ``Before it exited, it printed <n> new lines matching `<pattern>`.`` or ``Before the watch was lost, it printed <n> new lines matching `<pattern>`.``, with the lines, after the exit output.
A report holds at most the last 100 matching lines and cuts each one to 4&nbsp;KiB, while its count covers every match.
Lines that match in the cool-down before the process exits arrive only in the exit message's output.
After a lost connection, a message doesn't repeat or miss a line.
`process_output_max_events`, from 1 to 100 and 10 by default, caps those messages per process, and the exit message still arrives.
The workspace agent keeps only the first and the last 16&nbsp;KiB of a process's output, and once a process has printed more, matching sees only the kept last 16&nbsp;KiB, so earlier lines can be missed.
Matching reads the whole line as the workspace agent kept it, so a match past the first 4&nbsp;KiB of a long line still wakes the chat, but the message shows only those first 4&nbsp;KiB, with no marker.
The result also holds `processes`, with the `process_id`, `command`, and `output_pattern` of each watched process.
At `expires_at`, one last check reports the processes that exited by then, and no more matching line messages.
If a process has not reported an exit, the last check waits until the workspace agent says, at least 5&nbsp;seconds after the expiry, that it holds no exit it could not send, at most 10 minutes, so that an exit the agent could not send in time still arrives.
While a chat process runs, the agent says so every 30&nbsp;seconds.
It dates that statement by when Coder Agents answered its previous report, so a slow report or a skewed clock can't make the check miss an exit.
That check can also report an exit in the 15&nbsp;seconds after `expires_at`, but a check that runs later, such as after a busy turn, leaves out exits after `expires_at`, by the exit time that the workspace agent reported.
The call reads each process from the workspace agent before it returns, and fails with `process "<id>" is not a process of this chat: pass the background_process_id that execute returned` when the chat's workspace agent doesn't know it, or with `this chat has no workspace agent: start the process with execute first`.
A workspace agent too old to report processes fails the call with `the workspace agent of this chat is too old to report process events: restart the workspace to update its agent, or poll the process with schedule_wakeup`.
After the wake-up is saved, the call registers each watch with the workspace agent.
When that fails, the result also holds `warning`, which names the processes: their exits still wake the chat when the agent reports them, but an exit before the call and matching output can be missed.
More than 10 processes fail with `cannot wait for <n> processes at once, the maximum is 10`.
The call leaves out processes that an earlier call still watches, and fails with `these processes are already watched by wake-up <id>: end your turn to wait for them` when it leaves out all of them.
`process_output_pattern` without `process_ids` fails with `process_output_pattern requires process_ids`, and the cool-down or the limit without a pattern fails with `process_output_cooldown_seconds and process_output_max_events require process_output_pattern`.

Coder Agents looks for a due wake-up at its time and again in a sweep every minute.
It delivers at most 8 wake-ups at once, so a run can arrive well after its time when many are due or a delivery is slow.
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
The pill reads the list again every 30&nbsp;seconds while an events wake-up is active and every minute otherwise, so a change that a run makes can take that long to show.
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
The spend limits are \$20 of model cost in the chat and \$200 across the organization's chats that had a wake-up run in those 24&nbsp;hours.
An organization admin can set each limit higher or lower with `PUT /api/v2/chats/fork/organizations/{organization}/wakeup-caps`, with spend in millionths of a dollar, and a limit that the organization doesn't set keeps its default.
The spend counts every message in those chats, not only the ones that wake-ups started, and a model without prices counts as zero.
The check only uses the cost recorded so far, so the run that it lets through can take the spend past a limit, and runs on several replicas at once can pass an organization limit by a few runs.
Each replica keeps the organization's spend for a minute while it is under the limit, so the organization limit can stop runs up to a minute late, and it reads the spend again before it pauses a wake-up for it.
The runs are counted per UTC hour, so both run limits and the chats that count toward the organization's spend reach back to the start of the hour 24&nbsp;hours before the run, up to an hour more than 24&nbsp;hours.
When the chat or its organization has reached a limit, the wake-up pauses without sending its prompt.
A wake-up also pauses when a prompt hook denies its prompt, or when its cron schedule or its events are invalid.
A paused wake-up runs again only after a user resumes it through the API or the composer pill, and it still counts toward the chat's limit of 10 active or paused wake-ups.
A resumed wake-up that still hits a limit pauses again at its next run, and a canceled one no longer counts toward the limit.
When a wake-up pauses, Coder Agents sends the chat owner a Coder notification titled `Chat wake-up paused` that names the chat and the reason.
It sends at most one per chat and reason each UTC day, unless the chat gets a new title in between, because the notification text includes the title.
It uses the **Custom Notification** template, so the owner doesn't get it after turning that template off, and the wake-up stays paused all the same.
