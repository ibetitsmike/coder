# Todo list

This page is for a Coder Agents user who runs pstack skills, and shows how to track a chat's work with `todo_write` and `todo_read`.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

`todo_write` and `todo_read` exist only when the deployment enables the `fork-chat-todos` experiment.
Plan-mode turns and `explore` children don't get them.
An item's status is `pending`, `in_progress`, `done`, or `cancelled`.
A `todo_write` with any other status, such as `completed`, fails and stores nothing.
The list survives a compaction.
The web UI shows a non-empty list in a pill in the composer.
In the conversation, the web UI shows each `todo_write` and `todo_read` call as a card, such as **Updated todos (1/3 done)**.
The card of a call that returns a non-empty list expands to the list, and the card of a failed call expands to its error.
With the experiment, `list_agents` returns each child's list in `todos`, and its card shows the child's progress, such as **1/3 todos done**.
