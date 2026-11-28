# Context windows

This page is for a Coder Agents user who runs pstack skills, and shows how to pick how an organization's chats compact.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

With the `fork-token-budget` experiment, each organization picks how its chats compact.
`GET /api/v2/chats/fork/organizations/{organization}/token-budget` returns the setting, and `PUT` on the same path replaces it.
It has two fields:

- `strategy` is `summary`, the default, or `token_budget`.
  With `summary`, chats compact as they do without the experiment: they don't get `new_context` or `context_budget` requests, and they summarize at the compression threshold.
  `notes` and `chat_history` stay.
- `fallback` is `new_window`, the default, or `summary`.
  It applies only with `token_budget`, and picks what happens when a window reaches the usable limit and the chat didn't call `new_context`.

An organization admin can change the setting and an auditor can read it.
A member who can't read the organization's chat model configs gets status 404.
An invalid value fails with status 400, and the endpoints fail with status 403 without the experiment.
chatd reads the setting when it builds a step's tools, so a change applies from the next step.
In the web UI, an organization admin changes it in the **Context compaction** section of **Admin settings** > **AI** > **Coder Agents**, and an auditor can view it there.
The section shows only with the experiment.

With the `token_budget` strategy, a chat that has `notes`, `chat_history` and `new_context` works in context windows instead of summaries.
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
With the `summary` fallback, the chat summarizes at that point instead, as it does without the experiment.
The compaction model override writes that summary, and its own trigger still applies, so an override whose trigger comes before the usable limit summarizes there first.
A `new_context` call still starts a new window without a summary.
A chat keeps the setting its step started with, so a change takes effect from the next step.
A rollover waits for the results of the step's tool calls, so a call and its result stay in the same window.
The next window starts with a model-only message that starts with `<context_window id="w:2">`, with the window's ID.
It lists the notes and says to read `checkpoint.md` first, and holds nothing of the earlier conversation.
A rollover makes no model call, and the conversation shows a divider line, `Context window w:2 started at the agent's request` when the chat called `new_context`, or `Context window w:2 started at the context limit` when the window filled up.
Without the experiment, the same entry shows as a summary card that says `Context window w:2 started from the notes of this chat. No summary was made.`
Use `chat_history` to find a message of an earlier window.

A threshold of 100% turns off the requests and the rollover, and `new_context` still works.
With the `new_window` fallback, a compaction model override doesn't apply, since a rollover makes no summary.
A manual compaction from the composer still makes a summary.
