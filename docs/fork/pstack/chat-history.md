# Chat history

This page is for a Coder Agents user who runs pstack skills, and shows how to read the chat's own earlier messages with `chat_history`.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

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
A window that a [rollover](context-windows.md) started shows `chat_summarized`, as one a summary started does.
