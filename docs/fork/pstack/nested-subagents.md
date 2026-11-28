# Nested subagents

This page is for a Coder Agents user who runs pstack skills, and shows how to let a child spawn and collect its own workers.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

When the deployment enables the `fork-nested-subagents` experiment, a `general` child gets `spawn_agent` and the other subagent tools while its own children stay within `--chat-max-subagent-depth`.
The default depth of 2 allows a root, a coordinator, and its workers.
An organization's `max_depth` subagent limit can lower that depth, but not raise it.
An `explore` or `computer_use` child never gets them.
With the experiment, `wait_agent`, `message_agent`, `interrupt_agent`, and `list_agents` reach only a chat's direct children, so the root waits on the coordinator, not on its workers.
With the experiment, `list_agents` also takes a `chat_id` to list the children of that chat instead, and `recursive: true` to list every descendant instead of only the children.
The `chat_id` must be your own chat or one of your descendants, so `list_agents` never lists chats outside your subtree.
With either option, each entry also has `parent_chat_id`.
With the `fork-chat-todos` experiment, you can pass any listed ID to `read_chat_transcript`.
To message other chats in the family, refer to [Family messages](family-messages.md).
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
