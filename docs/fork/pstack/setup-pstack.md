# setup-pstack

This page is for a Coder Agents user who runs pstack skills, and shows how to run pstack's `setup-pstack` skill on Coder Agents.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

Follow its steps with these changes:

- Detect: the detected models are the configs that `list_subagent_models` returns.
  Write one `<model_config_id> <effort>` line per value in its `reasoning_efforts`, or one line with `none` when it lists none.
  The effort is the last token, so the budget step works as written.
  Keep `none` unchanged through the budget step.
- Defaults: map each default slug with the [Models](../pstack-adapter.md#models) rows.
- Write: write `~/.cursor/rules/pstack-models.mdc` in the workspace as the skill says, with each role's value as a line from the Detect step.
  If `~/.coder/AGENTS.md` still holds a block between `<!-- pstack-models -->` and `<!-- /pstack-models -->` from an earlier setup, delete that block, so chats get one set of role lines.
- With one config in `list_subagent_models`, every role runs on that config, including the roles that pstack spreads across model families, such as `arena runners` and `arena cross-judge pool`.
  Say so when you confirm the write, and in the report of each run that uses one of those roles.
- After the write, start a new chat or select **Refresh context** before you run another pstack skill.
