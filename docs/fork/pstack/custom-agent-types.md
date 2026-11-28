# Custom agent types

This page is for a Coder Agents user who runs pstack skills, and shows how to run pstack's agents as `spawn_agent` types.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.
Its code cites are on [pstack adapter evidence](https://github.com/ibetitsmike/coder/blob/main/docs/fork/pstack-adapter-evidence.md).

When the deployment enables the `fork-agent-definitions` experiment, `spawn_agent` accepts a custom type for each agent definition that the workspace agent finds.
It reads the `*.md` files in `agents/`, `.agents/agents/`, `.claude/agents/`, and `.cursor/agents/` of each scan root, including `~/.coder/agents/`.
A chat pins the definitions when it starts, like `AGENTS.md` and the skills, so start a new chat or select **Refresh context** after you add one.
`GET /api/v2/chats/{chat}` lists each definition file that the chat pinned in `context.resources`, with kind `subagent` and its `status`.
An `ok` file also reports `subagent_name`, `subagent_description`, and any parse warnings in `error`.
A file with any other status, such as `invalid`, reports the reason in `error` and adds no custom type, so check there when a type is missing from `spawn_agent`.
The context usage indicator in the chat composer lists the same files under **Agent definitions**, with each `ok` definition's warnings under its name and the other files as issues.
A definition with warnings turns the indicator to its warning color.
The `spawn_agent` description lists the custom types after the built-in ones.
The child gets the definition's body as a system message, so do not paste the body into `prompt`.
A definition without `model` runs on your model, and pstack's agents set none.
A `model` that matches no config in `list_subagent_models` fails the spawn.
An explicit `model_config_id` overrides the definition's `model`, and an explicit `reasoning_effort` overrides a valid definition effort.
A definition whose effort isn't a valid reasoning effort fails every spawn.
The experiment also lets `type: "computer_use"` take `model_config_id` and `reasoning_effort`.
That config must support computer use, and it replaces the organization's computer use model and its reasoning effort.
A custom type starts with the tools of its base type, which is `general` unless the definition sets `base_type: explore`.
With the `fork-subagent-isolation` experiment, a definition can also set `isolation` to `shared`, `worktree`, or `workspace`, and a spawn of its type that sets no `isolation` uses that value, as [Worktree isolation](worktree-isolation.md) and [Workspace isolation](workspace-isolation.md) describe.
An `isolation` in the `spawn_agent` call, `shared` included, wins over the definition.
Without the experiment, a spawn ignores the definition's `isolation`.
Any other `isolation` value makes the definition invalid, even without the experiment.
A workspace file with one gets the status `invalid` and adds no custom type, and the API refuses to create or update a stored definition with one with status 400, `Invalid agent definition content.`
Coder Agents then removes tools by the definition's `tools`, `disallowed_tools`, `readonly`, and `mcp` fields, as [Custom agent tool policy](custom-agent-tool-policy.md) describes.
Coder Agents ignores a definition named `general`, `explore`, or `computer_use`.
With the experiment, the API also stores definitions for one user in an organization, for a whole organization, and for a chat project.
To manage your personal definitions, select **Agents** in the Agents sidebar, or **Settings** and then **Agent definitions**, and pick the organization if you belong to more than one.
When the list is empty, it explains that chats can also use agent files at `.claude/agents/<name>.md` in their workspace, and **New agent definition** there opens the editor too if you can create one.
The page lists each definition's name and description, and its **Enabled** switch stops `spawn_agent` from offering a definition without deleting it.
**Delete** in a row's menu removes your personal definition and leaves workspace, organization, and project definitions with the same name alone.
**New agent definition** opens a dialog with **Name**, **Description**, **Model**, and **Body** fields, and **Create agent definition** saves them as a Claude Code agent markdown file.
An empty **Model** runs the definition on the chat's model.
**Import file** fills the fields from a Claude Code or Cursor agent markdown file and keeps its other front matter, such as `tools` or `isolation`, which the dialog has no field for.
**Edit** in a row's menu opens the same dialog with the saved definition, where **Name** can't change, and **Save agent definition** keeps every front matter key except the name, description, and model as the file has them.
If the page already shows that the definition changed or was deleted after the dialog opened, the dialog doesn't save and asks you to copy your changes and reopen it.
The save itself has no version check, so it can overwrite a change from another tab or the API that the page hasn't loaded.
The dialog shows the server's errors, such as `Invalid agent definition content.` or `An agent definition with this name already exists.`
The page doesn't list the workspace's definition files, such as the pstack agents that [Setup](install.md) copies to `~/.coder/agents/`.
`spawn_agent` also offers the chat owner's enabled personal definitions in the chat's organization, the enabled organization definitions that the owner can read, and the enabled definitions of the chat's project.
It offers project definitions only when the chat owner owns the project or the project is shared with them, and a child chat uses its root chat's project.
To manage a project's definitions, open the project from the Agents sidebar and select the **Agent definitions** row in its details panel.
If you can edit the project, you get **New agent definition**, **Edit**, **Delete**, and the **Enabled** switch.
Otherwise, you see the list read-only.
A name that only one of the workspace, personal, organization, and project sources defines keeps its bare name.
A name that more than one source defines is listed only as `workspace/<name>`, `personal/<name>`, `org/<name>`, or `project/<name>`.
A bare name that more than one source defines fails with `type must be one of:` and the listed types.
To start a chat as an agent type, set `agent_type` on `POST /api/v2/chats` to `general`, `explore`, or a personal, organization, or project definition that `spawn_agent` would offer the chat.
The chat keeps its usual system prompt with the definition's body after it, and it follows the definition's tool rules from its first turn.
If the type takes away `spawn_agent`, as `explore` and `readonly: true` do, the prompt also drops the guidance on subagents, and it drops the guidance on ACP subagents if the type takes away an `acp_` tool.
The prompt leaves out the advice to create a workspace if the type takes away `list_templates` or `create_workspace`, and it doesn't name the workspace tools if the type takes away `execute`, `read_file`, or `write_file`.
With `explore` or `readonly: true`, the prompt leaves out both.
A tool from your request's `unsafe_dynamic_tools` that the type takes away fails as an unknown tool if the model calls it.
It runs on the definition's model and reasoning effort, unless your request sets `model_config_id` or `reasoning_effort`.
A definition that sets `model` can start a chat in an organization without a default chat model.
A root chat ignores the definition's `isolation`.
A workspace definition can't start a chat, because Coder reads a chat's workspace definitions only after its workspace agent connects.
Such a name, an unknown name, another built-in type such as `computer_use`, and a definition that you can't read fail with status 400, and so does `agent_type` without the `fork-agent-definitions` experiment.
To list the types before a chat exists, call `GET /api/v2/chats/fork/organizations/{organization}/agent-types` with the experiment on.
Add `project=<id>` for the project's definitions, `workspace=<id>` for the definitions that the workspace's agent reports, and `model_config_id=<id>` for the model that the chat would set, which wins over a definition's model as it does at creation.
Each row gives the `name` to pass, with the same prefixes as `spawn_agent`, and its `source`, `description`, `model`, and `base_type`.
`root_available` reports whether `agent_type` can start a chat as the type, and when it can't, `root_unavailable_reason` says why, as for every workspace definition.
With the experiment, the web UI shows the same list when you select **Settings** in the Agents sidebar and then **Available agents**.
The page lists the types of one organization that you belong to, and the **No project** and **No workspace** menus add the definitions of a project or of one of your workspaces in that organization.
Each row shows the type's name and description, its source under **Tier**, and under **Availability** either `Available` or the reason that it can't start a chat.
**Recent chats** links up to three of the most recently updated chats in that organization that started or were spawned as the type, out of the chats that the Agents sidebar's default list has loaded.
A chat stores the type's name without its prefix, so a name that several sources define shows the chats of each of them.
With the experiment, the form that starts a new chat also has an agent type picker, which shows **Default agent** until you choose a type.
It lists the same types for the form's organization, project, workspace, and model, grouped by source, and you can search them by name or description.
A type that can't start a chat can't be chosen, and it shows the reason instead of its description.
The chosen type becomes `agent_type` on `POST /api/v2/chats`, and the chat starts as the default agent instead if the list no longer offers the type as available when you send, such as after you change the project or the model.
When the form's model picker shows a model, the form sends it as `model_config_id`, so the chat runs on that model and not on the definition's `model`.
**Manage agent definitions…** in the picker opens your personal definitions page.
With the experiment, you can delegate from the web UI by typing `@` in the composer at the start of a line or after a space and choosing an agent.
Coder inserts `Delegate to the <name> agent (spawn_agent type <name>):` and a space, with the name that `spawn_agent` accepts.
In a child chat, the menu opens only in a `general` child with `fork-nested-subagents`.
When you create an organization definition, Coder shares it with the whole organization, and a member who can share it can narrow who reads it with `PATCH /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition}/acl`.
To manage an organization's definitions, open **AI Settings** > **Agent definitions** and pick the organization.
The link and the page at `/ai/settings/agent-definitions` need the experiment and a user who can read the organization skills of an organization, such as an organization admin or auditor.
The picker lists the organizations whose skills you can read, and the `org` search parameter names the selected one.
An `org` that you can't read shows `You don't have permission to view this page` instead of another organization's definitions.
The table and the dialog work like those of the personal page, and the organization skill permissions decide what you can change.
Without the permission to create, the page has no **New agent definition**.
Without the permission to update, the rows have no **Edit** and show the enabled state without a switch.
Without the permission to delete, the rows have no **Delete**.
**Delete** removes the definition for everyone in the organization and leaves personal, project, and workspace definitions with the same name alone.
A member who can share a definition selects **Manage permissions** in its row's menu, adds members or groups in **Agent definition permissions**, and selects **Save permissions**.
The dialog offers the members and groups that `GET .../acl/available` lists.
`spawn_agent`, `wait_agent`, `message_agent`, `interrupt_agent`, and `list_agents` report a custom child's definition name in `type`, not `general`.
The chat list, `GET /api/v2/chats/{chat}`, and the `created` event of `GET /api/v2/chats/watch` report each child chat's type, and the type that a root chat started as, in `agent_type`, and so does the `POST /api/v2/chats` response.
The Agents page shows the type as a badge next to the chat's title, except for `general`.
In the conversation, the `spawn_agent` card of a custom child shows **Agent type:** and the name.
That name has no `workspace/`, `personal/`, `org/`, or `project/` prefix, so do not pass it back to `spawn_agent` when more than one source defines it.
Without the experiment, a custom type fails with `type must be one of:` and the built-in types.
