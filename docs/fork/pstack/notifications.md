# Notifications

This page is for a Coder Agents user who runs pstack skills, and shows how to send the chat owner a Coder notification.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.
Its code cites are on [pstack adapter evidence](https://github.com/ibetitsmike/coder/blob/main/docs/fork/pstack-adapter-evidence.md).

With the `fork-chat-wakeups` experiment, `notify_user` sends the chat owner a Coder notification.
The owner gets it in the Coder notifications inbox when the deployment keeps the inbox on, and by email or webhook when the deployment sends notifications that way.
Pass the text as `message`, up to 2000 bytes, and an optional `title`, up to 120 bytes.
When you omit `title` or leave it blank, the notification uses the chat title, cut to its first 120 bytes.
The result is `{"sent":true}`.
When the chat has sent 10 notifications in the past hour, the next call fails with `notification limit reached: at most 10 per chat per hour`.
The wake-up pause notifications of the chat count toward those 10, but Coder Agents still sends a pause notification when the chat is at the limit.
An empty `message` fails with `message is required`, and a chat that a system user owns fails with `chats owned by a system user cannot send notifications`.
The result is also `{"sent":true}` when Coder drops the notification, because the owner turned off the **Custom Notification** template, the chat sent the same title and message earlier in the same clock minute, or the deployment has the inbox off and sends no email or webhook notifications.
