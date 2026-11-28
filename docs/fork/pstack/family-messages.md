# Family messages

This page is for a Coder Agents user who runs pstack skills, and shows how to send messages between the chats of one family.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.
Its code cites are on [pstack adapter evidence](https://github.com/ibetitsmike/coder/blob/main/docs/fork/pstack-adapter-evidence.md).

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
With `fork-nested-subagents`, a message that wakes a finished child counts toward the family's live limit, as [Subagent limits](subagent-limits.md) describes, and a child that waits for a secret answer refuses messages.
With the experiment, the sender's conversation shows **Sent message to** or **Queued message for** and the recipient's title, with a link to the recipient and the message text.
A `list_family_chats` call shows **Found** and the number of family chats, with a link to each chat other than the caller.
The recipient's chat shows the message with **Message from agent** and a link to the sender.
