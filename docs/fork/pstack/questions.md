# Questions

This page is for a Coder Agents user who runs pstack skills, and shows how to ask the user a question from a chat.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.
Its code cites are on [pstack adapter evidence](https://github.com/ibetitsmike/coder/blob/main/docs/fork/pstack-adapter-evidence.md).

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
If your turn has ended, your chat continues only when the user sends you a message or a [wake-up](wake-ups.md) runs, and then `wait_agent` returns the child's reply.
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
