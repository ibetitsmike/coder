# Notes

This page is for a Coder Agents user who runs pstack skills, and shows how to keep chat notes that survive a compaction.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

`notes` exists only when the deployment enables the `fork-token-budget` experiment.
Plan-mode turns, `explore` children and read-only custom agent types don't get it.
A chat's notes belong to that chat, and no other chat can read them, a parent or a child included.
The actions are `list`, `read`, `write`, which replaces the note, `append`, which adds the content as a new line, and `delete`.
A path has 1 to 128 characters from `a-z`, `0-9`, `.`, `_`, `/` and `-`, and starts with a letter or a digit, such as `checkpoint.md` or `findings/auth.md`.
A note holds 16384 bytes at most, and a chat keeps 20 notes at most.
A write or an append that breaks a limit fails and stores nothing.
When Coder Agents runs a batch of tool calls again after a restart, a `write`, `append` or `delete` that already ran returns its first result and changes nothing, so an append adds its line once.
The notes survive a compaction, so keep the goal, the decisions, the progress and the next steps in `checkpoint.md`.
