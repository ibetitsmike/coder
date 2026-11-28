# Chat transcripts

This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

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
